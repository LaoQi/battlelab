package vbash

import "fmt"

type Pos struct {
	Line int
	Col  int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

type TokenKind int

const (
	TokEOF TokenKind = iota
	TokWord
	TokOp
	TokNewline
	TokComment
	TokHeredocBody
)

type OpKind int

const (
	OpNone OpKind = iota
	OpSemi
	OpAnd
	OpOr
	OpPipe
	OpAmp
	OpLess
	OpGreat
	OpDGreat
	OpDLess
	OpDLessDash
	OpGreatAmp
	OpLessAmp
	OpClobber
	OpAndGreat
	OpAndDGreat
	OpLessGreat
	OpLParen
	OpRParen
)

var opNames = map[OpKind]string{
	OpSemi: "';'", OpAnd: "'&&'", OpOr: "'||'", OpPipe: "'|'", OpAmp: "'&'",
	OpLess: "'<'", OpGreat: "'>'", OpDGreat: "'>>'", OpDLess: "'<<'", OpDLessDash: "'<<-'",
	OpGreatAmp: "'>&'", OpLessAmp: "'<&'", OpClobber: "'>|'", OpAndGreat: "'&>'",
	OpAndDGreat: "'&>>'", OpLessGreat: "'<>'", OpLParen: "'('", OpRParen: "')'",
}

func (o OpKind) String() string {
	if s, ok := opNames[o]; ok {
		return s
	}
	return "op"
}

type Token struct {
	Kind     TokenKind
	Op       OpKind
	Text     string
	Pos      Pos
	Heredocs []Heredoc
	Quoted   bool
}

type Heredoc struct {
	Delim      string
	StripTabs  bool
	ExpandBody bool
	Body       string
	Pos        Pos
}

func (t Token) String() string {
	switch t.Kind {
	case TokEOF:
		return "EOF"
	case TokNewline:
		return "NEWLINE"
	case TokOp:
		return t.Op.String()
	case TokComment:
		return "#" + t.Text
	}
	return fmt.Sprintf("%q", t.Text)
}
