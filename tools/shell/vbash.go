package shell

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LaoQi/battlelab/vbash"
)

func (t *Tool) runVbash(ctx context.Context, command string, timeoutSec int, dir string) *Result {
	res := &Result{Command: command, Cwd: dir}
	lease, _ := t.console.LendStdin()
	if lease == nil {
		lease = nullLease{}
	}
	defer lease.Release()
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var stdout, stderr streamCapture
	stdout.chunks = &res.Stdout
	stderr.chunks = &res.Stderr

	cfg := vbash.Config{
		Dir:          dir,
		Home:         t.homeDir(),
		Env:          t.environList(),
		Arg0:         vbashArg0(t.profile),
		Stdout:       &stdout,
		Stderr:       &stderr,
		Stdin:        emptyStdin(),
		ShellName:    vbashShellName(t.profile),
		ConfigureCmd: platform.ConfigureGroup,
		CancelCmd:    platform.KillGroup,
		Fence:        t.fence,
	}
	if f := lease.Stdin(); f != nil {
		cfg.Stdin = f
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

// runVbashInteractive 让 interactive 命令**仍由解释器执行**，只是把它接到借出的终端上：
// pty 平台上解释器的 stdin/stdout/stderr 都用借出的 pty slave，前台外部命令再把这个
// slave 设为控制终端，于是 sudo/ssh/gpg/pinentry 能打开 /dev/tty 直接与用户应答，
// 输出经 pty 泵实时上屏并同时进工具结果。借不到终端即报错，**不回落宿主 shell**
// （跨平台归一与「不依赖宿主 shell」是 vbash 的设计前提）。
func (t *Tool) runVbashInteractive(ctx context.Context, command string, timeoutSec int, dir string) *Result {
	res := &Result{Command: command, Cwd: dir}
	var capture streamCapture
	capture.chunks = &res.Stdout
	// 哨兵 cmd：借出接口的签名要 *exec.Cmd（rc 平台用它接线），执行体却是进程内解释器。
	lease, err := t.console.LendFull(&exec.Cmd{}, &capture)
	if err != nil || lease == nil {
		return &Result{Command: command, Err: ErrNoLend.Error()}
	}
	defer lease.Release()
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	dev := lease.Stdin()
	cfg := vbash.Config{
		Dir:       dir,
		Home:      t.homeDir(),
		Env:       t.environList(),
		Arg0:      vbashArg0(t.profile),
		Stdin:     emptyStdin(),
		ShellName: vbashShellName(t.profile),
		CancelCmd: platform.KillGroup,
		Fence:     t.fence,
	}
	if platform.PtyLend && dev != nil {
		// pty 一台设备同时承载三路，输出由泵转给 RealTTY 并写入 capture；前台子进程拿到 ctty。
		cfg.Stdin, cfg.Stdout, cfg.Stderr = dev, dev, dev
		cfg.Env = platform.TTYEnv(cfg.Env, dev)
		// 外部命令一律进独立会话（隔离掉调用方终端），前台那个再另行取得控制终端。
		cfg.ConfigureCmd = func(cmd *exec.Cmd) { platform.ConfigureTTY(cmd, dev) }
	} else {
		// 非 pty 平台（Windows 的 CONIN$ 直通）：只有 stdin 是终端，输出仍走结果管道。
		cfg.Stdout, cfg.Stderr = &capture, &capture
		if dev != nil {
			cfg.Stdin = dev
		}
		cfg.ConfigureCmd = platform.ConfigureGroup
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
	// 先还终端（泵会把 pty 里剩余的输出排进 capture），再封口结果。
	lease.Release()
	capture.finish()
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
