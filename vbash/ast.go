package vbash

import (
	"strconv"
	"strings"
)

type List struct {
	Items []*ListItem
	Pos   Pos
}

type ListItem struct {
	AndOr   *AndOr
	Sep     OpKind
	Newline bool
	Pos     Pos
}

type AndOr struct {
	Pipes []*PipeItem
	Pos   Pos
}

type PipeItem struct {
	Pipeline *Pipeline
	Op       OpKind
	Pos      Pos
}

type Pipeline struct {
	Commands []*Command
	Bang     bool
	Pos      Pos
}

type GroupKind int

const (
	GroupNone GroupKind = iota
	GroupSubshell
	GroupBrace
)

type Command struct {
	Assigns   []Assign
	Words     []Word
	Redirs    []*Redirect
	Group     *List
	GroupKind GroupKind
	Func      *FuncDef
	Compound  *Compound
	Pos       Pos
}

type CompoundKind int

const (
	CompoundNone CompoundKind = iota
	CompoundFor
	CompoundWhile
	CompoundUntil
	CompoundIf
)

type Compound struct {
	Kind  CompoundKind
	Name  string
	Words []Word
	Cond  *List
	Body  *List
	Elifs []*CondClause
	Else  *List
	Pos   Pos
}

type CondClause struct {
	Cond *List
	Body *List
	Pos  Pos
}

type FuncDef struct {
	Name string
	Body *Command
	Pos  Pos
}

type Assign struct {
	Name  string
	Value Word
	Pos   Pos
}

type Redirect struct {
	Fd      int
	Op      OpKind
	Target  Word
	Heredoc *Heredoc
	Pos     Pos
}

type Word struct {
	Segments []Segment
	Text     string
	Pos      Pos
	End      Pos
}

func LiteralWord(text string, pos Pos) Word {
	return Word{
		Segments: []Segment{{Text: text, Mode: ModeUnquoted, Pos: pos}},
		Text:     text,
		Pos:      pos,
		End:      pos,
	}
}

func (w Word) Segs() []Segment { return w.Segments }

func (w Word) Quoted() bool {
	for _, s := range w.Segments {
		if s.Mode != ModeUnquoted {
			return true
		}
	}
	return false
}

func (w Word) Plain() bool {
	if len(w.Segments) != 1 {
		return false
	}
	return w.Segments[0].Mode == ModeUnquoted
}

func (w Word) Digits() (int, bool) {
	if !w.Plain() || w.Text == "" {
		return 0, false
	}
	n := 0
	for _, r := range w.Text {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
		if n > 1<<20 {
			return 0, false
		}
	}
	return n, true
}

const DefaultFd = -1

func redirectsToStdin(op OpKind) bool {
	return op == OpLess || op == OpLessGreat || op == OpDLess || op == OpDLessDash || op == OpLessAmp
}

func (c *Command) noPrefix() bool {
	return len(c.Words) == 0 && len(c.Assigns) == 0 && len(c.Redirs) == 0 &&
		c.Group == nil && c.Func == nil && c.Compound == nil
}

func (c *Command) isSimple() bool {
	return c.Group == nil && c.Func == nil && c.Compound == nil
}

func (c *Command) IsEmpty() bool {
	return len(c.Assigns) == 0 && len(c.Words) == 0 && len(c.Redirs) == 0 &&
		c.Group == nil && c.Func == nil && c.Compound == nil
}

func (l *List) String() string {
	if l == nil {
		return ""
	}
	parts := make([]string, 0, len(l.Items))
	for _, it := range l.Items {
		parts = append(parts, it.String())
	}
	return strings.Join(parts, " ")
}

func (it *ListItem) String() string {
	s := it.AndOr.String()
	if it.Newline {
		return s + " nl"
	}
	switch it.Sep {
	case OpSemi:
		return s + " ;"
	case OpAmp:
		return s + " &"
	}
	return s
}

func (a *AndOr) String() string {
	if a == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range a.Pipes {
		switch p.Op {
		case OpAnd:
			b.WriteString(" && ")
		case OpOr:
			b.WriteString(" || ")
		}
		b.WriteString(p.Pipeline.String())
	}
	return b.String()
}

func (p *Pipeline) String() string {
	if p == nil {
		return ""
	}
	parts := make([]string, 0, len(p.Commands))
	for _, c := range p.Commands {
		parts = append(parts, c.String())
	}
	s := strings.Join(parts, " | ")
	if p.Bang {
		return "! " + s
	}
	return s
}

func (c *Command) String() string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("[")
	for i, a := range c.Assigns {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(a.Name)
		b.WriteString("=")
		b.WriteString(quoteForDump(a.Value.Text))
	}
	if len(c.Assigns) > 0 && (len(c.Words) > 0 || c.Group != nil) {
		b.WriteString(" ")
	}
	for i, w := range c.Words {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(quoteForDump(w.Text))
	}
	if c.Compound != nil {
		b.WriteString(c.Compound.String())
	}
	if c.Func != nil {
		b.WriteString("函数 ")
		b.WriteString(c.Func.Name)
		b.WriteString(" ")
		b.WriteString(c.Func.Body.String())
	}
	if c.Group != nil {
		if len(c.Words) > 0 {
			b.WriteString(" ")
		}
		if c.GroupKind == GroupBrace {
			b.WriteString("{ ")
			b.WriteString(c.Group.String())
			b.WriteString(" }")
		} else {
			b.WriteString("( ")
			b.WriteString(c.Group.String())
			b.WriteString(" )")
		}
	}
	for _, r := range c.Redirs {
		b.WriteString(" ")
		b.WriteString(r.String())
	}
	b.WriteString("]")
	return b.String()
}

func (r *Redirect) String() string {
	fd := ""
	if r.Fd != DefaultFd {
		fd = strconv.Itoa(r.Fd)
	}
	return fd + r.Op.String() + quoteForDump(r.Target.Text)
}

func quoteForDump(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n'\"") {
		return strconv.Quote(s)
	}
	return s
}

func (c *Compound) String() string {
	if c == nil {
		return ""
	}
	switch c.Kind {
	case CompoundFor:
		return "for " + c.Name + " in " + joinWords(c.Words) + " do " + c.Body.String() + " done"
	case CompoundWhile:
		return "while " + c.Cond.String() + " do " + c.Body.String() + " done"
	case CompoundUntil:
		return "until " + c.Cond.String() + " do " + c.Body.String() + " done"
	case CompoundIf:
		s := "if " + c.Cond.String() + " then " + c.Body.String()
		for _, e := range c.Elifs {
			s += " elif " + e.Cond.String() + " then " + e.Body.String()
		}
		if c.Else != nil {
			s += " else " + c.Else.String()
		}
		return s + " fi"
	}
	return ""
}

func joinWords(ws []Word) string {
	parts := make([]string, 0, len(ws))
	for _, w := range ws {
		parts = append(parts, w.Text)
	}
	return strings.Join(parts, " ")
}
