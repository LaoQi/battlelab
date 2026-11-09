package vbash

import (
	"bufio"
	"fmt"
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
	"kill":     builtinKill,
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
	if r.loopDepth() == 0 {
		return r.outsideLoop("break", pos)
	}
	n := loopDepthArg(argv)
	return 0, &loopControl{signal: loopBreak, depth: n}
}

func builtinContinue(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	if r.loopDepth() == 0 {
		return r.outsideLoop("continue", pos)
	}
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
	args := argv[1:]
	r.initJobs()
	if len(args) == 0 {
		for _, j := range r.jobs.pending() {
			<-j.done
		}
		return 0, nil
	}
	status := 0
	for _, a := range args {
		j := waitTarget(r, a)
		if j == nil {
			pid, ok := parseDecimalInt(a)
			_ = pid
			if ok {
				r.diagfName(io.Err, pos, "wait", "pid %s is not a child of this shell", a)
			} else {
				r.diagfName(io.Err, pos, "wait", "%s: not a pid or valid job spec", a)
			}
			status = 127
			continue
		}
		<-j.done
		code, _ := j.status()
		status = code
		r.jobs.consume(j)
	}
	return status, nil
}

func waitTarget(r *Runner, arg string) *job {
	pid, ok := parseDecimalInt(arg)
	if !ok {
		return nil
	}
	return r.jobs.byPid(pid)
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
		decoded, stop := decodeEchoEscapes(text)
		text = decoded
		if stop {
			newline = false
		}
	}
	if newline {
		text += "\n"
	}
	if err := io.WriteString(io.Out, text); err != nil {
		r.diagfName(io.Err, pos, "echo", "write error: %s", err.Error())
		return 1, nil
	}
	return 0, nil
}

func decodeEchoEscapes(s string) (string, bool) {
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
			return b.String(), true
		case '0':
			n, used := readOctalDigits(s[i+1:], 3)
			b.WriteByte(byte(n))
			i += used
		case 'x':
			n, used := readHexDigits(s[i+1:], 2)
			if used == 0 {
				b.WriteString("\\x")
				continue
			}
			b.WriteByte(byte(n))
			i += used
		case 'u':
			n, used := readHexDigits(s[i+1:], 4)
			if used == 0 {
				b.WriteString("\\u")
				continue
			}
			b.WriteRune(rune(n))
			i += used
		case 'U':
			n, used := readHexDigits(s[i+1:], 8)
			if used == 0 {
				b.WriteString("\\U")
				continue
			}
			b.WriteRune(rune(n))
			i += used
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), false
}

func readOctalDigits(s string, max int) (int, int) {
	n, used := 0, 0
	for used < max && used < len(s) && s[used] >= '0' && s[used] <= '7' {
		n = n*8 + int(s[used]-'0')
		used++
	}
	return n, used
}

func readHexDigits(s string, max int) (int, int) {
	n, used := 0, 0
	for used < max && used < len(s) {
		c := s[used]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return n, used
		}
		n = n*16 + d
		used++
	}
	return n, used
}

func builtinPrintf(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	varName, hasVar := "", false
	if len(args) >= 2 && args[0] == "-v" {
		varName, hasVar = args[1], true
		args = args[2:]
	} else if len(args) >= 1 && args[0] == "-v" {
		r.diagfName(io.Err, pos, "printf", "usage: printf [-v var] format [arguments]")
		return 2, nil
	}
	if len(args) == 0 {
		r.diagfName(io.Err, pos, "printf", "usage: printf [-v var] format [arguments]")
		return 2, nil
	}
	format := args[0]
	values := args[1:]
	out, warn := formatPrintf(format, values)
	if hasVar {
		if !validAssignName(varName) {
			r.diagfName(io.Err, pos, "printf", "%s: not a valid identifier", "`"+varName+"'")
			return 1, nil
		}
		if warn != "" {
			r.diagfName(io.Err, pos, "printf", "%s", warn)
			return 1, nil
		}
		r.Env.Set(varName, out, false)
		return 0, nil
	}
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
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			r.Env.SetArgs(append([]string(nil), args[i+1:]...))
			return 0, nil
		}
		if a == "-" {
			i++
			continue
		}
		if len(a) < 2 || (a[0] != '-' && a[0] != '+') || a[1] == '-' {
			break
		}
		on := a[0] == '-'
		i++
		for _, c := range a[1:] {
			if c == 'o' {
				if i < len(args) && !isOptionWord(args[i]) {
					if err := r.setOption(args[i], on, io, pos); err != nil {
						return 2, nil
					}
					i++
					continue
				}
				r.printOptions(io, on)
				continue
			}
			if err := r.setOption(string(c), on, io, pos); err != nil {
				return 2, nil
			}
		}
	}
	if i < len(args) {
		r.Env.SetArgs(append([]string(nil), args[i:]...))
	}
	return 0, nil
}

