package shell

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/LaoQi/battlelab/vbash"
)

type shellPlatform struct {
	GOOS           string
	Candidates     []string
	ConfigureGroup func(*exec.Cmd, vbash.CmdState)
	KillGroup      func(*exec.Cmd) error
	ProtectSignals func()
	ExitCode       func(error) (int, bool)
	Programs       []string
	Capabilities   func(*profile) string
	DecodeOutput   func([]byte) string

	// PtyLend 表示 LendFull 借出的是 pty 设备：可同时承载子进程的 stdin/stdout/stderr
	// 并充当控制终端。为 false 时（Windows 的 CONIN$ 直通）只有 stdin 可用，
	// 输出仍走工具结果管道。
	PtyLend bool
	// ConfigureTTY 把借出的终端设备设为子进程的控制终端，由 front 决定是否授予：
	// 前台命令一律授予（与其 stdin 是否被重定向无关），后台命令只进独立会话。
	ConfigureTTY func(*exec.Cmd, *os.File, bool)
	// TTYEnv 把 GPG_TTY/SSH_TTY 等变量指向借出的设备（非 pty 平台为 no-op）。
	TTYEnv func([]string, *os.File) []string
}

var protectOnce sync.Once

func ProtectTerminalSignals() { protectOnce.Do(platform.ProtectSignals) }

func fillDefaults(p shellPlatform) shellPlatform {
	if p.GOOS == "" {
		p.GOOS = runtime.GOOS
	}
	if p.ConfigureGroup == nil {
		p.ConfigureGroup = noopConfigureGroup
	}
	if p.KillGroup == nil {
		p.KillGroup = defaultKillGroup
	}
	if p.ProtectSignals == nil {
		p.ProtectSignals = noopProtectSignals
	}
	if p.ExitCode == nil {
		p.ExitCode = defaultExitCode
	}
	if p.Capabilities == nil {
		p.Capabilities = noopCapabilities
	}
	if p.DecodeOutput == nil {
		p.DecodeOutput = identityOutput
	}
	if p.ConfigureTTY == nil {
		p.ConfigureTTY = noopConfigureTTY
	}
	if p.TTYEnv == nil {
		p.TTYEnv = identityTTYEnv
	}
	return p
}

func noopConfigureTTY(*exec.Cmd, *os.File, bool) {}

func identityTTYEnv(env []string, _ *os.File) []string { return env }

func identityOutput(b []byte) string { return string(b) }

func noopConfigureGroup(*exec.Cmd, vbash.CmdState) {}

func noopProtectSignals() {}

func noopCapabilities(*profile) string { return "" }

func defaultKillGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}

func defaultExitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	return exitErr.ExitCode(), true
}
