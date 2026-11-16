//go:build linux

package shell

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 普通命令的 stdin 接虚拟 tty（与真实 shell 一致）：`tty` 认得出终端，
// 而 `read` 会阻塞等输入——这正是真实 shell 的行为。
// 需要「读到 EOF」的场景应显式重定向（`< /dev/null`）或接管道。
func TestRunShellStdinIsVirtualTTY(t *testing.T) {
	res := testShellTool(t).run(context.Background(), request{
		Command:    `sh -c 'tty; test -t 0 && echo stdin-tty || echo stdin-not-tty'`,
		TimeoutSec: 10,
	})
	out := shellOut(res)
	if !strings.Contains(out, "/dev/pts/") {
		t.Errorf("stdin 应接虚拟 tty: %q", out)
	}
	if !strings.Contains(out, "stdin-tty") {
		t.Errorf("stdin 应是终端: %q", out)
	}
}

// 显式重定向到空设备时，read 立即 EOF（与真实 shell 一致）。
func TestRunShellStdinRedirectNullImmediateEOF(t *testing.T) {
	start := time.Now()
	res := testShellTool(t).run(context.Background(), request{
		Command:    `read -r line < /dev/null; echo "RC=$? LINE=${line:-none}"`,
		TimeoutSec: 10,
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("重定向到空设备后 read 不应阻塞，实际 %v", elapsed)
	}
	out := shellOut(res)
	if !strings.Contains(out, "RC=1") || !strings.Contains(out, "LINE=none") {
		t.Errorf("重定向空设备后 read 应立即 EOF: %+v", res)
	}
}
