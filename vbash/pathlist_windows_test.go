//go:build windows

package vbash

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsSplitPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{""}},
		{"D:/a", []string{"D:/a"}},
		{"D:/a:D:/b", []string{"D:/a", "D:/b"}},
		// 段首不是盘符时照切：a 是相对目录名
		{"a:D:/b", []string{"a", "D:/b"}},
		// 只认 X:/、X:\、X: 结尾三种完整盘符前缀；D:x 切成两段（宁可 miss，
		// 不制造意外的 /d/x 命中）
		{"D:x", []string{"D", "x"}},
		{"D:", []string{"D:"}},
		// 历史脏段：与改造前的 ":" 切分保持一致
		{"rmC:/d/Git/x", []string{"rmC", "/d/Git/x"}},
		{"//server/share:x", []string{"//server/share", "x"}},
		{"a:b", []string{"a", "b"}},
		// 宿主列表分隔符 ';'：判据从严
		{"C:\\a;D:\\b", []string{"C:\\a", "D:\\b"}},
		{"C:\\a;;D:\\b", []string{"C:\\a", "", "D:\\b"}},
		{"C:\\a;notdrive", []string{"C:\\a", "notdrive"}},
		// ';' 就是 Windows 的 PATH 分隔符：混形态列表也按 ';' 切
		{"D:/x;/c/y", []string{"D:/x", "/c/y"}},
	}
	for _, c := range cases {
		got := windowsSplitPath(c.in)
		if len(got) != len(c.want) {
			t.Errorf("windowsSplitPath(%q) = %q，期望 %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("windowsSplitPath(%q) = %q，期望 %q", c.in, got, c.want)
				break
			}
		}
	}
}

func TestWindowsParsePathListNormalizes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"D:/tools/bin:/c/Windows", "/d/tools/bin:/c/Windows"},
		{"C:\\a;D:\\b", "/c/a:/d/b"},
		{"D:/a:D:/b", "/d/a:/d/b"},
		// 盘符结尾：D: 归一为 /d
		{"D:", "/d"},
	}
	for _, c := range cases {
		if got := ParsePathList(c.in).String(); got != c.want {
			t.Errorf("ParsePathList(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 出站回环：内部形态经 Host 转宿主形态、按 ';' 拼接后，必须能被重新解析成
// 同样的段数（盘符冒号被误当分隔符的回归）。
func TestWindowsPathListHostRoundTrip(t *testing.T) {
	p := ParsePathList("/c/tools/bin:/d/tools/bin")
	joined := strings.Join(p.Host(), plat.PathSep)
	if joined != "C:\\tools\\bin;D:\\tools\\bin" {
		t.Errorf("Host 拼接 = %q", joined)
	}
	if got := ParsePathList(joined); len(got) != 2 || got.String() != "/c/tools/bin:/d/tools/bin" {
		t.Errorf("回环后 = %q（%d 段）", got.String(), len(got))
	}
}

// 子进程环境里的 PATH 必须是宿主形态，且段边界不丢。
func TestWindowsEnvironPathHostForm(t *testing.T) {
	e := NewEnv()
	e.SetExported("PATH", "C:\\a;D:\\b")
	var got string
	for _, kv := range e.Environ() {
		if strings.HasPrefix(kv, "PATH=") {
			got = kv
		}
	}
	if got != "PATH=C:\\a;D:\\b" {
		t.Errorf("Environ PATH = %q，期望 PATH=C:\\a;D:\\b", got)
	}

	e2 := NewEnv()
	e2.SetExported("PATH", "D:/tools/bin")
	for _, kv := range e2.Environ() {
		if strings.HasPrefix(kv, "PATH=") {
			if kv != "PATH=D:\\tools\\bin" {
				t.Errorf("Environ PATH = %q，期望 PATH=D:\\tools\\bin", kv)
			}
		}
	}
}

// 赋值期归一：内部 PATH 必须是规范段序列，$PATH 展开与 export 列表都看这个。
func TestWindowsPathAssignmentNormalizes(t *testing.T) {
	e := NewEnv()
	e.Set("PATH", "D:/tools/bin", false)
	if v, _ := e.Lookup("PATH"); v != "/d/tools/bin" {
		t.Errorf("Set PATH 归一 = %q，期望 /d/tools/bin", v)
	}
	e.SetExported("PATH", "C:\\a;D:\\b")
	if v, _ := e.Lookup("PATH"); v != "/c/a:/d/b" {
		t.Errorf("SetExported PATH 归一 = %q，期望 /c/a:/d/b", v)
	}
}

// 端到端：宿主形态的 PATH 赋值必须能被解析并命中（改造前会被 ":" 误切）。
func TestWindowsPathAssignmentHostFormFindsTool(t *testing.T) {
	dir := tempDir(t)
	name := exeName("hb")
	writeTool(t, dir, name, true)

	hostDir := filepath.ToSlash(dir)
	got := runWithPath(t, dir, hostDir, "command -v hb")
	want := DisplayPath(plat.FromHost(dir)) + "/" + name + "\n"
	if got.stdout != want {
		t.Errorf("宿主形态 PATH 单段查找 = %q，期望 %q（stderr=%q）", got.stdout, want, got.stderr)
	}

	// ';' 宿主形态列表：命中同样的工具
	got = runWithPath(t, dir, hostDir+";C:/Windows", "command -v hb")
	if got.stdout != want {
		t.Errorf("宿主形态 PATH 列表查找 = %q，期望 %q（stderr=%q）", got.stdout, want, got.stderr)
	}
}
