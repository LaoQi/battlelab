//go:build linux || darwin

package shell

func normShellPath(p string) string { return p }
