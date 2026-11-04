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
	OpDSemi
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
	OpSemi: "';'", OpDSemi: "';;'", OpAnd: "'&&'", OpOr: "'||'", OpPipe: "'|'", OpAmp: "'&'",
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

type WordMode int

const (
	ModeUnquoted WordMode = iota
	ModeDoubleQuoted
	ModeLiteral
)

func (m WordMode) String() string {
	switch m {
	case ModeDoubleQuoted:
		return "双引号内"
	case ModeLiteral:
		return "字面"
	}
	return "未引用"
}

type SegmentKind int

const (
	SegLiteral SegmentKind = iota
	SegVar
	SegBraced
	SegArith
	SegCmdSub
)

func (k SegmentKind) String() string {
	switch k {
	case SegVar:
		return "变量"
	case SegBraced:
		return "${...}"
	case SegArith:
		return "$((...))"
	case SegCmdSub:
		return "$(...)"
	}
	return "字面"
}

type Segment struct {
	Kind  SegmentKind
	Text  string
	Inner string
	Mode  WordMode
	Sub   *List
	Pos   Pos
}

type Token struct {
	Kind     TokenKind
	Op       OpKind
	Text     string
	Pos      Pos
	End      Pos
	Heredocs []*Heredoc
	Quoted   bool
	segs     []Segment
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

func (t Token) Segments() []Segment { return t.segs }
