//go:build windows

package vbash

import "testing"

// skippableShPath 在 Windows 上跳过依赖 `sh` 的用例：Windows 没有 sh（除非装了
// Git for Windows/msys2 并恰好在 PATH 里），拿它读「子进程 PATH」不得不是一个
// 平台假设。`-p` 不改 PATH 这条语义由 POSIX 侧覆盖，实现本身无平台分片。
func skippableShPath(t *testing.T) string {
	t.Helper()
	t.Skip("Windows 无 sh：用 sh 读子进程 PATH 属 POSIX 假设，语义由 POSIX 侧覆盖")
	return ""
}
