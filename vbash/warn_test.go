package vbash

import (
	"strings"
	"testing"
)

// F7：未引用的 X:\ 形态要提示（不改语义）；引号内、heredoc、正斜杠不提示。
func TestWarnUnquotedDriveBackslash(t *testing.T) {
	dir := tempDir(t)

	got := runScript(t, dir, `echo D:\Per\vis`)
	if got.stdout != "D:Pervis\n" {
		t.Errorf("语义不应改变：stdout=%q", got.stdout)
	}
	if !strings.Contains(got.stderr, "警告") || !strings.Contains(got.stderr, "反斜杠") {
		t.Errorf("未引用 X:\\\\ 应给提示，stderr=%q", got.stderr)
	}

	// 引号内是合法写法，不提示
	got = runScript(t, dir, `echo "D:\Per\vis"`)
	if got.stderr != "" {
		t.Errorf("引号内不应提示，stderr=%q", got.stderr)
	}
	if got.stdout != "D:\\Per\\vis\n" {
		t.Errorf("引号内应原样保留反斜杠，stdout=%q", got.stdout)
	}

	// 变量段之后不应误报（ab$X:\y 不是盘符形态）
	got = runScript(t, dir, "X=1; echo ab$X:\\y")
	if got.stderr != "" {
		t.Errorf("变量段后不应提示，stderr=%q", got.stderr)
	}

	// 正斜杠与 heredoc 不提示
	got = runScript(t, dir, `echo D:/Per/vis`)
	if got.stderr != "" {
		t.Errorf("正斜杠不应提示，stderr=%q", got.stderr)
	}
	got = runScript(t, dir, "cat <<EOF\nD:\\Per\\vis\nEOF")
	if got.stderr != "" {
		t.Errorf("heredoc 不应提示，stderr=%q", got.stderr)
	}
	// 命令替换内部同样要提示：子 List 的警告必须合并到顶层，否则永不打印
	got = runScript(t, dir, `echo $(echo D:\Per\vis)`)
	if !strings.Contains(got.stderr, "警告") || !strings.Contains(got.stderr, "反斜杠") {
		t.Errorf("命令替换内的 X:\\\\ 也应提示，stderr=%q", got.stderr)
	}

	// 非 ASCII 单字母 + 冒号不是盘符形态（byte 截断会把 š 认成 'a'）
	got = runScript(t, dir, "echo \u0161:\\x")
	if got.stderr != "" {
		t.Errorf("非 ASCII 单字母不应提示，stderr=%q", got.stderr)
	}
}
