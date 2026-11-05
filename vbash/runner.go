package vbash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type Runner struct {
	Env       *Env
	IO        *IO
	ShellName string
	Ctx       context.Context
	Verbose   bool
	Exec      Handler

	ConfigureCmd func(*exec.Cmd)
	CancelCmd    func(*exec.Cmd) error

	expn          *expansion
	ErrExit       bool
	subStatus     int
	funcs         map[string]*funcDef
	saveDepth     int
	condDepth     int
	exemptErrExit bool
}

type Handler func(ctx context.Context, r *Runner, argv []string, io *IO) (int, error)

type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func Run(ctx context.Context, src string, cfg Config) (ExecResult, error) {
	list, err := Parse(src)
	if err != nil {
		return ExecResult{ExitCode: 2}, err
	}
	r := NewRunner(ctx, cfg)
	code, runErr := r.RunList(list)
	res := ExecResult{ExitCode: code}
	if b, ok := cfg.Stdout.(interface{ String() string }); ok && b != nil {
		res.Stdout = b.String()
	}
	if b, ok := cfg.Stderr.(interface{ String() string }); ok && b != nil {
		res.Stderr = b.String()
	}
	return res, runErr
}

type Config struct {
	Dir          string
	Home         string
	Env          []string
	Args         []string
	Arg0         string
	Stdout       io.Writer
	Stderr       io.Writer
	Stdin        io.Reader
	ShellName    string
	Handler      Handler
	ConfigureCmd func(*exec.Cmd)
	CancelCmd    func(*exec.Cmd) error
}

func NewRunner(ctx context.Context, cfg Config) *Runner {
	if ctx == nil {
		ctx = context.Background()
	}
	env := NewEnv()
	env.home = plat.FromHost(cfg.Home)
	env.dir = plat.FromHost(cfg.Dir)
	env.arg0 = cfg.Arg0
	if env.arg0 == "" {
		env.arg0 = "bash"
	}
	if len(cfg.Args) > 0 {
		env.SetArgs(cfg.Args)
	}
	for _, kv := range cfg.Env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			env.SetExported(kv[:i], internalVar(kv[:i], kv[i+1:]))
		}
	}
	name := cfg.ShellName
	if name == "" {
		name = defaultShellName
	}
	r := &Runner{
		Env:          env,
		Ctx:          ctx,
		ShellName:    name,
		Exec:         cfg.Handler,
		ConfigureCmd: cfg.ConfigureCmd,
		CancelCmd:    cfg.CancelCmd,
	}
	out := cfg.Stdout
	errw := cfg.Stderr
	in := cfg.Stdin
	r.IO = &IO{In: in, Out: out, Err: errw}
	r.expn = &expansion{env: env, runs: r, io: r.IO}
	return r
}

func (r *Runner) warnUnsupported(feature string, pos Pos) {
	r.diagf(r.IO.Err, pos, "%s: %s: unsupported by vbash", feature, "not implemented")
}

func (r *Runner) RunList(list *List) (int, error) {
	return r.runList(list, r.IO)
}

func (r *Runner) branch(env *Env, io *IO) *Runner {
	sub := &Runner{
		Env:          env,
		IO:           io,
		ShellName:    r.ShellName,
		Ctx:          r.Ctx,
		Exec:         r.Exec,
		ConfigureCmd: r.ConfigureCmd,
		CancelCmd:    r.CancelCmd,
	}
	sub.expn = &expansion{env: env, runs: sub, io: io}
	return sub
}

func (r *Runner) runList(list *List, io *IO) (int, error) {
	code := r.Env.Status()
	for _, item := range list.Items {
		if err := r.Ctx.Err(); err != nil {
			return r.Env.Status(), err
		}
		c, err := r.runAndOr(item.AndOr, io)
		code = c
		if err != nil {
			return code, err
		}
		if len(item.AndOr.Pipes) == 1 {
			if e := r.checkErrExit(); e != nil {
				return code, e
			}
		}
	}
	r.Env.SetStatus(code)
	return code, nil
}

