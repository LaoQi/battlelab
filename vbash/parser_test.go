package vbash

import (
	"errors"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *List {
	t.Helper()
	list, err := Parse(src)
	if err != nil {
		t.Fatalf("解析 %q 失败: %v", src, err)
	}
	return list
}

func singleCommand(t *testing.T, src string) *Command {
	t.Helper()
	list := mustParse(t, src)
	if len(list.Items) != 1 {
		t.Fatalf("%q 得到 %d 个列表项，期望 1", src, len(list.Items))
	}
	item := list.Items[0]
	if len(item.AndOr.Pipes) != 1 {
		t.Fatalf("%q 得到 %d 个 and-or 段，期望 1", src, len(item.AndOr.Pipes))
	}
	pl := item.AndOr.Pipes[0].Pipeline
	if len(pl.Commands) != 1 {
		t.Fatalf("%q 得到 %d 条管道命令，期望 1", src, len(pl.Commands))
	}
	return pl.Commands[0]
}

func wordTexts(cmd *Command) []string {
	out := make([]string, 0, len(cmd.Words))
	for _, w := range cmd.Words {
		out = append(out, w.Text)
	}
	return out
}

func TestParseSimpleCommand(t *testing.T) {
	cmd := singleCommand(t, "echo hello world")
	got := strings.Join(wordTexts(cmd), " ")
	if got != "echo hello world" {
		t.Fatalf("词序列为 %q", got)
	}
	if len(cmd.Assigns) != 0 || len(cmd.Redirs) != 0 {
		t.Fatalf("不应有赋值或重定向: %+v", cmd)
	}
}

func TestParseAssignmentPrefix(t *testing.T) {
	cmd := singleCommand(t, "FOO=bar BAZ='a b' echo hi")
	if len(cmd.Assigns) != 2 {
		t.Fatalf("赋值数量 %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Name != "FOO" || cmd.Assigns[0].Value.Text != "bar" {
		t.Errorf("第一个赋值为 %+v", cmd.Assigns[0])
	}
	if cmd.Assigns[1].Name != "BAZ" || cmd.Assigns[1].Value.Text != "a b" {
		t.Errorf("第二个赋值为 %+v", cmd.Assigns[1])
	}
	if got := strings.Join(wordTexts(cmd), " "); got != "echo hi" {
		t.Errorf("命令词为 %q", got)
	}
}

func TestParseAssignmentOnly(t *testing.T) {
	cmd := singleCommand(t, "X=1")
	if len(cmd.Assigns) != 1 || len(cmd.Words) != 0 {
		t.Fatalf("纯赋值解析为 %+v", cmd)
	}
	if cmd.Assigns[0].Name != "X" || cmd.Assigns[0].Value.Text != "1" {
		t.Errorf("赋值内容 %+v", cmd.Assigns[0])
	}
}

func TestParseAssignmentValueModes(t *testing.T) {
	cmd := singleCommand(t, `X="a  b" Y=$'c\td'`)
	if cmd.Assigns[0].Value.Text != "a  b" {
		t.Errorf("双引号值 %q", cmd.Assigns[0].Value.Text)
	}
	if cmd.Assigns[1].Value.Text != "c\td" {
		t.Errorf("$'' 值 %q", cmd.Assigns[1].Value.Text)
	}
}

func TestParsePipeline(t *testing.T) {
	list := mustParse(t, "cat a.txt | grep x | wc -l")
	pl := list.Items[0].AndOr.Pipes[0].Pipeline
	if len(pl.Commands) != 3 {
		t.Fatalf("管道命令数 %d", len(pl.Commands))
	}
	if pl.Commands[1].Words[0].Text != "grep" {
		t.Errorf("第二段为 %q", pl.Commands[1].Words[0].Text)
	}
}

func TestParseAndOrChain(t *testing.T) {
	list := mustParse(t, "a && b || c")
	pipes := list.Items[0].AndOr.Pipes
	if len(pipes) != 3 {
		t.Fatalf("and-or 段数 %d", len(pipes))
	}
	if pipes[0].Op != OpAnd || pipes[1].Op != OpOr {
		t.Errorf("操作符序列 %v %v", pipes[0].Op, pipes[1].Op)
	}
}

func TestParseListSeparators(t *testing.T) {
	list := mustParse(t, "a; b && c\nd")
	if len(list.Items) != 3 {
		t.Fatalf("列表项数 %d", len(list.Items))
	}
	if list.Items[0].Sep != OpSemi {
		t.Errorf("第一项分隔符 %v", list.Items[0].Sep)
	}
	if !list.Items[1].Newline {
		t.Errorf("第二项应以换行结束: %+v", list.Items[1])
	}
}

func TestParseNewlineAfterAndOr(t *testing.T) {
	list := mustParse(t, "a &&\n  b")
	pipes := list.Items[0].AndOr.Pipes
	if len(pipes) != 2 || pipes[0].Op != OpAnd {
		t.Fatalf("跨行 && 解析为 %+v", pipes)
	}
}

func TestParseRedirectFd(t *testing.T) {
	cases := []struct {
		src  string
		fd   int
		op   OpKind
		file string
	}{
		{"echo hi > f", DefaultFd, OpGreat, "f"},
		{"echo hi >> f", DefaultFd, OpDGreat, "f"},
		{"echo hi 2> f", 2, OpGreat, "f"},
		{"echo hi 2>&1", 2, OpGreatAmp, "1"},
		{"echo hi 1>&2", 1, OpGreatAmp, "2"},
		{"echo hi &> f", DefaultFd, OpAndGreat, "f"},
		{"echo hi &>> f", DefaultFd, OpAndDGreat, "f"},
		{"echo hi 2>> f", 2, OpDGreat, "f"},
		{"echo hi >| f", DefaultFd, OpClobber, "f"},
		{"echo hi 3<> f", 3, OpLessGreat, "f"},
		{"cat < in", DefaultFd, OpLess, "in"},
	}
	wantWords := map[string]string{
		"echo hi > f":   "echo hi",
		"echo hi >> f":  "echo hi",
		"echo hi 2> f":  "echo hi",
		"echo hi 2>&1":  "echo hi",
		"echo hi 1>&2":  "echo hi",
		"cat < in":      "cat",
		"echo hi &> f":  "echo hi",
		"echo hi &>> f": "echo hi",
		"echo hi 2>> f": "echo hi",
		"echo hi >| f":  "echo hi",
		"echo hi 3<> f": "echo hi",
	}
	for _, c := range cases {
		cmd := singleCommand(t, c.src)
		if len(cmd.Redirs) != 1 {
			t.Fatalf("%q 重定向数 %d", c.src, len(cmd.Redirs))
		}
		r := cmd.Redirs[0]
		if r.Fd != c.fd || r.Op != c.op || r.Target.Text != c.file {
			t.Errorf("%q 解析为 fd=%d op=%v target=%q，期望 fd=%d op=%v target=%q",
				c.src, r.Fd, r.Op, r.Target.Text, c.fd, c.op, c.file)
		}
		if got := strings.Join(wordTexts(cmd), " "); got != wantWords[c.src] {
			t.Errorf("%q 的命令词为 %q", c.src, got)
		}
	}
}

func TestParseRedirectOrderPreserved(t *testing.T) {
	cmd := singleCommand(t, "echo hi 2>&1 > f")
	if len(cmd.Redirs) != 2 {
		t.Fatalf("重定向数 %d", len(cmd.Redirs))
	}
	if cmd.Redirs[0].Fd != 2 || cmd.Redirs[0].Op != OpGreatAmp {
		t.Errorf("第一个重定向 %+v", cmd.Redirs[0])
	}
	if cmd.Redirs[1].Fd != DefaultFd || cmd.Redirs[1].Op != OpGreat {
		t.Errorf("第二个重定向 %+v", cmd.Redirs[1])
	}
}

func TestParseRedirectNoSpace(t *testing.T) {
	cmd := singleCommand(t, "echo hi 2>f")
	if len(cmd.Redirs) != 1 || cmd.Redirs[0].Fd != 2 || cmd.Redirs[0].Target.Text != "f" {
		t.Fatalf("无空格重定向解析为 %+v", cmd.Redirs)
	}
}

func TestParseQuotedDigitIsNotFd(t *testing.T) {
	cmd := singleCommand(t, `echo hi "2">f`)
	if len(cmd.Redirs) != 1 || cmd.Redirs[0].Fd != DefaultFd {
		t.Fatalf("引号内数字不应作为 fd: %+v", cmd.Redirs[0])
	}
	if got := strings.Join(wordTexts(cmd), " "); got != "echo hi 2" {
		t.Errorf("命令词为 %q", got)
	}
}

func TestParseSpacedDigitIsNotFd(t *testing.T) {
	cmd := singleCommand(t, "echo hi 2 > f")
	if len(cmd.Redirs) != 1 || cmd.Redirs[0].Fd != DefaultFd {
		t.Fatalf("空格分隔的数字不应作为 fd: %+v", cmd.Redirs[0])
	}
	if got := strings.Join(wordTexts(cmd), " "); got != "echo hi 2" {
		t.Errorf("命令词为 %q", got)
	}
}

func TestParseHeredocAttached(t *testing.T) {
	cmd := singleCommand(t, "cat <<'EOF'\nbody line\nEOF\n")
	if len(cmd.Redirs) != 1 {
		t.Fatalf("重定向数 %d", len(cmd.Redirs))
	}
	r := cmd.Redirs[0]
	if r.Op != OpDLess || r.Heredoc == nil {
		t.Fatalf("heredoc 重定向 %+v", r)
	}
	if r.Heredoc.Delim != "EOF" || r.Heredoc.Body != "body line\n" {
		t.Errorf("heredoc 内容 %+v", r.Heredoc)
	}
	if r.Heredoc.ExpandBody {
		t.Error("引号化终止符不应展开")
	}
}

func TestParseHeredocUnquotedExpand(t *testing.T) {
	cmd := singleCommand(t, "cat <<EOF\n$x\nEOF\n")
	if cmd.Redirs[0].Heredoc == nil || !cmd.Redirs[0].Heredoc.ExpandBody {
		t.Fatalf("未引号化终止符应展开: %+v", cmd.Redirs[0])
	}
}

func TestParseHeredocThenCommand(t *testing.T) {
	list := mustParse(t, "cat <<'EOF' > f\nbody\nEOF\nwc -l f\n")
	if len(list.Items) != 2 {
		t.Fatalf("列表项数 %d", len(list.Items))
	}
	cmd := list.Items[0].AndOr.Pipes[0].Pipeline.Commands[0]
	var out, hd int
	for _, r := range cmd.Redirs {
		if r.Op == OpGreat {
			out++
		}
		if r.Op == OpDLess {
			hd++
		}
	}
	if out != 1 || hd != 1 {
		t.Fatalf("重定向组合 %+v", cmd.Redirs)
	}
	second := list.Items[1].AndOr.Pipes[0].Pipeline.Commands[0]
	if second.Words[0].Text != "wc" {
		t.Errorf("终止符后的命令为 %q", second.Words[0].Text)
	}
}

func TestParseMultipleHeredocs(t *testing.T) {
	cmd := singleCommand(t, "cat <<A <<B\nbody-a\nA\nbody-b\nB\n")
	var got []string
	for _, r := range cmd.Redirs {
		if r.Heredoc != nil {
			got = append(got, r.Heredoc.Body)
		}
	}
	if len(got) != 2 || got[0] != "body-a\n" || got[1] != "body-b\n" {
		t.Fatalf("多 heredoc 体为 %q", got)
	}
}

func TestParseHeredocInPipeline(t *testing.T) {
	list := mustParse(t, "cat <<'EOF' | tr a-z A-Z\nabc\nEOF\n")
	pl := list.Items[0].AndOr.Pipes[0].Pipeline
	if len(pl.Commands) != 2 {
		t.Fatalf("管道段数 %d", len(pl.Commands))
	}
	if pl.Commands[0].Redirs[0].Heredoc == nil {
		t.Fatal("heredoc 未挂到首个命令")
	}
}

func TestParseSubshell(t *testing.T) {
	cmd := singleCommand(t, "( cd sub && pwd )")
	if cmd.GroupKind != GroupSubshell || cmd.Group == nil {
		t.Fatalf("子 shell 解析为 %+v", cmd)
	}
	if len(cmd.Group.Items) != 1 {
		t.Fatalf("子 shell 内列表项 %d", len(cmd.Group.Items))
	}
}

func TestParseBraceGroup(t *testing.T) {
	cmd := singleCommand(t, "{ echo a; echo b; }")
	if cmd.GroupKind != GroupBrace || cmd.Group == nil {
		t.Fatalf("分组解析为 %+v", cmd)
	}
	if len(cmd.Group.Items) != 2 {
		t.Fatalf("分组内列表项 %d", len(cmd.Group.Items))
	}
}

func TestParseFuncDef(t *testing.T) {
	cmd := singleCommand(t, "greet() { echo hi; }")
	if cmd.Func == nil {
		t.Fatalf("函数定义解析为 %+v", cmd)
	}
	if cmd.Func.Name != "greet" || cmd.Func.Body.Group == nil {
		t.Errorf("函数内容 %+v", cmd.Func)
	}
}

func TestParseBang(t *testing.T) {
	list := mustParse(t, "! false")
	pl := list.Items[0].AndOr.Pipes[0].Pipeline
	if !pl.Bang {
		t.Fatal("! 未识别")
	}
}

func TestParseCommentStripped(t *testing.T) {
	list := mustParse(t, "echo before  # 注释\necho after\n")
	if len(list.Items) != 2 {
		t.Fatalf("列表项数 %d", len(list.Items))
	}
	if got := strings.Join(wordTexts(list.Items[0].AndOr.Pipes[0].Pipeline.Commands[0]), " "); got != "echo before" {
		t.Errorf("首项命令词为 %q", got)
	}
}

func TestParseUnsupportedCompounds(t *testing.T) {
	cases := map[string]string{
		"if [ -f f ]; then echo y; fi":     "if",
		"for f in *.txt; do echo $f; done": "for",
		"case $v in a) echo A;; esac":      "case",
		"while true; do echo x; done":      "while",
		"[[ -f f ]] && echo y":             "[",
	}
	for src := range cases {
		_, err := Parse(src)
		var ue *UnsupportedError
		if !errors.As(err, &ue) {
			t.Errorf("%q 应报 UnsupportedError，得到 %v", src, err)
		}
	}
}

func TestParseSyntaxErrors(t *testing.T) {
	cases := []string{
		";",
		"|",
		"&& echo x",
		"echo a |",
		"echo a &&",
		"echo a; ;",
		"( echo a",
		"{ echo a;",
		"echo hi >",
	}
	for _, src := range cases {
		_, err := Parse(src)
		var se *SyntaxError
		if err == nil {
			t.Errorf("%q 应报语法错误", src)
			continue
		}
		if !errors.As(err, &se) {
			t.Errorf("%q 应报 SyntaxError，得到 %v", src, err)
		}
	}
}

func TestParseCommandStringRoundTrip(t *testing.T) {
	cases := []string{
		"echo hi",
		"a && b || c",
		"X=1 echo hi",
		"echo hi 2>&1",
	}
	for _, src := range cases {
		list := mustParse(t, src)
		if list.String() == "" {
			t.Errorf("%q 的字符串表示为空", src)
		}
	}
}

func checkTree(list *List) error {
	if list == nil {
		return errors.New("列表为 nil")
	}
	for _, item := range list.Items {
		if item.AndOr == nil || len(item.AndOr.Pipes) == 0 {
			return errors.New("and-or 段为空")
		}
		for _, pipe := range item.AndOr.Pipes {
			if pipe.Pipeline == nil || len(pipe.Pipeline.Commands) == 0 {
				return errors.New("管道为空")
			}
			for _, cmd := range pipe.Pipeline.Commands {
				if cmd.IsEmpty() {
					return errors.New("空命令节点")
				}
				for _, w := range cmd.Words {
					if len(w.Segments) == 0 {
						return errors.New("词没有片段")
					}
					if w.Text == "" && !w.Quoted() {
						return errors.New("空词")
					}
				}
				for _, r := range cmd.Redirs {
					if !isRedirectOp(r.Op) {
						return errors.New("非重定向操作符出现在重定向节点")
					}
					if r.Target.Text == "" && r.Op != OpGreatAmp && r.Op != OpLessAmp {
						return errors.New("重定向目标为空")
					}
					if r.Op == OpDLess || r.Op == OpDLessDash {
						if r.Heredoc == nil || r.Heredoc.Delim == "" {
							return errors.New("heredoc 未关联")
						}
					}
				}
				if cmd.Group != nil {
					if cmd.GroupKind == GroupNone {
						return errors.New("分组缺少类型")
					}
					if err := checkTree(cmd.Group); err != nil {
						return err
					}
				}
				if cmd.Func != nil {
					if cmd.Func.Body == nil || cmd.Func.Body.Group == nil {
						return errors.New("函数体缺少分组")
					}
					if err := checkTree(cmd.Func.Body.Group); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func TestCorpusTreeInvariants(t *testing.T) {
	for _, c := range loadBenchCommands(t) {
		list, err := Parse(c.Command)
		if err != nil {
			continue
		}
		if err := checkTree(list); err != nil {
			t.Errorf("%s 的 AST 不完整: %v（命令 %q）", c.Name, err, c.Command)
		}
	}
}

func TestParseExpansionSegments(t *testing.T) {
	cmd := singleCommand(t, `echo a$X "${Y}z" $((1+2)) $(echo hi)`)
	type want struct {
		kind  SegmentKind
		inner string
	}
	cases := []struct {
		word int
		segs []want
	}{
		{1, []want{{SegLiteral, ""}, {SegVar, "X"}}},
		{2, []want{{SegBraced, "Y"}, {SegLiteral, ""}}},
		{3, []want{{SegArith, "1+2"}}},
		{4, []want{{SegCmdSub, "echo hi"}}},
	}
	for _, c := range cases {
		segs := cmd.Words[c.word].Segments
		if len(segs) != len(c.segs) {
			t.Fatalf("第 %d 个词片段为 %+v，期望 %d 段", c.word, segs, len(c.segs))
		}
		for i, exp := range c.segs {
			if segs[i].Kind != exp.kind || segs[i].Inner != exp.inner {
				t.Errorf("第 %d 个词第 %d 段为 %+v，期望 %v/%q", c.word, i, segs[i], exp.kind, exp.inner)
			}
		}
	}
}

func TestParseCmdSubNested(t *testing.T) {
	cmd := singleCommand(t, `echo $(echo $(echo inner))`)
	seg := cmd.Words[1].Segments[0]
	if seg.Kind != SegCmdSub || seg.Sub == nil {
		t.Fatalf("命令替换未解析: %+v", seg)
	}
	if len(seg.Sub.Items) != 1 {
		t.Fatalf("命令替换内列表项 %d", len(seg.Sub.Items))
	}
	inner := seg.Sub.Items[0].AndOr.Pipes[0].Pipeline.Commands[0].Words[1]
	if inner.Segments[0].Sub == nil {
		t.Fatalf("嵌套命令替换未解析: %+v", inner)
	}
}

func TestParseSpecialParams(t *testing.T) {
	cmd := singleCommand(t, `echo $? $# $@ $* $$ $0 $1`)
	names := []string{"?", "#", "@", "*", "$", "0", "1"}
	if len(cmd.Words) != len(names)+1 {
		t.Fatalf("词数 %d", len(cmd.Words))
	}
	for i, name := range names {
		seg := cmd.Words[i+1].Segments[0]
		if seg.Kind != SegVar || seg.Inner != name {
			t.Errorf("第 %d 个特殊参数为 %+v，期望 %q", i, seg, name)
		}
	}
}

func TestParseVarGreedyName(t *testing.T) {
	cmd := singleCommand(t, `echo $Xdef "$Xdef"`)
	for _, idx := range []int{1, 2} {
		seg := cmd.Words[idx].Segments[0]
		if seg.Kind != SegVar || seg.Inner != "Xdef" {
			t.Errorf("第 %d 个词为 %+v，变量名应为 Xdef", idx, seg)
		}
	}
}

func TestParseSingleQuoteNoExpansion(t *testing.T) {
	cmd := singleCommand(t, `echo '$X $(y)'`)
	segs := cmd.Words[1].Segments
	if len(segs) != 1 || segs[0].Kind != SegLiteral {
		t.Fatalf("单引号内不应产生展开片段: %+v", segs)
	}
	if segs[0].Mode != ModeLiteral {
		t.Errorf("单引号片段模式为 %v", segs[0].Mode)
	}
}

func TestParseDoubleQuoteModeMarked(t *testing.T) {
	cmd := singleCommand(t, `echo "a $X b"`)
	segs := cmd.Words[1].Segments
	if len(segs) != 3 {
		t.Fatalf("片段数 %d: %+v", len(segs), segs)
	}
	if segs[1].Kind != SegVar || segs[1].Mode != ModeDoubleQuoted {
		t.Errorf("引号内变量片段为 %+v", segs[1])
	}
}
