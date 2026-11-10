//go:build !linux

package shell

import (
	"context"
	"os"

	"github.com/LaoQi/battlelab/vbash"
)

// 会话宿主只在 Linux 上提供：它依赖 pts 与 setsid/TIOCSCTTY 的会话语义。
// 其他平台沿用既有的进程组配置（Windows 的 CONIN$ 直通、darwin 尚未实现 LendFull）。

type sessionHost struct{}

func IsSessionHost() bool { return false }

func RunSessionHostIfRequested() {}

func startSessionHost(*os.File) (*sessionHost, error) { return nil, errHostUnavailable }

func (h *sessionHost) run(argv []string, cwd string, env []string, flags uint32) (int, error) {
	return 1, errHostUnavailable
}

func (h *sessionHost) close() {}

// hostExecHandler 在非 Linux 平台不会被调用（PtyLend 分支只存在于 posix pty 平台），
// 这里提供桩以满足公共文件的编译。
func hostExecHandler(h *sessionHost, dev *os.File, cwd string, env []string) vbash.Handler {
	return func(ctx context.Context, r *vbash.Runner, argv []string, in *vbash.IO) (int, error) {
		return 1, errHostUnavailable
	}
}
