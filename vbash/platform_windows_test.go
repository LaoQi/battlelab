//go:build windows

package vbash

import "testing"

func TestWindowsFromHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\Users\x`, "/c/Users/x"},
		{`C:/Users/x`, "/c/Users/x"},
		{`c:\`, "/c"},
		{`\\server\share\f`, "//server/share/f"},
		{"/already/posix", "/already/posix"},
		{"", ""},
	}
	for _, c := range cases {
		if got := windowsFromHost(c.in); got != c.want {
			t.Errorf("windowsFromHost(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestWindowsToHost(t *testing.T) {
	cases := []struct{ cwd, in, want string }{
		{"", "/c/Users/x", `C:\Users\x`},
		{"", "/c", `C:\`},
		{"", "//server/share", `\\server\share`},
		{"", "rel/f", `rel\f`},
		{`C:\work`, "rel/f", `C:\work\rel\f`},
	}
	for _, c := range cases {
		if got := windowsToHost(c.cwd, c.in); got != c.want {
			t.Errorf("windowsToHost(%q, %q) = %q，期望 %q", c.cwd, c.in, got, c.want)
		}
	}
}

func TestWindowsRoundTrip(t *testing.T) {
	for _, p := range []string{"/c/Users/x", "/d/work/sub"} {
		if got := windowsFromHost(windowsToHost("", p)); got != p {
			t.Errorf("%q 往返后为 %q", p, got)
		}
	}
}

func TestWindowsDevNull(t *testing.T) {
	for _, n := range []string{"/dev/null", "nul", "NUL"} {
		if !isDevNull(n) {
			t.Errorf("%q 应被识别为空设备", n)
		}
	}
}

func TestWindowsExitCodeMask(t *testing.T) {
	if got := windowsSignalName(2); got != "Interrupt" {
		t.Errorf("Ctrl-C 信号名: %q", got)
	}
}
