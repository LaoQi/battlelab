package vbash

import (
	"fmt"
	"io"
)

const defaultShellName = "/bin/bash"

func (r *Runner) name() string {
	if r.ShellName == "" {
		return defaultShellName
	}
	return r.ShellName
}

func (r *Runner) lineOf(pos Pos) int {
	if pos.Line < 1 {
		return 1
	}
	return pos.Line
}

func (r *Runner) diagf(errw io.Writer, pos Pos, format string, args ...any) {
	if errw == nil {
		return
	}
	fmt.Fprintf(errw, "%s: line %d: %s\n", r.name(), r.lineOf(pos), fmt.Sprintf(format, args...))
}

func (r *Runner) diagfName(errw io.Writer, pos Pos, name, format string, args ...any) {
	if errw == nil {
		return
	}
	fmt.Fprintf(errw, "%s: line %d: %s: %s\n", r.name(), r.lineOf(pos), name, fmt.Sprintf(format, args...))
}

func (r *Runner) eofDiag(errw io.Writer, msg string) {
	if errw == nil {
		return
	}
	quote := "'"
	switch msg {
	case "双引号未闭合":
		quote = "\""
	case "$'...' 未闭合":
		quote = "'"
	}
	fmt.Fprintf(errw, "%s: -c: line 1: unexpected EOF while looking for matching `%s'\n", r.name(), quote)
}

func (r *Runner) syntaxDiag(errw io.Writer, pos Pos, token, detail string) {
	if errw == nil {
		return
	}
	fmt.Fprintf(errw, "%s: -c: line %d: syntax error near unexpected token `%s'\n", r.name(), r.lineOf(pos), token)
	if detail != "" {
		fmt.Fprintf(errw, "%s: -c: line %d: `%s'\n", r.name(), r.lineOf(pos), detail)
	}
}

type RuntimeError struct {
	Msg string
	Pos Pos
}

func (e *RuntimeError) Error() string { return e.Msg }

func runtimeErrf(pos Pos, format string, args ...any) error {
	return &RuntimeError{Msg: fmt.Sprintf(format, args...), Pos: pos}
}

type ExitRequest struct {
	Code int
}

func (e ExitRequest) Error() string { return "exit " + itoa(e.Code) }

type ReturnRequest struct {
	Code int
}

func (e ReturnRequest) Error() string { return "return " + itoa(e.Code) }
