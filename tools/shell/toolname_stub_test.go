//go:build !linux && !darwin && !windows

package shell

// hostExeName 把测试工具名补成「本平台可执行」的形态（未知平台按 POSIX 口径）。
func hostExeName(base string) string { return base }

// hostPathSep 是 PATH 列表分隔符。
const hostPathSep = ":"
