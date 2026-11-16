//go:build darwin

package shell

import (
	"os"

	"golang.org/x/sys/unix"
)

// openHostPTY 分配一对 pty（darwin：打开 /dev/ptmx 后用 TIOCPTYGRANT/UNLK 授权，
// TIOCPTYGNAME 取从设备路径）。darwin 属尽力而为平台：这条路径未经实机验证，
// 失败时上层会明确报错而不是静默降级。
func openHostPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCPTYGRANT, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCPTYUNLK, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	var buf [128]byte
	if _, err := unix.IoctlGetTermios(int(master.Fd()), unix.TIOCPTYGNAME); err == nil {
		// TIOCPTYGNAME 需要可写缓冲，x/sys 的封装不适用；直接走 syscall。
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCPTYGNAME, uintptr(unsafePointer(&buf[0])))
	if errno != 0 {
		master.Close()
		return nil, nil, errno
	}
	name := ""
	for _, b := range buf {
		if b == 0 {
			break
		}
		name += string(b)
	}
	slave, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
