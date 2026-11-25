package vbash

import "testing"

// ParsePathList + String 必须在不含歧义形态时逐字回环：段顺序、空段、
// 相对名都原样保留。盘符形态的归一属平台分片用例（pathlist_windows_test.go）。
func TestParsePathListRoundTrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/usr/local/bin:/usr/bin:/bin", "/usr/local/bin:/usr/bin:/bin"},
		{"/a::/b", "/a::/b"}, // 空段等价于当前目录，必须保留
		{"", ""},             // 空 PATH 是单个空段
		{"rel/bin:.:/abs", "rel/bin:.:/abs"},
	}
	for _, c := range cases {
		if got := ParsePathList(c.in).String(); got != c.want {
			t.Errorf("ParsePathList(%q).String() = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestPathListInternalIsCopy(t *testing.T) {
	p := ParsePathList("/a:/b")
	got := p.Internal()
	got[0] = "/mutated"
	if p[0] != "/a" {
		t.Errorf("Internal 返回的切片是原数组的别名，改动泄漏回 PathList: %q", p[0])
	}
}

func TestPathListHostLength(t *testing.T) {
	p := ParsePathList("/a:/b:/c")
	if len(p.Host()) != 3 {
		t.Errorf("Host 段数 %d，期望 3", len(p.Host()))
	}
	for _, seg := range p.Host() {
		if seg == "" {
			t.Errorf("Host 段不应为空")
		}
	}
}