func isOptionWord(s string) bool {
	if len(s) > 1 && (s[0] == '-' || s[0] == '+') {
		return true
	}
	return false
}

func (r *Runner) setOption(name string, on bool, io *IO, pos Pos) error {
	switch name {
	case "e", "errexit":
		r.ErrExit = on
	case "u", "nounset":
		r.Env.NounSet = on
	case "x", "xtrace":
		r.XTrace = on
	case "pipefail":
		r.PipeFail = on
	default:
		if !knownOption(name) {
			r.diagfName(io.Err, pos, "set", "%s: invalid option name", name)
			return runtimeErrf(pos, "invalid option")
		}
	}
	return nil
}

func knownOption(name string) bool {
	switch name {
	case "a", "allexport", "b", "notify", "f", "noglob", "h", "hashall",
		"m", "monitor", "n", "noexec", "v", "verbose", "C", "noclobber",
		"E", "errtrace", "T", "functrace", "H", "histexpand", "B", "braceexpand",
		"P", "physical", "t", "onecmd", "k", "keyword", "p", "privileged",
		"i", "interactive", "s", "stdin", "l", "login", "c", "N":
		return true
	}
	return false
}

func (r *Runner) printOptions(io *IO, on bool) {
	if !on {
		return
	}
	for _, opt := range []struct {
		name string
		on   bool
	}{
		{"errexit", r.ErrExit},
		{"nounset", r.Env.NounSet},
		{"pipefail", r.PipeFail},
		{"xtrace", r.XTrace},
	} {
		state := "off"
		if opt.on {
			state = "on"
		}
		io.WriteString(io.Out, fmt.Sprintf("%-15s\t%s\n", opt.name, state))
	}
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

type statusKind int

const (
	statusOK statusKind = iota
	statusNumeric
	statusTooMany
)

func builtinExit(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	code, kind := statusArg(r, argv, io, pos, "exit")
	switch kind {
	case statusTooMany:
		return 1, ExitRequest{Code: 1}
	case statusNumeric:
		return 2, nil
	}
	return code, ExitRequest{Code: code}
}

func builtinReturn(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	code, kind := statusArg(r, argv, io, pos, "return")
	if kind == statusTooMany {
		return 1, ExitRequest{Code: 1}
	}
	if !r.Env.inFunc {
		r.diagfName(io.Err, pos, "return", "can only `return' from a function or sourced script")
		return 2, nil
	}
	if kind == statusNumeric {
		return 2, ReturnRequest{Code: 2}
	}
	return code, ReturnRequest{Code: code}
}

func statusArg(r *Runner, argv []string, io *IO, pos Pos, name string) (int, statusKind) {
	code := r.Env.Status()
	if len(argv) < 2 {
		return code, statusOK
	}
	if len(argv) > 2 {
		r.diagfName(io.Err, pos, name, "too many arguments")
		return 1, statusTooMany
	}
	v, ok := parseDecimalInt(argv[1])
	if !ok {
		r.diagfName(io.Err, pos, name, "%s: numeric argument required", argv[1])
		return 2, statusNumeric
	}
	return v & 0xff, statusOK
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
	line, err := readLine(io.input())
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

// readLine 读一行，且**不预读** fd 0 之后的字节（与 bash 的 read 一致）：
// `read x; head -1` 里的 head 必须还能读到下一行，所以这里按字节读到底层 reader，
// 绝不套 bufio（预读会把后续命令的输入吞进缓冲区）。
func readLine(rd io.Reader) (string, error) {
	if rd == nil {
		return "", io.EOF
	}
	if br, ok := rd.(*bufio.Reader); ok {
		line, err := br.ReadString('\n')
		return strings.TrimSuffix(line, "\n"), err
	}
	var b strings.Builder
	var one [1]byte
	for {
		n, err := rd.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(one[0])
		}
		if err != nil {
			if err == io.EOF && b.Len() > 0 {
				// 与 bash 一致：EOF 前有半行数据时照常赋值，read 返回 1。
				return b.String(), io.EOF
			}
			return b.String(), err
		}
	}
}
