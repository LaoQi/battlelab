package vbash

import (
	"errors"
	"os/exec"
	"sync"
)

type job struct {
	pid       int
	cmd       *exec.Cmd
	done      chan struct{}
	once      sync.Once
	code      int
	consumed  bool
	synthetic bool
}

func (j *job) finish(code int) {
	j.once.Do(func() {
		j.code = code
		close(j.done)
	})
}

func (j *job) wait() { j.finish(jobExitCode(j.cmd.Wait())) }

func (j *job) status() (int, bool) {
	select {
	case <-j.done:
		return j.code, true
	default:
		return 0, false
	}
}

func jobExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return plat.ExitCode(exitErr)
	}
	return 1
}

type jobTable struct {
	mu    sync.Mutex
	items []*job
	prev  *job
	sub   map[int]*job
}

func newJobTable() *jobTable {
	return &jobTable{sub: map[int]*job{}}
}

func (t *jobTable) add(pid int, cmd *exec.Cmd) *job {
	t.mu.Lock()
	defer t.mu.Unlock()
	j := &job{pid: pid, cmd: cmd, done: make(chan struct{})}
	t.items = append(t.items, j)
	t.sub[pid] = j
	t.prev = j
	return j
}

func (t *jobTable) addSynthetic() *job {
	t.mu.Lock()
	defer t.mu.Unlock()
	j := &job{done: make(chan struct{}), synthetic: true}
	t.items = append(t.items, j)
	return j
}

func (t *jobTable) pending() []*job {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*job, 0, len(t.items))
	for _, j := range t.items {
		if !j.consumed {
			out = append(out, j)
		}
	}
	return out
}

func (t *jobTable) byPid(pid int) *job {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sub[pid]
}

func (t *jobTable) lastPid() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.prev == nil {
		return 0, false
	}
	return t.prev.pid, true
}

func (t *jobTable) consume(j *job) {
	t.mu.Lock()
	defer t.mu.Unlock()
	j.consumed = true
	delete(t.sub, j.pid)
}

func (r *Runner) initJobs() {
	if r.jobs != nil {
		return
	}
	r.jobs = newJobTable()
	r.Env.jobs = r.jobs
}
