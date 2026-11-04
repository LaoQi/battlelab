package vbash

import "strings"

const maxNestDepth = 64

type parser struct {
	lex   *lexer
	buf   []Token
	err   error
	last  Token
	stop  []string
	depth int
}

func (p *parser) pushStop(words ...string) { p.stop = append(p.stop, words...) }

func (p *parser) popStop(n int) { p.stop = p.stop[:len(p.stop)-n] }

func (p *parser) atStop(tok Token) bool {
	if len(p.stop) == 0 {
		return false
	}
	text, ok := plainText(tok)
	if !ok {
		return false
	}
	for i := len(p.stop) - 1; i >= 0; i-- {
		if p.stop[i] == text {
			return true
		}
	}
	return false
}

type parseCtx int

const (
	ctxTop parseCtx = iota
	ctxSubshell
	ctxBrace
)

func Parse(src string) (*List, error) {
	return parseAt(src, 0)
}

func parseAt(src string, depth int) (*List, error) {
	p := &parser{lex: newLexer(src), depth: depth}
	return p.parseSource()
}

func (p *parser) parseSource() (*List, error) {
	list, err := p.parseList(ctxTop)
	if err != nil {
		p.lex.finishHeredocs()
		return nil, err
	}
	tok, err := p.peek(0)
	if err != nil {
		return nil, err
	}
	if tok.Kind != TokEOF {
		return nil, p.unexpected(tok)
	}
	p.lex.finishHeredocs()
	if err := resolveSubstitutions(list, p.depth); err != nil {
		return nil, err
	}
	return list, nil
}

