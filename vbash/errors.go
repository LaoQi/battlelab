package vbash

import "fmt"

type UnsupportedError struct {
	Construct string
	Hint      string
	Pos       Pos
}

func (e *UnsupportedError) Error() string {
	at := ""
	if e.Pos.Line > 0 {
		at = fmt.Sprintf("（第 %d 行第 %d 列）", e.Pos.Line, e.Pos.Col)
	}
	if e.Hint != "" {
		return fmt.Sprintf("vbash: 不支持的构造 %s%s：%s", e.Construct, at, e.Hint)
	}
	return fmt.Sprintf("vbash: 不支持的构造 %s%s", e.Construct, at)
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
	if e.Pos.Line > 0 {
		return fmt.Sprintf("vbash: 语法错误（第 %d 行第 %d 列）：%s", e.Pos.Line, e.Pos.Col, e.Msg)
	}
	return fmt.Sprintf("vbash: 语法错误：%s", e.Msg)
}
