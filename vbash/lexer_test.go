package vbash

import (
	"errors"
	"strings"
	"testing"
)

func lexAll(t *testing.T, src string) []Token {
	t.Helper()
	l := newLexer(src)
	var toks []Token
	for {
		tok, err := l.Next()
		if err != nil {
			t.Fatalf("扫描 %q 出错: %v", src, err)
		}
		if tok.Kind == TokEOF {
			return toks
		}
		toks = append(toks, tok)
	}
}

func words(toks []Token) []string {
	var out []string
	for _, tk := range toks {
		if tk.Kind == TokWord {
			out = append(out, tk.Text)
		}
	}
	return out
}

func TestQuotedSeparatorsNotSeparators(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{`echo "a;b"`, []string{"echo", "a;b"}},
		{`echo 'a|b'`, []string{"echo", "a|b"}},
		{`echo "a&&b"`, []string{"echo", "a&&b"}},
		{`echo 'a b'`, []string{"echo", "a b"}},
		{`echo "a\nb"`, []string{"echo", `a\nb`}},
	}
	for _, c := range cases {
		got := words(lexAll(t, c.src))
		if len(got) != len(c.want) {
			t.Fatalf("%q 切成 %q，期望 %q", c.src, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q 第 %d 个词为 %q，期望 %q", c.src, i, got[i], c.want[i])
			}
		}
	}
}

func TestSingleQuoteIsLiteral(t *testing.T) {
	got := words(lexAll(t, `echo '$HOME $((1+1)) \n'`))
	if got[1] != `$HOME $((1+1)) \n` {
		t.Errorf("单引号内不应展开，得到 %q", got[1])
	}
}

func TestDoubleQuoteOnlyFourEscapes(t *testing.T) {
	got := words(lexAll(t, `echo "a\$b\\c\"d\ne"`))
	want := `a$b\c"d\ne`
	if got[1] != want {
		t.Errorf("双引号转义得到 %q，期望 %q", got[1], want)
	}
}

func TestAnsiCQuoting(t *testing.T) {
	got := words(lexAll(t, `echo $'a\tb\nc'`))
	if got[1] != "a\tb\nc" {
		t.Errorf("$'...' 得到 %q", got[1])
	}
}

func TestBackslashEscapesOutsideQuotes(t *testing.T) {
	got := words(lexAll(t, `echo a\ b`))
	if len(got) != 2 || got[1] != "a b" {
		t.Errorf("反斜杠转义空格得到 %q", got)
	}
}

func TestLineContinuation(t *testing.T) {
	w := words(lexAll(t, "echo abc \\\n  def"))
	if len(w) != 3 || w[1] != "abc" || w[2] != "def" {
		t.Errorf("续行后有空格，应是两个词（真 bash 行为），得到 %q", w)
	}

	w = words(lexAll(t, "echo abc\\\ndef"))
	if len(w) != 2 || w[1] != "abcdef" {
		t.Errorf("词内续行应拼接，得到 %q", w)
	}
}

func TestComment(t *testing.T) {
	toks := lexAll(t, "echo a # comment\n")
	if toks[2].Kind != TokComment || toks[2].Text != " comment" {
		t.Errorf("注释解析错误: %v", toks)
	}
}

func TestOperators(t *testing.T) {
	toks := lexAll(t, `a && b || c ; d | e & f > g >> h < i 2>&1`)
	var ops []string
	for _, tk := range toks {
		if tk.Kind == TokOp {
			ops = append(ops, tk.Op.String())
		}
	}
	want := []string{"'&&'", "'||'", "';'", "'|'", "'&'", "'>'", "'>>'", "'<'", "'>&'"}
	if len(ops) != len(want) {
		t.Fatalf("操作符序列 %v，期望 %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("第 %d 个操作符 %s，期望 %s", i, ops[i], want[i])
		}
	}
}

func TestUnterminatedQuotes(t *testing.T) {
	for _, src := range []string{`echo 'abc`, `echo "abc`, `echo $'abc`} {
		l := newLexer(src)
		var err error
		for {
			var tok Token
			tok, err = l.Next()
			if err != nil || tok.Kind == TokEOF {
				break
			}
		}
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%q 应报 SyntaxError，得到 %v", src, err)
		}
	}
}

func TestBacktickUnsupported(t *testing.T) {
	l := newLexer("echo `echo hi`")
	var err error
	for {
		var tok Token
		tok, err = l.Next()
		if err != nil || tok.Kind == TokEOF {
			break
		}
	}
	var ue *UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("反引号应报 UnsupportedError，得到 %v", err)
	}
	if ue.Construct != "反引号命令替换" {
		t.Errorf("错误信息构造名不符: %q", ue.Construct)
	}
}

