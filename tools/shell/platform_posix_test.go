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

// 判定规则：是否授予控制终端由 front 决定，与子进程的 stdin 是什么无关。
// 回归背景：早先拿「stdin 是否正好是借出设备」当判据，把 `sudo ... < /dev/null`、
// `cmd | sudo ...` 这类前台命令误判成后台，它们进新会话却拿不到 ctty，
// sudo 于是报「读取密码需要一个终端」。
func TestPosixConfigureTTYRule(t *testing.T) {
	dev, err := os.CreateTemp(t.TempDir(), "fake-pty")
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	// 前台：stdin 可以是任意形态，都必须拿到控制终端，且经额外 fd 指定。
	for name, stdin := range map[string]io.Reader{
		"stdin=dev":     dev,
		"stdin=devnull": nil,
		"stdin=pipe":    strings.NewReader(""),
		"stdin=file":    os.Stderr,
	} {
		cmd := &exec.Cmd{Stdin: stdin}
		posixConfigureTTY(cmd, dev, true)
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
			t.Errorf("%s: 应进独立会话: %+v", name, cmd.SysProcAttr)
		}
		if !cmd.SysProcAttr.Setctty {
			t.Errorf("%s: 前台命令必须取得控制终端（与 stdin 形态无关）: %+v", name, cmd.SysProcAttr)
		}
		if cmd.SysProcAttr.Setpgid {
			t.Errorf("%s: 会话首进程不得再 Setpgid（EPERM）", name)
		}
		// 控制终端必须指向真的被放进 ExtraFiles 的那个设备。
		want := ttyCttyFd + len(cmd.ExtraFiles) - 1
		if len(cmd.ExtraFiles) == 0 || cmd.ExtraFiles[len(cmd.ExtraFiles)-1] != dev {
			t.Errorf("%s: 借出设备应经 ExtraFiles 交给子进程: %v", name, cmd.ExtraFiles)
		}
		if cmd.SysProcAttr.Ctty != want {
			t.Errorf("%s: Ctty 应指向 dev 的额外 fd %d，实际 %d", name, want, cmd.SysProcAttr.Ctty)
		}
	}

	// 后台/管道中间段：仍进独立会话，但不授予控制终端。
	cmd := &exec.Cmd{Stdin: dev}
	posixConfigureTTY(cmd, dev, false)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Errorf("后台命令仍应进独立会话: %+v", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.Setctty {
		t.Errorf("后台命令不得取得控制终端: %+v", cmd.SysProcAttr)
	}
	if len(cmd.ExtraFiles) != 0 {
		t.Errorf("后台命令不应携带额外 fd: %v", cmd.ExtraFiles)
	}

	// 借出设备为 nil（无 pty 平台调用）时退化成「新会话、无终端」。
	plain := &exec.Cmd{Stdin: dev}
	posixConfigureTTY(plain, nil, true)
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