func resolveSubstitutions(list *List, depth int) error {
	if list == nil {
		return nil
	}
	for _, item := range list.Items {
		for _, pipe := range item.AndOr.Pipes {
			for _, cmd := range pipe.Pipeline.Commands {
				for i := range cmd.Words {
					if err := resolveWord(&cmd.Words[i], depth); err != nil {
						return err
					}
				}
				for _, a := range cmd.Assigns {
					if err := resolveWord(&a.Value, depth); err != nil {
						return err
					}
				}
				for _, r := range cmd.Redirs {
					if err := resolveWord(&r.Target, depth); err != nil {
						return err
					}
				}
				if cmd.Group != nil {
					if err := resolveSubstitutions(cmd.Group, depth); err != nil {
						return err
					}
				}
				if cmd.Func != nil && cmd.Func.Body != nil {
					if err := resolveSubstitutions(cmd.Func.Body.Group, depth); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func resolveWord(w *Word, depth int) error {
	for i := range w.Segments {
		seg := &w.Segments[i]
		if seg.Kind != SegCmdSub || seg.Sub != nil || seg.Inner == "" {
			continue
		}
		if depth >= maxNestDepth {
			return &SyntaxError{
				Msg:   "命令替换嵌套过深（上限 " + itoa(maxNestDepth) + " 层）",
				Token: "$(",
				Line:  "命令替换嵌套超过 " + itoa(maxNestDepth) + " 层",
				Pos:   seg.Pos,
			}
		}
		list, err := parseAt(seg.Inner, depth+1)
		if err != nil {
			return err
		}
		seg.Sub = list
	}
	return nil
}

func (p *parser) source() (Token, error) {
	for {
		tok, err := p.lex.Next()
		if err != nil {
			return Token{}, err
		}
		if tok.Kind == TokComment || tok.Kind == TokHeredocBody {
			continue
		}
		return tok, nil
	}
}

func (p *parser) peek(n int) (Token, error) {
	if p.err != nil {
		return Token{}, p.err
	}
	for len(p.buf) <= n {
		tok, err := p.source()
		if err != nil {
			p.err = err
			return Token{}, err
		}
		p.buf = append(p.buf, tok)
	}
	return p.buf[n], nil
}

func (p *parser) next() (Token, error) {
	tok, err := p.peek(0)
	if err != nil {
		return Token{}, err
	}
	p.buf = p.buf[1:]
	p.last = tok
	return tok, nil
}

func (p *parser) pos() Pos { return p.last.End }

func (p *parser) unexpected(tok Token) error {
	return &SyntaxError{Msg: "未预期的记号 " + tok.String(), Token: bashTokenName(tok), Line: tok.Text, Pos: tok.Pos}
}

func bashTokenName(tok Token) string {
	switch tok.Kind {
	case TokEOF:
		return "end of file"
	case TokNewline:
		return "newline"
	case TokOp:
		switch tok.Op {
		case OpSemi:
			return ";"
		case OpDSemi:
			return ";;"
		case OpAnd:
			return "&&"
		case OpOr:
			return "||"
		case OpPipe:
			return "|"
		case OpAmp:
			return "&"
		case OpLess:
			return "<"
		case OpGreat:
			return ">"
		case OpLParen:
			return "("
		case OpRParen:
			return ")"
		}
	}
	return tok.Text
}

func isOp(tok Token, ops ...OpKind) bool {
	if tok.Kind != TokOp {
		return false
	}
	for _, op := range ops {
		if tok.Op == op {
			return true
		}
	}
	return false
}

func plainText(tok Token) (string, bool) {
	if tok.Kind != TokWord || tok.Quoted {
		return "", false
	}
	segs := tok.Segments()
	if len(segs) != 1 || segs[0].Mode != ModeUnquoted || segs[0].Kind != SegLiteral {
		return "", false
	}
	return tok.Text, true
}

func (p *parser) endsCommand(tok Token) bool {
	switch tok.Kind {
	case TokEOF, TokNewline:
		return true
	case TokOp:
		switch tok.Op {
		case OpSemi, OpDSemi, OpAmp, OpAnd, OpOr, OpPipe, OpRParen:
			return true
		}
	}
	return false
}

func (p *parser) skipNewlines() error {
	for {
		tok, err := p.peek(0)
		if err != nil {
			return err
		}
		if tok.Kind != TokNewline {
			return nil
		}
		if _, err := p.next(); err != nil {
			return err
		}
	}
}

func (p *parser) parseList(ctx parseCtx) (*List, error) {
	list := &List{Pos: p.pos()}
	for {
		if err := p.skipNewlines(); err != nil {
			return nil, err
		}
		tok, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		if tok.Kind == TokEOF {
			if ctx != ctxTop {
				return nil, p.missingClose(ctx)
			}
			return list, nil
		}
		if text, ok := plainText(tok); ok && text == "}" && ctx == ctxBrace {
			return list, nil
		}
		if isOp(tok, OpRParen) {
			if ctx == ctxSubshell {
				return list, nil
			}
			return nil, p.unexpected(tok)
		}
		if isOp(tok, OpSemi, OpAmp, OpAnd, OpOr, OpPipe, OpDSemi) {
			return nil, p.unexpected(tok)
		}
		if p.atStop(tok) {
			return list, nil
		}
		item, err := p.parseAndOr(ctx)
		if err != nil {
			return nil, err
		}
		next, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		switch {
		case isOp(next, OpSemi, OpAmp):
			if _, err := p.next(); err != nil {
				return nil, err
			}
			item.Sep = next.Op
		case next.Kind == TokNewline:
			if _, err := p.next(); err != nil {
				return nil, err
			}
			item.Newline = true
		case next.Kind == TokEOF, isOp(next, OpRParen):
			if ctx != ctxTop && next.Kind == TokEOF {
				return nil, p.missingClose(ctx)
			}
		default:
			return nil, p.unexpected(next)
		}
		list.Items = append(list.Items, item)
	}
}

func (p *parser) missingClose(ctx parseCtx) error {
	if ctx == ctxBrace {
		return &SyntaxError{Msg: "缺少 '}'", Pos: p.pos()}
	}
	return &SyntaxError{Msg: "缺少 ')'", Pos: p.pos()}
}

func (p *parser) parseAndOr(ctx parseCtx) (*ListItem, error) {
	item := &ListItem{Pos: p.pos()}
	and := &AndOr{Pos: p.pos()}
	for {
		pl, err := p.parsePipeline(ctx)
		if err != nil {
			return nil, err
		}
		and.Pipes = append(and.Pipes, &PipeItem{Pipeline: pl, Pos: pl.Pos})
		tok, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		if !isOp(tok, OpAnd, OpOr) {
			break
		}
		if _, err := p.next(); err != nil {
			return nil, err
		}
		and.Pipes[len(and.Pipes)-1].Op = tok.Op
		if err := p.skipNewlines(); err != nil {
			return nil, err
		}
		next, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		if p.endsCommand(next) {
			return nil, p.unexpected(next)
		}
	}
	item.AndOr = and
	return item, nil
}

func (p *parser) parsePipeline(ctx parseCtx) (*Pipeline, error) {
	pl := &Pipeline{Pos: p.pos()}
	tok, err := p.peek(0)
	if err != nil {
		return nil, err
	}
	if text, ok := plainText(tok); ok && text == "!" {
		if _, err := p.next(); err != nil {
			return nil, err
		}
		pl.Bang = true
	}
	for {
		cmd, err := p.parseCommand(ctx)
		if err != nil {
			return nil, err
		}
		pl.Commands = append(pl.Commands, cmd)
		next, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		if !isOp(next, OpPipe) {
			return pl, nil
		}
		if _, err := p.next(); err != nil {
			return nil, err
		}
		after, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		if p.endsCommand(after) {
			return nil, p.unexpected(after)
		}
	}
}

func (p *parser) parseCommand(ctx parseCtx) (*Command, error) {
	cmd := &Command{Pos: p.pos()}
	for {
		tok, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		if p.endsCommand(tok) {
			break
		}
		if cmd.Group != nil || cmd.Func != nil || cmd.Compound != nil {
			if tok.Kind == TokOp && isRedirectOp(tok.Op) {
				redir, err := p.parseRedirect(tok, DefaultFd)
				if err != nil {
					return nil, err
				}
				cmd.Redirs = append(cmd.Redirs, redir)
				continue
			}
			return nil, p.unexpected(tok)
		}
		if tok.Kind == TokOp {
			if tok.Op == OpLParen && cmd.noPrefix() {
				group, err := p.parseSubshell()
				if err != nil {
					return nil, err
				}
				cmd.Group = group
				cmd.GroupKind = GroupSubshell
				continue
			}
			if !isRedirectOp(tok.Op) {
				return nil, p.unexpected(tok)
			}
			redir, err := p.parseRedirect(tok, DefaultFd)
			if err != nil {
				return nil, err
			}
			cmd.Redirs = append(cmd.Redirs, redir)
			continue
		}
		if tok.Kind != TokWord {
			return nil, p.unexpected(tok)
		}
		if fd, ok, err := p.redirectFd(tok); err != nil {
			return nil, err
		} else if ok {
			if _, err := p.next(); err != nil {
				return nil, err
			}
			opTok, err := p.peek(0)
			if err != nil {
				return nil, err
			}
			redir, err := p.parseRedirect(opTok, fd)
			if err != nil {
				return nil, err
			}
			cmd.Redirs = append(cmd.Redirs, redir)
			continue
		}
		if len(cmd.Words) == 0 && cmd.Group == nil && cmd.Func == nil {
			if name, value, ok := splitAssignment(tok); ok {
				if _, err := p.next(); err != nil {
					return nil, err
				}
				cmd.Assigns = append(cmd.Assigns, Assign{Name: name, Value: value, Pos: tok.Pos})
				continue
			}
		}
		if cmd.noPrefix() {
			if text, ok := plainText(tok); ok && text == "{" {
				group, err := p.parseBraceGroup()
				if err != nil {
					return nil, err
				}
				cmd.Group = group
				cmd.GroupKind = GroupBrace
				continue
			}
			if ok, err := p.tryFuncDef(cmd); err != nil {
				return nil, err
			} else if ok {
				continue
			}
		}
		if cmd.noPrefix() {
			if text, ok := plainText(tok); ok {
				if compound, handled, err := p.parseCompound(text); err != nil {
					return nil, err
				} else if handled {
					cmd.Compound = compound
					continue
				}
			}
		}
		if text, ok := plainText(tok); ok && strings.HasPrefix(text, "[[") {
			return nil, unsupported("[[ ]] 条件表达式", "改用 [ ] 或 test", tok.Pos)
		}
		if _, err := p.next(); err != nil {
			return nil, err
		}
		cmd.Words = append(cmd.Words, Word{Segments: tok.Segments(), Text: tok.Text, Pos: tok.Pos, End: tok.End})
	}
	if cmd.IsEmpty() {
		tok, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		return nil, p.unexpected(tok)
	}
	return cmd, nil
}

func (p *parser) parseCompound(name string) (*Compound, bool, error) {
	switch name {
	case "for":
		c, err := p.parseFor()
		return c, true, err
	case "while":
		c, err := p.parseWhile(false)
		return c, true, err
	case "until":
		c, err := p.parseWhile(true)
		return c, true, err
	case "if":
		c, err := p.parseIf()
		return c, true, err
	case "then", "elif", "else", "fi", "do", "done", "esac":
		return nil, false, &SyntaxError{Msg: "未预期的记号 " + name, Token: name, Line: name, Pos: p.pos()}
	case "case":
		return nil, true, unsupported("case 语句", "case 属 P2，见 docs/vbash-plan.md §3.2", p.pos())
	case "select":
		return nil, true, unsupported("select 语句", "不支持", p.pos())
	case "function":
		return nil, true, unsupported("function 关键字定义函数", "改用 name() { ...; } 形式", p.pos())
	case "coproc":
		return nil, true, unsupported("coproc 协程", "不支持", p.pos())
	case "time":
		return nil, true, unsupported("time 关键字", "不支持", p.pos())
	}
	if _, ok := plainText(Token{Kind: TokWord, Text: name}); ok {
		if reservedWords[name] {
			return nil, false, &SyntaxError{Msg: "未预期的记号 " + name, Token: name, Line: name, Pos: p.pos()}
		}
	}
	return nil, false, nil
}

func (p *parser) redirectFd(tok Token) (int, bool, error) {
	if tok.Quoted {
		return DefaultFd, false, nil
	}
	if n, ok := plainDigits(tok); ok {
		opTok, err := p.peek(1)
		if err != nil {
			return DefaultFd, false, err
		}
		if opTok.Kind != TokOp || !isRedirectOp(opTok.Op) || tok.End != opTok.Pos {
			return DefaultFd, false, nil
		}
		return n, true, nil
	}
	return DefaultFd, false, nil
}

func plainDigits(tok Token) (int, bool) {
	if tok.Kind != TokWord || tok.Quoted {
		return 0, false
	}
	segs := tok.Segments()
	if len(segs) != 1 || segs[0].Mode != ModeUnquoted || segs[0].Kind != SegLiteral {
		return 0, false
	}
	return Word{Segments: segs, Text: tok.Text}.Digits()
}

func isRedirectOp(op OpKind) bool {
	switch op {
	case OpLess, OpGreat, OpDGreat, OpDLess, OpDLessDash, OpGreatAmp, OpLessAmp,
		OpClobber, OpAndGreat, OpAndDGreat, OpLessGreat:
		return true
	}
	return false
}

func (p *parser) parseRedirect(opTok Token, fd int) (*Redirect, error) {
	op := opTok.Op
	if !isRedirectOp(op) {
		return nil, p.unexpected(opTok)
	}
	if _, err := p.next(); err != nil {
		return nil, err
	}
	target, err := p.peek(0)
	if err != nil {
		return nil, err
	}
	if target.Kind != TokWord {
		return nil, &SyntaxError{Msg: "重定向缺少目标", Pos: opTok.Pos}
	}
	if _, err := p.next(); err != nil {
		return nil, err
	}
	r := &Redirect{Fd: fd, Op: op, Target: Word{Segments: target.segs, Text: target.Text, Pos: target.Pos, End: target.End}, Pos: opTok.Pos}
	if op == OpDLess || op == OpDLessDash {
		if len(target.Heredocs) != 1 {
			return nil, &SyntaxError{Msg: "heredoc 缺少终止符", Pos: opTok.Pos}
		}
		r.Heredoc = target.Heredocs[0]
	}
	return r, nil
}

func (p *parser) parseSubshell() (*List, error) {
	if _, err := p.next(); err != nil {
		return nil, err
	}
	list, err := p.parseList(ctxSubshell)
	if err != nil {
		return nil, err
	}
	tok, err := p.peek(0)
	if err != nil {
		return nil, err
	}
	if !isOp(tok, OpRParen) {
		return nil, p.missingClose(ctxSubshell)
	}
	if _, err := p.next(); err != nil {
		return nil, err
	}
	return list, nil
}

func (p *parser) parseBraceGroup() (*List, error) {
	if _, err := p.next(); err != nil {
		return nil, err
	}
	list, err := p.parseList(ctxBrace)
	if err != nil {
		return nil, err
	}
	tok, err := p.peek(0)
	if err != nil {
		return nil, err
	}
	if text, ok := plainText(tok); !ok || text != "}" {
		return nil, p.missingClose(ctxBrace)
	}
	if _, err := p.next(); err != nil {
		return nil, err
	}
	return list, nil
}

func (p *parser) tryFuncDef(cmd *Command) (bool, error) {
	head, err := p.peek(0)
	if err != nil {
		return false, err
	}
	name, ok := plainText(head)
	if !ok || !validFuncName(name) {
		return false, nil
	}
	open, err := p.peek(1)
	if err != nil {
		return false, err
	}
	closing, err := p.peek(2)
	if err != nil {
		return false, err
	}
	if !isOp(open, OpLParen) || !isOp(closing, OpRParen) {
		return false, nil
	}
	for i := 0; i < 3; i++ {
		if _, err := p.next(); err != nil {
			return false, err
		}
	}
	if err := p.skipNewlines(); err != nil {
		return false, err
	}
	tok, err := p.peek(0)
	if err != nil {
		return false, err
	}
	if p.endsCommand(tok) {
		return false, p.unexpected(tok)
	}
	body, err := p.parseCommand(ctxTop)
	if err != nil {
		return false, err
	}
	if body.Group == nil {
		return false, &SyntaxError{Msg: "函数体须为 { } 或 ( ) 分组", Pos: tok.Pos}
	}
	cmd.Func = &FuncDef{Name: name, Body: body, Pos: tok.Pos}
	return true, nil
}

var reservedWords = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "select": true, "function": true,
	"coproc": true, "time": true, "in": true, "}": true,
}

func validFuncName(name string) bool {
	if name == "" || name == "{" || name == "}" {
		return false
	}
	return !reservedWords[name]
}

func splitAssignment(tok Token) (string, Word, bool) {
	segs := tok.Segments()
	if len(segs) == 0 || segs[0].Mode != ModeUnquoted || segs[0].Kind != SegLiteral {
		return "", Word{}, false
	}
	first := segs[0].Text
	eq := strings.IndexByte(first, '=')
	if eq <= 0 {
		return "", Word{}, false
	}
	name := first[:eq]
	if !validAssignName(name) {
		return "", Word{}, false
	}
	w := Word{Segments: segs, Text: tok.Text, Pos: tok.Pos, End: tok.End}
	_, value := splitWord(w, eq+1)
	return name, value, true
}

func validAssignName(name string) bool {
	base := name
	if i := strings.IndexByte(name, '['); i >= 0 {
		if !strings.HasSuffix(name, "]") || i == 0 || i == len(name)-1 {
			return false
		}
		base = name[:i]
	}
	for i, r := range base {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func splitWord(w Word, n int) (Word, Word) {
	head := Word{Pos: w.Pos, End: w.End}
	tail := Word{Pos: w.Pos, End: w.End}
	left := n
	for _, seg := range w.Segments {
		runes := []rune(seg.Text)
		if left >= len(runes) {
			head.Segments = append(head.Segments, seg)
			head.Text += seg.Text
			left -= len(runes)
			if left > 0 && seg.Kind != SegLiteral {
				left = 0
			}
			continue
		}
		offset := left
		left = 0
		if offset > 0 {
			part := seg
			part.Text = string(runes[:offset])
			head.Segments = append(head.Segments, part)
			head.Text += part.Text
		}
		rest := seg
		rest.Text = string(runes[offset:])
		tail.Segments = append(tail.Segments, rest)
		tail.Text += rest.Text
	}
	if head.Text == "" {
		head.Segments = nil
	}
	if tail.Text == "" {
		tail.Segments = nil
	}
	return head, tail
}
