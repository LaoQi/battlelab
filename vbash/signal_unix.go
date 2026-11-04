//go:build linux || darwin

package vbash

import (
	"os/exec"
	"syscall"
)

func signalName(n int) string {
	switch syscall.Signal(n) {
	case syscall.SIGTERM:
		return "Terminated"
	case syscall.SIGKILL:
		return "Killed"
	case syscall.SIGINT:
		return "Interrupt"
	case syscall.SIGQUIT:
		return "Quit"
	case syscall.SIGPIPE:
		return "Broken pipe"
	case syscall.SIGHUP:
		return "Hangup"
	case syscall.SIGSEGV:
		return "Segmentation fault"
	case syscall.SIGABRT:
		return "Aborted"
	}
	return "Unknown signal"
}

func sysSignalNumber(err *exec.ExitError) int {
	if ws, ok := err.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return int(ws.Signal())
	}
	return 1
}
