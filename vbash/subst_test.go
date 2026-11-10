package vbash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileSubstitution(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `echo "[$(<in.txt)]"; x=$(<in.txt); echo "[$x]"`)
	want := "[hello\nworld]\n[hello\nworld]\n"
	if got.stdout != want {
		t.Fatalf("got %q want %q", got.stdout, want)
	}
}

func TestReadFileSubstitutionMissing(t *testing.T) {
	dir := t.TempDir()
	got := runScript(t, dir, `echo "[$(<missing.txt)]"`)
	if got.stdout != "[]\n" {
		t.Fatalf("got %q", got.stdout)
	}
	if got.stderr == "" {
		t.Fatal("缺文件时应打诊断")
	}
}

// 回归：Dir 为空时相对路径按**进程 cwd** 解析（glob 亦如此），不能因走 hostPath
// 而被钉到内部根 "/"。绝对路径则必须走内部→宿主转换（Windows 上 /d/x 不是宿主路径）。
func TestReadFileSubstitutionNoWorkDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var out, errb strings.Builder
	cfg := Config{Home: dir, Env: []string{testPath}, Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), ShellName: "/bin/bash"}
	if code, _ := RunScript(context.Background(), `echo "[$(<in.txt)]"`, cfg); code != 0 {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
	if got := out.String(); got != "[hello]\n" {
		t.Fatalf("Dir 为空时相对路径应按进程 cwd 解析: got %q stderr %q", got, errb.String())
	}
}
