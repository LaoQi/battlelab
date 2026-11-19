package tools_test

import (
	"os"
	"testing"

	"github.com/LaoQi/battlelab/tools/shell"
)

// TestMain 让本包的测试二进制也能作为「会话宿主」被自举：
// run_shell 会用当前可执行文件启动宿主进程来承载外部命令。
// 必须**不带平台 tag**：宿主机制在 linux/darwin/windows 上都启用。
func TestMain(m *testing.M) {
	shell.RunSessionHostIfRequested()
	os.Exit(m.Run())
}
