package shell

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LaoQi/battlelab/vbash"
)

func testVbashTool(t *testing.T, mutate ...func(*Config)) *Tool {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Fatalf("UserHomeDir 不可用: err=%v home=%q", err, home)
	}
	cfg := Config{
		Home:      home,
		Workspace: func() string { return cwd },
		Override:  BuiltinShellName,
		Environ:   os.Environ,
	}
	for _, f := range mutate {
		f(&cfg)
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	tool, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func runVbashString(t *testing.T, ctx context.Context, command string, timeoutSec int) string {
	t.Helper()
	return testVbashTool(t).run(ctx, request{Command: command, TimeoutSec: timeoutSec}).String()
}

func TestVbashBuiltinAndExpansion(t *testing.T) {
	got := runVbashString(t, context.Background(), "echo $((1+2)) hi", 10)
	if !strings.Contains(got, "stdout:\n3 hi") {
		t.Errorf("got %q", got)
	}
}

func TestVbashStderrAndExitCode(t *testing.T) {
	res := testVbashTool(t).run(context.Background(), request{Command: "echo oops >&2; exit 3", TimeoutSec: 10})
	if !strings.Contains(res.String(), "stderr:\noops") {
		t.Errorf("stderr 缺失: %q", res.String())
	}
	if res.ExitCode != 3 {
		t.Errorf("退出码应为 3: %+v", res)
	}
}

func TestVbashExternalPassthrough(t *testing.T) {
	res := testVbashTool(t).run(context.Background(), request{Command: "ls /nonexistent-xyz", TimeoutSec: 10})
	if res.ExitCode == 0 {
		t.Fatalf("外部程序失败应保留退出码: %+v", res)
	}
	if len(res.Stderr) == 0 {
		t.Errorf("外部程序 stderr 应被捕获: %+v", res)
	}
}

func TestVbashEnvInjectedToExternal(t *testing.T) {
	tool := testVbashTool(t, func(c *Config) {
		c.Environ = func() []string { return append(os.Environ(), "VBASH_PROBE=yes") }
	})
	got := tool.run(context.Background(), request{Command: "sh -c 'echo probe=$VBASH_PROBE'", TimeoutSec: 10}).String()
	if !strings.Contains(got, "probe=yes") {
		t.Errorf("注入的环境变量应到达外部命令: %q", got)
	}
}

func TestVbashPwdDefaultCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	res := testVbashTool(t).run(context.Background(), request{Command: "pwd", TimeoutSec: 10})
	if got := normShellPath(strings.TrimSpace(shellStdout(res))); got != normShellPath(cwd) {
		t.Errorf("默认目录应为进程 cwd: got %q want %q", got, cwd)
	}
}

func TestVbashExplicitCwd(t *testing.T) {
	dir := t.TempDir()
	res := testVbashTool(t).run(context.Background(), request{Command: "pwd", Cwd: dir, TimeoutSec: 10})
	if got := normShellPath(strings.TrimSpace(shellStdout(res))); got != normShellPath(dir) {
		t.Errorf("cwd 参数应生效: got %q want %q", got, dir)
	}
}

func TestVbashUnsupportedDoesNotFallBack(t *testing.T) {
	res := testVbashTool(t).run(context.Background(), request{Command: "case x in a) echo a;; esac", TimeoutSec: 10})
	if res.ExitCode != 2 {
		t.Errorf("不支持构造应报退出码 2: %+v", res)
	}
	if !strings.Contains(res.Err, "不支持的构造") {
		t.Errorf("应报不支持而非回落执行: %+v", res)
	}
	if strings.Contains(shellStdout(res), "a\n") {
		t.Errorf("不应执行: %+v", res)
	}
}

func TestVbashSyntaxErrorReported(t *testing.T) {
	res := testVbashTool(t).run(context.Background(), request{Command: "echo 'unclosed", TimeoutSec: 10})
	if res.ExitCode != 2 {
		t.Errorf("语法错误应报退出码 2: %+v", res)
	}
	if len(res.Stderr) == 0 || !strings.Contains(res.Stderr[0].Data, "syntax error") && !strings.Contains(res.Stderr[0].Data, "unexpected EOF") {
		t.Errorf("应输出语法诊断: %+v", res.Stderr)
	}
	if res.Err != "" {
		t.Errorf("语法错误不应重复写入 error 行: %q", res.Err)
	}
}

func TestVbashTimeout(t *testing.T) {
	res := testVbashTool(t).run(context.Background(), request{Command: "sleep 5", TimeoutSec: 1})
	if !res.TimedOut || !strings.Contains(res.String(), "超时") {
		t.Fatalf("应报超时: %+v", res.String())
	}
}