func (r *Runner) runAndOr(and *AndOr, io *IO) (int, error) {
	code := 0
	var err error
	skip := false
	chain := len(and.Pipes) > 1
	lastOp := OpNone
	if chain {
		lastOp = and.Pipes[len(and.Pipes)-2].Op
	}
	reached := false
	for i, p := range and.Pipes {
		if i > 0 {
			switch and.Pipes[i-1].Op {
			case OpAnd:
				skip = code != 0
			case OpOr:
				skip = code == 0
			default:
				skip = false
			}
		}
		if skip {
			continue
		}
		if chain {
			r.condDepth++
		}
		code, err = r.runPipeline(p.Pipeline, io)
		if chain {
			r.condDepth--
		}
		if err != nil {
			return code, err
		}
		r.Env.SetStatus(code)
		reached = i == len(and.Pipes)-1
	}
	if chain && reached && code != 0 && r.ErrExit && r.condDepth == 0 {
		if lastOp != OpOr {
			return code, ExitRequest{Code: code}
		}
	}
	return code, nil
}

func (r *Runner) runPipeline(pl *Pipeline, io *IO) (int, error) {
	if len(pl.Commands) == 1 {
		code, err := r.runCommand(pl.Commands[0], io)
		if pl.Bang && err == nil {
			code = btoi(code == 0)
			r.Env.SetStatus(code)
		}
		return code, err
	}
	type result struct {
		code int
		err  error
	}
	results := make([]result, len(pl.Commands))
	var wg sync.WaitGroup
	in := io.In
	var pending *os.File
	base := r.Env.Clone()
	for i, cmd := range pl.Commands {
		last := i == len(pl.Commands)-1
		branch := r.branch(base.Clone(), io)
		childIO := io.clone()
		childIO.In = in
		consumed := pending
		if !last {
			pr, pw, err := os.Pipe()
			if err != nil {
				if consumed != nil {
					consumed.Close()
				}
				wg.Wait()
				return 1, err
			}
			childIO.Out = pw
			pending = pr
			in = pr
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				defer func() {
					pw.Close()
					if consumed != nil {
						consumed.Close()
					}
				}()
				code, runErr := branch.runCommand(cmd, childIO)
				results[idx] = result{code: code, err: runErr}
			}()
			continue
		}
		code, runErr := branch.runCommand(cmd, childIO)
		if consumed != nil {
			consumed.Close()
		}
		results[i] = result{code: code, err: runErr}
	}
	wg.Wait()
	for _, res := range results {
		if res.err != nil {
			r.Env.SetStatus(results[len(results)-1].code)
			return results[len(results)-1].code, res.err
		}
	}
	code := results[len(results)-1].code
	if pl.Bang {
		code = btoi(code == 0)
	}
	r.Env.SetStatus(code)
	return code, nil
}

