package vbash

import (
	"context"
	"strings"
	"testing"
	"time"
)

func nestCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func runNest(t *testing.T, src string) (int, error) {
	t.Helper()
	dir := tempDir(t)
	var out, errb strings.Builder
	cfg := Config{
		Dir:       dir,
		Home:      dir,
		Env:       []string{"PATH=/usr/local/bin:/usr/bin:/bin"},
		Stdout:    &out,
		Stderr:    &errb,
		Stdin:     strings.NewReader(""),
		ShellName: "/bin/bash",
	}
	ctx, cancel := nestCtx(t)
	defer cancel()
	return RunScript(ctx, src, cfg)
}

func TestNestDepthCmdSub(t *testing.T) {
	ok := "echo " + strings.Repeat("$(", maxNestDepth) + "true" + strings.Repeat(")", maxNestDepth)
	if _, err := runNest(t, ok); err != nil {
		t.Fatalf("上限内命令替换应可解析: %v", err)
	}
	bad := "echo " + strings.Repeat("$(", maxNestDepth+1) + "true" + strings.Repeat(")", maxNestDepth+1)
	code, err := runNest(t, bad)
	if err == nil {
		t.Fatalf("超过上限应报错")
	}
	if code != 2 {
		t.Errorf("退出码为 %d，期望 2", code)
	}
	if !strings.Contains(err.Error(), "嵌套过深") {
		t.Errorf("错误信息为 %q", err.Error())
	}
}

func TestNestDepthBraced(t *testing.T) {
	ok := "echo " + strings.Repeat("${x:-", maxNestDepth-1) + "ok" + strings.Repeat("}", maxNestDepth-1)
	if _, err := runNest(t, ok); err != nil {
		t.Fatalf("上限内参数展开应可解析: %v", err)
	}
	bad := "echo " + strings.Repeat("${x:-", maxNestDepth+1) + "ok" + strings.Repeat("}", maxNestDepth+1)
	if _, err := runNest(t, bad); err == nil {
		t.Fatalf("超过上限应报错")
	} else if !strings.Contains(err.Error(), "嵌套过深") {
		t.Errorf("错误信息为 %q", err.Error())
	}
}

func TestNestDepthArith(t *testing.T) {
	ok := "echo $(( " + strings.Repeat("(", maxNestDepth) + "1" + strings.Repeat(")", maxNestDepth) + " ))"
	if _, err := runNest(t, ok); err != nil {
		t.Fatalf("上限内算术应可求值: %v", err)
	}
	bad := "echo $(( " + strings.Repeat("(", maxNestDepth+1) + "1" + strings.Repeat(")", maxNestDepth+1) + " ))"
	if _, err := runNest(t, bad); err == nil {
		t.Fatalf("超过上限应报错")
	} else if !strings.Contains(err.Error(), "嵌套过深") {
		t.Errorf("错误信息为 %q", err.Error())
	}
}

func TestNestDepthNoMemoryBlowup(t *testing.T) {
	deep := 50000
	src := "echo " + strings.Repeat("$(", deep) + "true" + strings.Repeat(")", deep)
	start := time.Now()
	if _, err := runNest(t, src); err == nil {
		t.Fatalf("深层嵌套应报错而非耗尽内存")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("深层嵌套耗时 %v，疑似退化", d)
	}
}
