//go:build windows

package shell

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
)

// hostPlatform 的 Windows 实现：没有 /dev/tty、没有会话、没有进程组信号，
// 也没有 SIGTTIN/SIGTTOU —— 交互式程序走 UAC 弹窗或控制台，不读「控制终端」。
// 因此这里不做终端模拟，只保留宿主存在的意义：把命令集中在一个进程下，
// 以便按进程树统一终止（taskkill /T）。
type hostPlatform struct {
	mu sync.Mutex
}

func hostPlatformSetup(*frameWriter) (*hostPlatform, error) { return &hostPlatform{}, nil }

// writeTTY 在 Windows 上无虚拟 tty，按键转发无落点。
func (hp *hostPlatform) writeTTY([]byte) {}

func (hp *hostPlatform) close() {}

func (hp *hostPlatform) exec(id uint32, req reqPayload, j *hostJob, out *frameWriter) int {
	if len(req.argv) == 0 {
		return 0
	}
	// 只 exec 解释器解析好的路径，不查 PATH：宿主的进程环境与解释器的 `$PATH` 无关。
	cmd := exec.Command(req.path, req.argv[1:]...)
	cmd.Args[0] = req.argv[0]
	cmd.Dir = req.cwd
	if len(req.env) > 0 {
		cmd.Env = req.env
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return 1
	}
	cmd.Stdin = inR
	j.attachStdin(inW)
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
	if err := cmd.Start(); err != nil {
		inR.Close()
		// 启动失败（cwd 不存在、路径不可 exec 等）：诊断写回 stderr，pgid 报 0。
		// 不报 0 的话父进程只能等满握手超时，把「命令起不来」误报成「宿主不可用」。
		_ = out.write(frameData, id, append([]byte{2}, err.Error()+"\n"...))
		_ = out.write(frameStart, id, putU32(0))
		return 127
	}
	inR.Close()
	j.mu.Lock()
	j.pgid = cmd.Process.Pid
	j.mu.Unlock()
	_ = out.write(frameStart, id, putU32(uint32(cmd.Process.Pid)))
	werr := cmd.Wait()
	if werr == nil {
		return 0
	}
	if exitErr, ok := werr.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
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

// hostSignalGroup 在 Windows 上按进程树终止（没有进程组信号语义）。
func hostSignalGroup(pgid, sig int) error {
	if pgid <= 0 {
		return os.ErrProcessDone
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pgid)).Run(); err == nil {
		return nil
	}
	p, err := os.FindProcess(pgid)
	if err != nil {
		return os.ErrProcessDone
	}
	return p.Kill()
}

const sigKillNum = 9

var _ = syscall.Getpid
