package shell

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunBuiltinExecutesScript(t *testing.T) {
	inv, err := resolve(BuiltinShellName, lookPathStub("bash"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	script := "echo built:$((1+2)); echo oops >&2"
	if err := inv.Run(context.Background(), script, RunOptions{Stdout: &out, Stderr: &errb, Env: os.Environ()}); err != nil {
		t.Fatalf("内置解释器执行失败: %v", err)
	}
	if got := out.String(); got != "built:3\n" {
		t.Errorf("stdout 不符: %q", got)
	}
	if got := errb.String(); got != "oops\n" {
		t.Errorf("stderr 不符: %q", got)
	}
}

func TestRunBuiltinNeedsNoHostShell(t *testing.T) {
	inv := Invocation{Name: BuiltinShellName, Kind: KindPosix, Builtin: true}
	var out bytes.Buffer
	if err := inv.Run(context.Background(), "echo hi", RunOptions{Stdout: &out}); err != nil {
		t.Fatalf("手工构造的内置入口应可执行: %v", err)
	}
	if out.String() != "hi\n" {
		t.Errorf("输出不符: %q", out.String())
	}
}

func TestRunBuiltinUsesDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	inv := Invocation{Name: BuiltinShellName, Kind: KindPosix, Builtin: true}
	var out bytes.Buffer
	err := inv.Run(context.Background(), "pwd; echo $TANYA_TEST_VAR", RunOptions{
		Dir: dir, Env: []string{"TANYA_TEST_VAR=ok"}, Stdout: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(normShellPath(got), normShellPath(dir)) {
		t.Errorf("cwd 未生效: %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("环境变量未注入: %q", got)
	}
}

func TestRunBuiltinHonorsContext(t *testing.T) {
	inv := Invocation{Name: BuiltinShellName, Kind: KindPosix, Builtin: true}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	err := inv.Run(ctx, "while true; do :; done", RunOptions{Stdout: &out})
	if err == nil {
		t.Fatal("取消后应返回错误，避免旁路执行永不返回")
	}
}

func TestRunRealShellExecutesScript(t *testing.T) {
	sh := stubShellScript(t)
	inv := Invocation{Argv: []string{sh, "-c"}, Name: "stub", Kind: KindPosix}
	var out bytes.Buffer
	if err := inv.Run(context.Background(), "the-script", RunOptions{Stdout: &out}); err != nil {
		t.Fatalf("真实 shell 路径执行失败: %v", err)
	}
	if got := out.String(); got != "stub:the-script\n" {
		t.Errorf("脚本未作为末位参数传给 shell: %q", got)
	}
}

func TestRunRealShellReportsFailure(t *testing.T) {
	sh := stubShellScript(t)
	inv := Invocation{Argv: []string{sh, "-c"}, Name: "stub", Kind: KindPosix}
	if err := inv.Run(context.Background(), "fail", RunOptions{Stdout: &bytes.Buffer{}}); err == nil {
		t.Fatal("非零退出应返回错误")
	}
}

func TestRunWithoutArgvFails(t *testing.T) {
	if err := (Invocation{Name: "broken", Kind: KindPosix}).Run(context.Background(), "echo hi", RunOptions{}); err == nil {
		t.Fatal("既非内置又没有 argv 时应报错")
	}
}

func stubShellScript(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub shell 依赖 POSIX 脚本")
	}
	path := filepath.Join(t.TempDir(), "stubsh")
	body := "#!/bin/sh\ncase \"$2\" in fail) exit 3;; esac\nprintf 'stub:%s\\n' \"$2\"\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
