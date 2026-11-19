//go:build !linux && !darwin && !windows

package vbash

import "testing"

// skippableShPath 在未知平台跳过依赖 `sh` 的用例（同 Windows 理由）。
func skippableShPath(t *testing.T) string {
	t.Helper()
	t.Skip("未知平台无 sh 保证：语义由 POSIX 侧覆盖")
	return ""
}
