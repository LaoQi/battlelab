//go:build windows

package ctty

import "testing"

func TestFallbackCPFollowsConsoleOutputCP(t *testing.T) {
	cp, ok := ConsoleOutputCP()
	if !ok || cp == 0 {
		t.Skip("无控制台，无法验证码页跟随")
	}
	if got := FallbackCP(); got != cp {
		t.Errorf("FallbackCP 应返回控制台当前输出码页: got=%d want=%d", got, cp)
	}
}

func TestFallbackCPAfterEnsureUTF8(t *testing.T) {
	cp, ok := ConsoleOutputCP()
	if !ok || cp == 0 {
		t.Skip("无控制台，无法验证码页切换")
	}
	t.Cleanup(RestoreUTF8)

	EnsureUTF8()
	if got := FallbackCP(); got != cpUTF8 {
		t.Errorf("EnsureUTF8 之后解码码页应为 65001: %d", got)
	}

	RestoreUTF8()
	if got := FallbackCP(); got != cp {
		t.Errorf("RestoreUTF8 之后解码码页应回到原值: got=%d want=%d", got, cp)
	}
}
