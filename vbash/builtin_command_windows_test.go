//go:build windows

package vbash

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCommandPathLookupWindowsSuffixes 锁住 Windows 的后缀语义：
// 名字不带后缀时按 .exe/.cmd/.bat/.com/"" 逐个试，名字自带后缀时只试一次。
func TestCommandPathLookupWindowsSuffixes(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// .cmd 与 .bat 在 windowsIsExec 里算可执行（不依赖 Unix 的执行位）
	for _, name := range []string{"tool.cmd", "plain.bat"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("echo hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := runWithPath(t, dir, "./bin:/usr/bin", `command -v tool`)
	if want := "./bin/tool.cmd\n"; got.stdout != want {
		t.Errorf("不带后缀的名字应补 .cmd 命中，得 %q，期望 %q", got.stdout, want)
	}
	got = runWithPath(t, dir, "./bin:/usr/bin", `command -v plain.bat`)
	if want := "./bin/plain.bat\n"; got.stdout != want {
		t.Errorf("自带后缀的名字应原样命中，得 %q，期望 %q", got.stdout, want)
	}
	// 自带后缀时不重复补后缀：`tool.cmd` 不能退化成找 `tool.cmd.exe`
	got = runWithPath(t, dir, "./bin:/usr/bin", `command -v tool.cmd`)
	if want := "./bin/tool.cmd\n"; got.stdout != want {
		t.Errorf("自带后缀不应重复补后缀，得 %q，期望 %q", got.stdout, want)
	}
}
