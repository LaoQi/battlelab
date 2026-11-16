package shell

import (
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
	lease Lease
	host  *sessionHost
	stop_ chan struct{}
	once  sync.Once
	wg    sync.WaitGroup
}

func startTTYRelay(lease Lease, host *sessionHost) *ttyRelay {
	r := &ttyRelay{lease: lease, host: host, stop_: make(chan struct{})}
	dev := lease.Stdin()
	if dev == nil {
		return r
	}
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
	})
	r.wg.Wait()
}
