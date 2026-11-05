package vbash

import (
	"strconv"
	"strings"
	"testing"
)

func TestFuncDepthLimit(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "f() { f; }\nf\necho after=$?")
	if out.code != 0 {
		t.Errorf("超限应只让该次调用失败、脚本继续，退出码为 %d", out.code)
	}
	if !strings.Contains(out.stderr, "maximum function nesting level exceeded") {
		t.Errorf("缺少诊断: %q", out.stderr)
	}
	if !strings.Contains(out.stderr, "("+strconv.Itoa(maxFuncDepth)+")") {
		t.Errorf("诊断缺上限数字: %q", out.stderr)
	}
	if !strings.Contains(out.stdout, "after=1") {
		t.Errorf("超限调用应返回 1: %q", out.stdout)
	}
}

func TestFuncDepthWithinLimit(t *testing.T) {
	dir := tempDir(t)
	src := "f() { if [ \"$1\" -lt " + strconv.Itoa(maxFuncDepth-1) + " ]; then f $(( $1 + 1 )); fi; }\nf 0\necho ok"
	out := runScript(t, dir, src)
	if out.code != 0 || out.stderr != "" {
		t.Fatalf("上限内递归应正常: code=%d stderr=%q", out.code, out.stderr)
	}
	if !strings.Contains(out.stdout, "ok") {
		t.Errorf("输出为 %q", out.stdout)
	}
}

func TestFuncDepthThroughSubshell(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "f() { ( f ); }\nf\necho after=$?")
	if !strings.Contains(out.stderr, "maximum function nesting level exceeded") {
		t.Errorf("子 shell 递归也应受限: %q", out.stderr)
	}
	if !strings.Contains(out.stdout, "after=1") {
		t.Errorf("输出为 %q", out.stdout)
	}
}

func TestFuncVisibleInPipelineAndSubshell(t *testing.T) {
	dir := tempDir(t)
	cases := map[string]string{
		"f() { echo x; }\nf | cat":         "x\n",
		"f() { cat; }\necho hi | f":        "hi\n",
		"f() { echo F; }\necho \"[$(f)]\"": "[F]\n",
		"f() { echo a; }\n( f )":           "a\n",
		"f() { echo n=$#; }\nf 1 2 | cat":  "n=2\n",
	}
	for src, want := range cases {
		out := runScript(t, dir, src)
		if out.code != 0 || out.stdout != want {
			t.Errorf("%q: code=%d out=%q err=%q，期望 %q", src, out.code, out.stdout, out.stderr, want)
		}
	}
}

func TestFuncDefinedInSubshellDoesNotLeak(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "( g() { echo g; }; g )\ng")
	if !strings.Contains(out.stdout, "g\n") {
		t.Errorf("子 shell 内调用应成功: %q", out.stdout)
	}
	if !strings.Contains(out.stderr, "command not found") {
		t.Errorf("子 shell 内定义不应泄漏: out=%q err=%q", out.stdout, out.stderr)
	}
}

func TestFuncRedefineInSubshellIsolated(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "f() { echo one; }\nf | ( f() { echo two; }; f )\nf")
	if !strings.Contains(out.stdout, "two\n") || !strings.Contains(out.stdout, "one\n") {
		t.Errorf("段内重定义应只影响该段: %q", out.stdout)
	}
}
