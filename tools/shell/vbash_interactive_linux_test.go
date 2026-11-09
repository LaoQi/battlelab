//go:build linux

package shell

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// ptyConsole 模拟 tanya 的 Console.LendFull：分配一对 pty，slave（Stdin() 的返回值）
// 交给解释器作 stdio，master 一侧把输出泵给 capture；feed 在 delay 之后模拟用户键入。
type ptyConsole struct {
	feed  string
	delay time.Duration
	fail  bool
	mu    sync.Mutex
	out   strings.Builder
}

func (c *ptyConsole) LendStdin() (Lease, error) { return nullLease{}, nil }

func (c *ptyConsole) LendFull(_ *exec.Cmd, capture io.Writer) (Lease, error) {
	if c.fail {
		return nil, ErrNoLend
	}
	master, slave, err := openTestPTY()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.out.Reset()
	c.mu.Unlock()
	l := &ptyLease{master: master, slave: slave, pumpDone: make(chan struct{})}
	forward := func(b []byte) {
		c.mu.Lock()
		c.out.Write(b)
		c.mu.Unlock()
		if capture != nil {
			_, _ = capture.Write(b)
		}
	}
	go func() {
		defer close(l.pumpDone)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				forward(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	if c.feed != "" {
		go func() {
			time.Sleep(c.delay)
			_, _ = master.Write([]byte(c.feed))
		}()
	}
	return l, nil
}

// raw 返回 pty 上出现过的全部字节（含回显），用于断言不依赖工具结果。
func (c *ptyConsole) raw() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

type ptyLease struct {
	master   *os.File
	slave    *os.File
	pumpDone chan struct{}
	once     sync.Once
	released bool
}

func (l *ptyLease) Stdin() *os.File { return l.slave }

// Release 复刻真实租约的次序：先关 slave（解释器一侧的写入端）让泵把 master 里
// 残留的字节读尽（读尽后读 master 返回 EIO），再关 master。
// 反过来直接关设备会把尚未取走的输出连同结果一起丢掉。
func (l *ptyLease) Release() {
	l.once.Do(func() {
		l.released = true
		l.slave.Close()
		if l.pumpDone != nil {
			select {
			case <-l.pumpDone:
			case <-time.After(2 * time.Second):
			}
		}
		l.master.Close()
	})
}

func openTestPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	ptn, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(ptn), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

func vbashPtyTool(t *testing.T, c Console) *Tool {
	t.Helper()
	return testVbashTool(t, func(cfg *Config) { cfg.Console = c })
}

// interactive 命令在 vbash 下仍由解释器执行：内建 read 能从终端读到用户输入。
func TestVbashInteractiveBuiltinRead(t *testing.T) {
	con := &ptyConsole{feed: "hello\n", delay: 300 * time.Millisecond}
	res := vbashPtyTool(t, con).run(context.Background(), request{
		Command: `read -r ans; echo "got=[$ans]"`, Interactive: true, TimeoutSec: 20,
	})
	if res.Err != "" || res.TimedOut {
		t.Fatalf("执行异常: %+v（pty 原始输出 %q）", res, con.raw())
	}
	if got := shellStdout(res); !strings.Contains(got, "got=[hello]") {
		t.Errorf("内建 read 应读到终端输入: %q（pty 原始输出 %q）", got, con.raw())
	}
}

// 外部命令拿到真实 pts 作控制终端，可打开 /dev/tty（sudo/ssh/gpg 依赖这一点）。
func TestVbashInteractiveExternalGetsControllingTTY(t *testing.T) {
	res := vbashPtyTool(t, &ptyConsole{}).run(context.Background(), request{
		Command:     `sh -c 'tty; exec 3</dev/tty && echo devtty-ok || echo devtty-fail'`,
		Interactive: true, TimeoutSec: 20,
	})
	out := shellStdout(res)
	if !strings.Contains(out, "/dev/pts/") {
		t.Errorf("外部命令应看到 pty: %q", out)
	}
	if !strings.Contains(out, "devtty-ok") {
		t.Errorf("外部命令应能打开 /dev/tty: %q", out)
	}
}

// GPG_TTY/SSH_TTY 指向借出的设备，否则 gpg/ssh 会去问 agent 要终端。
func TestVbashInteractiveTTYEnv(t *testing.T) {
	res := vbashPtyTool(t, &ptyConsole{}).run(context.Background(), request{
		Command:     `sh -c 'echo "gpg=$GPG_TTY ssh=$SSH_TTY"; tty'`,
		Interactive: true, TimeoutSec: 20,
	})
	out := shellStdout(res)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("输出不足: %q", out)
	}
	pts := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(pts, "/dev/pts/") {
		t.Fatalf("未拿到 pty: %q", out)
	}
	if !strings.Contains(lines[0], "gpg="+pts) || !strings.Contains(lines[0], "ssh="+pts) {
		t.Errorf("GPG_TTY/SSH_TTY 应指向 %s，实际 %q", pts, lines[0])
	}
}

// 回归：内建 read 曾把 fd 0 包成 bufio.Reader，之后的外部命令被 os/exec 改成管道拷贝，
// 在不会 EOF 的终端上双向死锁。read 不预读、不替换 fd 0 后该序列必须正常返回。
func TestVbashInteractiveReadThenExternalDoesNotHang(t *testing.T) {
	con := &ptyConsole{feed: "abc\n", delay: 300 * time.Millisecond}
	done := make(chan *Result, 1)
	go func() {
		done <- vbashPtyTool(t, con).run(context.Background(), request{
			Command:     `read -r ans; echo "got=$ans"; sh -c 'echo after-ok'`,
			Interactive: true, TimeoutSec: 20,
		})
	}()
	select {
	case res := <-done:
		out := shellStdout(res)
		if !strings.Contains(out, "got=abc") || !strings.Contains(out, "after-ok") {
			t.Errorf("read 后的外部命令未正常执行: %+v", res)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("read 之后执行外部命令挂死（fd 0 被缓冲器劫持）")
	}
}

// 前台命令拿控制终端、后台命令不拿：判据是「子进程的 stdin 正好是借出的设备」。
// 直接观察效果——后台分支的 fd0 已被换成空读取器，因此打不开 /dev/tty。
func TestVbashInteractiveBackgroundSkipsTTY(t *testing.T) {
	out := t.TempDir() + "/bg.txt"
	res := vbashPtyTool(t, &ptyConsole{}).run(context.Background(), request{
		Command: "sh -c '(exec 3</dev/tty) 2>/dev/null && echo fg-ok || echo fg-fail'; " +
			"{ sh -c '(exec 3</dev/tty) 2>/dev/null && echo bg-ok || echo bg-fail' > " + out +
			" & }; sleep 1; cat " + out,
		Interactive: true, TimeoutSec: 20,
	})
	got := shellStdout(res)
	if !strings.Contains(got, "fg-ok") {
		t.Errorf("前台命令应能打开控制终端: %q", got)
	}
	if !strings.Contains(got, "bg-fail") {
		t.Errorf("后台命令不应被赋予控制终端: %q", got)
	}
}

// 借不到终端即报错，绝不回落宿主 shell。
func TestVbashInteractiveWithoutTerminalErrors(t *testing.T) {
	res := vbashPtyTool(t, &ptyConsole{fail: true}).run(context.Background(), request{
		Command: "echo should-not-run", Interactive: true, TimeoutSec: 10,
	})
	if !strings.Contains(res.Err, "interactive 不支持") {
		t.Fatalf("借出失败应明确报错: %+v", res)
	}
	if strings.Contains(shellOut(res), "should-not-run") {
		t.Errorf("借出失败不应执行命令（也不得回落宿主 shell）: %+v", res)
	}
}

// interactive 路径同样要保留退出码、超时与中断语义。
func TestVbashInteractiveExitCodeAndTimeout(t *testing.T) {
	res := vbashPtyTool(t, &ptyConsole{}).run(context.Background(), request{
		Command: "sh -c 'exit 7'", Interactive: true, TimeoutSec: 20,
	})
	if res.ExitCode != 7 {
		t.Errorf("退出码应保留: %+v", res)
	}
	res = vbashPtyTool(t, &ptyConsole{}).run(context.Background(), request{
		Command: "sleep 5", Interactive: true, TimeoutSec: 1,
	})
	if !res.TimedOut {
		t.Errorf("应报超时: %+v", res)
	}
}
