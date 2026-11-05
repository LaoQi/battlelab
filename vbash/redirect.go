package vbash

import (
	"io"
	"os"
	"strings"
)

type redirectCleanup struct {
	files []*os.File
}

func (c *redirectCleanup) close() {
	for _, f := range c.files {
		f.Close()
	}
}

func (r *Runner) applyRedirects(cmd *Command, base *IO) (*IO, func(), error) {
	if len(cmd.Redirs) == 0 {
		return base, nil, nil
	}
	io := base.clone()
	cl := &redirectCleanup{}
	for _, redir := range cmd.Redirs {
		if err := r.applyRedirect(io, redir, cl); err != nil {
			cl.close()
			return nil, nil, err
		}
	}
	return io, cl.close, nil
}

func (r *Runner) applyRedirect(io *IO, redir *Redirect, cl *redirectCleanup) error {
	fd := redir.Fd
	if fd == DefaultFd {
		switch redir.Op {
		case OpLess, OpLessGreat, OpDLess, OpDLessDash, OpLessAmp:
			fd = 0
		case OpAndGreat, OpAndDGreat:
			fd = -2
		default:
			fd = 1
		}
	}
	target := ""
	if redir.Heredoc != nil {
		target = redir.Heredoc.Body
	} else if redir.Target.Text != "" || redir.Op != OpGreatAmp && redir.Op != OpLessAmp {
		fields, err := r.expn.expand(redir.Target)
		if err != nil {
			return err
		}
		if len(fields) == 0 {
			return runtimeErrf(redir.Pos, "%s: ambiguous redirect", redir.Op.String())
		}
		if len(fields) > 1 {
			return runtimeErrf(redir.Pos, "%s: ambiguous redirect", redir.Target.Text)
		}
		target = fields[0]
	}
	switch redir.Op {
	case OpAndGreat, OpAndDGreat:
		flags := os.O_CREATE | os.O_WRONLY
		if redir.Op == OpAndDGreat {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		f, err := os.OpenFile(r.hostPath(target), flags, 0o666)
		if err != nil {
			r.diagf(r.IO.Err, redir.Pos, "%s: %s", target, errText(err))
			return errRedirectFailed
		}
		cl.files = append(cl.files, f)
		io.withWriter(1, f)
		io.withWriter(2, f)
		return nil
	case OpGreatAmp, OpLessAmp:
		if n, ok := numericName(target); ok {
			src, found := io.writer(n)
			if !found {
				r.diagf(r.IO.Err, redir.Pos, "%s: Bad file descriptor", target)
				return errRedirectFailed
			}
			io.withWriter(fd, src)
			return nil
		}
		f, err := os.OpenFile(r.hostPath(target), os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			r.diagf(r.IO.Err, redir.Pos, "%s: %s", target, errText(err))
			return errRedirectFailed
		}
		cl.files = append(cl.files, f)
		io.withWriter(fd, f)
		return nil
	case OpLess:
		if isDevNull(target) {
			io.In = emptyReader()
			return nil
		}
		f, err := os.Open(r.hostPath(target))
		if err != nil {
			r.diagf(r.IO.Err, redir.Pos, "%s: %s", target, errText(err))
			return errRedirectFailed
		}
		cl.files = append(cl.files, f)
		io.In = f
		return nil
	case OpDLess, OpDLessDash:
		body := target
		if redir.Heredoc != nil && redir.Heredoc.ExpandBody {
			expanded, err := r.expandHeredoc(body)
			if err != nil {
				return err
			}
			body = expanded
		}
		io.In = stringReader(body)
		return nil
	case OpGreat, OpDGreat, OpClobber, OpLessGreat:
		flags := os.O_CREATE | os.O_WRONLY
		switch redir.Op {
		case OpDGreat:
			flags |= os.O_APPEND
		default:
			flags |= os.O_TRUNC
		}
		if isDevNull(target) {
			io.withWriter(fd, devNull())
			return nil
		}
		f, err := os.OpenFile(r.hostPath(target), flags, 0o666)
		if err != nil {
			r.diagf(r.IO.Err, redir.Pos, "%s: %s", target, errText(err))
			return errRedirectFailed
		}
		cl.files = append(cl.files, f)
		io.withWriter(fd, f)
		return nil
	}
	return runtimeErrf(redir.Pos, "不支持的重定向 %s", redir.Op.String())
}

func (r *Runner) expandHeredoc(body string) (string, error) {
	var b strings.Builder
	i := 0
	for i < len(body) {
		c := body[i]
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}
		rest := body[i:]
		segs, err := lexWordSegments(rest)
		if err != nil || len(segs) == 0 {
			b.WriteByte(c)
			i++
			continue
		}
		first := segs[0]
		if first.Kind == SegLiteral {
			b.WriteByte(c)
			i++
			continue
		}
		v, err := r.expn.parameter(first)
		if err != nil {
			return "", err
		}
		b.WriteString(v)
		i += len(first.Text)
	}
	return b.String(), nil
}

func errText(err error) string {
	if os.IsNotExist(err) {
		return "No such file or directory"
	}
	if os.IsPermission(err) {
		return "Permission denied"
	}
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

var errRedirectFailed = runtimeErrf(Pos{}, "redirect failed")

var _ = io.Discard
