package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LaoQi/battlelab/vbash"
)

// 宿主协议与路径形态的回归用例：v0.4.0 把外部命令改由独立宿主执行后，
// 协议里带的是解释器的**内部** POSIX 路径（/c/Users/...），宿主的 exec.Cmd.Dir
// 无法 chdir；又因为宿主 stderr 被丢弃、启动失败也不回报，父进程只能等满
// 2s 握手超时并误报「会话宿主不可用」。这里锁住四件事：
//   1. 脚本内改 PATH 后，命令仍按 shell 的 PATH 解析（宿主的进程环境无关）；
//   2. 命令起不来时立刻失败，不吞 2s，也不误报成「宿主不可用」；
//   3. 协议带出去的 cwd 是宿主形态；
//   4. 命令的 $0 仍是用户写的原文（argv 不被路径替换污染）。
//
// 全部用例不带平台 tag：这些是**跨平台**语义，Windows 上正是最严重的那一侧。

// elapsedGuard 是「命令起不来」这类失败的上限：宿主就绪后一条命令的失败
// 应该在毫秒级返回；撞上它说明又退回到「等满握手超时」。
const elapsedGuard = time.Second

func runTimed(t *testing.T, command string) (*Result, time.Duration) {
	t.Helper()
	start := time.Now()
	res := testVbashTool(t).run(context.Background(), request{Command: command, TimeoutSec: 10})
	return res, time.Since(start)
}

// TestHostRespectsScriptPath：脚本里改 PATH 后命令必须按新 PATH 解析。
// 宿主进程有自己的进程环境，`exec.Command` 的 PATH 查找与解释器的 `$PATH` 无关；
// 解释器必须把解析好的宿主路径交给宿主，而不是把裸名字甩过去。
func TestHostRespectsScriptPath(t *testing.T) {
	dir := t.TempDir()
	name := hostExeName("pathtool")
	writeHostExe(t, filepath.Join(dir, name))
	res, elapsed := runTimed(t, "PATH="+dir+hostPathSep+"$PATH; pathtool")
	if !strings.Contains(shellStdout(res), "PAThtool-OK") {
		t.Fatalf("脚本内改 PATH 后命令应执行成功：%+v（耗时 %v）", res, elapsed)
	}
	if res.Err != "" {
		t.Errorf("不应有错误：%q", res.Err)
	}
}

// TestHostStartFailureIsFastAndHonest：命令起不来时不吞握手超时、
// 不误报「会话宿主不可用」，且带上真实的失败原因。
func TestHostStartFailureIsFastAndHonest(t *testing.T) {
	res, elapsed := runTimed(t, "no-such-cmd-xyz --flag")
	if elapsed > elapsedGuard {
		t.Errorf("命令不存在应在毫秒级失败，实际耗时 %v（怀疑又走成了握手超时）", elapsed)
	}
	if strings.Contains(res.String(), "会话宿主不可用") {
		t.Errorf("宿主是好的，不该报「会话宿主不可用」：%q", res.String())
	}
	if res.ExitCode != 127 {
		t.Errorf("命令不存在的退出码应为 127，实得 %d（%q）", res.ExitCode, res.String())
	}
}

// TestHostStartFailureInHost：直接向宿主发一条**起不来**的请求，锁住
// 「命令自身启动失败」与「宿主不可用」必须区分开。
//
// 这条路径解释器侧到不了（它在把命令交给宿主前就自己 stat 过了），但生产上会撞见：
// Windows 上 cwd 是 `/c/...` 这类内部形态时，父目录 stat 得好好的，宿主的
// `exec.Cmd.Dir` 却 chdir 失败——正是 v0.4.0 的发病点。那时若不回报 pgid=0，
// 父进程只能等满 2s 握手超时并误报「会话宿主不可用」。
func TestHostStartFailureInHost(t *testing.T) {
	h, err := startSessionHost()
	if err != nil {
		t.Fatalf("起宿主失败：%v", err)
	}
	defer h.close()

	dir := t.TempDir()
	var out, errb strings.Builder
	start := time.Now()
	code, runErr := h.run(context.Background(), reqPayload{
		path: filepath.Join(dir, "does-not-exist", hostExeName("nope")),
		argv: []string{"nope"},
		cwd:  dir,
		env:  os.Environ(),
	}, &vbash.IO{In: emptyStdin(), Out: &out, Err: &errb})
	elapsed := time.Since(start)

	if code != 127 {
		t.Errorf("启动失败应返回 127，实得 %d", code)
	}
	if errors.Is(runErr, errHostUnavailable) {
		t.Errorf("命令起不来≠宿主不可用，不该返回 errHostUnavailable（耗时 %v）", elapsed)
	}
	if elapsed > elapsedGuard {
		t.Errorf("启动失败应在毫秒级返回，实际耗时 %v（怀疑又等满了握手超时）", elapsed)
	}
	if errb.Len() == 0 {
		t.Errorf("宿主的启动失败原因应写回 stderr，实际为空")
	}
}

