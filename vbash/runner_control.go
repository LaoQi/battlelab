package vbash

type loopSignal int

const (
	loopNone loopSignal = iota
	loopBreak
	loopContinue
)

func isLoopSignal(err error) (loopSignal, error) {
	if err == nil {
		return loopNone, nil
	}
	switch e := err.(type) {
	case *loopControl:
		if e.depth > 1 {
			return e.signal, &loopControl{signal: e.signal, depth: e.depth - 1}
		}
		return e.signal, nil
	}
	return loopNone, err
}

type loopControl struct {
	signal loopSignal
	depth  int
}

func (e *loopControl) Error() string {
	if e.signal == loopBreak {
		return "break"
	}
	return "continue"
}

func (r *Runner) runCompound(c *Compound, io *IO) (int, error) {
	switch c.Kind {
	case CompoundFor:
		return r.runFor(c, io)
	case CompoundWhile, CompoundUntil:
		return r.runWhile(c, io)
	case CompoundIf:
		return r.runIf(c, io)
	}
	return 1, runtimeErrf(c.Pos, "不支持的复合命令")
}

func (r *Runner) runFor(c *Compound, io *IO) (int, error) {
	var items []string
	if c.NoIn {
		items = append(items, r.Env.Args()...)
	} else {
		expanded, err := r.wordListToItems(c.Words, c)
		if err != nil {
			return 1, err
		}
		items = expanded
	}
	return r.runLoopBody(c, items, io)
}

func (r *Runner) wordListToItems(words []Word, c *Compound) ([]string, error) {
	var items []string
	lim := &braceLimits{}
	for _, w := range words {
		list, err := lim.word(w)
		if err != nil {
			return nil, err
		}
		for _, bw := range list {
			fs, err := r.expn.expand(bw)
			if err != nil {
				return nil, err
			}
			items = append(items, fs...)
		}
	}
	return items, nil
}

func (r *Runner) runLoopBody(c *Compound, items []string, io *IO) (int, error) {
	defer r.enterLoop()()
	status := 0
	for _, item := range items {
		r.Env.Set(c.Name, item, false)
		code, err := r.runList(c.Body, io)
		if err != nil {
			sig, rest := isLoopSignal(err)
			switch sig {
			case loopBreak:
				if rest != nil {
					return code, rest
				}
				return 0, nil
			case loopContinue:
				if rest != nil {
					return code, rest
				}
				status = 0
				continue
			}
			return code, err
		}
		status = code
	}
	r.Env.SetStatus(status)
	return status, nil
}

func (r *Runner) runWhile(c *Compound, io *IO) (int, error) {
	defer r.enterLoop()()
	status := 0
	for {
		code, err := r.inCondition(func() (int, error) { return r.runList(c.Cond, io) })
		if err != nil {
			return code, err
		}
		ok := code == 0
		if c.Kind == CompoundUntil {
			ok = !ok
		}
		if !ok {
			break
		}
		code, err = r.runList(c.Body, io)
		if err != nil {
			sig, rest := isLoopSignal(err)
			switch sig {
			case loopBreak:
				if rest != nil {
					return code, rest
				}
				return 0, nil
			case loopContinue:
				if rest != nil {
					return code, rest
				}
				continue
			}
			return code, err
		}
		status = code
	}
	r.Env.SetStatus(status)
	return status, nil
}

func (r *Runner) runIf(c *Compound, io *IO) (int, error) {
	code, err := r.inCondition(func() (int, error) { return r.runList(c.Cond, io) })
	if err != nil {
		return code, err
	}
	if code == 0 {
		return r.runList(c.Body, io)
	}
	for _, clause := range c.Elifs {
		code, err := r.inCondition(func() (int, error) { return r.runList(clause.Cond, io) })
		if err != nil {
			return code, err
		}
		if code == 0 {
			return r.runList(clause.Body, io)
		}
	}
	if c.Else != nil {
		return r.runList(c.Else, io)
	}
	r.Env.SetStatus(0)
	return 0, nil
}
