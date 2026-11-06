package shell

import (
	"context"
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
		Vbash:     true,
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
	if got := strings.TrimSpace(shellStdout(res)); got != cwd {
		t.Errorf("默认目录应为进程 cwd: got %q want %q", got, cwd)
	}
}

func TestVbashExplicitCwd(t *testing.T) {
	dir := t.TempDir()
	res := testVbashTool(t).run(context.Background(), request{Command: "pwd", Cwd: dir, TimeoutSec: 10})
	if got := strings.TrimSpace(shellStdout(res)); got != dir {
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

func TestVbashInteractiveBypasses(t *testing.T) {
	tool := testVbashTool(t, func(c *Config) { c.Console = nullConsole{} })
	res := tool.run(context.Background(), request{Command: "echo hi", Interactive: true, TimeoutSec: 10})
	if res.Err != ErrNoLend.Error() {
		t.Fatalf("interactive 应走终端借出路径而非解释器: %+v", res)
	}
}

func TestVbashConfigAcceptsNonPosixShell(t *testing.T) {
	tool, err := New(Config{Override: "pwsh", LookPath: lookPathStub("pwsh"), Vbash: true, Environ: os.Environ, Programs: []string{}})
	if err != nil {
		t.Fatalf("非 POSIX shell 上启用 vbash 应被接受（跨平台归一是本特性目标）: %v", err)
	}
	desc := tool.toolDesc()
	if !strings.Contains(desc, "内置 bash 解释器") {
		t.Errorf("描述应说明由内置解释器执行: %q", desc)
	}
	if strings.Contains(desc, "PowerShell 语法") {
		t.Errorf("启用 vbash 后描述不应再声称 PowerShell 语法: %q", desc)
	}
	if !strings.Contains(desc, "盘符写作 /c/ 形式") {
		t.Errorf("非 POSIX 宿主上描述应给出路径写法提示: %q", desc)
	}
}

func TestVbashConfigRequiresEnviron(t *testing.T) {
	if _, err := New(Config{LookPath: lookPathStub("bash"), Vbash: true}); err == nil {
		t.Fatal("启用 vbash 应要求注入 Environ")
	}
}

func TestVbashDescriptionMentionsInterpreter(t *testing.T) {
	if !strings.Contains(testVbashTool(t).toolDesc(), "内置 bash 解释器") {
		t.Error("启用 vbash 时描述应说明解释器")
	}
	if strings.Contains(testShellTool(t).toolDesc(), "内置 bash 解释器") {
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
