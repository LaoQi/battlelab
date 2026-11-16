//go:build linux

package shell

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 会话宿主架构下 vbash 的终端语义：一次 run_shell 自建一台虚拟 tty，
// 所有外部命令共享它（各自独立进程组、前台命令设前台组）。
// 因此不再有「向调用方借终端」这一步 —— 下列用例覆盖新语义。

func hostTool(t *testing.T) *Tool {
	t.Helper()
	return testVbashTool(t, func(cfg *Config) { cfg.Console = nullConsole{} })
}

// 外部命令看到的是宿主的虚拟 pts，并且能打开 /dev/tty。
func TestVbashHostProvidesControllingTTY(t *testing.T) {
	res := hostTool(t).run(context.Background(), request{
		Command:    `sh -c 'tty; exec 3</dev/tty && echo devtty-ok || echo devtty-fail'`,
		TimeoutSec: 20,
	})
	out := shellStdout(res)
	if !strings.Contains(out, "/dev/pts/") {
		t.Errorf("外部命令应看到 pty: %q", out)
	}
	if !strings.Contains(out, "devtty-ok") {
		t.Errorf("外部命令应能打开 /dev/tty: %q", out)
	}
}

// GPG_TTY/SSH_TTY 指向虚拟设备，否则 gpg/ssh 会去问 agent 要终端。
func TestVbashHostTTYEnv(t *testing.T) {
	res := hostTool(t).run(context.Background(), request{
		Command:    `sh -c 'echo "gpg=$GPG_TTY ssh=$SSH_TTY"; tty'`,
		TimeoutSec: 20,
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

// stdin 未被重定向时接虚拟 tty：`tty`/`test -t 0` 与真实 shell 一致。
func TestVbashHostPlainStdinIsTTY(t *testing.T) {
	res := hostTool(t).run(context.Background(), request{
		Command:    `sh -c 'test -t 0 && echo stdin-tty || echo stdin-not-tty'`,
		TimeoutSec: 20,
	})
	if got := shellStdout(res); !strings.Contains(got, "stdin-tty") {
		t.Errorf("普通命令的 stdin 应接虚拟 tty: %q", got)
	}
}

// 管道/重定向时 stdin 走协议搬运，不接终端。
func TestVbashHostRedirectedStdinNotTTY(t *testing.T) {
	res := hostTool(t).run(context.Background(), request{
		Command:    `echo hi | sh -c 'test -t 0 && echo pipe-tty || echo pipe-not-tty; cat'`,
		TimeoutSec: 20,
	})
	got := shellStdout(res)
	if !strings.Contains(got, "pipe-not-tty") {
		t.Errorf("管道段的 stdin 不应是终端: %q", got)
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("管道数据应送达: %q", got)
	}
}

// 管道各段共享同一台虚拟 tty（各段都能打开 /dev/tty）。
func TestVbashHostPipelineSharesTerminal(t *testing.T) {
	res := hostTool(t).run(context.Background(), request{
		Command:    `sh -c 'exec 3</dev/tty && echo seg1-ok' | cat; sh -c 'exec 3</dev/tty && echo seg2-ok'`,
		TimeoutSec: 20,
	})
	got := shellStdout(res)
	if !strings.Contains(got, "seg1-ok") || !strings.Contains(got, "seg2-ok") {
		t.Errorf("管道各段应共享同一控制终端: %q", got)
	}
}

// 后台命令与前台共用同一控制终端，但只有前台命令是前台进程组。
func TestVbashHostBackgroundNotForeground(t *testing.T) {
	out := t.TempDir() + "/bg.txt"
	res := hostTool(t).run(context.Background(), request{
		Command: "sh -c 'echo fg-tpgid=$(ps -o tpgid= -p $$|tr -d \" \")' ; " +
			"{ sh -c 'echo bg-tpgid=$(ps -o tpgid= -p $$|tr -d \" \")' > " + out + " & }; sleep 1; cat " + out,
		TimeoutSec: 20,
	})
	got := shellStdout(res)
	if !strings.Contains(got, "fg-tpgid=") {
		t.Errorf("前台命令应能取到 tpgid: %q", got)
	}
	if !strings.Contains(got, "bg-tpgid=") {
		t.Errorf("后台命令应能取到 tpgid: %q", got)
	}
}

// 并发命令不互相阻塞：后台长命令不拖住前台命令。
func TestVbashHostConcurrentNotBlocked(t *testing.T) {
	start := time.Now()
	res := hostTool(t).run(context.Background(), request{
		Command:    `sleep 1 & echo hi`,
		TimeoutSec: 20,
	})
	if got := shellStdout(res); !strings.Contains(got, "hi") {
		t.Errorf("前台命令应立即执行: %q", got)
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Errorf("前台命令被后台命令阻塞了: %v", d)
	}
}

// 命令替换的输出不得泄漏到工具结果。
func TestVbashHostCommandSubstitutionIsolated(t *testing.T) {
	res := hostTool(t).run(context.Background(), request{
		Command:    `x=$(seq 1 100); echo "len=${#x}"`,
		TimeoutSec: 20,
	})
	got := shellStdout(res)
	if !strings.Contains(got, "len=291") {
		t.Errorf("命令替换应只进变量: %q", got)
	}
	if strings.Count(got, "\n") > 3 {
		t.Errorf("命令替换的输出泄漏到 stdout 了: %q", got)
	}
}

// 超时能杀掉宿主里那条命令的进程组。
func TestVbashHostTimeoutKillsGroup(t *testing.T) {
	res := hostTool(t).run(context.Background(), request{
		Command:    `sleep 300`,
		TimeoutSec: 1,
	})
	if !res.TimedOut {
		t.Errorf("应超时: %+v", res)
	}
}
