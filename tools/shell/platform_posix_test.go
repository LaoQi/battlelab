//go:build linux || darwin

package shell

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPlatformPosixCandidates(t *testing.T) {
	if got := strings.Join(platform.Candidates, "/"); got != "bash/sh/ash" {
		t.Errorf("posix 候选链 = %q", got)
	}
}

func TestPlatformPosixPrograms(t *testing.T) {
	want := []string{
		"ls", "cat", "head", "tail", "grep", "rg", "fd", "sed", "awk",
		"find", "sort", "wc", "cut", "tr", "xargs",
		"git", "curl", "wget", "go", "node", "python",
	}
	if strings.Join(platform.Programs, ",") != strings.Join(want, ",") {
		t.Errorf("posix 程序清单 = %v", platform.Programs)
	}
}

func TestPlatformPosixCapabilities(t *testing.T) {
	want := "管道与文本工具链（grep/sed/awk/xargs 等）可直接组合；交互式程序（sudo/ssh/gpg/read 等）必须显式 interactive: true（命令与终端直通、可直接应答），普通命令的 stdin 不接终端；被信号终止的命令按 128+signum 记退出码。"
	if got := platform.Capabilities(&profile{Name: "bash", Kind: KindPosix}); got != want {
		t.Errorf("posix 能力描述不匹配:\n got %q\nwant %q", got, want)
	}
}
func TestPlatformPosixTTYCaps(t *testing.T) {
	if !platform.PtyLend {
		t.Error("posix 的 LendFull 借出的是 pty，PtyLend 应为 true")
	}
	if platform.ConfigureTTY == nil || platform.TTYEnv == nil {
		t.Fatalf("平台能力存在缺项: %+v", platform)
	}
}

// 判定规则：只有 stdin 正好是借出的 pty slave 才设控制终端；
// 但无论前台还是后台，一律进独立会话（否则子进程能用调用方的终端）。
func TestPosixConfigureTTYRule(t *testing.T) {
	dev, err := os.CreateTemp(t.TempDir(), "fake-pty")
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	front := &exec.Cmd{Stdin: dev}
	posixConfigureTTY(front, dev)
	if front.SysProcAttr == nil || !front.SysProcAttr.Setsid || !front.SysProcAttr.Setctty {
		t.Errorf("stdin 是借出设备时应取得控制终端: %+v", front.SysProcAttr)
	}
	if front.SysProcAttr.Ctty != 0 {
		t.Errorf("Ctty 应为子进程的 fd 0: %d", front.SysProcAttr.Ctty)
	}
	if front.SysProcAttr.Setpgid {
		t.Error("会话首进程不得再 Setpgid（EPERM）")
	}

	// 后台/管道/重定向：stdin 不是该设备。
	for name, stdin := range map[string]io.Reader{
		"other file": os.Stderr,
		"pipe":       strings.NewReader(""),
		"nil":        nil,
	} {
		cmd := &exec.Cmd{Stdin: stdin}
		posixConfigureTTY(cmd, dev)
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
			t.Errorf("%s: 仍应进独立会话: %+v", name, cmd.SysProcAttr)
		}
		if cmd.SysProcAttr.Setctty {
			t.Errorf("%s: stdin 不是借出设备时不得设控制终端: %+v", name, cmd.SysProcAttr)
		}
	}

	// 借出设备为 nil（无 pty 平台调用）时退化成「新会话、无终端」。
	plain := &exec.Cmd{Stdin: dev}
	posixConfigureTTY(plain, nil)
	if plain.SysProcAttr == nil || !plain.SysProcAttr.Setsid || plain.SysProcAttr.Setctty {
		t.Errorf("无设备时应仅进新会话: %+v", plain.SysProcAttr)
	}
}

func TestPosixTTYEnv(t *testing.T) {
	dev, err := os.CreateTemp(t.TempDir(), "fake-pty")
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	got := platform.TTYEnv([]string{"PATH=/bin", "GPG_TTY=/dev/tty1"}, dev)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "GPG_TTY="+dev.Name()) {
		t.Errorf("GPG_TTY 应被覆盖为借出设备: %v", got)
	}
	if !strings.Contains(joined, "SSH_TTY="+dev.Name()) {
		t.Errorf("SSH_TTY 应补上借出设备: %v", got)
	}
	if !strings.Contains(joined, "PATH=/bin") {
		t.Errorf("其余变量应保留: %v", got)
	}
	if again := platform.TTYEnv(got, dev); strings.Join(again, " ") != joined {
		t.Errorf("重复调用应幂等: %v", again)
	}
	if got := platform.TTYEnv([]string{"A=1"}, nil); strings.Join(got, " ") != "A=1" {
		t.Errorf("无设备时应原样返回: %v", got)
	}
}
