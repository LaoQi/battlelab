//go:build linux || darwin

package ctty

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// BecomeSessionLeader 让当前进程成为新会话的首进程，并把 dev 设为它的控制终端。
//
// 仅供**独立会话宿主进程**使用：调用后本进程脱离原会话（也就脱离调用方的控制终端），
// 因此不能在宿主 REPL/服务器进程里直接调用。宿主进程随后 fork 出各条命令，
// 命令只 setpgid、不 setsid，于是任意多条命令（含并发存活的管道各段）都能共享
// 这一个控制终端——这正是真实 bash 的模型。
func BecomeSessionLeader(dev *os.File) error {
	if dev == nil {
		return syscall.EINVAL
	}
	if _, err := unix.Setsid(); err != nil {
		return err
	}
	return SetControllingTTY(dev)
}

// SetControllingTTY 把已打开的终端设备设为当前会话的控制终端。
func SetControllingTTY(dev *os.File) error {
	if dev == nil {
		return syscall.EINVAL
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dev.Fd(), unix.TIOCSCTTY, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// SetForeground 把 pid 所在的进程组设为 dev 上的前台进程组。
//
// 非前台进程组读终端会被 SIGTTIN 停住（写会被 SIGTTOU 停住），所以宿主在启动一条
// 面对终端的命令后必须调它；命令退出后再把前台组收回宿主自己。
// 宿主需要先忽略 SIGTTOU，否则它自己调用时也会被停住。
func SetForeground(dev *os.File, pid int) error {
	if dev == nil || pid <= 0 {
		return syscall.EINVAL
	}
	pgid := pid // TIOCSPGRP 取进程组 id；叫 pid 是为了贴合系统调用的直观语义
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dev.Fd(), unix.TIOCSPGRP, uintptr(unsafe.Pointer(&pgid)))
	if errno != 0 {
		return errno
	}
	return nil
}

// ForegroundGroup 返回 dev 上前台进程组的 id。
func ForegroundGroup(dev *os.File) (int, error) {
	if dev == nil {
		return 0, syscall.EINVAL
	}
	pgid, err := unix.IoctlGetInt(int(dev.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return 0, err
	}
	return pgid, nil
}

// IgnoreTTOU 让进程忽略 SIGTTOU：后台进程组里做终端操作（tcsetpgrp）时的默认
// 行为是被停住，宿主必须屏蔽掉才能可靠地管理前台组。
func IgnoreTTOU() {
	signalIgnoreTTOU()
}
