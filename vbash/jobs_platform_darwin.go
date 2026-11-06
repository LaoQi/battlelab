//go:build darwin

package vbash

type procInfo struct {
	PID       int
	PPID      int
	PGID      int
	SID       int
	StartTime uint64
	State     string
}

func procTable() []procInfo { return nil }

func procStartTime(pid int) uint64 { return 0 }

func setSubreaper() bool { return false }
