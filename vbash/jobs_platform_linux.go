//go:build linux

package vbash

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

type procInfo struct {
	PID       int
	PPID      int
	PGID      int
	SID       int
	StartTime uint64
	State     string
}

func procTable() []procInfo {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	out := make([]procInfo, 0, len(ents))
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if p, ok := readProcInfo(pid); ok {
			out = append(out, p)
		}
	}
	return out
}

func procStartTime(pid int) uint64 {
	p, ok := readProcInfo(pid)
	if !ok {
		return 0
	}
	return p.StartTime
}

func readProcInfo(pid int) (procInfo, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procInfo{}, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return procInfo{}, false
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 4 {
		return procInfo{}, false
	}
	ppid, _ := strconv.Atoi(f[1])
	pgid, _ := strconv.Atoi(f[2])
	sid, _ := strconv.Atoi(f[3])
	info := procInfo{PID: pid, PPID: ppid, PGID: pgid, SID: sid, State: f[0]}
	if len(f) > 19 {
		info.StartTime, _ = strconv.ParseUint(f[19], 10, 64)
	}
	return info, true
}

func setSubreaper() bool {
	_, _, errno := syscall.RawSyscall(36, 1, 0, 0)
	return errno == 0
}
