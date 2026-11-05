package vbash

import (
	"bufio"
	"io"
	"os"
	"strings"
)

type builtinFunc func(r *Runner, argv []string, io *IO, pos Pos) (int, error)

var builtins = map[string]builtinFunc{
	":":        builtinColon,
	"true":     builtinTrue,
	"false":    builtinFalse,
	"echo":     builtinEcho,
	"printf":   builtinPrintf,
	"cd":       builtinCd,
	"pwd":      builtinPwd,
	"export":   builtinExport,
	"unset":    builtinUnset,
	"set":      builtinSet,
	"shift":    builtinShift,
	"exit":     builtinExit,
	"return":   builtinReturn,
	"read":     builtinRead,
	"wait":     builtinWait,
	"local":    builtinLocal,
	"break":    builtinBreak,
	"continue": builtinContinue,
	"test":     builtinTest,
	"[":        builtinTest,
}

func canBuiltin(name string) bool {
	_, ok := builtins[name]
	return ok
}

func (r *Runner) runBuiltin(name string, argv []string, io *IO, pos Pos) (int, bool, error) {
	fn, ok := builtins[name]
	if !ok {
		return 0, false, nil
	}
	code, err := fn(r, argv, io, pos)
	return code, true, err
}

func builtinLocal(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	if !r.Env.inFunc {
		r.diagfName(io.Err, pos, "local", "can only be used in a function")
		return 1, nil
	}
	code := 0
	for _, a := range argv[1:] {
		if a == "--" || strings.HasPrefix(a, "-") {
			continue
		}
		name, value := a, ""
		if i := strings.IndexByte(a, '='); i > 0 {
			name, value = a[:i], a[i+1:]
		}
		if !validAssignName(name) {
			r.diagfName(io.Err, pos, "local", "%s: not a valid identifier", "`"+a+"'")
			code = 1
			continue
		}
		r.Env.markLocal(name)
		r.Env.Set(name, value, false)
	}
	return code, nil
}

func builtinBreak(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	n := loopDepthArg(argv)
	return 0, &loopControl{signal: loopBreak, depth: n}
}

func builtinContinue(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	n := loopDepthArg(argv)
	return 0, &loopControl{signal: loopContinue, depth: n}
}

func loopDepthArg(argv []string) int {
	if len(argv) > 1 {
		if n, ok := parseInt(argv[1]); ok && n > 0 {
			return n
		}
	}
	return 1
}

func builtinWait(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	return 0, nil
}

func builtinColon(r *Runner, argv []string, io *IO, pos Pos) (int, error) { return 0, nil }

func builtinTrue(r *Runner, argv []string, io *IO, pos Pos) (int, error) { return 0, nil }

func builtinFalse(r *Runner, argv []string, io *IO, pos Pos) (int, error) { return 1, nil }

