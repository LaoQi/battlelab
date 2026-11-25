//go:build windows

package shell

import (
	"context"
	"strings"
	"testing"
)

// F4：cwd 参数要接受 vbash 自己宣传的 /d/... 形态（Windows 上既非绝对路径也
// Stat 不到，必须先翻宿主形态），并让 Result.Cwd / pwd 用正斜杠宿主形态。
func TestResolveCwdVbashForm(t *testing.T) {
	dir := t.TempDir()
	internal := "/" + strings.ToLower(dir[:1]) + strings.ReplaceAll(dir[2:], "\\", "/")

	res := testVbashTool(t).run(context.Background(), request{Command: "pwd", TimeoutSec: 10, Cwd: internal})
	if res.Err != "" {
		t.Fatalf("/d/... 形态的 cwd 应被接受，Err=%q", res.Err)
	}
	want := strings.ToUpper(dir[:1]) + ":" + strings.ReplaceAll(dir[2:], "\\", "/")
	var out strings.Builder
	for _, c := range res.Stdout {
		out.WriteString(c.Data)
	}
	if got := strings.TrimSpace(out.String()); got != want {
		t.Errorf("pwd = %q，期望 %q", got, want)
	}
	if res.Cwd != want {
		t.Errorf("Result.Cwd = %q，期望与 pwd 同形态 %q", res.Cwd, want)
	}
}