func (r *Runner) runCommand(cmd *Command, io *IO) (int, error) {
	if cmd.Compound != nil {
		io2, cleanup, err := r.applyRedirects(cmd, io)
		if err != nil {
			if errors.Is(err, errRedirectFailed) {
				r.Env.SetStatus(1)
				return 1, nil
			}
			return 1, err
		}
		if cleanup != nil {
			defer cleanup()
		}
		return r.runCompound(cmd.Compound, io2)
	}
	if cmd.Func != nil {
		r.registerFunc(cmd.Func.Name, cmd.Func.Body)
		r.Env.SetStatus(0)
		return 0, nil
	}
	if cmd.Group != nil {
		io2, gcleanup, err := r.applyRedirects(cmd, io)
		if err != nil {
			if errors.Is(err, errRedirectFailed) {
				r.Env.SetStatus(1)
				return 1, nil
			}
			return 1, err
		}
		if gcleanup != nil {
			defer gcleanup()
		}
		var code int
		var gerr error
		if cmd.GroupKind == GroupSubshell {
			code, gerr = r.runSubshell(cmd.Group, io2)
		} else {
			code, gerr = r.runBraceGroup(cmd.Group, io2)
		}
		r.Env.SetStatus(code)
		return code, gerr
	}
	io, cleanup, err := r.applyRedirects(cmd, io)
	if err != nil {
		if errors.Is(err, errRedirectFailed) {
			r.Env.SetStatus(1)
			return 1, nil
		}
		return 1, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(cmd.Words) == 0 {
		status := 0
		for _, a := range cmd.Assigns {
			code, err := r.assign(a, io)
			if err != nil {
				return 1, err
			}
			if code >= 0 {
				status = code
			}
		}
		r.Env.SetStatus(status)
		return status, nil
	}
	argv, err := r.expn.expandWords(cmd.Words)
	if err != nil {
		return 1, err
	}
	if len(argv) == 0 {
		r.Env.SetStatus(0)
		return 0, nil
	}
	assignments := make([]Assign, 0, len(cmd.Assigns))
	for _, a := range cmd.Assigns {
		assignments = append(assignments, a)
	}
	if len(assignments) > 0 {
		saved := r.Env.Clone()
		for _, a := range assignments {
			value, err := r.expn.expandToString(a.Value)
			if err != nil {
				return 1, err
			}
			r.Env.SetExported(a.Name, value)
		}
		code, err := r.dispatch(argv, io, cmd.Pos)
		r.Env.vars = saved.vars
		r.Env.SetStatus(code)
		return code, err
	}
	code, err := r.dispatch(argv, io, cmd.Pos)
	r.Env.SetStatus(code)
	return code, err
}

func (r *Runner) dispatch(argv []string, io *IO, pos Pos) (int, error) {
	name := argv[0]
	if code, ok, err := r.callFunc(name, argv, io); ok {
		return code, err
	}
	if canBuiltin(name) {
		code, handled, err := r.runBuiltin(name, argv, io, pos)
		if handled {
			return code, err
		}
	}
	return r.runExternal(argv, io, pos)
}

func (r *Runner) assign(a Assign, io *IO) (int, error) {
	hasSub := false
	for _, seg := range a.Value.Segments {
		if seg.Kind == SegCmdSub {
			hasSub = true
		}
	}
	r.subStatus = -1
	value, err := r.expn.expandToString(a.Value)
	if err != nil {
		return 1, err
	}
	status := -1
	if hasSub && r.subStatus >= 0 {
		status = r.subStatus
	}
	r.Env.Set(a.Name, value, false)
	if status >= 0 {
		r.Env.SetStatus(status)
	}
	return status, nil
}

func (r *Runner) commandSubstitution(seg Segment) (string, error) {
	body := seg.Inner
	var out strings.Builder
	subIO := &IO{In: r.IO.In, Out: &out, Err: r.IO.Err}
	var list *List
	if seg.Sub != nil {
		list = seg.Sub
	} else {
		parsed, err := Parse(body)
		if err != nil {
			return "", err
		}
		list = parsed
	}
	sub := r.branch(r.Env.Clone(), subIO)
	code, err := sub.runList(list, subIO)
	if err != nil {
		var exitReq ExitRequest
		if !errors.As(err, &exitReq) {
			return "", err
		}
	}
	r.subStatus = code
	return strings.TrimRight(out.String(), "\n"), nil
}

func (r *Runner) expandToString(w Word) (string, error) {
	fields, err := r.expn.expand(w)
	if err != nil {
		return "", err
	}
	return strings.Join(fields, " "), nil
}

func (r *Runner) warnHeredoc(errw io.Writer, h UnterminatedHeredoc) {
	if errw == nil {
		return
	}
	fmt.Fprintf(errw, "%s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')\n",
		r.name(), h.EndLine, h.Line, h.Wanted)
}

func RunScript(ctx context.Context, src string, cfg Config) (int, error) {
	r := NewRunner(ctx, cfg)
	if strings.TrimSpace(src) == "" {
		return 0, nil
	}
	lx := newLexer(src)
	p := &parser{lex: lx}
	list, err := p.parseSource()
	if err != nil {
		r.reportSyntaxError(err, src)
		return 2, err
	}
	for _, h := range lx.unterminatedHeredocs() {
		r.warnHeredoc(r.IO.Err, h)
	}
	return r.RunList(list)
}

func (r *Runner) reportSyntaxError(err error, src string) {
	var se *SyntaxError
	if !errors.As(err, &se) {
		return
	}
	if se.EOF {
		r.eofDiag(r.IO.Err, se.Msg)
		return
	}
	token := se.Token
	if token == "" {
		token = se.Msg
	}
	r.syntaxDiag(r.IO.Err, se.Pos, token, firstLine(src))
}

func firstLine(src string) string {
	if i := strings.IndexByte(src, '\n'); i >= 0 {
		return src[:i]
	}
	return src
}
