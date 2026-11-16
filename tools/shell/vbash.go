package shell

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LaoQi/battlelab/vbash"
)

// runVbash 执行一条 vbash 脚本。普通与 interactive 走**同一条**路径：
// 外部命令一律交给会话宿主，宿主在虚拟 pty 内为命令建独立进程组、并按需
// 把它设为该 pty 的前台组。于是 sudo/ssh/gpg 在脚本任意位置（含管道中间段）
// 都能打开 /dev/tty 与用户应答，而 tanya 自己始终是真实终端的前台组。
//
// interactive 只影响**呈现方式**：为真时把虚拟 tty 的 master 双向泵接到真实
// 终端（提示实时上屏、按键转发），为假时输出只进工具结果。
func (t *Tool) runVbash(ctx context.Context, command string, timeoutSec int, dir string, interactive bool) *Result {
	res := &Result{Command: command, Cwd: dir}
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var stdout, stderr streamCapture
	stdout.chunks = &res.Stdout
	stderr.chunks = &res.Stderr

	// 启动宿主：它自建虚拟 pty，并把 master 交回本进程做双向泵。
	host, herr := startSessionHost()
	if herr != nil {
		res.Err = ErrNoLend.Error()
		res.Duration = time.Since(start)
		return res
	}
	defer host.close()

	// interactive 时把虚拟 tty 接到真实终端：命令写 /dev/tty 的提示实时上屏，
	// 用户按键转发进虚拟 tty（sudo/ssh/gpg 因此可直接应答）。
	// 非 interactive 时虚拟 tty 的输出只进工具结果，不上屏。
	var ttyPump *ttyRelay
	if !interactive {
		// 终端锚点租约：内建命令的重定向（printf ... > /dev/tty）直接写 tanya
		// 进程的真实终端，模式污染（autowrap off、光标隐藏、备用屏）必须在
		// 归还时复位。interactive 分支由 LendFull 的归还路径承担同一职责，
		// 这里为非 interactive 补上锚点（不得与 LendFull 并用：console 的
		// lend 记账不是计数器，嵌套借用会在首个归还时提前唤醒前台读者）。
		lease, _ := t.console.LendStdin()
		if lease == nil {
			lease = nullLease{}
		}
		defer lease.Release()
	}
	if interactive {
		if lease, err := t.console.LendFull(&exec.Cmd{}, &stdout); err == nil && lease != nil {
			ttyPump = startTTYRelay(lease, host)
			defer func() {
				ttyPump.stop()
				lease.Release()
			}()
			// 虚拟 tty 的输出（命令写 /dev/tty 的提示）实时上屏，同时也进工具结果。
			host.onTTY = func(p []byte) {
				if dev := lease.Stdin(); dev != nil {
					_, _ = dev.Write(p)
				}
			}
		}
	}

	cfg := vbash.Config{
		Dir:       dir,
		Home:      t.homeDir(),
		Env:       t.environList(),
		Arg0:      vbashArg0(t.profile),
		Stdout:    &stdout,
		Stderr:    &stderr,
		Stdin:     &hostTTYReader{h: host, ctx: runCtx},
		ShellName: vbashShellName(t.profile),
		CancelCmd: platform.KillGroup,
		Fence:     t.fence,
		Handler:   t.hostHandler(host),
	}
	if runCtx.Err() != nil {
		res.Interrupted = true
		res.NotStarted = true
		res.Duration = time.Since(start)
		return res
	}
	var code int
	var runErr error
	_, abandoned := waitBounded(func() error {
		code, runErr = vbash.RunScript(runCtx, command, cfg)
		return nil
	}, runCtx.Done(), shellKillGrace)
	stdout.finish()
	stderr.finish()
	res.Duration = time.Since(start)

	if abandoned {
		res.TimedOut = true
		res.Err = MsgAbandoned
		return res
	}
	if ctx.Err() != nil {
		res.Interrupted = true
		return res
	}
	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		return res
	}
	res.ExitCode = code
	t.classifyVbashError(res, runErr)
	return res
}

// hostTTYReader 把内建命令的 stdin 接到宿主的虚拟 tty。
// 每次 Read 向宿主请求一行——这与终端「按行交付」的语义一致，也让
// `read -r x` 与 `sh -c 'read -r x'` 行为相同。
type hostTTYReader struct {
	h   *sessionHost
	ctx context.Context
	buf []byte
}

func (r *hostTTYReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		line, ok := r.h.readLine(r.ctx)
		if !ok {
			return 0, io.EOF
		}
		r.buf = append([]byte(line), '\n')
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// isPlainStdin 报告该 stdin 是否「未被重定向/接管道」。
// emptyStdin() 是工具在无人喂输入时放的哨兵；命令替换、管道、重定向、
// heredoc 都会把它换成别的 reader。
func isPlainStdin(r io.Reader) bool {
	switch r.(type) {
	case emptyStdinReader, *hostTTYReader:
		// 工具层的顶层 stdin（虚拟 tty）或未接输入：命令的 stdin 也接虚拟 tty。
		return true
	}
	return false
}

// hostHandler 把解释器的外部命令交给会话宿主执行。
//
// stdio 全部经协议搬运（见 transport.go）：宿主不需要打开父进程的任何 fd，
// 也不需要 socketpair/ExtraFiles —— 这是跨平台一致的前提。
//
// 关键是**三种 stdio 形态都要如实搬运**，不能一律写工具结果：
//   - `a | b`：b 的 In 是上一段管道的读端 *os.File，要由父进程搬进 frameIn；
//   - `x=$(cmd)`：Out 是命令替换的内存 buffer，输出必须回那里而不是工具结果；
//   - `cmd > file`：Out 是重定向打开的文件。
func (t *Tool) hostHandler(h *sessionHost) vbash.Handler {
	return func(ctx context.Context, r *vbash.Runner, argv []string, in *vbash.IO) (int, error) {
		// 前台标记由解释器给出：它才知道这条命令是不是 & 起的后台作业。
		req := reqPayload{
			argv:  argv,
			cwd:   r.Env.Dir(),
			env:   r.Env.Environ(),
			front: !r.Background(),
			// stdin 未被重定向/接管道时，命令的 stdin 接虚拟 tty：这样
			// `tty`、`test -t 0`、直接读 stdin 的程序行为与真实 shell 一致。
			// 有管道/重定向/heredoc 时一律走协议搬运。
			stdinTTY: isPlainStdin(in.In),
		}
		return h.run(ctx, req, in)
	}
}

func vbashArg0(p *profile) string {
	if p.Kind == KindPosix && !p.Builtin {
		return filepath.Base(p.Path)
	}
	return "bash"
}

func vbashShellName(p *profile) string {
	if p.Kind == KindPosix && !p.Builtin {
		return p.Path
	}
	return BuiltinShellName
}

func (t *Tool) classifyVbashError(res *Result, err error) {
	if err == nil {
		return
	}
	var exitReq vbash.ExitRequest
	if errors.As(err, &exitReq) {
		return
	}
	var syntax *vbash.SyntaxError
	if errors.As(err, &syntax) {
		return
	}
	var unsupported *vbash.UnsupportedError
	if errors.As(err, &unsupported) {
		res.Err = strings.TrimPrefix(unsupported.Error(), "vbash: ")
		return
	}
	res.Err = err.Error()
}

func (t *Tool) homeDir() string {
	if t.home != "" {
		return t.home
	}
	for _, kv := range t.environList() {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			return v
		}
	}
	return ""
}
