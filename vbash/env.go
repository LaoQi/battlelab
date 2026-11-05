package vbash

import (
	"path"
	"sort"
	"strconv"
	"strings"
)

type Var struct {
	Value    string
	Exported bool
}

type Env struct {
	vars   map[string]*Var
	args   []string
	arg0   string
	dir    string
	home   string
	prev   int
	pid    int
	inFunc bool
	locals []string
}

func NewEnv() *Env {
	e := &Env{vars: map[string]*Var{}}
	e.Set("IFS", " \t\n", false)
	return e
}

func (e *Env) Set(name, value string, exported bool) {
	if v, ok := e.vars[name]; ok {
		v.Value = value
		v.Exported = v.Exported || exported
		return
	}
	e.vars[name] = &Var{Value: value, Exported: exported}
}

func (e *Env) SetExported(name, value string) {
	if v, ok := e.vars[name]; ok {
		v.Value = value
		v.Exported = true
		return
	}
	e.vars[name] = &Var{Value: value, Exported: true}
}

func (e *Env) Unset(name string) { delete(e.vars, name) }

func (e *Env) Lookup(name string) (string, bool) {
	switch name {
	case "?":
		return strconv.Itoa(e.prev), true
	case "#":
		return strconv.Itoa(len(e.args)), true
	case "0":
		return e.arg0, true
	case "@", "*":
		return strings.Join(e.args, " "), true
	case "$":
		return strconv.Itoa(e.pid), true
	case "!":
		return "", true
	case "-":
		return "", true
	case "PWD":
		return e.dir, true
	}
	if n, ok := numericName(name); ok {
		if n >= 1 && n <= len(e.args) {
			return e.args[n-1], true
		}
		return "", false
	}
	v, ok := e.vars[name]
	if !ok {
		return "", false
	}
	return v.Value, true
}

func numericName(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	n := 0
	for _, r := range name {
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

func (e *Env) Export(name string) {
	if v, ok := e.vars[name]; ok {
		v.Exported = true
		return
	}
	e.vars[name] = &Var{Exported: true}
}

func (e *Env) Names() []string {
	out := make([]string, 0, len(e.vars))
	for name := range e.vars {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (e *Env) Environ() []string {
	out := make([]string, 0, len(e.vars)+2)
	seen := map[string]bool{}
	for name, v := range e.vars {
		if !v.Exported || name == "IFS" {
			continue
		}
		out = append(out, name+"="+hostVar(name, v.Value))
		seen[name] = true
	}
	if !seen["PWD"] {
		out = append(out, "PWD="+hostVar("PWD", e.dir))
	}
	sort.Strings(out)
	return out
}

var pathVars = map[string]bool{"HOME": true, "PWD": true, "OLDPWD": true}

func hostVar(name, value string) string {
	switch {
	case name == "PATH":
		return hostPathList(value)
	case pathVars[name]:
		return plat.ToHost(plat.FromHost(value), plat.FromHost(value))
	}
	return value
}

func internalVar(name, value string) string {
	switch {
	case name == "PATH":
		return internalPathList(value)
	case pathVars[name]:
		return plat.FromHost(value)
	}
	return value
}

func internalPathList(value string) string {
	parts := strings.Split(value, plat.PathSep)
	for i, part := range parts {
		parts[i] = plat.FromHost(part)
	}
	return strings.Join(parts, ":")
}

func hostPathList(value string) string {
	parts := strings.Split(value, ":")
	for i, part := range parts {
		parts[i] = plat.ToHost("", part)
	}
	return strings.Join(parts, plat.PathSep)
}

func (e *Env) markLocal(name string) {
	for _, n := range e.locals {
		if n == name {
			return
		}
	}
	e.locals = append(e.locals, name)
}

func (e *Env) Clone() *Env {
	c := &Env{
		vars:   make(map[string]*Var, len(e.vars)),
		args:   append([]string(nil), e.args...),
		arg0:   e.arg0,
		dir:    e.dir,
		home:   e.home,
		prev:   e.prev,
		pid:    e.pid,
		inFunc: e.inFunc,
	}
	c.locals = append([]string(nil), e.locals...)
	for name, v := range e.vars {
		c.vars[name] = &Var{Value: v.Value, Exported: v.Exported}
	}
	return c
}

func (e *Env) Args() []string { return e.args }

func (e *Env) SetArgs(args []string) { e.args = args }

func (e *Env) Dir() string { return e.dir }

func (e *Env) Home() string { return e.home }

func (e *Env) Status() int { return e.prev }

func (e *Env) SetStatus(n int) { e.prev = n }

func (e *Env) Chdir(dir string) { e.dir = dir }

func (e *Env) IFS() string {
	v, ok := e.Lookup("IFS")
	if !ok {
		return " \t\n"
	}
	return v
}

func joinPath(dir, name string) string {
	if strings.HasPrefix(name, "/") {
		return path.Clean(name)
	}
	if dir == "" {
		dir = "/"
	}
	return path.Clean(dir + "/" + name)
}
