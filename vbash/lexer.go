package vbash

import (
	"strings"
)

type lexer struct {
	src          []rune
	i            int
	line         int
	col          int
	pending      []*Heredoc
	unterminated []UnterminatedHeredoc
	pendingStart bool
	expectDelim  *delimExpect
}

type delimExpect struct {
	stripTabs bool
	opPos     Pos
}

func newLexer(src string) *lexer {
	return &lexer{src: []rune(normalizeNewlines(src)), line: 1, col: 1}
}

func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func (l *lexer) pos() Pos { return Pos{Line: l.line, Col: l.col} }

func (l *lexer) eof() bool { return l.i >= len(l.src) }

func (l *lexer) peek() rune {
	if l.eof() {
		return 0
	}
	return l.src[l.i]
}

func (l *lexer) peekAt(n int) rune {
	if l.i+n >= len(l.src) {
		return 0
	}
	return l.src[l.i+n]
}

func (l *lexer) advance() rune {
	if l.eof() {
		return 0
	}
	r := l.src[l.i]
	l.i++
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *lexer) unread() {
	if l.i == 0 {
		return
	}
	l.i--
	if l.src[l.i] == '\n' {
		l.line--
		l.col = 1
		for j := l.i - 1; j >= 0 && l.src[j] != '\n'; j-- {
			l.col++
		}
	} else {
		l.col--
	}
}

func isBlank(r rune) bool { return r == ' ' || r == '\t' }

func isMeta(r rune) bool {
	switch r {
	case ' ', '\t', '\n', ';', '&', '|', '<', '>', '(', ')':
		return true
	}
	return false
}

func (l *lexer) skipBlanks() {
	for !l.eof() {
		if isBlank(l.peek()) {
			l.advance()
			continue
		}
		if l.peek() == '\\' && l.peekAt(1) == '\n' {
			l.advance()
			l.advance()
			continue
		}
		return
	}
}

func (l *lexer) Next() (Token, error) {
	if l.expectDelim != nil {
		exp := l.expectDelim
		l.expectDelim = nil
		return l.readHeredocDelim(exp)
	}
	if l.pendingStart {
		l.pendingStart = false
		return l.readHeredocBodies()
	}
	l.skipBlanks()
	if l.eof() {
		return Token{Kind: TokEOF, Pos: l.pos(), End: l.pos()}, nil
	}
	start := l.pos()
	r := l.peek()

	switch r {
	case '\n':
		l.advance()
		if len(l.pending) > 0 {
			l.pendingStart = true
		}
		return Token{Kind: TokNewline, Text: "\n", Pos: start, End: l.pos()}, nil
	case '#':
		return l.readComment(start), nil
	}

	if construct, hint, ok := unsupportedOp(l.src[l.i:]); ok {
		return Token{}, unsupported(construct, hint, start)
	}

	if op, n := matchOp(l.src[l.i:]); n > 0 {
		for range n {
			l.advance()
		}
		tok := Token{Kind: TokOp, Op: op, Pos: start, End: l.pos()}
		if op == OpDLess || op == OpDLessDash {
			l.expectDelim = &delimExpect{stripTabs: op == OpDLessDash, opPos: start}
		}
		return tok, nil
	}

	return l.readWord(start)
}

