//go:build !linux && !darwin

package shell

import "os"

// relayPipeMode 在无 termios 的平台无从设置：Windows 的租约交出的是控制台
// 句柄，回显与行缓冲由控制台自身处理，relay 按原模式搬运。
func relayPipeMode(*os.File) func() { return nil }
