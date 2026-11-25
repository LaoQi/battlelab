package vbash

import (
	"os"
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
	vars    map[string]*Var
	args    []string
	arg0    string
	dir     string
	home    string
	prev    int
	pid     int
	inFunc  bool
	jobs    *jobTable
	locals  []string
	NounSet bool
}

func NewEnv() *Env {
	e := &Env{vars: map[string]*Var{}, pid: os.Getpid()}
	e.Set("IFS", " \t\n", false)
	return e
}

func (e *Env) Set(name, value string, exported bool) {
	value = internalVar(name, value)
	if v, ok := e.vars[name]; ok {
		v.Value = value
		v.Exported = v.Exported || exported
		return
	}
	e.vars[name] = &Var{Value: value, Exported: exported}
}

func (e *Env) SetExported(name, value string) {
	value = internalVar(name, value)
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
		if e.jobs != nil {
			if pid, ok := e.jobs.lastPid(); ok {
				return strconv.Itoa(pid), true
			}
		}
		return "", false
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

// LookupDisplay 返回脚本可见的形态：HOME/PWD/OLDPWD 渲染成宿主形态（Windows），
// 其余与 Lookup 相同。内部消费者（cd -、PATH 查找、Env.Home/Dir）必须用 Lookup/
// 字段本身，否则宿主形态会写进内部状态。PATH 不在此列：$PATH 暂保持内部形态。
func (e *Env) LookupDisplay(name string) (string, bool) {
	v, ok := e.Lookup(name)
	if !ok {
		return "", false
	}
	if pathVars[name] {
		return DisplayPath(v), true
	}
	return v, true
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
		if name == "PATH" {
			out = append(out, "PATH="+strings.Join(ParsePathList(v.Value).Host(), plat.PathSep))
			seen[name] = true
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

// pathVars 是需要做边界形态转换的路径类变量：内部形态（/c/...）与宿主形态
// （C:\...）在注入/导出/展开时各取所需。PATH 不在其中（数组化后单独处理）。
var pathVars = map[string]bool{
	"HOME": true, "PWD": true, "OLDPWD": true,
	"TMP": true, "TEMP": true, "APPDATA": true,
}

func hostVar(name, value string) string {
	switch {
	case pathVars[name]:
		return plat.ToHost(plat.FromHost(value), plat.FromHost(value))
	}
	return value
}

func internalVar(name, value string) string {
	switch {
	case name == "PATH":
		return ParsePathList(value).String()
	case pathVars[name]:
		return plat.FromHost(value)
	}
	return value
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
		vars:    make(map[string]*Var, len(e.vars)),
		args:    append([]string(nil), e.args...),
		arg0:    e.arg0,
		dir:     e.dir,
		home:    e.home,
		prev:    e.prev,
		pid:     e.pid,
		inFunc:  e.inFunc,
		jobs:    e.jobs,
		NounSet: e.NounSet,
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

func (e *Env) JoinArgs() string {
	sep := " "
	if v, ok := e.Lookup("IFS"); ok {
		if v == "" {
			sep = ""
		} else {
			sep = v[:1]
		}
	}
	return strings.Join(e.args, sep)
}

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

type prefixSave struct {
	name     string
	value    string
	exported bool
	existed  bool
}

func savePrefixVars(e *Env, assigns []Assign) []prefixSave {
	saved := make([]prefixSave, 0, len(assigns))
	seen := make(map[string]bool, len(assigns))
	for _, a := range assigns {
		if seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		ps := prefixSave{name: a.Name}
		if v, ok := e.vars[a.Name]; ok {
			ps.value, ps.exported, ps.existed = v.Value, v.Exported, true
		}
		saved = append(saved, ps)
	}
	return saved
}

func restorePrefixVars(e *Env, saved []prefixSave) {
	for _, ps := range saved {
		if !ps.existed {
			delete(e.vars, ps.name)
			continue
		}
		e.vars[ps.name] = &Var{Value: ps.value, Exported: ps.exported}
	}
}