func TestVbashLoopHonorsDeadline(t *testing.T) {
	start := time.Now()
	res := testVbashTool(t).run(context.Background(), request{Command: "while true; do :; done", TimeoutSec: 1})
	if !res.TimedOut {
		t.Fatalf("纯内建循环应被超时打断: %+v", res)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("超时响应过慢: %v", elapsed)
	}
}

func TestVbashInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	res := testVbashTool(t).run(ctx, request{Command: "sleep 5", TimeoutSec: 60})
	if !res.Interrupted {
		t.Fatalf("应报中断: %+v", res)
	}
}

func TestVbashInterruptNotStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := testVbashTool(t).run(ctx, request{Command: "echo hi", TimeoutSec: 60})
	if !res.Interrupted || !res.NotStarted {
		t.Fatalf("应报未执行: %+v", res)
	}
	if strings.Contains(shellStdout(res), "hi") {
		t.Errorf("命令不应执行: %+v", res)
	}
}

func TestVbashTruncation(t *testing.T) {
	got := runVbashString(t, context.Background(), "printf '%0.sa' $(seq 1 200000)", 30)
	if !strings.Contains(got, "中间截断 150000 字节") {
		t.Errorf("应包含中间截断标记: len=%d", len(got))
	}
}

func TestVbashCwdFieldKeptOnlyWhenExplicit(t *testing.T) {
	implicit := testVbashTool(t).run(context.Background(), request{Command: "true", TimeoutSec: 10})
	if implicit.Cwd != "" {
		t.Errorf("未显式指定 cwd 时结果不应回填: %q", implicit.Cwd)
	}
	dir := t.TempDir()
	explicit := testVbashTool(t).run(context.Background(), request{Command: "true", Cwd: dir, TimeoutSec: 10})
	if explicit.Cwd != dir {
		t.Errorf("显式 cwd 应回填: %q", explicit.Cwd)
	}
}

// vbash 下 interactive 仍进解释器，且**不需要**向调用方借终端：
// 宿主自建虚拟 tty，因此注入 nullConsole 也能正常执行。
func TestVbashInteractiveRunsWithoutBorrowingTerminal(t *testing.T) {
	tool := testVbashTool(t, func(c *Config) { c.Console = nullConsole{} })
	res := tool.run(context.Background(), request{Command: "echo hi", Interactive: true, TimeoutSec: 10})
	if res.Err != "" {
		t.Fatalf("interactive 不应因借不到终端而报错: %+v", res)
	}
	if !strings.Contains(shellStdout(res), "hi") {
		t.Errorf("interactive 应正常执行命令: %+v", res)
	}
}

func TestVbashIgnoresHostShell(t *testing.T) {
	tool, err := New(Config{LookPath: lookPathStub("pwsh"), Override: BuiltinShellName, Environ: os.Environ, Programs: []string{}})
	if err != nil {
		t.Fatalf("shell: vbash 不应依赖宿主 shell 是否存在（跨平台归一是本特性目标）: %v", err)
	}
	if !tool.profile.Builtin {
		t.Error("shell: vbash 应解析为内置解释器 profile")
	}
	desc := tool.toolDesc()
	if !strings.Contains(desc, "内置 bash 解释器") {
		t.Errorf("描述应说明由内置解释器执行: %q", desc)
	}
	if strings.Contains(desc, "PowerShell 语法") {
		t.Errorf("内置解释器模式下描述不应再声称 PowerShell 语法: %q", desc)
	}
}

func TestVbashConfigRequiresEnviron(t *testing.T) {
	if _, err := New(Config{LookPath: lookPathStub("bash"), Override: BuiltinShellName}); err == nil {
		t.Fatal("启用 vbash 应要求注入 Environ")
	}
}

func TestVbashDescriptionMentionsInterpreter(t *testing.T) {
	if !strings.Contains(testVbashTool(t).toolDesc(), "内置 bash 解释器") {
		t.Error("启用 vbash 时描述应说明解释器")
	}
	if strings.Contains(testShellTool(t, func(c *Config) { c.Override = "" }).toolDesc(), "内置 bash 解释器") {
		t.Error("未启用时描述不应提及解释器")
	}
}

func TestVbashDescriptionListsUnsupported(t *testing.T) {
	desc := testVbashTool(t).toolDesc()
	for _, want := range []string{"case", "[[ ]]", "反引号", "<<<", "数组", "进程替换"} {
		if !strings.Contains(desc, want) {
			t.Errorf("描述应列出不支持构造 %q，避免模型只能试错: %q", want, desc)
		}
	}
}

func TestVbashFenceWiring(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("平台不支持围栏")
	}
	fence := vbash.NewFence()
	tool := testVbashTool(t, func(c *Config) { c.Fence = fence })
	res := tool.run(context.Background(), request{Command: "sleep 43 & echo pid=$!", TimeoutSec: 10})
	if !strings.Contains(res.String(), "pid=") {
		t.Fatalf("后台命令未启动: %q", res.String())
	}
	time.Sleep(300 * time.Millisecond)
	if got := fence.Reap(); got != 1 {
		t.Fatalf("经 Tool 起的后台作业应进围栏，Reap 清理 %d 个", got)
	}
}

