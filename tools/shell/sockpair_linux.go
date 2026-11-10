//go:build linux

package shell

import "golang.org/x/sys/unix"

// unixSocketpair 建一对 AX_UNIX 流式 socket；CLOEXEC 由 os/exec 在 ExtraFiles
// 传递时自动清除，这里加上不影响子进程继承。
func unixSocketpair() ([2]int, error) {
	return unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
}
