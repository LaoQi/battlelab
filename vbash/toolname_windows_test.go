//go:build windows

package vbash

// exeName 把一个测试工具名补成「本平台可执行」的形态。
// Windows 没有执行位，plat.IsExec 按 PATHEXT 后缀判定，裸名文件不算可执行。
func exeName(base string) string { return base + ".exe" }

// testPathSep 是 PATH 列表分隔符。
const testPathSep = ";"

// testPathNoTools 是一个不含待查工具的 PATH（供「必须 miss」的用例用）。
const testPathNoTools = "/c/Windows"