func builtinEcho(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	newline := true
	interpret := false
	for len(args) > 0 {
		arg := args[0]
		if len(arg) < 2 || arg[0] != '-' {
			break
		}
		valid := true
		for _, c := range arg[1:] {
			if c != 'n' && c != 'e' && c != 'E' {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		for _, c := range arg[1:] {
			switch c {
			case 'n':
				newline = false
			case 'e':
				interpret = true
			case 'E':
				interpret = false
			}
		}
		args = args[1:]
	}
	text := strings.Join(args, " ")
	if interpret {
		text = decodeEchoEscapes(text)
	}
	out := io.Out
	if newline {
		text += "\n"
	}
	if err := io.WriteString(out, text); err != nil {
		r.diagfName(io.Err, pos, "echo", "write error: %s", err.Error())
		return 1, nil
	}
	return 0, nil
}

func decodeEchoEscapes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'f':
			b.WriteByte(12)
		case 'v':
			b.WriteByte(11)
		case '\\':
			b.WriteByte('\\')
		case 'e', 'E':
			b.WriteByte(0x1b)
		case 'c':
			return b.String()
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func builtinPrintf(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	if len(args) == 0 {
		r.diagfName(io.Err, pos, "printf", "usage: printf format [arguments]")
		return 2, nil
	}
	format := args[0]
	values := args[1:]
	out, warn := formatPrintf(format, values)
	if werr := io.WriteString(io.Out, out); werr != nil {
		r.diagfName(io.Err, pos, "printf", "write error: %s", werr.Error())
		return 1, nil
	}
	if warn != "" {
		r.diagfName(io.Err, pos, "printf", "%s", warn)
		return 1, nil
	}
	return 0, nil
}

func builtinCd(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	target := ""
	physical := false
	for len(args) > 0 {
		if args[0] == "-P" {
			physical = true
			args = args[1:]
			continue
		}
		if args[0] == "-L" {
			physical = false
			args = args[1:]
			continue
		}
		break
	}
	if len(args) > 0 {
		target = args[0]
	} else {
		target = r.Env.Home()
	}
	shown := target
	var newDir string
	switch target {
	case "-":
		old, ok := r.Env.Lookup("OLDPWD")
		if !ok || old == "" {
			r.diagfName(io.Err, pos, "cd", "OLDPWD not set")
			return 1, nil
		}
		newDir = old
		io.WriteString(io.Out, r.Env.Dir()+"\n")
	default:
		newDir = r.posixPath(target)
	}
	if physical {
		if resolved, err := resolveSymlinks(r.Env.Dir(), newDir); err == nil {
			newDir = resolved
		}
	}
	if args != nil && len(args) > 0 && args[0] == "-" {
		shown = newDir
	}
	st, err := os.Stat(plat.ToHost(r.Env.Dir(), newDir))
	if err != nil {
		r.diagfName(io.Err, pos, "cd", "%s: %s", shown, errText(err))
		return 1, nil
	}
	if !st.IsDir() {
		r.diagfName(io.Err, pos, "cd", "%s: Not a directory", shown)
		return 1, nil
	}
	old := r.Env.Dir()
	r.Env.Chdir(newDir)
	r.Env.SetExported("PWD", newDir)
	r.Env.SetExported("OLDPWD", old)
	return 0, nil
}

func builtinPwd(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	physical := false
	for _, a := range argv[1:] {
		if a == "-P" {
			physical = true
		}
	}
	dir := r.Env.Dir()
	if physical {
		if resolved, err := resolveSymlinks(dir, dir); err == nil {
			dir = resolved
		}
	}
	io.WriteString(io.Out, dir+"\n")
	return 0, nil
}

func builtinExport(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	if len(args) == 0 {
		for _, name := range r.Env.Names() {
			v, _ := r.Env.Lookup(name)
			io.WriteString(io.Out, "declare -x "+name+"=\""+v+"\"\n")
		}
		return 0, nil
	}
	for _, a := range args {
		if a == "-p" {
			continue
		}
		if i := strings.IndexByte(a, '='); i > 0 {
			r.Env.SetExported(a[:i], a[i+1:])
			continue
		}
		r.Env.Export(a)
	}
	return 0, nil
}

func builtinUnset(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	for _, a := range argv[1:] {
		if a == "-v" || a == "-f" {
			continue
		}
		r.Env.Unset(a)
	}
	return 0, nil
}

func builtinSet(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	if len(args) == 0 {
		for _, name := range r.Env.Names() {
			v, _ := r.Env.Lookup(name)
			io.WriteString(io.Out, name+"="+v+"\n")
		}
		return 0, nil
	}
	dashCount := 0
	for _, a := range args {
		if a == "--" {
			dashCount++
			break
		}
		if len(a) > 1 && (a[0] == '-' || a[0] == '+') && a[1] == '-' {
			continue
		}
		if len(a) > 1 && a[0] == '-' {
			if strings.ContainsAny(a, "e") {
				r.ErrExit = true
			}
			dashCount++
			continue
		}
		if len(a) > 1 && a[0] == '+' {
			dashCount++
			continue
		}
		break
	}
	rest := args
	if dashCount > 0 {
		rest = args[dashCount:]
	}
	if dashCount > 0 && len(rest) == 0 {
		r.Env.SetArgs(nil)
		return 0, nil
	}
	r.Env.SetArgs(rest)
	return 0, nil
}

func builtinShift(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	n := 1
	if len(argv) > 1 {
		if v, ok := parseInt(argv[1]); ok {
			n = v
		}
	}
	args := r.Env.Args()
	if n > len(args) {
		return 1, nil
	}
	r.Env.SetArgs(args[n:])
	return 0, nil
}

func builtinExit(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	code := r.Env.Status()
	if len(argv) > 1 {
		if v, ok := parseInt(argv[1]); ok {
			code = v & 0xff
		}
	}
	return code, ExitRequest{Code: code}
}

func builtinReturn(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	code := r.Env.Status()
	if len(argv) > 1 {
		if v, ok := parseInt(argv[1]); ok {
			code = v & 0xff
		}
	}
	if !r.Env.inFunc {
		r.diagfName(io.Err, pos, "return", "can only `return' from a function or sourced script")
	}
	return code, ReturnRequest{Code: code}
}

func builtinRead(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	raw := false
	names := []string{}
	for _, a := range args {
		if a == "-r" {
			raw = true
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			continue
		}
		names = append(names, a)
	}
	if len(names) == 0 {
		names = []string{"REPLY"}
	}
	if io.closed(0) {
		r.diagfName(io.Err, pos, "read", "0: Bad file descriptor")
		for _, name := range names {
			r.Env.Set(name, "", false)
		}
		return 1, nil
	}
	line, err := readLine(io.bufferedInput())
	if err != nil && line == "" {
		for _, name := range names {
			r.Env.Set(name, "", false)
		}
		return 1, nil
	}
	if !raw {
		line = strings.ReplaceAll(line, "\\", "")
	}
	parts := splitIFS(line, r.Env.IFS())
	for i, name := range names {
		if i == len(names)-1 {
			if i < len(parts) {
				r.Env.Set(name, strings.Join(parts[i:], " "), false)
			} else {
				r.Env.Set(name, "", false)
			}
			break
		}
		if i < len(parts) {
			r.Env.Set(name, parts[i], false)
		} else {
			r.Env.Set(name, "", false)
		}
	}
	return 0, nil
}

func readLine(rd io.Reader) (string, error) {
	if rd == nil {
		return "", io.EOF
	}
	br, ok := rd.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(rd)
	}
	line, err := br.ReadString('\n')
	return strings.TrimSuffix(line, "\n"), err
}
