package vbash

import (
	"context"
	"errors"
)

func (r *Runner) runBackground(and *AndOr, io *IO) (int, error) {
	r.initJobs()
	if single := backgroundCommand(and); single != nil && !asyncNeedsSubshell(r, single) {
		if code, ok := r.startAsync(single, io, and.Pos); ok {
			return code, nil
		}
	}
	sub := r.branch(r.Env.Clone(), io.clone())
	sub.Ctx = backgroundCtx(r.Ctx)
	sub.bg = true
	sub.initJobs()
	j := r.jobs.addSynthetic()
	go func() {
		code, err := sub.runAndOr(and, io)
		var exitReq ExitRequest
		if err != nil && errors.As(err, &exitReq) {
			code = exitReq.Code
		}
		j.finish(code)
	}()
	r.Env.SetStatus(0)
	return 0, nil
}

func backgroundCommand(and *AndOr) *Command {
	if len(and.Pipes) != 1 {
		return nil
	}
	pl := and.Pipes[0].Pipeline
	if pl == nil || pl.Bang || len(pl.Commands) != 1 {
		return nil
	}
	cmd := pl.Commands[0]
	if cmd.Compound != nil || cmd.Func != nil || cmd.Group != nil || isExecCommand(cmd) {
		return nil
	}
	return cmd
}

func asyncNeedsSubshell(r *Runner, cmd *Command) bool {
	name := literalCommandName(cmd)
	return name != "" && (canBuiltin(name) || r.funcs[name] != nil)
}

func literalCommandName(cmd *Command) string {
	if len(cmd.Words) == 0 {
		return ""
	}
	first := cmd.Words[0]
	if first.Quoted() {
		return ""
	}
	for _, seg := range first.Segments {
		if seg.Kind != SegLiteral || seg.Mode != ModeUnquoted {
			return ""
		}
	}
	return first.Text
}

func (r *Runner) startAsync(cmd *Command, io *IO, pos Pos) (int, bool) {
	io2, cleanup, err := r.applyRedirects(cmd, io)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		r.Env.SetStatus(1)
		return 1, true
	}
	drop := func() {
		if cleanup != nil {
			cleanup()
		}
	}
	if len(cmd.Words) == 0 {
		drop()
		env := r.Env.Clone()
		savedEnv := r.Env
		r.Env = env
		defer func() { r.Env = savedEnv }()
		for _, a := range cmd.Assigns {
			if _, err := r.assign(a, io2); err != nil {
				return 1, true
			}
		}
		savedEnv.SetStatus(0)
		return 0, true
	}
	argv, err := r.expn.expandWords(cmd.Words)
	if err != nil {
		drop()
		return 1, true
	}
	if len(argv) == 0 {
		drop()
		r.Env.SetStatus(0)
		return 0, true
	}
	env := r.Env
	if len(cmd.Assigns) > 0 {
		env = r.Env.Clone()
		for _, a := range cmd.Assigns {
			value, err := r.expn.expandToString(a.Value)
			if err != nil {
				drop()
				return 1, true
			}
			env.SetExported(a.Name, value)
		}
	}
	full, _, ok := r.resolveExternal(argv[0], io2, pos)
	if !ok {
		drop()
		return 1, true
	}
	bgIO := io2.clone()
	if io2.In == io.In {
		bgIO.In = emptyReader()
	}
	sub := r.branch(env, bgIO)
	// 这是 `&` 起的后台作业：标记下去，让 ConfigureCmd 知道不该授予终端。
	sub.bg = true
	execCmd := sub.newCmd(backgroundCtx(r.Ctx), full, argv, bgIO)
	execCmd.Cancel = nil
	execCmd.WaitDelay = 0
	if err := execCmd.Start(); err != nil {
		drop()
		r.diagf(bgIO.Err, pos, "%s: %s", argv[0], errText(err))
		return 1, true
	}
	j := r.jobs.add(execCmd.Process.Pid, execCmd)
	r.fence.register(execCmd.Process.Pid, j.done)
	go func() {
		j.wait()
		drop()
	}()
	r.Env.SetStatus(0)
	return 0, true
}

func backgroundCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithoutCancel(ctx)
}
