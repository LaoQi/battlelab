//go:build linux || darwin

package shell

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/LaoQi/battlelab/ctty"
	"github.com/LaoQi/battlelab/vbash"
)

// ttyCttyFd 是控制终端在**子进程内**的 fd 号：借出的 pty slave 通过 ExtraFiles
// 交到子进程 fd 3（os/exec 的第一个额外 fd），Setctty 再指向它。这样控制终端
// 与 stdin（fd 0）彻底解耦，stdin 被重定向成 /dev/null 或管道也不影响取终端。
const ttyCttyFd = 3

var posixCandidates = []string{"bash", "sh", "ash"}

var posixPrograms = []string{
	"ls", "cat", "head", "tail", "grep", "rg", "fd", "sed", "awk",
	"find", "sort", "wc", "cut", "tr", "xargs",
	"git", "curl", "wget", "go", "node", "python",
}

func posixCapabilities(*profile) string {
	return "管道与文本工具链（grep/sed/awk/xargs 等）可直接组合；交互式程序（sudo/ssh/gpg/read 等）必须显式 interactive: true（命令与终端直通、可直接应答），普通命令的 stdin 不接终端；被信号终止的命令按 128+signum 记退出码。"
}

func posixConfigureGroup(cmd *exec.Cmd, _ vbash.CmdState) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// posixConfigureTTY 把 interactive 场景下的外部命令放进独立会话，并决定它是否取得
// 借出终端的控制权。判据是**调用方给出的前台标记**，而不是「子进程的 stdin 是什么」：
//
//   - 早先的实现拿 stdin 是否等于借出的 pty slave 当判据，本意是区分前台与后台，
//     却把显式重定向 stdin 的前台命令（`sudo ... < /dev/null`、`cmd | sudo ...`）
//     一并误判成后台。这些命令进了新会话却拿不到控制终端，sudo/gpg/ssh 打开
//     /dev/tty 失败，报「读取密码需要一个终端」。
//   - 现在前台标记由解释器直接给出（它知道这条命令是不是后台作业），拿到终端的
//     命令一律通过额外 fd 3 指定控制终端，与 stdin 的重定向形态完全解耦。
//
// 独立会话仍是隔离的关键：否则子进程继承调用方（REPL）的会话与控制终端，
// 后台命令或漏标 interactive 的命令照样能打开 /dev/tty 抢走 REPL 的终端。
// 入新会话且不设 ctty 时 /dev/tty 直接打不开（ENXIO）。
//
// 注意这里是**替换** ConfigureGroup 的 Setpgid 而非叠加：会话首进程不能再 setpgid（EPERM）。
// 新会话首进程本身就是进程组长，KillGroup 的 -pid 组杀依然有效。
func posixConfigureTTY(cmd *exec.Cmd, dev *os.File, front bool) {
	attr := &syscall.SysProcAttr{Setsid: true}
	if dev != nil && front {
		// 用额外 fd 承载控制终端，而不是要求 stdin 就是该设备：
		// 这样 `cmd < /dev/null`、`a | cmd` 这类命令依旧拿得到终端。
		// ExtraFiles 从子进程 fd 3 起按序编号，追加到末尾以保证 Ctty 指向的就是 dev。
		cmd.ExtraFiles = append(cmd.ExtraFiles, dev)
		attr.Setctty = true
		attr.Ctty = ttyCttyFd + len(cmd.ExtraFiles) - 1
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
