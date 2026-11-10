//go:build linux

package shell

import (
	"os"
	"testing"
)

// TestMain 让本包的测试二进制也能作为「会话宿主」被自举：
// run_shell 的 interactive 路径会用当前可执行文件启动宿主进程。
func TestMain(m *testing.M) {
	RunSessionHostIfRequested()
	os.Exit(m.Run())
}
