//go:build linux || darwin

package vbash

import (
	"os"
	"os/exec"
	"syscall"
)

func posixPlatform() platformOps {
	return platformOps{
		DevNull:      []string{"/dev/null"},
		DefaultPath:  "/usr/local/bin:/usr/bin:/bin",
		PathSep:      ":",
		ExecSuffixes: []string{""},
		Argv0:        identityPath,
		IsExec:       func(_ string, st os.FileInfo) bool { return !st.IsDir() && st.Mode()&0o111 != 0 },
		SignalNumber: posixSignalNumber,
		ExitCode:     posixExitCode,
		SignalName:   posixSignalName,
	}
}

func posixSignalName(n int) string {
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

func posixSignalNumber(err *exec.ExitError) int {
	if ws, ok := err.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return int(ws.Signal())
	}
	return 1
}

func posixExitCode(err *exec.ExitError) int {
	if code := err.ExitCode(); code >= 0 {
		return code
	}
	return 128 + posixSignalNumber(err)
}