func TestHeredocQuotedBody(t *testing.T) {
	src := "cat <<'EOF'\n$HOME body\nEOF\nwc -l\n"
	toks := lexAll(t, src)
	var body string
	for _, tk := range toks {
		if tk.Kind == TokHeredocBody {
			body = tk.Text
		}
	}
	if body != "$HOME body\n" {
		t.Errorf("heredoc 体得到 %q", body)
	}
	if !strings.Contains(src, "wc -l") {
		t.Fatal("测试用例本身有问题")
	}
	var sawWC bool
	for _, tk := range toks {
		if tk.Kind == TokWord && tk.Text == "wc" {
			sawWC = true
		}
	}
	if !sawWC {
		t.Error("终止符之后的命令丢失")
	}
}

func TestHeredocMultiple(t *testing.T) {
	src := "cat <<A <<B\nbody-a\nA\nbody-b\nB\n"
	toks := lexAll(t, src)
	var bodies []string
	for _, tk := range toks {
		if tk.Kind == TokHeredocBody {
			bodies = append(bodies, tk.Text)
		}
	}
	if len(bodies) != 2 || bodies[0] != "body-a\n" || bodies[1] != "body-b\n" {
		t.Errorf("多 heredoc 得到 %q", bodies)
	}
}

func TestHeredocStripTabs(t *testing.T) {
	src := "cat <<-EOF\n\tbody\n\tEOF\n"
	toks := lexAll(t, src)
	var body string
	for _, tk := range toks {
		if tk.Kind == TokHeredocBody {
			body = tk.Text
		}
	}
	if body != "body\n" {
		t.Errorf("<<- 应剥制表符，得到 %q", body)
	}
}

func TestHeredocUnterminated(t *testing.T) {
	toks := lexAll(t, "cat <<'EOF'\nbody without terminator\n")
	var body string
	for _, tk := range toks {
		if tk.Kind == TokHeredocBody {
			body = tk.Text
		}
	}
	if !strings.Contains(body, "body without terminator") {
		t.Errorf("未闭合 heredoc 应按 EOF 截断，得到 %q", body)
	}
}

func TestCRLFNormalized(t *testing.T) {
	toks := lexAll(t, "echo a\r\necho b\r\n")
	if toks[0].Text != "echo" || toks[2].Text != "\n" {
		t.Errorf("CRLF 未归一: %v", toks)
	}
}

func TestHeredocBodyWithTerminatorLikeLine(t *testing.T) {
	src := "cat <<'EOF'\nEOF_not_really\nEOF\n"
	toks := lexAll(t, src)
	var body string
	for _, tk := range toks {
		if tk.Kind == TokHeredocBody {
			body = tk.Text
		}
	}
	if body != "EOF_not_really\n" {
		t.Errorf("相似终止符不应提前结束，得到 %q", body)
	}
}

func lexAllNoFatal(t *testing.T, src string) []Token {
	l := newLexer(src)
	var toks []Token
	for {
		tok, err := l.Next()
		if err != nil || tok.Kind == TokEOF {
			return toks
		}
		toks = append(toks, tok)
	}
}

func TestArithAndCmdSubStayOneWord(t *testing.T) {
	cases := map[string]string{
		`echo $((1+2*3))`:        `$((1+2*3))`,
		`echo $((N*N))`:          `$((N*N))`,
		`echo $(( (1+2)*3 ))`:    `$(( (1+2)*3 ))`,
		`echo $(printf 'a\nb')`:  `$(printf 'a\nb')`,
		`echo $(echo $(echo x))`: `$(echo $(echo x))`,
	}
	for src, want := range cases {
		w := words(lexAll(t, src))
		if len(w) != 2 || w[1] != want {
			t.Errorf("%q 得到 %q，期望整体为 %q", src, w, want)
		}
	}
}

func TestParensAreTokens(t *testing.T) {
	toks := lexAll(t, "( echo a ) ; { echo b; }")
	var ops []OpKind
	for _, tk := range toks {
		if tk.Kind == TokOp {
			ops = append(ops, tk.Op)
		}
	}
	var want = []OpKind{OpLParen, OpRParen, OpSemi, OpSemi}
	if len(ops) != 4 || ops[0] != OpLParen || ops[1] != OpRParen {
		t.Errorf("括号 token 得到 %v，期望 %v", ops, want)
	}
}

func TestUnterminatedArith(t *testing.T) {
	l := newLexer("echo $((1+2)")
	var err error
	for {
		var tok Token
		tok, err = l.Next()
		if err != nil || tok.Kind == TokEOF {
			break
		}
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Errorf("未闭合 $(( 应报 SyntaxError，得到 %v", err)
	}
}
