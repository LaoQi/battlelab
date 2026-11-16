//go:build linux || darwin

package shell

import (
	"os"

	"github.com/LaoQi/battlelab/ctty"
	"golang.org/x/sys/unix"
)

// relayPipeMode 把租约交出的桥 pty slave 设为「哑管道」模式，返回恢复函数。
//
// relay 在真实终端与宿主虚拟 tty 之间搬运字节，桥本身不该有任何终端意志：
//   - 关 ECHO/ECHONL：按键不得在桥上回显。回显职责完全交给虚拟 tty——那里
//     才是被跑程序的地盘，sudo/ssh 关掉的回显才能端到端生效；否则密码会被
//     桥的回显泄露到屏幕（ECHONL 不关的话，回车仍会漏一个换行）。
//   - 关 ICANON：逐键转发而非攒行，行编辑也交给虚拟 tty 的行规程——程序把
//     虚拟 tty 设成 raw 时按键语义才能原样到达。
//   - 关 ISIG：信号处置归 tanya 的真实终端（^C 仍归 tanya），桥不产生信号。
//   - 关 OPOST：虚拟 tty 的输出已按自身模式整形（含 ONLCR），直通上屏，
//     不在桥上二次转换（否则 \r\n 会变 \r\r\n）。
//
// 失败（设备不是 tty、平台不支持等）返回 nil，relay 按原模式工作。
//
// 注意 Fd() 的副作用：os.File.Fd 会把 fd 从运行时 poller 的非阻塞模式切回
// 阻塞模式，之后阻塞中的 Read 走裸 syscall、Close 再也唤不醒它（stop 的
// 强制唤醒依赖「可轮询设备上 Close 解除阻塞读」）。因此 ioctl 完成后必须
// 立刻恢复 O_NONBLOCK——本函数在 relay 的读 goroutine 启动前调用，不存在
// 「恢复时刻已有阻塞中的 Read」的窗口。
func relayPipeMode(dev *os.File) func() {
	if dev == nil {
		return nil
	}
	fd := int(dev.Fd())
	saved, err := ctty.GetTermios(fd)
	if err != nil {
		return nil
	}
	raw := saved
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG
	raw.Oflag &^= unix.OPOST
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := ctty.SetTermios(fd, raw); err != nil {
		return nil
	}
	_ = unix.SetNonblock(fd, true)
	return func() {
		_ = ctty.SetTermios(fd, saved)
	}
}
