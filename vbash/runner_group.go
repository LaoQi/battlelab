package vbash

type funcDef struct {
	name string
	body *Command
}

func (r *Runner) runSubshell(list *List, io *IO) (int, error) {
	sub := &Runner{Env: r.Env.Clone(), ShellName: r.ShellName, Ctx: r.Ctx, Exec: r.Exec}
	sub.IO = io
	sub.expn = &expansion{env: sub.Env, runs: sub, io: io}
	sub.ErrExit = r.ErrExit
	sub.funcs = r.funcs
	code, err := sub.runList(list, io)
	var exitReq ExitRequest
	if err != nil && asExit(err, &exitReq) {
		return exitReq.Code, nil
	}
	return code, err
}

func (r *Runner) runBraceGroup(list *List, io *IO) (int, error) {
	return r.runList(list, io)
}

func asExit(err error, target *ExitRequest) bool {
	e, ok := err.(ExitRequest)
	if ok {
		*target = e
		return true
	}
	return false
}

func (r *Runner) registerFunc(name string, body *Command) {
	if r.funcs == nil {
		r.funcs = map[string]*funcDef{}
	}
	r.funcs[name] = &funcDef{name: name, body: body}
}

func (r *Runner) callFunc(name string, argv []string, io *IO) (int, bool, error) {
	if r.funcs == nil {
		return 0, false, nil
	}
	fn, ok := r.funcs[name]
	if !ok {
		return 0, false, nil
	}
	savedEnv := r.Env
	savedExpn := r.expn
	savedArgs := r.Env.Args()
	local := r.Env.Clone()
	local.SetArgs(argv[1:])
	local.arg0 = name
	local.inFunc = true
	r.Env = local
	r.expn = &expansion{env: local, runs: r, io: r.IO}
	defer func() {
		r.Env = savedEnv
		r.expn = savedExpn
		savedEnv.SetArgs(savedArgs)
	}()
	r.saveDepth++
	code, err := r.runFuncBody(fn, io)
	r.saveDepth--
	var ret ReturnRequest
	if err != nil {
		if e, ok := err.(ReturnRequest); ok {
			ret = e
			r.Env = savedEnv
			r.expn = savedExpn
			savedEnv.SetStatus(ret.Code)
			return ret.Code, true, nil
		}
	}
	return code, true, err
}

func (r *Runner) runFuncBody(fn *funcDef, io *IO) (int, error) {
	if fn.body.Group != nil {
		return r.runList(fn.body.Group, io)
	}
	return r.runCommand(fn.body, io)
}