// TestHostCwdIsHostForm：协议带出去的 cwd 必须是宿主形态（POSIX 原样，
// Windows 上不能是 /c/... 这类内部形态），否则宿主的 exec.Cmd.Dir 无法 chdir。
//
// 注意这条用例在 POSIX 上是**恒等**的（FromHost/ToHost 都是 identity），
// 把 r.HostDir() 换回 r.Env.Dir() 它也不会红——形态转换只在 Windows 上发生，
// 该侧要靠真机 `go test ./...` 才验证得到（`make cross` 只编译）。
// 这里保留它是为了让 pwd 真的跑一遍宿主 chdir，挡住「协议字段接错」这类错。
func TestHostCwdIsHostForm(t *testing.T) {
	dir := t.TempDir()
	req := decodeRoundTrip(t, reqPayload{
		path:  "/bin/pwd",
		argv:  []string{"pwd"},
		cwd:   dir,
		env:   []string{"A=b"},
		front: true,
	})
	if req.cwd != dir {
		t.Errorf("cwd 往返后为 %q，期望 %q", req.cwd, dir)
	}
	// 真的跑一条 pwd：宿主的 chdir 若失败，这里会退化成错误而不是目录。
	res := testVbashTool(t).run(context.Background(), request{Command: "pwd", TimeoutSec: 10, Cwd: dir})
	if got := normShellPath(strings.TrimSpace(shellStdout(res))); got != normShellPath(dir) {
		t.Fatalf("pwd 应输出指定目录：got %q want %q（%+v）", got, dir, res)
	}
	if res.Err != "" {
		t.Errorf("不应有错误：%q", res.Err)
	}
}

// TestHostKeepsArgv0：请求里 path 与 argv 是两个字段——path 是解析好的可执行
// 路径，argv[0] 是用户写的原文。混成一个会让子进程的 `$0` 变成绝对路径。
func TestHostKeepsArgv0(t *testing.T) {
	req := decodeRoundTrip(t, reqPayload{
		path: "/resolved/abs/tool",
		argv: []string{"tool", "--flag"},
		cwd:  "/tmp",
	})
	if req.path != "/resolved/abs/tool" {
		t.Errorf("path 往返后为 %q", req.path)
	}
	if len(req.argv) != 2 || req.argv[0] != "tool" {
		t.Errorf("argv 应保持用户原文，实得 %v", req.argv)
	}
}

// writeHostExe 造一个本平台可执行的小脚本。
func writeHostExe(t *testing.T, path string) {
	t.Helper()
	body := "#!/bin/sh\necho PAThtool-OK\n"
	if runtime.GOOS == "windows" {
		body = "@echo PAThtool-OK\r\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func decodeRoundTrip(t *testing.T, in reqPayload) reqPayload {
	t.Helper()
	out, ok := decodeReq(encodeReq(in))
	if !ok {
		t.Fatal("decodeReq 失败")
	}
	return out
}

// TestHostErrorMessageKeepsStderr：握手失败时的诊断必须带上宿主的 stderr，
// 同时保持 errors.Is(err, errHostUnavailable) 成立（调用方靠它区分「宿主没起来」）。
// 丢了 stderr，同类问题就只剩「会话宿主不可用」这一句，永远只能翻代码。
func TestHostErrorMessageKeepsStderr(t *testing.T) {
	if err := hostErrorMessage(nil); !errors.Is(err, errHostUnavailable) {
		t.Errorf("无 stderr 时应原样返回 errHostUnavailable：%v", err)
	}
	b := &tailBuffer{max: 64}
	_, _ = b.Write([]byte("panic: boom\n"))
	err := hostErrorMessage(b)
	if !errors.Is(err, errHostUnavailable) {
		t.Errorf("带 stderr 时仍须 errors.Is 成立：%v", err)
	}
	if !strings.Contains(err.Error(), "panic: boom") {
		t.Errorf("诊断应包含宿主 stderr：%q", err.Error())
	}
}

// TestTailBufferKeepsTail：环形缓冲只留尾部，避免宿主狂打日志撑爆内存。
func TestTailBufferKeepsTail(t *testing.T) {
	b := &tailBuffer{max: 8}
	_, _ = b.Write([]byte("0123456789abcdef"))
	if got := b.String(); got != "89abcdef" {
		t.Errorf("应只留尾部 8 字节，实得 %q", got)
	}
	_, _ = b.Write([]byte("XY"))
	if got := b.String(); got != "abcdefXY" {
		t.Errorf("继续写应滚动保留尾部，实得 %q", got)
	}
}

// TestHostNeverChdirsProcess：宿主只设子进程的 `cmd.Dir`，**从不调 `os.Chdir`**
// ——进程 cwd 恒为启动目录是 §9 不变量 4（`docs/shell-tool.md`），宿主进程也必须守住，
// 否则宿主里同时跑的多条命令会互相改对方的工作目录。
//
// 这里用源码级断言而不是运行期探针：运行期看到的 cwd 无法区分「谁改的」，
// 而这正是要禁止的那件事。
func TestHostNeverChdirsProcess(t *testing.T) {
	// 两个平台分片都必须把协议里的 cwd 交给子进程（cmd.Dir），而不是改进程 cwd。
	for _, f := range []string{"sessionhost_posix.go", "sessionhost_windows.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", f, err)
		}
		if strings.Contains(string(src), "os.Chdir") {
			t.Errorf("%s 出现 os.Chdir：宿主只应设 cmd.Dir（子进程 cwd），"+
				"不得改进程 cwd（§9 不变量 4）", f)
		}
		if !strings.Contains(string(src), "cmd.Dir = req.cwd") {
			t.Errorf("%s 应把协议里的 cwd 交给 cmd.Dir", f)
		}
	}
}
