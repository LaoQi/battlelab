package vbash

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAbsInternalPosix(t *testing.T) {
	cases := []struct{ dir, target, want string }{
		{"/a/b", "c", "/a/b/c"},
		{"/a/b", "/c", "/c"},
		{"/a/b", "../c", "/a/c"},
		{"", "c", "/c"},
		{"/", "c", "/c"},
	}
	for _, c := range cases {
		if got := absInternal(c.dir, c.target); got != c.want {
			t.Errorf("absInternal(%q, %q) = %q，期望 %q", c.dir, c.target, got, c.want)
		}
	}
}

func TestDevNullMatchesPlatformTable(t *testing.T) {
	if !isDevNull("/dev/null") {
		t.Error("/dev/null 应被识别为空设备")
	}
	if isDevNull("/dev/nullx") {
		t.Error("/dev/nullx 不应被识别为空设备")
	}
}

func TestPathLikeNeedsSlash(t *testing.T) {
	if isPathLike("ls") {
		t.Error("不带分隔符的命令名不应视为路径")
	}
	if !isPathLike("./ls") || !isPathLike("/bin/ls") {
		t.Error("带 / 的命令名应视为路径")
	}
}

func TestHostPathRoundTrip(t *testing.T) {
	dir := "/work"
	if got := absInternal(dir, "sub/f"); got != "/work/sub/f" {
		t.Errorf("相对路径应拼在工作目录下: %q", got)
	}
	if got := absInternal(dir, "/abs/f"); got != "/abs/f" {
		t.Errorf("绝对路径应原样保留: %q", got)
	}
}

func TestRunningPathMissingExitCode(t *testing.T) {
	got := runScript(t, tempDir(t), `./nope; echo rc=$?`)
	if got.stdout != "rc=127\n" {
		t.Errorf("不存在的路径型命令应记 127，输出 %q", got.stdout)
	}
	if want := "/bin/bash: line 1: ./nope: No such file or directory\n"; got.stderr != want {
		t.Errorf("诊断不匹配:\n got %q\nwant %q", got.stderr, want)
	}
}

func TestRunningPathNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 执行位与 PATH 语义用例")
	}
	dir := tempDir(t)
	plain := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `./plain.txt; echo rc=$?`)
	if got.stdout != "rc=126\n" {
		t.Errorf("不可执行的常规文件应记 126，输出 %q（stderr=%q）", got.stdout, got.stderr)
	}
}

func TestLookPathSkipsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 执行位与 PATH 语义用例")
	}
	dir := tempDir(t)
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bin, "probe"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(real, "probe")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `PATH=`+bin+`:`+real+`; probe`)
	if got.stdout != "hi\n" {
		t.Errorf("同名目录不应抢占 PATH 查找，输出 %q（stderr=%q）", got.stdout, got.stderr)
	}
}
