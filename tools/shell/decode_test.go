package shell

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToUTF8Passthrough(t *testing.T) {
	for _, in := range []string{"", "ascii", "中文测试", "emoji 🎉 mixed"} {
		if got := toUTF8([]byte(in)); got != in {
			t.Errorf("合法 UTF-8 应原样直通: in=%q got=%q", in, got)
		}
	}
	if got := toUTF8(nil); got != "" {
		t.Errorf("空输入应返回空串: %q", got)
	}
}

func TestToUTF8InvalidRoutesToDecoder(t *testing.T) {
	orig := decodeStream
	t.Cleanup(func() { decodeStream = orig })
	decodeStream = func(b []byte) string { return "DECODED<" + string(b) + ">" }

	gbk := []byte{0xC4, 0xE3, 0xBA, 0xC3}
	got := toUTF8(gbk)
	if !strings.HasPrefix(got, "DECODED<") || !strings.HasSuffix(got, ">") {
		t.Errorf("非法 UTF-8 应路由到注入的解码器: %q", got)
	}
	if !bytes.Contains([]byte(got), gbk) {
		t.Errorf("解码器应收到原始字节: %q", got)
	}
}

func TestFinishJoinsContiguousSeam(t *testing.T) {
	orig := decodeStream
	t.Cleanup(func() { decodeStream = orig })
	var calls [][]byte
	decodeStream = func(b []byte) string { calls = append(calls, b); return "[" + string(b) + "]" }

	var chunks []Chunk
	c := streamCapture{chunks: &chunks}
	pad := strings.Repeat("a", MaxOutput-2)
	c.Write([]byte(pad))
	c.Write([]byte{0xC4, 0xE3})
	c.Write([]byte{0xC4, 0xE3})
	c.Write([]byte("b"))
	c.finish()

	if len(chunks) != 1 {
		t.Fatalf("连续流应合并为单一 chunk: %+v", chunks)
	}
	if len(calls) != 1 {
		t.Fatalf("middle==0 时应整体解码一次（跨 head/tail 边界的多字节序列不能被切断）: calls=%d", len(calls))
	}
	if len(calls[0]) != MaxOutput+3 {
		t.Errorf("解码器应收到完整拼接缓冲: len=%d", len(calls[0]))
	}
	if !strings.HasPrefix(chunks[0].Data, "[") || !strings.HasSuffix(chunks[0].Data, "]") {
		t.Errorf("chunk 应为解码器输出: %q…", chunks[0].Data[:16])
	}
	if !strings.HasPrefix(chunks[0].Data[1:], pad) || !strings.HasSuffix(chunks[0].Data, "b]") {
		t.Errorf("拼接内容错位: head=%q tail=%q", chunks[0].Data[1:16], chunks[0].Data[len(chunks[0].Data)-8:])
	}
	if chunks[0].Truncated != 0 {
		t.Errorf("连续流不应有截断计数: %d", chunks[0].Truncated)
	}
}

func TestFinishDecodesDisjointFragments(t *testing.T) {
	orig := decodeStream
	t.Cleanup(func() { decodeStream = orig })
	var calls [][]byte
	decodeStream = func(b []byte) string { calls = append(calls, b); return "[" + string(b) + "]" }

	var chunks []Chunk
	c := streamCapture{chunks: &chunks}
	invalid := strings.Repeat("\x81", MaxOutput*2+1)
	c.Write([]byte(invalid))
	c.finish()

	if len(chunks) != 2 {
		t.Fatalf("超限流应产出 2 chunk: %+v", chunks)
	}
	if len(calls) != 2 {
		t.Fatalf("不连续片段应各自解码: calls=%d", len(calls))
	}
	if len(calls[0]) != MaxOutput || len(calls[1]) != MaxOutput/2+1 {
		t.Errorf("解码输入长度异常: head=%d tail=%d", len(calls[0]), len(calls[1]))
	}
	if chunks[1].Truncated != MaxOutput/2 {
		t.Errorf("截断计数应保留: %d", chunks[1].Truncated)
	}
	for i, ch := range chunks {
		if !strings.HasPrefix(ch.Data, "[") || !strings.HasSuffix(ch.Data, "]") {
			t.Errorf("chunk[%d] 应为解码器输出", i)
		}
	}
}

