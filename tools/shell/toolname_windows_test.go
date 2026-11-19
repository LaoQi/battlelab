//go:build windows

package shell

// hostExeName 把测试工具名补成「本平台可执行」的形态。
// Windows 没有执行位，可执行性由 PATHEXT 后缀决定。
func hostExeName(base string) string { return base + ".cmd" }

// hostPathSep 是 PATH 列表分隔符。
const hostPathSep = ";"
