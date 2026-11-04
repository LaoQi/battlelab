//go:build !(linux || darwin)

package vbash

import "os/exec"

func sysSignalNumber(err *exec.ExitError) int { return 1 }

func signalName(n int) string { return "Terminated" }
