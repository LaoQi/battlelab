package shell

import (
	"os"
	"sync"
)

// ttyRelay 把宿主的虚拟 tty 接到调用方借出的真实终端：
//
//	真实终端按键 ──→ 虚拟 tty（命令的 /dev/tty 因此可应答）
//	虚拟 tty 输出 ──→ 真实终端（提示实时上屏）
//
// 真实终端始终是 tanya 的前台组、ISIG 保持开启，所以 ^C 仍归 tanya；
// 命令写 /dev/tty 的内容经虚拟 tty 转出后既上屏也进工具结果。
type ttyRelay struct {
	lease   Lease
	host    *sessionHost
	dev     *os.File
	restore func()
	stop_   chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

func startTTYRelay(lease Lease, host *sessionHost) *ttyRelay {
	r := &ttyRelay{lease: lease, host: host, stop_: make(chan struct{})}
	dev := lease.Stdin()
	if dev == nil {
		return r
	}
	r.dev = dev
	r.restore = relayPipeMode(dev)
	// 虚拟 tty → 真实终端：宿主已经在 onTTY 里把字节交给调用方，
	// 这里只负责按键方向（真实终端 → 虚拟 tty），输出方向由 onTTY 承担。
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		buf := make([]byte, hostOutChunk)
		for {
			select {
			case <-r.stop_:
				return
			default:
			}
			n, err := dev.Read(buf)
			if n > 0 {
				_ = host.writeTTY(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

func (r *ttyRelay) stop() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		close(r.stop_)
		// 读 goroutine 阻塞在 dev.Read 上，close(stop_) 唤不醒它。先在 fd
		// 仍有效时恢复桥的 termios，再关闭设备本身强制唤醒（可轮询设备上
		// *os.File.Close 会解除阻塞中的读，返回 file already closed）。
		// 租约侧的 Release 随后也会关同一个设备，重复关闭无害；不关的话
		// stop() 永远等不到 goroutine 退出，interactive 收尾就挂死在这里。
		if r.restore != nil {
			r.restore()
		}
		if r.dev != nil {
			_ = r.dev.Close()
		}
	})
	r.wg.Wait()
}
