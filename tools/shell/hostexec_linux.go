//go:build linux

package shell

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/LaoQi/battlelab/vbash"
)

// hostExecHandler 把解释器的外部命令交给会话宿主执行。
//
// 宿主是独立会话首进程、持有借出的 pts；命令在宿主里 fork，只建新进程组并按需
// 设为前台组。于是管道任意位置（含中间段）都能打开 `/dev/tty`，
// sudo/gpg/ssh 在 `cmd | sudo ...`、`sudo ... | cmd` 里都能与用户应答。
func hostExecHandler(h *sessionHost, dev *os.File, cwd string, env []string) vbash.Handler {
	return func(ctx context.Context, r *vbash.Runner, argv []string, in *vbash.IO) (int, error) {
		flags := uint32(0)
		if r.Background() {
			flags |= hostFlagNoTTY
		}
		stdio, cleanup, err := bridgeStdio(in)
		if err != nil {
			return 1, err
		}
		defer cleanup()
		code, err := h.runWithContext(ctx, argv, cwd, env, flags, stdio)
		if err != nil {
			return code, err
		}
		return code, nil
	}
}

// bridgeStdio 把解释器的 IO 落成三个可交给宿主的 fd（宿主按编号在父进程里打开）。
//
// 解释器的 fd 0/1/2 有三种形态：真实的 *os.File（pty slave、重定向的文件、
// 管道端点），以及内存 reader/writer（`< /dev/null` 被折成空读取器、heredoc 的
// 字符串读取器、捕获用的 buffer）。内存形态没法让宿主直接 open，这里建一根临时
// 管道，父进程起 goroutine 搬运数据，从而保持「宿主只认 fd」的简单协议。
func bridgeStdio(x *vbash.IO) ([]int, func(), error) {
	var closers []io.Closer
	stop := make(chan struct{})
	var wg sync.WaitGroup
	cleanup := func() {
		close(stop)
		for _, c := range closers {
			_ = c.Close()
		}
		wg.Wait()
	}
	asFile := func(v any) (*os.File, bool) {
		f, ok := v.(*os.File)
		return f, ok
	}
	// pipePair 建一根管道，返回（宿主侧可 open 的端点，父进程侧端点）。
	pipePair := func(writeEnd bool) (*os.File, *os.File, error) {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		if writeEnd {
			closers = append(closers, pw)
			return pr, pw, nil
		}
		closers = append(closers, pr)
		return pw, pr, nil
	}

	var fds []int
	// ---- stdin ----
	if f, ok := asFile(x.In); ok {
		fds = append(fds, int(f.Fd()))
	} else {
		hostSide, parentSide, err := pipePair(true) // 父进程写 -> 宿主读
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		fds = append(fds, int(hostSide.Fd()))
		wg.Add(1)
		go func(rd io.Reader) {
			defer wg.Done()
			_, _ = io.Copy(parentSide, rd)
			_ = parentSide.Close()
		}(x.In)
	}
	// ---- stdout / stderr ----
	for _, w := range []io.Writer{x.Out, x.Err} {
		if f, ok := asFile(w); ok {
			fds = append(fds, int(f.Fd()))
			continue
		}
		hostSide, parentSide, err := pipePair(false) // 宿主写 -> 父进程读
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		fds = append(fds, int(hostSide.Fd()))
		wg.Add(1)
		go func(dst io.Writer) {
			defer wg.Done()
			_, _ = io.Copy(dst, parentSide)
		}(w)
	}
	return fds, cleanup, nil
}
