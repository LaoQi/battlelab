package vbash

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func braceFields(t *testing.T, src string) []string {
	t.Helper()
	w := LiteralWord(src, Pos{})
	var out []string
	words, err := (&braceLimits{}).word(w)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	for _, bw := range words {
		text := ""
		for _, seg := range bw.Segments {
			text += seg.Text
		}
		out = append(out, text)
	}
	return out
}

func TestBraceList(t *testing.T) {
	cases := map[string][]string{
		"{a,b}":        {"a", "b"},
		"pre{a,b}post": {"preapost", "prebpost"},
		"{a,b}{1,2}":   {"a1", "a2", "b1", "b2"},
		"{a,}":         {"a"},
		"{,a}":         {"a"},
		"{a}":          {"{a}"},
		"{}":           {"{}"},
		"a{b,c}d":      {"abd", "acd"},
	}
	for src, want := range cases {
		got := braceFields(t, src)
		if len(got) != len(want) {
			t.Fatalf("%s: got %q want %q", src, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %q want %q", src, got, want)
			}
		}
	}
}

func TestBraceRange(t *testing.T) {
	cases := map[string][]string{
		"{1..3}":     {"1", "2", "3"},
		"{3..1}":     {"3", "2", "1"},
		"{01..03}":   {"01", "02", "03"},
		"{1..10..3}": {"1", "4", "7", "10"},
		"{a..c}":     {"a", "b", "c"},
		"{a..e..2}":  {"a", "c", "e"},
		"{-2..2}":    {"-2", "-1", "0", "1", "2"},
		"{1..a}":     {"{1..a}"},
		"{..}":       {"{..}"},
	}
	for src, want := range cases {
		got := braceFields(t, src)
		if len(got) != len(want) {
			t.Fatalf("%s: got %q want %q", src, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %q want %q", src, got, want)
			}
		}
	}
}

func TestBraceQuotedAndEscaped(t *testing.T) {
	for _, src := range []string{"\"{a,b}\"", "a\\{1,2\\}b", "'{a,b}'"} {
		var w Word
		if src == "\"{a,b}\"" {
			w = Word{Segments: []Segment{{Kind: SegLiteral, Text: "{a,b}", Mode: ModeDoubleQuoted}}}
		} else if src == "'{a,b}'" {
			w = Word{Segments: []Segment{{Kind: SegLiteral, Text: "{a,b}", Mode: ModeLiteral}}}
		} else {
			w = Word{Segments: []Segment{
				{Kind: SegLiteral, Text: "a{"},
				{Kind: SegLiteral, Text: "1,2", Mode: ModeUnquoted},
				{Kind: SegLiteral, Text: "}b", Mode: ModeLiteral},
			}}
		}
		got, err := (&braceLimits{}).word(w)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if len(got) != 1 {
			t.Fatalf("%s: 不应展开，got %d 个词", src, len(got))
		}
	}
}

func TestBraceDepthLimit(t *testing.T) {
	word := func(depth int) Word {
		src := strings.Repeat("{a,", depth) + "x" + strings.Repeat("}", depth)
		return LiteralWord(src, Pos{})
	}
	if got, err := (&braceLimits{}).word(word(maxNestDepth)); err != nil {
		t.Fatalf("上限内 %d 层应可展开: %v", maxNestDepth, err)
	} else if len(got) != maxNestDepth+1 {
		t.Errorf("%d 层得到 %d 个词，期望 %d", maxNestDepth, len(got), maxNestDepth+1)
	}
	_, err := (&braceLimits{}).word(word(maxNestDepth + 1))
	if err == nil {
		t.Fatalf("超过 %d 层应报错", maxNestDepth)
	}
	if !strings.Contains(err.Error(), "嵌套过深") {
		t.Errorf("错误信息为 %q", err.Error())
	}
}

func TestBraceFieldLimit(t *testing.T) {
	cartesian := func(n int) Word {
		return LiteralWord(strings.Repeat("{a,b}", n), Pos{})
	}
	in := 14
	if 1<<in != maxBraceFields {
		t.Fatalf("用例规模需与上限对齐：%d != %d", 1<<in, maxBraceFields)
	}
	got, err := (&braceLimits{}).word(cartesian(in))
	if err != nil {
		t.Fatalf("上限内 %d 个字段应可展开: %v", 1<<in, err)
	}
	if len(got) != 1<<in {
		t.Errorf("得到 %d 个词，期望 %d", len(got), 1<<in)
	}
	_, err = (&braceLimits{}).word(cartesian(in + 1))
	if err == nil {
		t.Fatalf("超过 %d 个字段应报错", maxBraceFields)
	}
	if !strings.Contains(err.Error(), "字段过多") {
		t.Errorf("错误信息为 %q", err.Error())
	}
}

func TestBraceRangeLimit(t *testing.T) {
	got, err := (&braceLimits{}).word(LiteralWord("{1.."+strconv.Itoa(maxBraceFields)+"}", Pos{}))
	if err != nil {
		t.Fatalf("上限内序列应可展开: %v", err)
	}
	if len(got) != maxBraceFields {
		t.Errorf("得到 %d 个词，期望 %d", len(got), maxBraceFields)
	}
	if _, err := (&braceLimits{}).word(LiteralWord("{1..999999999}", Pos{})); err == nil {
		t.Fatalf("超大序列应立即报错")
	} else if !strings.Contains(err.Error(), "字段过多") {
		t.Errorf("错误信息为 %q", err.Error())
	}
}

func TestBraceFieldBudgetPerWordList(t *testing.T) {
	x := &expansion{}
	words := make([]Word, 8)
	for i := range words {
		words[i] = LiteralWord(strings.Repeat("{a,b}", 12), Pos{})
	}
	if _, err := x.expandWords(words[:1]); err != nil {
		t.Fatalf("单词上限内应可展开: %v", err)
	}
	if _, err := x.expandWords(words); err == nil {
		t.Fatalf("多个词合计超限应报错")
	} else if !strings.Contains(err.Error(), "字段过多") {
		t.Errorf("错误信息为 %q", err.Error())
	}
}

func TestBraceUnmatchedOpenLinear(t *testing.T) {
	elapsed := func(n int) time.Duration {
		start := time.Now()
		w := LiteralWord(strings.Repeat("{a", n), Pos{})
		if _, err := (&braceLimits{}).word(w); err != nil {
			t.Fatalf("%d 个未闭合 `{` 不应报错: %v", n, err)
		}
		return time.Since(start)
	}
	small := elapsed(2000)
	large := elapsed(20000)
	if small < time.Microsecond {
		small = time.Microsecond
	}
	if factor := float64(large) / float64(small); factor > 60 {
		t.Errorf("规模增长 10 倍耗时增长 %.1f 倍（%v → %v），疑似退化为 O(n²)", factor, small, large)
	}
}