func matchOp(s []rune) (OpKind, int) {
	if len(s) == 0 {
		return OpNone, 0
	}
	at := func(n int, c rune) bool { return len(s) > n && s[n] == c }
	switch s[0] {
	case ';':
		if at(1, ';') {
			if at(2, '&') {
				return OpNone, 0
			}
			return OpDSemi, 2
		}
		return OpSemi, 1
	case '&':
		switch {
		case at(1, '&'):
			return OpAnd, 2
		case at(1, '>'):
			if at(2, '>') {
				return OpAndDGreat, 3
			}
			return OpAndGreat, 2
		}
		return OpAmp, 1
	case '|':
		if at(1, '|') {
			return OpOr, 2
		}
		if at(1, '&') {
			return OpNone, 0
		}
		return OpPipe, 1
	case '<':
		switch {
		case at(1, '<'):
			if at(2, '<') {
				return OpNone, 0
			}
			if at(2, '-') {
				return OpDLessDash, 3
			}
			return OpDLess, 2
		case at(1, '&'):
			return OpLessAmp, 2
		case at(1, '>'):
			return OpLessGreat, 2
		case at(1, '('):
			return OpNone, 0
		}
		return OpLess, 1
	case '>':
		switch {
		case at(1, '>'):
			return OpDGreat, 2
		case at(1, '&'):
			return OpGreatAmp, 2
		case at(1, '|'):
			return OpClobber, 2
		case at(1, '('):
			return OpNone, 0
		}
		return OpGreat, 1
	case '(':
		if at(1, '(') {
			return OpNone, 0
		}
		return OpLParen, 1
	case ')':
		return OpRParen, 1
	}
	return OpNone, 0
}

type opPrefix struct {
	Text      string
	Construct string
	Hint      string
}

var unsupportedOps = []opPrefix{
	{";;&", "';;&' 分支结束符", "改用 ';;'"},
	{";&", "';' '&' 分支结束符", "改用 ';;'"},
	{"|&", "管道符号 '|&'（合并 stderr）", "改用 2>&1 |"},
	{"<<<", "here-string '<<<'", "改用 << heredoc"},
	{"<(", "进程替换 '<(...)'", "改用 $(...) 与临时文件"},
	{">(", "进程替换 '>(...)'", "改用 $(...) 与临时文件"},
	{"((", "算术命令 '((...))'", "改用 $((...)) 展开"},
}

func unsupportedOp(s []rune) (string, string, bool) {
	n := len(s)
	if n > 3 {
		n = 3
	}
	prefix := string(s[:n])
	for _, p := range unsupportedOps {
		if strings.HasPrefix(prefix, p.Text) {
			return p.Construct, p.Hint, true
		}
	}
	return "", "", false
}

func (l *lexer) readComment(start Pos) Token {
	l.advance()
	var b strings.Builder
	for !l.eof() && l.peek() != '\n' {
		b.WriteRune(l.advance())
	}
	return Token{Kind: TokComment, Text: b.String(), Pos: start, End: l.pos()}
}

type wordBuilder struct {
	text strings.Builder
	segs []Segment
	mode WordMode
	pos  Pos
}

func newWordBuilder(mode WordMode, pos Pos) *wordBuilder {
	return &wordBuilder{mode: mode, pos: pos}
}

func (w *wordBuilder) setMode(m WordMode, pos Pos) {
	w.mode = m
	w.pos = pos
}

func (w *wordBuilder) literal(s string) {
	if s == "" {
		return
	}
	w.text.WriteString(s)
	if n := len(w.segs); n > 0 && w.segs[n-1].Kind == SegLiteral && w.segs[n-1].Mode == w.mode {
		w.segs[n-1].Text += s
		return
	}
	w.segs = append(w.segs, Segment{Kind: SegLiteral, Text: s, Mode: w.mode, Pos: w.pos})
}

func (w *wordBuilder) literalRune(r rune) { w.literal(string(r)) }

func (w *wordBuilder) segment(kind SegmentKind, text, inner string, pos Pos) {
	w.text.WriteString(text)
	w.segs = append(w.segs, Segment{Kind: kind, Text: text, Inner: inner, Mode: w.mode, Pos: pos})
}

func (w *wordBuilder) mark() {
	if n := len(w.segs); n > 0 && w.segs[n-1].Mode == w.mode && w.segs[n-1].Kind == SegLiteral {
		return
	}
	w.segs = append(w.segs, Segment{Kind: SegLiteral, Mode: w.mode, Pos: w.pos})
}

func (w *wordBuilder) finish() []Segment {
	return compactSegments(w.segs)
}

