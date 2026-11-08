package shell

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/LaoQi/battlelab/vbash"
)

func (t *Tool) runVbash(ctx context.Context, command string, timeoutSec int, dir string) *Result {
	res := &Result{Command: command, Cwd: dir}
	lease, _ := t.console.LendStdin()
	if lease == nil {
		lease = nullLease{}
	}
	defer lease.Release()
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var stdout, stderr streamCapture
	stdout.chunks = &res.Stdout
	stderr.chunks = &res.Stderr

	cfg := vbash.Config{
		Dir:          dir,
		Home:         t.homeDir(),
		Env:          t.environList(),
		Arg0:         vbashArg0(t.profile),
		Stdout:       &stdout,
		Stderr:       &stderr,
		Stdin:        strings.NewReader(""),
		ShellName:    vbashShellName(t.profile),
		ConfigureCmd: platform.ConfigureGroup,
		CancelCmd:    platform.KillGroup,
		Fence:        t.fence,
	}
	if f := lease.Stdin(); f != nil {
		cfg.Stdin = f
	}
	if runCtx.Err() != nil {
		res.Interrupted = true
		res.NotStarted = true
		res.Duration = time.Since(start)
		return res
	}
	var code int
	var runErr error
	_, abandoned := waitBounded(func() error {
		code, runErr = vbash.RunScript(runCtx, command, cfg)
		return nil
	}, runCtx.Done(), shellKillGrace)
	stdout.finish()
	stderr.finish()
	res.Duration = time.Since(start)

	if abandoned {
		res.TimedOut = true
		res.Err = MsgAbandoned
		return res
	}
	if ctx.Err() != nil {
		res.Interrupted = true
		return res
	}
	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		return res
	}
	res.ExitCode = code
	t.classifyVbashError(res, runErr)
	return res
}

func vbashArg0(p *profile) string {
	if p.Kind == KindPosix && !p.Builtin {
		return filepath.Base(p.Path)
	}
	return "bash"
}

func vbashShellName(p *profile) string {
	if p.Kind == KindPosix && !p.Builtin {
		return p.Path
	}
	return BuiltinShellName
}

func (t *Tool) classifyVbashError(res *Result, err error) {
	if err == nil {
		return
	}
	var exitReq vbash.ExitRequest
	if errors.As(err, &exitReq) {
		return
	}
	var syntax *vbash.SyntaxError
	if errors.As(err, &syntax) {
		return
	}
	var unsupported *vbash.UnsupportedError
	if errors.As(err, &unsupported) {
		res.Err = strings.TrimPrefix(unsupported.Error(), "vbash: ")
		return
	}
	res.Err = err.Error()
}

func (t *Tool) homeDir() string {
	if t.home != "" {
		return t.home
	}
	for _, kv := range t.environList() {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			return v
		}
	}
	return ""
}
