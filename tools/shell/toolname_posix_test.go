//go:build linux || darwin

package shell

// hostExeName 把测试工具名补成「本平台可执行」的形态。
// POSIX 靠 mode 位移判可执行，文件名不用后缀。
func hostExeName(base string) string { return base }

// hostPathSep 是 PATH 列表分隔符。
const hostPathSep = ":"
