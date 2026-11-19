package shell

import (
	"os"
	"testing"
)

// TestMain 让本包的测试二进制也能作为「会话宿主」被自举：
// run_shell 的所有外部命令都由「当前可执行文件」起的宿主进程承载，测试二进制
// 不接这个钩子就会被当成宿主跑起来却进不了宿主循环，表现为握手超时或递归自举。
// 必须**不带平台 tag**：宿主机制在 linux/darwin/windows 上都启用。
func TestMain(m *testing.M) {
	RunSessionHostIfRequested()
	os.Exit(m.Run())
}
