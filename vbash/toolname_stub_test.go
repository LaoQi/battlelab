//go:build !linux && !darwin && !windows

package vbash

// exeName 把一个测试工具名补成「本平台可执行」的形态（未知平台按 POSIX 口径）。
func exeName(base string) string { return base }

// testPathSep 是 PATH 列表分隔符。
const testPathSep = ":"

// testPathNoTools 是一个不含待查工具的 PATH（供「必须 miss」的用例用）。
const testPathNoTools = "/usr/bin:/bin"
