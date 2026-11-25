//go:build windows

package vbash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsHostArg(t *testing.T) {
	tmp := os.TempDir()
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/d/x", `D:\x`, true},
		{"/d", `D:\`, true},
		{"//server/share/x", `\\server\share\x`, true},
		{"/tmp", tmp, true},
		{"/tmp/x", filepath.Join(tmp, "x"), true},
		// 含冒号：URL、已是宿主形态、PATH 列表一律不动
		{"D:/y", "", false},
		{"http://a/b", "", false},
		// 非盘符绝对路径留给 msys 工具
		{"/usr/bin", "", false},
		{"rel/x", "", false},
		{"-o", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := windowsHostArg(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("windowsHostArg(%q) = (%q, %v)，期望 (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestWindowsArgvHostPaths(t *testing.T) {
	dir := tempDir(t)
	name := exeName("ha")
	writeTool(t, dir, name, true)

	var got []string
	newCfg := func(noArgvHost bool) Config {
		return Config{
			Dir:        dir,
			Home:       dir,
			Env:        []string{"PATH=" + plat.FromHost(dir)},
			Stdin:      strings.NewReader(""),
			NoArgvHost: noArgvHost,
			Handler: func(_ context.Context, _ *Runner, _ string, argv []string, _ *IO) (int, error) {
				got = append([]string(nil), argv...)
				return 0, nil
			},
		}
	}

	src := "ha /d/x D:/y /usr/bin http://a/b /tmp/z"
	if _, err := RunScript(context.Background(), src, newCfg(false)); err != nil {
		t.Fatal(err)
	}
	want := []string{"ha", `D:\x`, "D:/y", "/usr/bin", "http://a/b", filepath.Join(os.TempDir(), "z")}
	if len(got) != len(want) {
		t.Fatalf("argv = %q，期望 %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q，期望 %q（完整 %q）", i, got[i], want[i], got)
		}
	}

	// 回退开关：NoArgvHost 时 argv 原样
	if _, err := RunScript(context.Background(), src, newCfg(true)); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"ha", "/d/x", "D:/y", "/usr/bin", "http://a/b", "/tmp/z"} {
		if got[i] != want {
			t.Errorf("NoArgvHost 下 argv[%d] = %q，期望 %q", i, got[i], want)
		}
	}
}