func TestVbashViaShellConfigValue(t *testing.T) {
	cfg := Config{LookPath: lookPathStub("bash"), Override: "vbash", Environ: os.Environ, Programs: []string{}}
	tool, err := New(cfg)
	if err != nil {
		t.Fatalf("shell: vbash 应与 bash/pwsh 同级被接受: %v", err)
	}
	if !tool.profile.Builtin {
		t.Fatal("shell: vbash 应解析为内置解释器")
	}
	if tool.profile.Name != BuiltinShellName {
		t.Errorf("Name 应为 %q，实际 %q", BuiltinShellName, tool.profile.Name)
	}
	if tool.profile.Path != "" {
		t.Errorf("内置解释器不应有磁盘路径，实际 %q", tool.profile.Path)
	}
}

func TestVbashShellNameCaseInsensitive(t *testing.T) {
	for _, v := range []string{"vbash", "VBASH", " Vbash "} {
		tool, err := New(Config{LookPath: lookPathStub("bash"), Override: v, Environ: os.Environ, Programs: []string{}})
		if err != nil {
			t.Fatalf("%q 应解析为内置解释器: %v", v, err)
		}
		if !tool.profile.Builtin {
			t.Errorf("%q 应解析为内置解释器", v)
		}
	}
}

func TestRegularShellStaysNonBuiltin(t *testing.T) {
	for _, v := range []string{"bash", "sh", "zsh"} {
		tool, err := New(Config{LookPath: lookPathStub(v), Override: v, Programs: []string{}})
		if err != nil {
			t.Fatalf("%q 应正常解析: %v", v, err)
		}
		if tool.profile.Builtin {
			t.Errorf("%q 不应被当作内置解释器", v)
		}
	}
}

func TestResolveBuiltinNeedsNoHostShell(t *testing.T) {
	inv, err := resolve(BuiltinShellName, lookPathStub("bash", "sh"))
	if err != nil {
		t.Fatalf("Resolve(%q) 失败: %v", BuiltinShellName, err)
	}
	if !inv.Builtin {
		t.Fatal("内置解释器应标记 Builtin，供调用方选执行路径")
	}
	if inv.Name != BuiltinShellName {
		t.Errorf("Name 应为 %q，实际 %q", BuiltinShellName, inv.Name)
	}
	if len(inv.Argv) != 0 {
		t.Errorf("内置解释器无需宿主 argv，实际 %q", inv.Argv)
	}
}

func TestResolveBuiltinIgnoresLookPath(t *testing.T) {
	called := false
	lookPath := func(string) (string, error) {
		called = true
		return "", errors.New("not found")
	}
	if _, err := resolve("vbash", lookPath); err != nil {
		t.Fatalf("内置解释器不应依赖 PATH: %v", err)
	}
	if called {
		t.Error("解析内置解释器不应查 PATH")
	}
}

// TestVbashCommandBuiltinWithHandler 锁住生产路径的关键点：tanya 装配 vbash 时会注入
// Console.Handler（tools/shell/vbash.go 的 t.hostHandler）。`command -v` 有自己的
// bash 口径查找（`commandPathLookup`：非可执行也算命中、路径不 clean），不复用
// `lookPath`，两者不能混。
func TestVbashCommandBuiltinWithHandler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 用例：断言的是 /usr/bin 这类路径形态")
	}
	tool := testVbashTool(t)
	res := tool.run(context.Background(), request{
		Command:    `command -v echo; command -v nosuchtool-xyz; echo "rc=$?"`,
		TimeoutSec: 10,
	})
	got := res.String()
	// 内建 echo 应被识别为内建（-v 只回名字）
	if !strings.Contains(got, "echo") {
		t.Errorf("`command -v echo` 应命中内建，实际 %q", got)
	}
	// miss 必须真的 miss（PATH 里没有这个名字就不该打印它）
	if strings.Contains(got, "nosuchtool-xyz") {
		t.Errorf("`command -v nosuchtool-xyz` 不应命中，实际 %q", got)
	}
	if !strings.Contains(got, "rc=1") {
		t.Errorf("全 miss 时退出码应为 1，实际 %q", got)
	}
}

// TestVbashCommandVBashDescMentionsCommand 确认工具描述里 command 不再落在"不支持"口径。
func TestVbashCommandNotInUnsupportedList(t *testing.T) {
	desc := testVbashTool(t).toolDesc()
	if strings.Contains(desc, "command/") || strings.Contains(desc, "`command`") {
		t.Errorf("command 已实现，不应出现在不支持清单里: %q", desc)
	}
}
