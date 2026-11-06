package vbash

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type safeBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func runJobs(t *testing.T, dir, src string, fence *Fence, timeout time.Duration) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out, errb safeBuf
	cfg := Config{
		Dir:       dir,
		Home:      dir,
		Env:       []string{"PATH=/usr/local/bin:/usr/bin:/bin"},
		Stdout:    &out,
		Stderr:    &errb,
		Stdin:     strings.NewReader(""),
		ShellName: "/bin/bash",
		Fence:     fence,
	}
	code, _ := RunScript(ctx, src, cfg)
	return code, out.String(), errb.String()
}

func TestBackgroundDoesNotBlock(t *testing.T) {
	start := time.Now()
	code, out, _ := runJobs(t, t.TempDir(), "sleep 2 & echo launched", nil, 10*time.Second)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("退出码 %d", code)
	}
	if !strings.Contains(out, "launched") {
		t.Fatalf("stdout %q", out)
	}
	if elapsed > time.Second {
		t.Fatalf("& 未异步，耗时 %v", elapsed)
	}
}

func TestBackgroundParallel(t *testing.T) {
	start := time.Now()
	code, out, _ := runJobs(t, t.TempDir(), "sleep 1 & sleep 1 & sleep 1 & wait; echo done", nil, 15*time.Second)
	elapsed := time.Since(start)
	if code != 0 || !strings.Contains(out, "done") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("后台未并行，耗时 %v", elapsed)
	}
}

func TestBackgroundErrexit(t *testing.T) {
	code, out, _ := runJobs(t, t.TempDir(), "set -e; false & echo after; echo end", nil, 10*time.Second)
	if code != 0 {
		t.Fatalf("退出码 %d（bash 应为 0）", code)
	}
	if out != "after\nend\n" {
		t.Fatalf("stdout %q", out)
	}
}

func TestBackgroundPidAndWaitStatus(t *testing.T) {
	code, out, _ := runJobs(t, t.TempDir(), `sh -c 'exit 3' & p=$!; wait $p; echo st=$?; echo p=$p`, nil, 10*time.Second)
	if code != 0 {
		t.Fatalf("退出码 %d", code)
	}
	if !strings.Contains(out, "st=3") {
		t.Fatalf("wait 未返回作业状态: %q", out)
	}
	if strings.Contains(out, "p=\n") || strings.Contains(out, "p= ") {
		t.Fatalf("$! 为空: %q", out)
	}
}

func TestBackgroundEnvIsolated(t *testing.T) {
	_, out, _ := runJobs(t, t.TempDir(), `x=1; x=2 & echo "x=$x"`, nil, 10*time.Second)
	if strings.TrimSpace(out) != "x=1" {
		t.Fatalf("后台赋值回写了父环境: %q", out)
	}
}

func TestFenceReapsBackground(t *testing.T) {
	if !plat.CanReap {
		t.Skip("平台不支持收容")
	}
	dir := t.TempDir()
	fence := NewFence()
	code, out, _ := runJobs(t, dir, "sleep 45 & echo pid=$!", fence, 5*time.Second)
	if code != 0 || !strings.Contains(out, "pid=") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	time.Sleep(200 * time.Millisecond)
	if got := fence.Reap(); got != 1 {
		t.Fatalf("Reap 清理 %d 个，期望 1", got)
	}
	time.Sleep(300 * time.Millisecond)
	if aliveByPattern("sleep 45") {
		t.Fatal("收割后 sleep 45 仍存活")
	}
}

