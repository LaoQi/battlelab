//go:build linux || darwin

package vbash

import "testing"

// skippableShPath 返回一条含 `sh` 的自定义 PATH，并断言该用例在本平台可用。
// POSIX 上 /bin/sh 必定存在，路径前加一段伪造条目即可验证 `-p` 不改子进程 PATH。
func skippableShPath(t *testing.T) string {
	t.Helper()
	return "/tmp/custom-path:/usr/bin:/bin"
}
