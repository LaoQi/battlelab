//go:build linux || darwin

package vbash

import "syscall"

func reapChildren() int {
	n := 0
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			return n
		}
		n++
	}
}

func procSid(pid int) (int, bool) {
	r0, _, errno := syscall.RawSyscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if errno != 0 {
		return 0, false
	}
	return int(r0), true
}