func TestFenceKeepsSetsidEscape(t *testing.T) {
	if !plat.CanReap {
		t.Skip("平台不支持收容")
	}
	dir := t.TempDir()
	fence := NewFence()
	runJobs(t, dir, "setsid sleep 46 & echo launched", fence, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	if got := fence.Reap(); got != 0 {
		t.Fatalf("主动 setsid 应放行，却清理了 %d 个", got)
	}
	alive := aliveByPattern("sleep 46")
	exec.Command("bash", "-c", "pkill -9 -f '[s]leep 46'").Run()
	if !alive {
		t.Fatal("主动 setsid 的进程被错误清理")
	}
}

func TestBackgroundSurvivesRound(t *testing.T) {
	if !plat.CanReap {
		t.Skip("平台不支持收容")
	}
	dir := t.TempDir()
	fence := NewFence()
	runJobs(t, dir, "sleep 47 & echo round1", fence, 5*time.Second)
	runJobs(t, dir, "echo round2", fence, 5*time.Second)
	alive := aliveByPattern("sleep 47")
	fence.Reap()
	time.Sleep(300 * time.Millisecond)
	after := aliveByPattern("sleep 47")
	exec.Command("bash", "-c", "pkill -9 -f '[s]leep 47'").Run()
	if !alive {
		t.Fatal("后台进程未跨轮存活")
	}
	if after {
		t.Fatal("围栏收割后仍存活")
	}
}

func TestFenceCoversCompoundBackground(t *testing.T) {
	if !plat.CanReap {
		t.Skip("平台不支持收容")
	}
	dir := t.TempDir()
	fence := NewFence()
	runJobs(t, dir, "( sleep 44 ) & echo launched", fence, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	if got := fence.Reap(); got != 1 {
		t.Fatalf("复合 & 分支应被围栏覆盖，Reap 清理 %d 个，期望 1", got)
	}
	time.Sleep(300 * time.Millisecond)
	if aliveByPattern("sleep 44") {
		t.Fatal("收割后 ( ) & 内的进程仍存活")
	}
}

func TestFenceCoversOrphanOfExitedRoot(t *testing.T) {
	if !plat.CanReap {
		t.Skip("平台不支持收容")
	}
	dir := t.TempDir()
	fence := NewFence()
	runJobs(t, dir, `sh -c 'sleep 31 &' & echo launched`, fence, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	if !aliveByPattern("sleep 31") {
		t.Skip("孤儿未存活，无法验证收割")
	}
	fence.Reap()
	time.Sleep(300 * time.Millisecond)
	after := aliveByPattern("sleep 31")
	exec.Command("bash", "-c", "pkill -9 -f '[s]leep 31'").Run()
	if after {
		t.Fatal("根退出后的孤儿（同进程组）未被收割")
	}
}

func TestFenceReleasesSetsidGrandchild(t *testing.T) {
	if !plat.CanReap {
		t.Skip("平台不支持收容")
	}
	dir := t.TempDir()
	fence := NewFence()
	runJobs(t, dir, `python3 -c 'import subprocess,time; subprocess.Popen(["sleep","21"], start_new_session=True); time.sleep(2)' & echo launched`, fence, 5*time.Second)
	time.Sleep(500 * time.Millisecond)
	if got := fence.Reap(); got != 1 {
		t.Fatalf("应只收割根进程组 1 个，实际 %d", got)
	}
	alive := aliveByPattern("sleep 21")
	exec.Command("bash", "-c", "pkill -9 -f 'sleep 2[1]'").Run()
	if !alive {
		t.Fatal("孙进程 setsid 后应整棵放行，却被误杀")
	}
}

func TestWaitNoArgWaitsSubshellBranch(t *testing.T) {
	start := time.Now()
	code, out, _ := runJobs(t, t.TempDir(), "( sleep 1 ) & wait; echo done", nil, 10*time.Second)
	elapsed := time.Since(start)
	if code != 0 || !strings.Contains(out, "done") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("wait 无参未等待 goroutine 分支，耗时 %v", elapsed)
	}
}

func TestWaitCompletedJobKeepsStatus(t *testing.T) {
	code, out, _ := runJobs(t, t.TempDir(), `sh -c 'exit 3' & p=$!; sleep 0.3; wait $p; echo st=$?`, nil, 10*time.Second)
	if code != 0 || !strings.Contains(out, "st=3") {
		t.Fatalf("已完成作业的状态被丢弃: code=%d out=%q", code, out)
	}
}

func TestBangEmptyWithoutJobs(t *testing.T) {
	_, out, _ := runJobs(t, t.TempDir(), `echo bang=[$!]`, nil, 5*time.Second)
	if strings.TrimSpace(out) != "bang=[]" {
		t.Fatalf("无作业时 $! 应为空: %q", out)
	}
	code2, _, errb2 := runJobs(t, t.TempDir(), `set -u; echo $!`, nil, 5*time.Second)
	if code2 != 127 || !strings.Contains(errb2, "unbound") {
		t.Fatalf("set -u 下无作业 $! 应报 unbound: code=%d stderr=%q", code2, errb2)
	}
}

func TestBangPersistsAfterCompletion(t *testing.T) {
	code, out, _ := runJobs(t, t.TempDir(), `sh -c 'exit 0' & p=$!; sleep 0.3; wait $p; echo same=$([ "$p" = "$!" ] && echo yes || echo no)`, nil, 10*time.Second)
	if code != 0 || !strings.Contains(out, "same=yes") {
		t.Fatalf("作业完成后 $! 应保留最后 pid: code=%d out=%q", code, out)
	}
}

func TestBuiltinAndFuncInBackground(t *testing.T) {
	code, out, errb := runJobs(t, t.TempDir(), `export X=1 & f(){ echo fn; }; f & wait`, nil, 10*time.Second)
	if code != 0 {
		t.Fatalf("退出码 %d stderr=%q", code, errb)
	}
	if !strings.Contains(out, "fn") {
		t.Fatalf("后台函数未执行: %q", out)
	}
	if strings.Contains(errb, "command not found") {
		t.Fatalf("内建被当外部命令查找: %q", errb)
	}
}

func TestSyncWriterSerializes(t *testing.T) {
	var buf strings.Builder
	w := NewSyncWriter(&buf)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Write([]byte("abcde"))
		}()
	}
	wg.Wait()
	if len(buf.String()) != 250 {
		t.Fatalf("写入丢失，长度 %d", len(buf.String()))
	}
}

func aliveByPattern(pat string) bool {
	for _, p := range procTable() {
		if p.State == "Z" {
			continue
		}
		b, err := os.ReadFile("/proc/" + strconv.Itoa(p.PID) + "/cmdline")
		if err != nil {
			continue
		}
		line := strings.ReplaceAll(string(b), "\x00", " ")
		if strings.TrimSpace(line) == pat {
			return true
		}
	}
	return false
}

var _ = os.Getpid
