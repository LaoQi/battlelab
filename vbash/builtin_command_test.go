package vbash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTool 在 dir 里造一个文件，exec 决定是否加可执行位。
func writeTool(t *testing.T, dir, name string, exec bool) string {
	t.Helper()
	p := filepath.Join(dir, name)
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\ntrue\n"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// runWithPath 用自定义 PATH 与工作目录跑一段脚本。
func runWithPath(t *testing.T, dir, path, src string) runOut {
	t.Helper()
	var out, errb strings.Builder
	cfg := Config{
		Dir:       dir,
		Home:      dir,
		Env:       []string{"PATH=" + path},
		Stdout:    &out,
		Stderr:    &errb,
		Stdin:     strings.NewReader(""),
		ShellName: "/bin/bash",
	}
	code, _ := RunScript(context.Background(), src, cfg)
	return runOut{code: code, stdout: out.String(), stderr: errb.String()}
}

func TestCommandQueryLookupOrder(t *testing.T) {
	dir := tempDir(t)
	cases := []struct {
		src  string
		want string
		code int
	}{
		// 关键字优先于一切
		{`command -v if`, "if\n", 0},
		{`command -V if`, "if is a shell keyword\n", 0},
		// 函数优先于内建
		{`f(){ :; }; command -v echo; command -V echo`, "echo\necho is a shell builtin\n", 0},
		{`f(){ :; }; command -v f`, "f\n", 0},
		{`f(){ :; }; command -V f`, "f is a function\n", 0},
		// 内建
		{`command -v printf`, "printf\n", 0},
		{`command -V printf`, "printf is a shell builtin\n", 0},
		// 函数优先于内建：同名函数存在时 -V 必须报 function（bash 口径，实测 echo(){:;} 后报函数）
		{`echo(){ :; }; command -V echo`, "echo is a function\n", 0},
		{`echo(){ :; }; command -v echo`, "echo\n", 0},
		// miss
		{`command -v no-such-cmd-xyz`, "", 1},
		{`command -V no-such-cmd-xyz`, "", 1},
	}
	for _, c := range cases {
		got := runScript(t, dir, c.src)
		if got.stdout != c.want || got.code != c.code {
			t.Errorf("%s → stdout=%q code=%d，期望 %q code=%d", c.src, got.stdout, got.code, c.want, c.code)
		}
	}
}

func TestCommandVMissIsSilent(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `command -v no-such-cmd-xyz`)
	if got.stderr != "" {
		t.Errorf("-v 的 miss 不应写 stderr，实得 %q", got.stderr)
	}
	if got.code != 1 {
		t.Errorf("退出码为 %d，期望 1", got.code)
	}
}

func TestCommandVMissDiagnosticForV(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `command -V no-such-cmd-xyz`)
	want := "/bin/bash: line 1: command: no-such-cmd-xyz: not found\n"
	if got.stderr != want {
		t.Errorf("stderr 为 %q，期望 %q", got.stderr, want)
	}
	if got.code != 1 {
		t.Errorf("退出码为 %d，期望 1", got.code)
	}
}

func TestCommandAnyHitExitsZero(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `command -v no-such-cmd-xyz echo; echo "rc=$?"`)
	if got.stdout != "echo\nrc=0\n" {
		t.Errorf("输出为 %q，期望「echo 后 rc=0」", got.stdout)
	}
	// 全 miss 才 1；-V 的 miss 会写 stderr，故只断言退出码
	got = runScript(t, dir, `command -v no1 no2; echo "rc=$?"`)
	if got.stdout != "rc=1\n" {
		t.Errorf("全 miss 时输出为 %q，期望 rc=1", got.stdout)
	}
}

func TestCommandNoArgsExitsZero(t *testing.T) {
	dir := tempDir(t)
	for _, src := range []string{`command`, `command -p`, `command -v`, `command -V`, `command -v --`} {
		got := runScript(t, dir, src+`; echo "rc=$?"`)
		if got.stdout != "rc=0\n" {
			t.Errorf("%s → stdout=%q，期望 rc=0 且无输出", src, got.stdout)
		}
	}
}