// TestStreamCaptureSplitsOnRuneBoundary 锁住头尾窗口的切点对齐 UTF-8 边界：
// 中文/emoji 流跨 head 边界与 tail 滚动边界时不得劈开多字节字符。
// 旧实现按字节硬切，`"a" + "中"*40000` 会产出 `\xe4\xb8` 这样的半个汉字，
// 落盘 json.Marshal 后变成 U+FFFD（静默数据损坏）。
func TestStreamCaptureSplitsOnRuneBoundary(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		unit   string
		repeat int
	}{
		{"无前缀", "", "中", 40000},
		{"前缀1字节", "a", "中", 40000},
		{"前缀2字节", "ab", "中", 40000},
		{"前缀3字节", "abc", "中", 40000},
		{"emoji", "a", "🎉", 30000},
		{"混合文本", "日志: ", "测试内容", 12000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var chunks []Chunk
			cap := streamCapture{chunks: &chunks}
			cap.Write([]byte(c.prefix + strings.Repeat(c.unit, c.repeat)))
			cap.finish()
			if len(chunks) == 0 {
				t.Fatal("应产出 chunk")
			}
			for i, ch := range chunks {
				if !utf8.ValidString(ch.Data) {
					t.Errorf("chunk[%d] 不是合法 UTF-8（切点劈开了多字节字符）: %q", i, tailBytes(ch.Data, 12))
				}
				if strings.ContainsRune(ch.Data, utf8.RuneError) {
					t.Errorf("chunk[%d] 含替换字符: %q", i, tailBytes(ch.Data, 12))
				}
			}
		})
	}
}

// TestStreamCaptureTailRollRuneBoundary 锁住 tail 滚动切点：窗口满时丢弃前一半，
// 切点同样要对齐 rune 边界，否则 tail 开头是半个字符。
func TestStreamCaptureTailRollRuneBoundary(t *testing.T) {
	var chunks []Chunk
	c := streamCapture{chunks: &chunks}
	c.Write([]byte(strings.Repeat("a", MaxOutput)))
	c.Write([]byte("b"))
	c.Write([]byte(strings.Repeat("中", 10000)))
	c.Write([]byte("c"))
	c.Write([]byte("dd"))
	c.finish()
	if len(chunks) != 2 {
		t.Fatalf("应产出 2 chunk: %d", len(chunks))
	}
	for i, ch := range chunks {
		if !utf8.ValidString(ch.Data) {
			t.Errorf("chunk[%d] 不是合法 UTF-8: %q", i, headBytes(ch.Data, 12))
		}
	}
	if chunks[1].Truncated <= 0 {
		t.Errorf("tail 滚动应记截断计数: %d", chunks[1].Truncated)
	}
}

// TestStreamCaptureKeepsByteLimit 锁住字节上限语义不变：切点对齐只挪位置，
// head 不超 MaxOutput、丢字节计数如实（对齐回退的那几个字节不能算进 middle）。
func TestStreamCaptureKeepsByteLimit(t *testing.T) {
	var chunks []Chunk
	c := streamCapture{chunks: &chunks}
	stream := "a" + strings.Repeat("中", 20000)
	c.Write([]byte(stream))
	c.finish()
	if len(chunks) != 2 {
		t.Fatalf("应产出 2 chunk: %d", len(chunks))
	}
	if len(chunks[0].Data) > MaxOutput {
		t.Errorf("head 超上限: %d > %d", len(chunks[0].Data), MaxOutput)
	}
	kept := len(chunks[0].Data) + len(chunks[1].Data)
	if kept+int(chunks[1].Truncated) != len(stream) {
		t.Errorf("字节记账不平: kept=%d truncated=%d total=%d", kept, chunks[1].Truncated, len(stream))
	}
}

// TestRuneBoundary 锁住边界判定本身：尾部残缺序列返回序列首字节下标，
// 完整序列与非法输入都返回 len(b)（非法输入保持旧硬切行为，不替它猜边界）。
func TestRuneBoundary(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"中中中", 9},
		{"a中", 4},
		{"abc", 3},
		{"", 0},
		{"中"[:2], 0},
		{"a" + "中"[:2], 1},
		{"ab" + "🎉"[:3], 2},
		{strings.Repeat("\x81", 4), 4},
	}
	for _, c := range cases {
		if got := runeBoundary([]byte(c.in)); got != c.want {
			t.Errorf("runeBoundary(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func headBytes(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

func tailBytes(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[len(s)-n:]
}
