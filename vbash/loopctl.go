package vbash

func (r *Runner) loopDepth() int { return r.loopNest }

func (r *Runner) enterLoop() func() {
	r.loopNest++
	return func() { r.loopNest-- }
}

func (r *Runner) outsideLoop(name string, pos Pos) (int, error) {
	r.diagfName(r.IO.Err, pos, name, "only meaningful in a `for', `while', or `until' loop")
	return 0, nil
}
