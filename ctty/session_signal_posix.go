//go:build linux || darwin

package ctty

import (
	"os/signal"

	"golang.org/x/sys/unix"
)

// signalIgnoreTTOU 忽略 SIGTTOU：宿主在后台进程组里调 tcsetpgrp 时，
// 默认会因 SIGTTOU 被停住。
func signalIgnoreTTOU() {
	signal.Ignore(unix.SIGTTOU)
}