func (l *lexer) readWord(start Pos) (Token, error) {
	w := newWordBuilder(ModeUnquoted, start)
	quoted := false
	for !l.eof() {
		r := l.peek()
		if isMeta(r) {
			break
		}
		switch r {
		case '\\':
			l.advance()
			pos := l.pos()
			if l.eof() {
				w.setMode(ModeLiteral, pos)
				w.literalRune('\\')
				w.setMode(ModeUnquoted, pos)
				quoted = true
				continue
			}
			if l.peek() == '\n' {
				l.advance()
				continue
			}
			c := l.advance()
			w.setMode(ModeLiteral, pos)
			w.literalRune(c)
			w.setMode(ModeUnquoted, l.pos())
			quoted = true
		case '\'':
			quoted = true
			l.advance()
			l.beginQuoted(w, ModeLiteral)
			if err := l.readSingleQuoted(w); err != nil {
				return Token{}, err
			}
			w.setMode(ModeUnquoted, l.pos())
		case '"':
			quoted = true
			l.advance()
			l.beginQuoted(w, ModeDoubleQuoted)
			if err := l.readDoubleQuoted(w); err != nil {
				return Token{}, err
			}
			w.setMode(ModeUnquoted, l.pos())
		case '$':
			expanded, err := l.readDollar(w)
			if err != nil {
				return Token{}, err
			}
			if expanded {
				continue
			}
			l.advance()
			w.literal("$")
		case '`':
			return Token{}, unsupported("反引号命令替换", "改用 $(...)", l.pos())
		default:
			l.advance()
			w.literalRune(r)
		}
	}
	w.mark()
	return Token{
		Kind:   TokWord,
		Text:   w.text.String(),
		Pos:    start,
		End:    l.pos(),
		Quoted: quoted,
		segs:   w.finish(),
	}, nil
}

func lexWordSegments(text string) ([]Segment, error) {
	if text == "" {
		return nil, nil
	}
	l := newLexer(text)
	tok, err := l.Next()
	if err != nil {
		return nil, err
	}
	if tok.Kind != TokWord {
		return []Segment{{Kind: SegLiteral, Text: text, Mode: ModeUnquoted}}, nil
	}
	segs := tok.Segments()
	if rest, err := l.Next(); err == nil && rest.Kind != TokEOF {
		var out []Segment
		out = append(out, segs...)
		out = append(out, Segment{Kind: SegLiteral, Text: " " + rest.Text, Mode: ModeUnquoted})
		return out, nil
	}
	return segs, nil
}

