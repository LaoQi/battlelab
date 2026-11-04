package vbash

func (r *Runner) checkErrExit() error {
	if !r.ErrExit || r.condDepth > 0 || r.exemptErrExit {
		return nil
	}
	code := r.Env.Status()
	if code == 0 {
		return nil
	}
	return ExitRequest{Code: code}
}

func (r *Runner) inCondition(fn func() (int, error)) (int, error) {
	r.condDepth++
	defer func() { r.condDepth-- }()
	return fn()
}
