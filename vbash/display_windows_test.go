//go:build windows

package vbash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsDisplayPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/d/per/vis", "D:/per/vis"},
		{"/d", "D:/"},
		{"/c/Users/x", "C:/Users/x"},
		{"//server/share/x", "//server/share/x"},
		{"sub/t1.exe", "sub/t1.exe"},
		// 非盘符绝对路径跟随显式映射到当前盘根
		{"/usr/bin", strings.ReplaceAll(filepath.Join(mustAbsRoot(t), "usr", "bin"), "\\", "/")},
		{"", ""},
	}
	for _, c := range cases {
		if got := DisplayPath(c.in); got != c.want {
			t.Errorf("DisplayPath(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// pwd / $PWD / cd - 回显必须是宿主形态（正斜杠），且内部目录不变。
func TestWindowsPwdFamilyHostForm(t *testing.T) {
	dir := tempDir(t)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wantSub := DisplayPath(plat.FromHost(sub))
	wantDir := DisplayPath(plat.FromHost(dir))

	got := runScript(t, dir, "cd sub; pwd; echo $PWD")
	if got.stdout != wantSub+"\n"+wantSub+"\n" {
		t.Errorf("cd sub 后 pwd/$PWD = %q，期望 %q", got.stdout, wantSub+"\n"+wantSub+"\n")
	}

	// cd - 回显旧目录，也是宿主形态
	got = runScript(t, dir, "cd sub; cd -")
	if got.stdout != wantSub+"\n" {
		t.Errorf("cd - 输出 = %q，期望 %q", got.stdout, wantSub+"\n")
	}

	// 往返：$PWD 是宿主形态，但 cd 能原样吃回去，内部状态不漂
	got = runScript(t, dir, `x=$(pwd); cd "$x"; pwd`)
	if got.stdout != wantDir+"\n" {
		t.Errorf("$(pwd) 往返后 pwd = %q，期望 %q（stderr=%q）", got.stdout, wantDir, got.stderr)
	}
}

func TestWindowsTildeAndHomeHostForm(t *testing.T) {
	dir := tempDir(t)
	want := DisplayPath(plat.FromHost(dir))
	got := runScript(t, dir, "echo ~; echo ~+")
	if got.stdout != want+"\n"+want+"\n" {
		t.Errorf("~/~+ = %q，期望两行 %q", got.stdout, want)
	}
}

func TestWindowsCommandVerboseHostForm(t *testing.T) {
	dir := tempDir(t)
	name := exeName("vt")
	writeTool(t, dir, name, true)
	got := runWithPath(t, dir, plat.FromHost(dir), "command -V vt")
	want := "vt is " + DisplayPath(plat.FromHost(dir)) + "/" + name + "\n"
	if got.stdout != want {
		t.Errorf("command -V = %q，期望 %q", got.stdout, want)
	}
}

func TestWindowsExportListHostForm(t *testing.T) {
	dir := tempDir(t)
	want := `declare -x PWD="` + DisplayPath(plat.FromHost(filepath.Join(dir, "sub"))) + `"`
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, "cd sub; export")
	if !strings.Contains(got.stdout, want) {
		t.Errorf("export 列表里没有 %q；实得 %q", want, got.stdout)
	}
}