func compactSegments(segs []Segment) []Segment {
	out := make([]Segment, 0, len(segs))
	for _, seg := range segs {
		if seg.Kind == SegLiteral && seg.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Kind == SegLiteral && seg.Kind == SegLiteral && out[n-1].Mode == seg.Mode {
			out[n-1].Text += seg.Text
			continue
		}
		out = append(out, seg)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (l *lexer) beginQuoted(w *wordBuilder, mode WordMode) {
	pos := l.pos()
	w.setMode(mode, pos)
	w.mark()
}

func (l *lexer) readDollar(w *wordBuilder) (bool, error) {
	pos := l.pos()
	n1 := l.peekAt(1)
	switch {
	case n1 == '\'' && w.mode != ModeDoubleQuoted:
		l.advance()
		l.advance()
		w.setMode(ModeLiteral, l.pos())
		w.mark()
		if err := l.readAnsiCQuoted(w); err != nil {
			return false, err
		}
		w.setMode(ModeUnquoted, l.pos())
		return true, nil
	case n1 == '(' && l.peekAt(2) == '(':
		return true, l.readArith(w, pos)
	case n1 == '(':
		return true, l.readCmdSub(w, pos)
	case n1 == '{':
		return true, l.readBraced(w, pos)
	case isNameStart(n1):
		l.advance()
		var b strings.Builder
		for !l.eof() && isNameRune(l.peek()) {
			b.WriteRune(l.advance())
		}
		name := b.String()
		w.segment(SegVar, "$"+name, name, pos)
		return true, nil
	case isSpecialParam(n1):
		l.advance()
		c := l.advance()
		w.segment(SegVar, "$"+string(c), string(c), pos)
		return true, nil
	}
	return false, nil
}

func isNameStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isNameRune(r rune) bool { return isNameStart(r) || (r >= '0' && r <= '9') }

func isSpecialParam(r rune) bool {
	switch r {
	case '?', '#', '@', '*', '$', '!', '-':
		return true
	}
	return r >= '0' && r <= '9'
}

func (l *lexer) readArith(w *wordBuilder, pos Pos) error {
	start := l.pos()
	l.advance()
	l.advance()
	l.advance()
	var b strings.Builder
	depth := 0
	for !l.eof() {
		r := l.advance()
		switch r {
		case '(':
			depth++
		case ')':
			if depth == 0 && l.peek() == ')' {
				l.advance()
				inner := b.String()
				w.segment(SegArith, "$(("+inner+"))", inner, pos)
				return nil
			}
			depth--
		case '\\':
			b.WriteRune(r)
			if !l.eof() {
				b.WriteRune(l.advance())
			}
			continue
		}
		b.WriteRune(r)
	}
	return &SyntaxError{Msg: "$(( 未闭合", Pos: start}
}

func (l *lexer) readCmdSub(w *wordBuilder, pos Pos) error {
	start := l.pos()
	l.advance()
	l.advance()
	var b strings.Builder
	depth := 0
	quote := rune(0)
	for !l.eof() {
		r := l.advance()
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && !l.eof() {
				b.WriteRune(l.advance())
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '(':
			depth++
		case ')':
			if depth == 0 {
				inner := b.String()
				w.segment(SegCmdSub, "$("+inner+")", inner, pos)
				return nil
			}
			depth--
		case '\\':
			b.WriteRune(r)
			if !l.eof() {
				b.WriteRune(l.advance())
			}
			continue
		}
		b.WriteRune(r)
	}
	return &SyntaxError{Msg: "$( 未闭合", Pos: start}
}

func (l *lexer) readBraced(w *wordBuilder, pos Pos) error {
	start := l.pos()
	l.advance()
	l.advance()
	var b strings.Builder
	depth := 0
	for !l.eof() {
		r := l.advance()
		switch r {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				inner := b.String()
				w.segment(SegBraced, "${"+inner+"}", inner, pos)
				return nil
			}
			depth--
		case '\\':
			b.WriteRune(r)
			if !l.eof() {
				b.WriteRune(l.advance())
			}
			continue
		}
		b.WriteRune(r)
	}
	return &SyntaxError{Msg: "${ 未闭合", Pos: start}
}

func (l *lexer) readSingleQuoted(w *wordBuilder) error {
	start := l.pos()
	for !l.eof() {
		r := l.advance()
		if r == '\'' {
			return nil
		}
		w.literalRune(r)
	}
	return &SyntaxError{Msg: "单引号未闭合", EOF: true, Pos: start}
}

func (l *lexer) readDoubleQuoted(w *wordBuilder) error {
	start := l.pos()
	for !l.eof() {
		r := l.advance()
		switch r {
		case '"':
			return nil
		case '\\':
			pos := l.pos()
			if l.eof() {
				w.setMode(ModeLiteral, pos)
				w.literalRune('\\')
				w.setMode(ModeDoubleQuoted, pos)
				continue
			}
			n := l.peek()
			switch n {
			case '$', '`', '"', '\\':
				l.advance()
				w.setMode(ModeLiteral, pos)
				w.literalRune(n)
				w.setMode(ModeDoubleQuoted, l.pos())
			case '\n':
				l.advance()
			default:
				w.literalRune('\\')
			}
		case '`':
			return unsupported("反引号命令替换", "改用 $(...)", l.pos())
		case '$':
			l.unread()
			expanded, err := l.readDollar(w)
			if err != nil {
				return err
			}
			if !expanded {
				l.advance()
				w.literalRune('$')
			}
		default:
			w.literalRune(r)
		}
	}
	return &SyntaxError{Msg: "双引号未闭合", EOF: true, Pos: start}
}

func (l *lexer) readAnsiCQuoted(w *wordBuilder) error {
	start := l.pos()
	for !l.eof() {
		r := l.advance()
		if r == '\'' {
			return nil
		}
		if r != '\\' {
			w.literalRune(r)
			continue
		}
		if l.eof() {
			w.literalRune('\\')
			break
		}
		e := l.advance()
		switch e {
		case 'n':
			w.literalRune('\n')
		case 't':
			w.literalRune('\t')
		case 'r':
			w.literalRune('\r')
		case 'a':
			w.literalRune('\a')
		case 'b':
			w.literalRune('\b')
		case 'f':
			w.literalRune('\f')
		case 'v':
			w.literalRune('\v')
		case '\\':
			w.literalRune('\\')
		case '\'':
			w.literalRune('\'')
		case '"':
			w.literalRune('"')
		case 'e', 'E':
			w.literalRune(0x1b)
		default:
			w.literalRune('\\')
			w.literalRune(e)
		}
	}
	return &SyntaxError{Msg: "$'...' 未闭合", EOF: true, Pos: start}
}

func (l *lexer) readHeredocDelim(exp *delimExpect) (Token, error) {
	l.skipBlanks()
	if l.eof() || l.peek() == '\n' {
		return Token{}, &SyntaxError{Msg: "heredoc 缺少终止符", Pos: exp.opPos}
	}
	start := l.pos()
	tok, err := l.readWord(start)
	if err != nil {
		return Token{}, err
	}
	if tok.Text == "" {
		return Token{}, &SyntaxError{Msg: "heredoc 缺少终止符", Pos: start}
	}
	h := &Heredoc{
		Delim:      tok.Text,
		StripTabs:  exp.stripTabs,
		ExpandBody: !tok.Quoted,
		Pos:        start,
	}
	l.pending = append(l.pending, h)
	tok.Heredocs = []*Heredoc{h}
	return tok, nil
}

func (l *lexer) finishHeredocs() {
	for len(l.pending) > 0 {
		l.readHeredocBody()
	}
}

func (l *lexer) unterminatedHeredocs() []UnterminatedHeredoc {
	return l.unterminated
}

func (l *lexer) readHeredocBodies() (Token, error) {
	h := l.readHeredocBody()
	if h == nil {
		return Token{Kind: TokEOF, Pos: l.pos(), End: l.pos()}, nil
	}
	if len(l.pending) > 0 {
		l.pendingStart = true
	}
	return Token{Kind: TokHeredocBody, Text: h.Body, Pos: h.Pos, End: l.pos(), Heredocs: []*Heredoc{h}}, nil
}

func (l *lexer) readHeredocBody() *Heredoc {
	if len(l.pending) == 0 {
		return nil
	}
	h := l.pending[0]
	l.pending = l.pending[1:]
	if !l.eof() && l.peek() == '\n' {
		l.advance()
	}
	var body strings.Builder
	closed := false
	for {
		if l.eof() {
			break
		}
		lineStart := l.i
		for !l.eof() && l.peek() != '\n' {
			l.advance()
		}
		line := string(l.src[lineStart:l.i])
		if !l.eof() {
			l.advance()
		}
		cmp := line
		if h.StripTabs {
			cmp = strings.TrimLeft(cmp, "\t")
		}
		if cmp == h.Delim {
			closed = true
			break
		}
		if h.StripTabs {
			body.WriteString(strings.TrimLeft(line, "\t"))
		} else {
			body.WriteString(line)
		}
		if !l.eof() {
			body.WriteString("\n")
		}
	}
	if !closed && body.Len() > 0 {
		body.WriteString("\n")
	}
	h.Body = body.String()
	if !closed {
		l.unterminated = append(l.unterminated, UnterminatedHeredoc{
			Wanted:  h.Delim,
			Line:    h.Pos.Line,
			EndLine: l.line,
		})
	}
	return h
}
