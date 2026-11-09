//go:build linux || darwin

package shell

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/LaoQi/battlelab/ctty"
)

var posixCandidates = []string{"bash", "sh", "ash"}

var posixPrograms = []string{
	"ls", "cat", "head", "tail", "grep", "rg", "fd", "sed", "awk",
	"find", "sort", "wc", "cut", "tr", "xargs",
	"git", "curl", "wget", "go", "node", "python",
}

func posixCapabilities(*profile) string {
	return "管道与文本工具链（grep/sed/awk/xargs 等）可直接组合；交互式程序（sudo/ssh/gpg/read 等）必须显式 interactive: true（命令与终端直通、可直接应答），普通命令的 stdin 不接终端；被信号终止的命令按 128+signum 记退出码。"
}

func posixConfigureGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// posixConfigureTTY 把 interactive 场景下的外部命令放进**独立会话**，并只在子进程的
// stdin 正好是借出的 pty slave 时把该设备设为控制终端：
//
//   - 独立会话是隔离的关键：否则子进程继承调用方（REPL）的会话与控制终端，
//     后台命令或漏标 interactive 的命令照样能打开 /dev/tty 抢走 REPL 的终端；
//     入新会话且不设 ctty 时 /dev/tty 直接打不开（ENXIO）。
//   - 前台命令（stdin 就是这个 slave）再拿 ctty，sudo/ssh/gpg/pinentry 才能与用户应答。
//   - 后台命令的 fd0 已被解释器换成空读取器，判据自然不成立；管道段与重定向命令同理。
//
// 注意这里是**替换** ConfigureGroup 的 Setpgid 而非叠加：会话首进程不能再 setpgid（EPERM）。
// 新会话首进程本身就是进程组长，KillGroup 的 -pid 组杀依然有效。
func posixConfigureTTY(cmd *exec.Cmd, dev *os.File) {
	attr := &syscall.SysProcAttr{Setsid: true}
	if dev != nil {
		if f, ok := cmd.Stdin.(*os.File); ok && f.Name() == dev.Name() {
			attr.Setctty = true
			attr.Ctty = 0
		}
	}
	cmd.SysProcAttr = attr
}

// posixTTYEnv 把 gpg/ssh 认的终端变量指到借出的设备，
// 否则它们会去问 gpg-agent 要 tty 或读继承来的旧值。
func posixTTYEnv(env []string, dev *os.File) []string {
	if dev == nil {
		return env
	}
	for _, key := range []string{"GPG_TTY", "SSH_TTY"} {
		env = setEnvVar(env, key, dev.Name())
	}
	return env
}

func setEnvVar(env []string, key, val string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

func posixKillGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}

func posixProtectSignals() {
	ctty.ProtectJobSignals()
}

func posixExitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), true
	}
	return exitErr.ExitCode(), true
}
