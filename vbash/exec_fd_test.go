package vbash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecRedirectPersists(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `exec > out.txt 2>&1; echo first; ls no_such_path; echo rc=$?`)
	if got.stdout != "" {
		t.Errorf("exec 重定向后 stdout 应进文件，实际 %q", got.stdout)
	}
	if got.stderr != "" {
		t.Errorf("exec 重定向后 stderr 应进文件，实际 %q", got.stderr)
	}
	body, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "first") || !strings.Contains(text, "rc=2") {
		t.Errorf("文件应含后续命令输出: %q", text)
	}
}

func TestExecRedirectAppend(t *testing.T) {
	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "log.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `exec >> log.txt; echo a; echo b; echo done >&2`)
	if got.stdout != "" {
		t.Errorf("stdout 应被重定向: %q", got.stdout)
	}
	body, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "seed\na\nb\n" {
		t.Errorf("追加写入内容不匹配: %q", string(body))
	}
}

func TestExecCommandStopsScript(t *testing.T) {
	got := runScript(t, tempDir(t), `exec echo replaced; echo after`)
	if got.stdout != "replaced\n" {
		t.Errorf("exec 带命令后不应继续执行: %q", got.stdout)
	}
	if got.code != 0 {
		t.Errorf("退出码应取被替换命令: %d", got.code)
	}
}

func TestExecCommandExitCode(t *testing.T) {
	got := runScript(t, tempDir(t), `exec false; echo after`)
	if strings.Contains(got.stdout, "after") {
		t.Errorf("exec 后不应继续: %q", got.stdout)
	}
	if got.code != 1 {
		t.Errorf("退出码应取被替换命令: %d", got.code)
	}
}

func TestExecNoArgsIsNoop(t *testing.T) {
	got := runScript(t, tempDir(t), `echo a; exec; echo b`)
	if got.stdout != "a\nb\n" {
		t.Errorf("裸 exec 应无副作用: %q", got.stdout)
	}
}

func TestFdCloseStdoutFailsBuiltin(t *testing.T) {
	got := runScript(t, tempDir(t), `echo hi >&-; echo "rc=$?" >&2`)
	if got.stdout != "" {
		t.Errorf("stdout 已关闭不应有输出: %q", got.stdout)
	}
	if !strings.Contains(got.stderr, "write error: Bad file descriptor") {
		t.Errorf("应报写入错误: %q", got.stderr)
	}
	if !strings.Contains(got.stderr, "rc=1") {
		t.Errorf("写入失败应记退出码 1: %q", got.stderr)
	}
}

func TestFdCloseThenUseFails(t *testing.T) {
	got := runScript(t, tempDir(t), `exec 3>&-; echo x >&3; echo "rc=$?"`)
	if !strings.Contains(got.stderr, "Bad file descriptor") {
		t.Errorf("写入已关闭 fd 应报错: %q", got.stderr)
	}
	if !strings.Contains(got.stdout, "rc=1") {
		t.Errorf("应记退出码 1: %q", got.stdout)
	}
}

func TestFdDupPersists(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `exec 3> f3.txt; echo hi >&3; exec 3>&-; cat f3.txt`)
	if got.stdout != "hi\n" {
		t.Errorf("持久 fd 应可写: %q", got.stdout)
	}
}

func TestFdCloseStderrKeepsStdout(t *testing.T) {
	got := runScript(t, tempDir(t), `echo hi 2>&-; echo rc=$?`)
	if got.stdout != "hi\nrc=0\n" {
		t.Errorf("关闭 stderr 不应影响 stdout: %q", got.stdout)
	}
}

func TestExternalExtraFdWarns(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `sh -c 'echo o >&3' 3> f.txt; echo rc=$?`)
	if !strings.Contains(got.stderr, "not passed to external commands") {
		t.Errorf("应向用户提示 fd 3 未透传: %q", got.stderr)
	}
	if !strings.Contains(got.stdout, "rc=1") {
		t.Errorf("子进程写 fd 3 应失败: %q", got.stdout)
	}
}
