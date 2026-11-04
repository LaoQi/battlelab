package vbash

import (
	"bufio"
	"io"
	"os"
	"path"
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
		io.WriteString(out, text+"\n")
		return 0, nil
	}
	io.WriteString(out, text)
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
	out, err := formatPrintf(format, values)
	if err != nil {
		r.diagfName(io.Err, pos, "printf", "%s", err.Error())
		return 1, nil
	}
	io.WriteString(io.Out, out)
	return 0, nil
}

func formatPrintf(format string, values []string) (string, error) {
	var b strings.Builder
	vi := 0
	for {
		consumed := false
		i := 0
		for i < len(format) {
			c := format[i]
			if c == '\\' && i+1 < len(format) {
				next := format[i+1]
				switch next {
				case 'n':
					b.WriteByte('\n')
					i += 2
					continue
				case 't':
					b.WriteByte('\t')
					i += 2
					continue
				case 'r':
					b.WriteByte('\r')
					i += 2
					continue
				case '\\':
					b.WriteByte('\\')
					i += 2
					continue
				case '"':
					b.WriteByte('"')
					i += 2
					continue
				case 'a':
					b.WriteByte(7)
					i += 2
					continue
				case 'b':
					b.WriteByte(8)
					i += 2
					continue
				case 'f':
					b.WriteByte(12)
					i += 2
					continue
				case 'v':
					b.WriteByte(11)
					i += 2
					continue
				case 'e':
					b.WriteByte(0x1b)
					i += 2
					continue
				case '0', '1', '2', '3', '4', '5', '6', '7':
					j := i + 1
					val := 0
					for k := 0; k < 3 && j < len(format) && format[j] >= '0' && format[j] <= '7'; k++ {
						val = val*8 + int(format[j]-'0')
						j++
					}
					b.WriteByte(byte(val))
					i = j
					continue
				case 'x':
					j := i + 2
					val := 0
					n := 0
					for n < 2 && j < len(format) && isHexDigit(format[j]) {
						val = val*16 + hexVal(format[j])
						j++
						n++
					}
					if n == 0 {
						b.WriteByte('\\')
						b.WriteByte('x')
						i += 2
						continue
					}
					b.WriteByte(byte(val))
					i = j
					continue
				}
				b.WriteByte(c)
				i++
				continue
			}
			if c != '%' {
				b.WriteByte(c)
				i++
				continue
			}
			if i+1 < len(format) && format[i+1] == '%' {
				b.WriteByte('%')
				i += 2
				continue
			}
			end := scanVerb(format, i+1)
			if end < 0 {
				b.WriteByte(c)
				i++
				continue
			}
			verb := format[end]
			flags := format[i+1 : end]
			arg := ""
			if vi < len(values) {
				arg = values[vi]
				vi++
				consumed = true
			} else {
				arg = zeroValue(verb, flags)
			}
			b.WriteString(applyVerb(verb, flags, arg))
			i = end + 1
		}
		if vi >= len(values) || !consumed {
			break
		}
	}
	return b.String(), nil
}

func scanVerb(format string, start int) int {
	for i := start; i < len(format); i++ {
		c := format[i]
		switch c {
		case 's', 'd', 'i', 'u', 'o', 'x', 'X', 'c', 'f', 'e', 'g', 'b', 'q':
			return i
		case '-', '+', ' ', '#', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.', '\'':
			continue
		default:
			return -1
		}
	}
	return -1
}

func zeroValue(verb byte, flags string) string {
	switch verb {
	case 'd', 'i', 'u', 'o', 'x', 'X', 'f', 'e', 'g':
		return "0"
	}
	return ""
}

func applyVerb(verb byte, flags, arg string) string {
	switch verb {
	case 's':
		return padArg(flags, arg)
	case 'c':
		if arg == "" {
			return padArg(flags, "")
		}
		return padArg(flags, arg[:1])
	case 'b':
		return padArg(flags, decodeEchoEscapes(arg))
	case 'q':
		if arg == "" {
			return "''"
		}
		if strings.IndexFunc(arg, func(r rune) bool { return !isSafeChar(r) }) < 0 {
			return arg
		}
		return shellQuote(arg)
	case 'd', 'i', 'u', 'o', 'x', 'X':
		n, ok := parseSigned(arg)
		if !ok {
			return padArg(flags, "0")
		}
		return padNumber(flags, n, verb)
	case 'f', 'e', 'g':
		return padArg(flags, arg)
	}
	return arg
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

func isSafeChar(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == '/' || r == ':' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func parseSigned(arg string) (int64, bool) {
	if arg == "" {
		return 0, false
	}
	s := arg
	if s[0] == '\'' || s[0] == '"' {
		if len(s) > 1 {
			return int64(s[1]), true
		}
		return 0, false
	}
	neg := false
	i := 0
	if s[i] == '-' {
		neg = true
		i++
	} else if s[i] == '+' {
		i++
	}
	var n int64
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + int64(s[i]-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

func padNumber(flags string, n int64, verb byte) string {
	var text string
	switch verb {
	case 'o':
		text = itoa64(n, 8)
	case 'x':
		text = itoa64(n, 16)
	case 'X':
		text = strings.ToUpper(itoa64(n, 16))
	default:
		text = itoa64(n, 10)
	}
	return padArg(flags, text)
}

func itoa64(n int64, base int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := "0123456789abcdef"
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%int64(base)]}, b...)
		n /= int64(base)
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func padArg(flags, text string) string {
	left := false
	zero := false
	width := 0
	prec := -1
	i := 0
	for i < len(flags) {
		switch flags[i] {
		case '-':
			left = true
		case '0':
			zero = true
		case '.':
			i++
			prec = 0
			for i < len(flags) && flags[i] >= '0' && flags[i] <= '9' {
				prec = prec*10 + int(flags[i]-'0')
				i++
			}
			continue
		default:
			if flags[i] >= '0' && flags[i] <= '9' {
				width = width*10 + int(flags[i]-'0')
			}
		}
		i++
	}
	if prec >= 0 {
		r := []rune(text)
		if len(r) > prec {
			text = string(r[:prec])
		}
	}
	fill := " "
	if zero && !left {
		fill = "0"
	}
	for len([]rune(text)) < width {
		if left {
			text += fill
			continue
		}
		text = fill + text
	}
	return text
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
		if !strings.HasPrefix(target, "/") {
			target = r.Env.Dir() + "/" + target
		}
		newDir = path.Clean(target)
	}
	if physical {
		if resolved, err := resolveSymlinks(newDir); err == nil {
			newDir = resolved
		}
	}
	if args != nil && len(args) > 0 && args[0] == "-" {
		shown = newDir
	}
	st, err := os.Stat(newDir)
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

func resolveSymlinks(p string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	cur := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = path.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil {
			return p, err
		}
		if st.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(cur)
		if err != nil {
			return p, err
		}
		if strings.HasPrefix(target, "/") {
			cur = target
			continue
		}
		cur = path.Join(path.Dir(cur), target)
	}
	return path.Clean(cur), nil
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
		if resolved, err := resolveSymlinks(dir); err == nil {
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
