package vbash

import "errors"

func isExecCommand(cmd *Command) bool {
	if len(cmd.Assigns) > 0 || len(cmd.Words) == 0 {
		return false
	}
	first := cmd.Words[0]
	if first.Quoted() || first.Text != "exec" {
		return false
	}
	for _, seg := range first.Segments {
		if seg.Kind != SegLiteral || seg.Mode != ModeUnquoted {
			return false
		}
	}
	return true
}

func (r *Runner) runExec(cmd *Command, io *IO) (int, error) {
	argv, err := r.expn.expandWords(cmd.Words)
	if err != nil {
		r.Env.SetStatus(1)
		return 1, err
	}
	if len(argv) == 0 || argv[0] != "exec" {
		r.Env.SetStatus(0)
		return 0, nil
	}
	args := argv[1:]
	for len(args) > 0 {
		switch args[0] {
		case "-c", "-l":
			args = args[1:]
			continue
		case "-a":
			if len(args) > 1 {
				args = args[2:]
				continue
			}
			args = nil
			continue
		}
		break
	}
	if len(args) == 0 {
		if err := r.applyExecRedirects(cmd); err != nil {
			if errors.Is(err, errRedirectFailed) {
				r.Env.SetStatus(1)
				return 1, nil
			}
			r.Env.SetStatus(1)
			return 1, err
		}
		r.Env.SetStatus(0)
		return 0, nil
	}
	target := r.IO.clone()
	cl := &redirectCleanup{}
	for _, redir := range cmd.Redirs {
		if err := r.applyRedirect(target, redir, cl); err != nil {
			cl.close()
			if errors.Is(err, errRedirectFailed) {
				r.Env.SetStatus(1)
				return 1, nil
			}
			r.Env.SetStatus(1)
			return 1, err
		}
	}
	code, err := r.dispatch(args, target, cmd.Pos)
	cl.close()
	r.Env.SetStatus(code)
	if err != nil {
		return code, err
	}
	return code, ExitRequest{Code: code}
}
