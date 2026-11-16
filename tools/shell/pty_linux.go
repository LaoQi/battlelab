//go:build linux

package shell

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// openHostPTY 分配一对 pty（Linux：/dev/ptmx + TIOCGPTN 取从设备号）。
func openHostPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	ptn, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(ptn), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
