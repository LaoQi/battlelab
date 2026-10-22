package tools

import (
	"github.com/LaoQi/battlelab/agent"
	"github.com/LaoQi/battlelab/tools/builtin"
	"github.com/LaoQi/battlelab/tools/image"
	"github.com/LaoQi/battlelab/tools/shell"
)

type Options struct {
	ShellOverride string
	Home          string
	Workspace     func() string
	Console       shell.Console
	LookPath      func(string) (string, error)
	Programs      []string
	ImageMaxBytes int
	ImageResize   bool
	ImageDetail   string
}

func Shell(o Options) (*shell.Tool, error) {
	return shell.New(shell.Config{
		Override:  o.ShellOverride,
		LookPath:  o.LookPath,
		Home:      o.Home,
		Workspace: o.Workspace,
		Console:   o.Console,
		Programs:  o.Programs,
	})
}

func Standard(o Options) ([]agent.Tool, error) {
	sh, err := Shell(o)
	if err != nil {
		return nil, err
	}
	list := []agent.Tool{sh, image.New(image.Config{
		Workspace: o.Workspace,
		Home:      o.Home,
		MaxBytes:  o.ImageMaxBytes,
		Resize:    o.ImageResize,
		Detail:    o.ImageDetail,
	})}
	return append(list, builtin.Tools()...), nil
}
