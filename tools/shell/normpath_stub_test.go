//go:build !linux && !darwin && !windows

package shell

func normShellPath(p string) string { return p }