func TestCommandPathLookupRelativeForms(t *testing.T) {
	dir := tempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows 无执行位：可执行文件要有 PATHEXT 后缀，否则不是可执行（见 plat.IsExec）。
	name := exeName("t1")
	writeTool(t, filepath.Join(dir, "sub"), name, true)
	cases := []struct{ path, wantV, wantVerbose string }{
		// 只削末尾一个 '/'：`sub//` → `sub//t1`（bash 口径，不 clean）
		{"sub", "sub/" + name + "\n", name + " is DIR/sub/" + name + "\n"},
		{"sub/", "sub/" + name + "\n", name + " is DIR/sub/" + name + "\n"},
		{"sub//", "sub//" + name + "\n", name + " is DIR/sub//" + name + "\n"},
		{"./sub", "./sub/" + name + "\n", name + " is DIR/sub/" + name + "\n"},
		{"sub/.", "sub/./" + name + "\n", name + " is DIR/sub/./" + name + "\n"},
	}
	for _, c := range cases {
		wantV := strings.ReplaceAll(c.wantV, "DIR", filepath.ToSlash(dir))
		got := runWithPath(t, dir, c.path, `command -v `+name)
		if got.stdout != wantV {
			t.Errorf("PATH=%q → -v 得 %q，期望 %q", c.path, got.stdout, wantV)
		}
		wantVerbose := strings.ReplaceAll(c.wantVerbose, "DIR", filepath.ToSlash(dir))
		got = runWithPath(t, dir, c.path, `command -V `+name)
		if got.stdout != wantVerbose {
			t.Errorf("PATH=%q → -V 得 %q，期望 %q", c.path, got.stdout, wantVerbose)
		}
	}
}

func TestCommandPathLookupSkipsDirsOnly(t *testing.T) {
	dir := tempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "d1", "somedir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, filepath.Join(dir, "d1"), "ntool", false) // 非可执行
	// 目录名不算命中
	got := runWithPath(t, dir, "d1", `command -v somedir; echo "rc=$?"`)
	if got.stdout != "rc=1\n" {
		t.Errorf("目录不算命中，实得 %q", got.stdout)
	}
	// 非可执行文件**算**命中（bash 口径）
	got = runWithPath(t, dir, "d1", `command -v ntool`)
	if got.stdout != "d1/ntool\n" {
		t.Errorf("非可执行文件应命中，实得 %q", got.stdout)
	}
}

