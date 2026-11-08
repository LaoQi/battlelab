package shell

import (
	"context"
	"io"
	"os/exec"
	"strings"

	"github.com/LaoQi/battlelab/vbash"
)

type RunOptions struct {
	Dir    string
	Home   string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Fence  *vbash.Fence
}

func (inv Invocation) Run(ctx context.Context, script string, o RunOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if inv.Builtin {
		return inv.runBuiltin(ctx, script, o)
	}
	return inv.runArgv(ctx, script, o)
}

func (inv Invocation) runBuiltin(ctx context.Context, script string, o RunOptions) error {
	cfg := vbash.Config{
		Dir:          o.Dir,
		Home:         o.Home,
		Env:          o.Env,
		Arg0:         "bash",
		Stdout:       o.Stdout,
		Stderr:       o.Stderr,
		Stdin:        o.Stdin,
		ShellName:    BuiltinShellName,
		ConfigureCmd: platform.ConfigureGroup,
		CancelCmd:    platform.KillGroup,
		Fence:        o.Fence,
	}
	if cfg.Stdin == nil {
		cfg.Stdin = emptyStdin()
	}
	_, err := vbash.RunScript(ctx, script, cfg)
	return err
}

func (inv Invocation) runArgv(ctx context.Context, script string, o RunOptions) error {
	if len(inv.Argv) == 0 {
		return ErrNoInvocation
	}
	argv := make([]string, 0, len(inv.Argv)+1)
	argv = append(argv, inv.Argv...)
	argv = append(argv, script)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	cmd.Stdin = o.Stdin
	cmd.Stdout = o.Stdout
	cmd.Stderr = o.Stderr
	platform.ConfigureGroup(cmd)
	cmd.Cancel = func() error { return platform.KillGroup(cmd) }
	cmd.WaitDelay = shellWaitDelay
	return cmd.Run()
}

func emptyStdin() io.Reader { return strings.NewReader("") }
