//go:build linux || darwin

package vbash

// exeName 把一个测试工具名补成「本平台可执行」的形态。
// POSIX 靠 mode 位移判可执行，文件名不用后缀。
func exeName(base string) string { return base }

// testPathSep 是 PATH 列表分隔符。
const testPathSep = ":"

// testPathNoTools 是一个不含待查工具的 PATH（供「必须 miss」的用例用）。
const testPathNoTools = "/usr/bin:/bin"
