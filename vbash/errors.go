package vbash

import "fmt"

type UnsupportedError struct {
	Construct string
	Hint      string
	Pos       Pos
}

func (e *UnsupportedError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("vbash: 不支持的构造 %s（第 %d 行第 %d 列）：%s", e.Construct, e.Pos.Line, e.Pos.Col, e.Hint)
	}
	return fmt.Sprintf("vbash: 不支持的构造 %s（第 %d 行第 %d 列）", e.Construct, e.Pos.Line, e.Pos.Col)
}

func unsupported(construct, hint string, pos Pos) error {
	return &UnsupportedError{Construct: construct, Hint: hint, Pos: pos}
}

type SyntaxError struct {
	Msg   string
	Token string
	Line  string
	EOF   bool
	Pos   Pos
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("vbash: 语法错误（第 %d 行第 %d 列）：%s", e.Pos.Line, e.Pos.Col, e.Msg)
}
