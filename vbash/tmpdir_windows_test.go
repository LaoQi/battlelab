//go:build windows

package vbash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsToHostTempDir(t *testing.T) {
	tmp := os.TempDir()
	cases := []struct{ in, want string }{
		{"/tmp", tmp},
		{"/tmp/x.txt", filepath.Join(tmp, "x.txt")},
		{"/tmp/a/b", filepath.Join(tmp, "a", "b")},
		// /tmp 前缀必须精确匹配目录边界
		{"/tmpfoo/bar", filepath.Join(mustAbsRoot(t), "tmpfoo", "bar")},
	}
	for _, c := range cases {
		if got := windowsToHost("", c.in); got != c.want {
			t.Errorf("windowsToHost(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 非盘符绝对路径显式落到当前盘根，不再是盘符相对形式
	if got := windowsToHost("", "/usr/bin"); got != filepath.Join(mustAbsRoot(t), "usr", "bin") {
		t.Errorf("windowsToHost(/usr/bin) = %q", got)
	}
}

func mustAbsRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(`\`)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// 端到端：/tmp 的写入、读取、test、cd 必须落在同一个目录（%TEMP%）。
func TestWindowsSlashTmpConsistent(t *testing.T) {
	dir := tempDir(t)
	name := "/tmp/vbash-f3-" + filepath.Base(dir) + ".txt"
	abs := filepath.Join(os.TempDir(), filepath.Base(name))
	t.Cleanup(func() { os.Remove(abs) })

	script := "echo hi > " + name + "; cat " + name + "; test -f " + name + " && echo T; cd /tmp && pwd"
	got := runScript(t, dir, script)
	wantPwd := DisplayPath(plat.FromHost(os.TempDir()))
	want := "hi\nT\n" + wantPwd + "\n"
	if got.stdout != want {
		t.Errorf("/tmp 行为不一致：stdout=%q，期望 %q（stderr=%q）", got.stdout, want, got.stderr)
	}
}
