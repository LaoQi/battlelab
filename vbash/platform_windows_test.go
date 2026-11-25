//go:build windows

package vbash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsFromHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\Users\x`, "/c/Users/x"},
		{`C:/Users/x`, "/c/Users/x"},
		{`c:\`, "/c"},
		{`\\server\share\f`, "//server/share/f"},
		{"/already/posix", "/already/posix"},
		// F5：内部形态的盘符输入统一小写
		{"/D/per", "/d/per"},
		{"/D", "/d"},
		{"", ""},
	}
	for _, c := range cases {
		if got := windowsFromHost(c.in); got != c.want {
			t.Errorf("windowsFromHost(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestWindowsToHost(t *testing.T) {
	cases := []struct{ cwd, in, want string }{
		{"", "/c/Users/x", `C:\Users\x`},
		{"", "/c", `C:\`},
		{"", "//server/share", `\\server\share`},
		{"", "rel/f", `rel\f`},
		{`C:\work`, "rel/f", `C:\work\rel\f`},
	}
	for _, c := range cases {
		if got := windowsToHost(c.cwd, c.in); got != c.want {
			t.Errorf("windowsToHost(%q, %q) = %q，期望 %q", c.cwd, c.in, got, c.want)
		}
	}
}

func TestWindowsRoundTrip(t *testing.T) {
	for _, p := range []string{"/c/Users/x", "/d/work/sub"} {
		if got := windowsFromHost(windowsToHost("", p)); got != p {
			t.Errorf("%q 往返后为 %q", p, got)
		}
	}
}

func TestWindowsDevNull(t *testing.T) {
	for _, n := range []string{"/dev/null", "nul", "NUL"} {
		if !isDevNull(n) {
			t.Errorf("%q 应被识别为空设备", n)
		}
	}
}

func TestWindowsExitCodeMask(t *testing.T) {
	if got := windowsSignalName(2); got != "Interrupt" {
		t.Errorf("Ctrl-C 信号名: %q", got)
	}
}

// TestWindowsIsExecNeedsKnownSuffix 锁住「裸名文件在 Windows 上不可执行」：
// 判可执行只能看后缀是否在 PATHEXT 清单内。不能把查找用的空串候选
// （`ls` 要先试 `ls.exe`，试不到再试裸名）当成「裸名也算可执行」——
// 那会把 `d1/nx` 这种无扩展名普通文件判成可执行，`command -v` 于是选中它。
func TestWindowsIsExecNeedsKnownSuffix(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		want bool
	}{
		{"tool.exe", true},
		{"tool.cmd", true},
		{"tool.bat", true},
		{"tool.com", true},
		{"TOOL.EXE", true},  // 后缀判定不分大小写
		{"tool", false},     // 裸名不可执行
		{"tool.txt", false}, // 不在清单内的后缀
		{"script.sh", false},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := windowsIsExec(p, st); got != c.want {
			t.Errorf("windowsIsExec(%q) = %v，期望 %v", c.name, got, c.want)
		}
	}
	// 目录即便带可执行后缀也不算
	if err := os.MkdirAll(filepath.Join(dir, "d.exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "d.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if windowsIsExec(filepath.Join(dir, "d.exe"), st) {
		t.Error("目录不应被当作可执行文件")
	}
}