func TestCommandPathLookupPrefersExecutable(t *testing.T) {
	dir := tempDir(t)
	for _, d := range []string{"d1", "d2", "d3"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 同名文件：d1 里不可执行（裸名，Windows 上同样不可执行），d3 里可执行。
	writeTool(t, filepath.Join(dir, "d1"), "nx", false)
	exe := exeName("nx")
	writeTool(t, filepath.Join(dir, "d3"), exe, true)
	// 先找可执行的那一个：Windows 上查找时会给裸名补 .exe，命中 d3 的 nx.exe
	got := runWithPath(t, dir, "d1"+testPathSep+"d3", `command -v nx`)
	if want := "d3/" + exe + "\n"; got.stdout != want {
		t.Errorf("应优先可执行，实得 %q，期望 %q", got.stdout, want)
	}
}

func TestCommandPathLookupFallsBackToNonExecutable(t *testing.T) {
	dir := tempDir(t)
	for _, d := range []string{"d4", "d5"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTool(t, filepath.Join(dir, "d4"), "ny", false)
	writeTool(t, filepath.Join(dir, "d5"), "ny", false)
	// 全都没有可执行时，回落到**第一个**非目录文件
	got := runWithPath(t, dir, "d4"+testPathSep+"d5", `command -v ny`)
	if got.stdout != "d4/ny\n" {
		t.Errorf("应回落到第一个非目录文件，实得 %q", got.stdout)
	}
}

func TestCommandPathLookupEmptyEntryMeansDot(t *testing.T) {
	dir := tempDir(t)
	writeTool(t, dir, "ctool", true)
	for _, p := range []string{"", testPathSep, "." + testPathSep, testPathSep + testPathNoTools} {
		got := runWithPath(t, dir, p, `command -v ctool`)
		if got.stdout != "./ctool\n" {
			t.Errorf("PATH=%q → %q，期望 ./ctool", p, got.stdout)
		}
	}
}

func TestCommandPathLikeArgNeedsExecutable(t *testing.T) {
	dir := tempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := exeName("t1")
	writeTool(t, filepath.Join(dir, "sub"), name, true)
	writeTool(t, dir, "ne1", false)
	if err := os.MkdirAll(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ src, want string }{
		// 含 '/' 的参数：命中即原样回显，不做绝对化
		{`command -v ./sub/` + name, "./sub/" + name + "\n"},
		{`command -V ./sub/` + name, "./sub/" + name + " is ./sub/" + name + "\n"},
		{`command -v ne1 2>/dev/null; command -v ./ne1`, ""},
		{`command -v ./ne1; echo "rc=$?"`, "rc=1\n"},
		{`command -v ./adir; echo "rc=$?"`, "rc=1\n"},
	}
	for _, c := range cases {
		got := runWithPath(t, dir, testPathNoTools, c.src)
		if got.stdout != c.want {
			t.Errorf("%s → %q，期望 %q", c.src, got.stdout, c.want)
		}
	}
}

func TestCommandOptionParsing(t *testing.T) {
	dir := tempDir(t)
	// 选项簇：-v/-V 后出现者生效
	got := runScript(t, dir, `command -vV ls; command -Vv ls`)
	if !strings.Contains(got.stdout, " is ") || strings.HasPrefix(got.stdout, "/") {
		t.Errorf("-vV 应由 -V 生效，实得 %q", got.stdout)
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	absLike := len(lines) == 2 && (strings.HasPrefix(lines[1], "/") || (len(lines[1]) > 1 && lines[1][1] == ':'))
	if len(lines) != 2 || !strings.Contains(lines[0], " is ") || !absLike {
		t.Errorf("-vV/-Vv 顺序语义错，实得 %q", got.stdout)
	}
	// `--` 终止选项
	if got := runScript(t, dir, `command -v -- ls`); got.stdout == "" {
		t.Error("`command -v -- ls` 应输出 ls 路径")
	}
	// 选项在第一个非选项参数处终止：`command echo -v` 的 -v 是 echo 的参数
	if got := runScript(t, dir, `command echo -v`); got.stdout != "-v\n" {
		t.Errorf("`command echo -v` 应输出 -v，实得 %q", got.stdout)
	}
}

func TestCommandInvalidOption(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `command -z ls`)
	want := "/bin/bash: line 1: command: -z: invalid option\ncommand: usage: command [-pVv] command [arg ...]\n"
	if got.stderr != want {
		t.Errorf("stderr 为 %q，期望 %q", got.stderr, want)
	}
	if got.code != 2 {
		t.Errorf("退出码为 %d，期望 2", got.code)
	}
	// `--xxx` 报的是 `--` 而不是整个参数（bash 口径）
	got = runScript(t, dir, `command --bogus ls`)
	if !strings.Contains(got.stderr, "command: --: invalid option") {
		t.Errorf("`--bogus` 应报 `--`，实得 %q", got.stderr)
	}
}

func TestCommandExecuteSkipsFunctions(t *testing.T) {
	dir := tempDir(t)
	// 同名函数存在时，`command f` 跳过函数查找 → 外部程序找不到 → 127
	got := runScript(t, dir, `f(){ echo FUNC; }; command f`)
	if got.stdout != "" {
		t.Errorf("应跳过函数，实得 stdout=%q", got.stdout)
	}
	if !strings.Contains(got.stderr, "f: command not found") {
		t.Errorf("应报 command not found，实得 %q", got.stderr)
	}
	if got.code != 127 {
		t.Errorf("退出码为 %d，期望 127", got.code)
	}
	// 不用 command 时函数照常生效
	if got := runScript(t, dir, `f(){ echo FUNC; }; f`); got.stdout != "FUNC\n" {
		t.Errorf("直接调用应为 FUNC，实得 %q", got.stdout)
	}
	// 内建与外部程序照常执行
	if got := runScript(t, dir, `command echo hi`); got.stdout != "hi\n" {
		t.Errorf("`command echo hi` 应为 hi，实得 %q", got.stdout)
	}
	if got := runScript(t, dir, `command cd /tmp && pwd`); got.stdout != DisplayPath(plat.FromHost("/tmp"))+"\n" {
		t.Errorf("`command cd` 应切目录，实得 %q", got.stdout)
	}
}

func TestCommandPDoesNotLeakPath(t *testing.T) {
	dir := tempDir(t)
	// -p 只影响本次查找，不改子进程的 PATH：拿 `sh -c 'echo $PATH'` 读子进程环境。
	// Windows 上没有 sh 这类「必定存在且能打印自身环境」的程序（见下面 Skip 的理由）。
	customPath := skippableShPath(t)
	got := runWithPath(t, dir, customPath, `command -p sh -c 'echo $PATH'`)
	if got.stdout != customPath+"\n" {
		t.Errorf("-p 不应改子进程 PATH，实得 %q，期望 %q", got.stdout, customPath)
	}
}

func TestCommandPVUsesDefaultPath(t *testing.T) {
	dir := tempDir(t)
	name := exeName("ptool")
	writeTool(t, dir, name, true)
	customPath := dir + testPathSep + testPathNoTools
	// 自定义 PATH 里有 ptool，但 -p 改用 plat.DefaultPath → miss
	got := runWithPath(t, dir, customPath, `command -p -v ptool; echo "rc=$?"`)
	if got.stdout != "rc=1\n" {
		t.Errorf("-p 应改用默认 PATH，实得 %q", got.stdout)
	}
	// 不加 -p 时能在自定义 PATH 里找到
	got = runWithPath(t, dir, customPath, `command -v ptool`)
	if want := filepath.ToSlash(dir) + "/" + name + "\n"; got.stdout != want {
		t.Errorf("不加 -p 应命中自定义 PATH，实得 %q，期望 %q", got.stdout, want)
	}
	// 执行态同理：-p 只查默认 PATH，自定义 PATH 里的 ptool 不该被执行到
	got = runWithPath(t, dir, customPath, `command -p ptool; echo "rc=$?"`)
	if !strings.Contains(got.stdout, "rc=127") {
		t.Errorf("-p 执行态应只有默认 PATH 生效（ptool 不可达 → 127），实得 %q", got.stdout)
	}
}
