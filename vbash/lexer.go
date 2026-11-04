package vbash

import (
	"strings"
)

type lexer struct {
	src          []rune
	i            int
	line         int
	col          int
	pending      []Heredoc
	heredocs     []Heredoc
	pendingStart bool
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
	if l.pendingStart {
		l.pendingStart = false
		return l.readHeredocBodies()
	}
	l.skipBlanks()
	if l.eof() {
		return Token{Kind: TokEOF, Pos: l.pos()}, nil
	}
	start := l.pos()
	r := l.peek()

	switch r {
	case '\n':
		l.advance()
		if len(l.pending) > 0 {
			l.pendingStart = true
		}
		return Token{Kind: TokNewline, Text: "\n", Pos: start}, nil
	case '#':
		return l.readComment(start), nil
	}

	if op, n := matchOp(l.src[l.i:]); n > 0 {
		for range n {
			l.advance()
		}
		tok := Token{Kind: TokOp, Op: op, Pos: start}
		if op == OpDLess || op == OpDLessDash {
			if err := l.registerHeredoc(op == OpDLessDash, start); err != nil {
				return Token{}, err
			}
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

func (l *lexer) readComment(start Pos) Token {
	l.advance()
	var b strings.Builder
	for !l.eof() && l.peek() != '\n' {
		b.WriteRune(l.advance())
	}
	return Token{Kind: TokComment, Text: b.String(), Pos: start}
}

func (l *lexer) readWord(start Pos) (Token, error) {
	var b strings.Builder
	quoted := false
	for !l.eof() {
		r := l.peek()
		if isMeta(r) {
			break
		}
		switch r {
		case '\\':
			l.advance()
			if l.eof() {
				break
			}
			if l.peek() == '\n' {
				l.advance()
				continue
			}
			b.WriteRune(l.advance())
			quoted = true
		case '\'':
			quoted = true
			l.advance()
			if err := l.readSingleQuoted(&b); err != nil {
				return Token{}, err
			}
		case '"':
			quoted = true
			l.advance()
			if err := l.readDoubleQuoted(&b); err != nil {
				return Token{}, err
			}
		case '$':
			if l.peekAt(1) == '\'' {
				quoted = true
				l.advance()
				l.advance()
				if err := l.readAnsiCQuoted(&b); err != nil {
					return Token{}, err
				}
				continue
			}
			if l.peekAt(1) == '(' && l.peekAt(2) == '(' {
				if err := l.readArithExpansion(&b); err != nil {
					return Token{}, err
				}
				continue
			}
			if l.peekAt(1) == '(' {
				if err := l.readCmdSubstitution(&b); err != nil {
					return Token{}, err
				}
				continue
			}
			b.WriteRune(l.advance())
		case '`':
			return Token{}, unsupported("反引号命令替换", "改用 $(...)", l.pos())
		default:
			b.WriteRune(l.advance())
		}
	}
	return Token{Kind: TokWord, Text: b.String(), Pos: start, Quoted: quoted}, nil
}

func (l *lexer) readSingleQuoted(b *strings.Builder) error {
	start := l.pos()
	for !l.eof() {
		r := l.advance()
		if r == '\'' {
			return nil
		}
		b.WriteRune(r)
	}
	return &SyntaxError{Msg: "单引号未闭合", Pos: start}
}

func (l *lexer) readDoubleQuoted(b *strings.Builder) error {
	start := l.pos()
	for !l.eof() {
		r := l.advance()
		switch r {
		case '"':
			return nil
		case '\\':
			if l.eof() {
				b.WriteRune('\\')
				break
			}
			n := l.peek()
			switch n {
			case '$', '`', '"', '\\':
				b.WriteRune(l.advance())
			case '\n':
				l.advance()
			default:
				b.WriteRune('\\')
			}
		case '`':
			return unsupported("反引号命令替换", "改用 $(...)", l.pos())
		default:
			b.WriteRune(r)
		}
	}
	return &SyntaxError{Msg: "双引号未闭合", Pos: start}
}

func (l *lexer) readAnsiCQuoted(b *strings.Builder) error {
	start := l.pos()
	for !l.eof() {
		r := l.advance()
		if r == '\'' {
			return nil
		}
		if r != '\\' {
			b.WriteRune(r)
			continue
		}
		if l.eof() {
			b.WriteRune('\\')
			break
		}
		e := l.advance()
		switch e {
		case 'n':
			b.WriteRune('\n')
		case 't':
			b.WriteRune('\t')
		case 'r':
			b.WriteRune('\r')
		case 'a':
			b.WriteRune('\a')
		case 'b':
			b.WriteRune('\b')
		case 'f':
			b.WriteRune('\f')
		case 'v':
			b.WriteRune('\v')
		case '\\':
			b.WriteRune('\\')
		case '\'':
			b.WriteRune('\'')
		case '"':
			b.WriteRune('"')
		case 'e', 'E':
			b.WriteRune(0x1b)
		default:
			b.WriteRune('\\')
			b.WriteRune(e)
		}
	}
	return &SyntaxError{Msg: "$'...' 未闭合", Pos: start}
}

func (l *lexer) registerHeredoc(stripTabs bool, opPos Pos) error {
	l.skipBlanks()
	if l.eof() {
		return &SyntaxError{Msg: "heredoc 缺少终止符", Pos: opPos}
	}
	start := l.pos()
	var delim strings.Builder
	expand := true
	quotedDelim := false
	for !l.eof() {
		r := l.peek()
		if isBlank(r) || r == '\n' || r == ';' || r == '&' || r == '|' || r == '<' || r == '>' {
			break
		}
		switch r {
		case '\'':
			quotedDelim = true
			l.advance()
			for !l.eof() {
				c := l.advance()
				if c == '\'' {
					break
				}
				delim.WriteRune(c)
			}
		case '"':
			quotedDelim = true
			l.advance()
			for !l.eof() {
				c := l.advance()
				if c == '"' {
					break
				}
				delim.WriteRune(c)
			}
		case '\\':
			quotedDelim = true
			l.advance()
			if !l.eof() {
				delim.WriteRune(l.advance())
			}
		default:
			delim.WriteRune(l.advance())
		}
	}
	if delim.Len() == 0 {
		return &SyntaxError{Msg: "heredoc 缺少终止符", Pos: start}
	}
	if quotedDelim {
		expand = false
	}
	l.pending = append(l.pending, Heredoc{
		Delim:      delim.String(),
		StripTabs:  stripTabs,
		ExpandBody: expand,
		Pos:        start,
	})
	return nil
}

func (l *lexer) readHeredocBodies() (Token, error) {
	h := l.pending[0]
	l.pending = l.pending[1:]
	if !l.eof() && l.peek() == '\n' {
		l.advance()
	}
	var body strings.Builder
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
	h.Body = body.String()
	l.heredocs = append(l.heredocs, h)
	if len(l.pending) > 0 {
		l.pendingStart = true
	}
	return Token{Kind: TokHeredocBody, Text: h.Body, Pos: h.Pos, Heredocs: []Heredoc{h}}, nil
}

func (l *lexer) readArithExpansion(b *strings.Builder) error {
	start := l.pos()
	b.WriteString("$((")
	l.advance()
	l.advance()
	l.advance()
	depth := 0
	for !l.eof() {
		r := l.advance()
		switch r {
		case '(':
			depth++
		case ')':
			if depth == 0 && l.peek() == ')' {
				l.advance()
				b.WriteString("))")
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

func (l *lexer) readCmdSubstitution(b *strings.Builder) error {
	start := l.pos()
	b.WriteString("$(")
	l.advance()
	l.advance()
	depth := 0
	for !l.eof() {
		r := l.advance()
		switch r {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				b.WriteRune(')')
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
