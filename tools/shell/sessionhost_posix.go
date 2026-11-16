//go:build linux || darwin

package shell

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/LaoQi/battlelab/ctty"
)

// hostPlatform 是宿主的平台实现：为命令准备终端与会话语义。
//
// POSIX 上宿主是**虚拟 pty 的会话首进程**，并且自己就是该 pty 的前台进程组：
// 于是各命令读 /dev/tty 不会被 SIGTTIN 停住，也不会因处置被忽略而拿到 EIO。
// 这正是真实交互 shell 的处境（shell 自己是前台组），只是这里的终端是虚拟的。
type hostPlatform struct {
	dev    *os.File // 虚拟 pty slave（宿主持有，作为命令的 /dev/tty）
	master *os.File // 虚拟 pty master（输出经 frameTTY 送父进程，输入由 frameTTYIn 写入）
	out    *frameWriter
	mu     sync.Mutex
}

// hostPlatformSetup 让当前进程成为新会话首进程，并接管一台自建虚拟 pty。
func hostPlatformSetup(out *frameWriter) (*hostPlatform, error) {
	master, slave, err := openHostPTY()
	if err != nil {
		return nil, err
	}
	if err := ctty.BecomeSessionLeader(slave); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	// 宿主管理前台组时会做终端操作（tcsetpgrp），必须忽略 SIGTTOU 否则自己被停住。
	// 注意这里**不碰 SIGTTIN**：命令都是前台组，不需要「忽略读终端信号」这条退路。
	ctty.IgnoreTTOU()
	hp := &hostPlatform{dev: slave, master: master, out: out}
	// 虚拟 tty 是命令的 /dev/tty：命令往它写的内容要送回父进程上屏。
	hp.startTTYPump()
	return hp, nil
}

// startTTYPump 把虚拟 tty master 上出现的字节转成 frameTTY 帧发给父进程。
// 父进程据此决定是否写真实终端（interactive）以及是否进工具结果。
func (hp *hostPlatform) startTTYPump() {
	if hp.master == nil || hp.out == nil {
		return
	}
	go func() {
		buf := make([]byte, hostOutChunk)
		for {
			n, err := hp.master.Read(buf)
			if n > 0 {
				if werr := hp.out.write(frameTTY, 0, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

// writeTTY 把父进程转发的按键写进虚拟 tty（命令的 /dev/tty 因此可应答）。
func (hp *hostPlatform) writeTTY(p []byte) {
	if hp.master == nil || len(p) == 0 {
		return
	}
	_, _ = hp.master.Write(p)
}

func (hp *hostPlatform) close() {
	if hp == nil {
		return
	}
	if hp.master != nil {
		hp.master.Close()
	}
	if hp.dev != nil {
		hp.dev.Close()
	}
}

// exec 在宿主里执行一条命令：建独立进程组（为了能按组杀、也为了前台组切换），
// 前台命令再把该组设为终端前台组。
func (hp *hostPlatform) exec(id uint32, req reqPayload, j *hostJob, out *frameWriter) int {
	if len(req.argv) == 0 {
		return 0
	}
	cmd := exec.Command(req.argv[0], req.argv[1:]...)
	cmd.Dir = req.cwd
	if len(req.env) > 0 {
		cmd.Env = req.env
	}
	// gpg/ssh 认 GPG_TTY/SSH_TTY：指向本宿主持有的虚拟 tty，否则它们会去问
	// agent 要终端，或者沿用从 tanya 继承来的旧值（那是真实终端，不该给命令）。
	if hp.dev != nil {
		cmd.Env = posixTTYEnv(cmd.Env, hp.dev)
	}
	// stdin 有两种形态：
	//   - 未被重定向（stdinTTY）：直接接虚拟 tty，与真实 shell 一致，
	//     `tty`/`test -t 0`/直接读 stdin 的程序都按终端语义工作；
	//   - 有管道/重定向/heredoc：接一根管道，由父进程经 frameIn 搬运。
	var inW *os.File
	if req.stdinTTY && hp.dev != nil {
		// 接虚拟 tty：命令读 stdin 等价读终端（`tty`/`test -t 0`/`read` 依赖它）。
		// 注意这会命令一直持有终端读端，但命令自己退出后 Wait 即返回，
		// 不依赖 EOF（sh/bash 不读 stdin 时不会等）。
		cmd.Stdin = hp.dev
	} else {
		inR, w, err := os.Pipe()
		if err != nil {
			return 1
		}
		cmd.Stdin = inR
		inW = w
		defer inR.Close()
	}
	if inW != nil {
		j.attachStdin(inW)
	}
	defer func() {
		j.mu.Lock()
		if j.in != nil {
			j.in.Close()
			j.in = nil
		}
		j.mu.Unlock()
	}()
	cmd.Stdout = &frameSink{out: out, job: id, fd: 1}
	cmd.Stderr = &frameSink{out: out, job: id, fd: 2}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// stdout/stderr 是 frameSink（非 *os.File），os/exec 会建内部管道并在 Wait 里
	// 等拷贝完成；命令的子孙若继承并长期持有该管道，Wait 会一直挂。
	// WaitDelay 让 Wait 在进程退出后限时放弃等待 I/O 拷贝。
	cmd.WaitDelay = shellWaitDelay
	if err := cmd.Start(); err != nil {
		return 127
	}
	j.mu.Lock()
	j.pgid = cmd.Process.Pid
	j.mu.Unlock()
	// 先回报 pgid，父进程据此在超时/中断时杀组。
	_ = out.write(frameStart, id, putU32(uint32(cmd.Process.Pid)))
	if req.front {
		_ = ctty.SetForeground(hp.dev, cmd.Process.Pid)
	}
	werr := cmd.Wait()
	if req.front {
		_ = ctty.SetForeground(hp.dev, os.Getpid())
	}
	return exitCodeOf(werr)
}

// frameSink 把命令输出切块成 frameData 帧。
type frameSink struct {
	out  *frameWriter
	job  uint32
	fd   int
	mu   sync.Mutex
	rest []byte
}

func (s *frameSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		chunk := p
		if len(chunk) > hostOutChunk {
			chunk = chunk[:hostOutChunk]
		}
		p = p[len(chunk):]
		payload := make([]byte, 1+len(chunk))
		payload[0] = byte(s.fd)
		copy(payload[1:], chunk)
		if err := s.out.write(frameData, s.job, payload); err != nil {
			return n, err
		}
	}
	return n, nil
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
		return exitErr.ExitCode()
	}
	return 1
}

// hostSignalGroup 向进程组投信号（先 CONT 唤醒可能被停住的成员）。
func hostSignalGroup(pgid, sig int) error {
	if pgid <= 0 {
		return os.ErrProcessDone
	}
	if sig == sigKillNum {
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
	}
	err := syscall.Kill(-pgid, syscall.Signal(sig))
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}

// sigKillNum 是 SIGKILL 的编号（transport/host 两侧共用，避免各平台重复常量）。
const sigKillNum = 9
