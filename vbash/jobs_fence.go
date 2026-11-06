package vbash

import (
	"io"
	"sync"
	"time"
)

const jobKillGrace = 2 * time.Second

const sigKill = 9

// SyncWriter 串行化并发写，保证后台作业与前台命令交替输出时不会撕裂调用方的 writer。
// 调用方可对 Out/Err 各自包一层再交给 Config，两侧不共享锁，避免无谓互斥。
type SyncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func NewSyncWriter(w io.Writer) *SyncWriter {
	if w == nil {
		return nil
	}
	if sw, ok := w.(*SyncWriter); ok {
		return sw
	}
	return &SyncWriter{w: w}
}

func (s *SyncWriter) Write(p []byte) (int, error) {
	if s == nil || s.w == nil {
		return len(p), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// EnableSubreaper 让本进程成为"子收割者"：后代进程若成为孤儿会被重新挂到本进程下，
// 从而在退出收割时仍可被找到。POSIX 上为 PR_SET_CHILD_SUB_REAPER，零权限即可，
// 不改变任何现有进程行为；调用方应在主进程启动时调用一次。
func EnableSubreaper() bool { return setSubreaper() }

// Fence 进程围栏：登记 `&` 分支起的进程（管理范围即登记范围；前台命令的生命周期
// 归 CancelCmd/ctx 超时，不进围栏）。调用方在主进程退出路径上调用一次 Reap 统一收割；
// 判据为会话：sid 不同于主进程会话的整棵进程树视为主动逃逸（setsid/double-fork），一律放行。
type Fence struct {
	mu    sync.Mutex
	procs map[int]procRef
	dead  bool
}

type procRef struct {
	pid       int
	startTime uint64
	done      chan struct{}
}

func NewFence() *Fence { return &Fence{procs: map[int]procRef{}} }

func (f *Fence) register(pid int, done chan struct{}) {
	if f == nil || pid <= 0 {
		return
	}
	start := procStartTime(pid)
	f.mu.Lock()
	if f.dead {
		f.mu.Unlock()
		plat.KillProcessGroup(pid, sigKill)
		return
	}
	f.procs[pid] = procRef{pid: pid, startTime: start, done: done}
	f.mu.Unlock()
}

// Reap 收割围栏登记的后台分支进程（含跨轮存活者），并回收僵尸子进程。
// 返回被清理的进程组数。
func (f *Fence) Reap() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	f.dead = true
	refs := make([]procRef, 0, len(f.procs))
	for _, r := range f.procs {
		refs = append(refs, r)
	}
	f.procs = nil
	f.mu.Unlock()

	mainSid, mainSidOK := procSid(0)
	if !mainSidOK {
		mainSid = -1
	}
	table := procTable()
	byPid := make(map[int]procInfo, len(table))
	children := make(map[int][]int, len(table))
	for _, p := range table {
		byPid[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}

	groups := make(map[int]bool)
	var walk func(pid int)
	walk = func(pid int) {
		for _, child := range children[pid] {
			p := byPid[child]
			if p.SID != mainSid || p.State == "Z" {
				continue
			}
			groups[p.PGID] = true
			walk(child)
		}
	}

	for _, ref := range refs {
		if p, ok := byPid[ref.pid]; ok {
			if ref.startTime != 0 && p.StartTime != 0 && p.StartTime != ref.startTime {
				continue
			}
			if p.SID != mainSid {
				continue
			}
			groups[p.PGID] = true
			walk(ref.pid)
			continue
		}
		if sid, ok := procSid(ref.pid); ok && sid != mainSid {
			continue
		}
		groups[ref.pid] = true
	}

	n := 0
	for g := range groups {
		if plat.KillProcessGroup(g, sigKill) == nil {
			n++
		}
	}
	deadline := time.After(jobKillGrace)
wait:
	for _, ref := range refs {
		if ref.done == nil {
			continue
		}
		select {
		case <-ref.done:
		case <-deadline:
			break wait
		}
	}
	reapChildren()
	return n
}
