package vbash

import (
	"strings"
)

type expansion struct {
	env   *Env
	runs  *Runner
	io    *IO
	depth int
}

func (x *expansion) commandSubstitution(seg Segment) (string, error) {
	return x.runs.commandSubstitution(seg)
}

type fieldState struct {
	parts  []string
	quoted bool
	flush  func(bool)
	emit   func(string)
}

func (s *fieldState) text() string { return strings.Join(s.parts, "") }

func (s *fieldState) put(text string, quoted bool) {
	if quoted {
		s.quoted = true
		s.parts = append(s.parts, text)
		return
	}
	if text == "" {
		return
	}
	s.quoted = true
	s.parts = append(s.parts, text)
}

func (s *fieldState) putSplit(value, ifs string) {
	parts := splitIFS(value, ifs)
	if len(parts) == 0 {
		return
	}
	s.quoted = true
	s.parts = append(s.parts, parts[0])
	for _, p := range parts[1:] {
		s.flush(false)
		s.parts = append(s.parts, p)
		s.quoted = true
	}
}

func (x *expansion) expandWords(list []Word) ([]string, error) {
	var out []string
	lim := &braceLimits{}
	for _, w := range list {
		words, err := lim.word(w)
		if err != nil {
			return nil, err
		}
		for _, bw := range words {
			fs, err := x.expand(bw)
			if err != nil {
				return nil, err
			}
			out = append(out, fs...)
		}
	}
	return out, nil
}

func (x *expansion) expand(w Word) ([]string, error) {
	if len(w.Segments) == 0 {
		if w.Quoted() {
			return []string{""}, nil
		}
		return nil, nil
	}
	segs := x.applyTilde(w)
	var out []string
	st := &fieldState{}
	st.emit = func(s string) { out = append(out, s) }
	st.flush = func(force bool) {
		if len(st.parts) > 0 || force || st.quoted {
			out = append(out, st.text())
		}
		st.parts = nil
		st.quoted = false
	}
	for _, seg := range segs {
		if err := x.applySegment(st, seg); err != nil {
			return nil, err
		}
	}
	st.flush(false)
	if !x.needsGlob(segs) {
		return out, nil
	}
	var globbed []string
	for _, name := range out {
		if hasGlobMeta(name) {
			globbed = append(globbed, x.runs.globExpand(name)...)
			continue
		}
		globbed = append(globbed, name)
	}
	return globbed, nil
}

func (x *expansion) applySegment(st *fieldState, seg Segment) error {
	switch seg.Kind {
	case SegLiteral:
		st.put(seg.Text, seg.Mode != ModeUnquoted)
		return nil
	case SegVar, SegBraced:
		if seg.Inner == "@" && seg.Mode != ModeUnquoted {
			args := x.env.Args()
			if len(args) == 0 {
				return nil
			}
			st.put(args[0], true)
			for _, a := range args[1:] {
				st.flush(false)
				st.put(a, true)
			}
			return nil
		}
		if seg.Inner == "*" && seg.Mode != ModeUnquoted {
			st.put(x.env.JoinArgs(), true)
			return nil
		}
		if seg.Inner == "*" {
			st.putSplit(x.env.JoinArgs(), x.env.IFS())
			return nil
		}
		if x.env.NounSet && seg.Kind == SegVar {
			if _, set := x.env.Lookup(seg.Inner); !set {
				if name, ok := expandUnboundName(seg); ok {
					return x.runs.unboundAt(name, seg.Pos)
				}
			}
		}
		value, err := x.parameter(seg)
		if err != nil {
			return err
		}
		if seg.Mode != ModeUnquoted {
			st.put(value, true)
			return nil
		}
		st.putSplit(value, x.env.IFS())
		return nil
	case SegArith:
		v, err := x.arith(seg.Inner)
		if err != nil {
			return err
		}
		st.put(itoa(v), seg.Mode != ModeUnquoted)
		return nil
	case SegCmdSub:
		text, err := x.commandSubstitution(seg)
		if err != nil {
			return err
		}
		if seg.Mode != ModeUnquoted {
			st.put(text, true)
			return nil
		}
		st.putSplit(text, x.env.IFS())
		return nil
	}
	return nil
}

func (x *expansion) needsGlob(segs []Segment) bool {
	for _, seg := range segs {
		if seg.Kind == SegLiteral && seg.Mode == ModeUnquoted && hasGlobMeta(seg.Text) {
			return true
		}
	}
	return false
}

func (x *expansion) applyTilde(w Word) []Segment {
	segs := w.Segments
	if len(segs) == 0 {
		return segs
	}
	first := segs[0]
	if first.Kind != SegLiteral || first.Mode != ModeUnquoted || !strings.HasPrefix(first.Text, "~") {
		return segs
	}
	text := first.Text
	end := strings.IndexAny(text, "/")
	var prefix, rest string
	if end < 0 {
		prefix, rest = text, ""
	} else {
		prefix, rest = text[:end], text[end:]
	}
	home := x.tildeHome(prefix)
	if home == "" {
		return segs
	}
	out := []Segment{{Kind: SegLiteral, Text: home, Mode: ModeLiteral, Pos: first.Pos}}
	if rest != "" {
		out = append(out, Segment{Kind: SegLiteral, Text: rest, Mode: ModeUnquoted, Pos: first.Pos})
	}
	return append(out, segs[1:]...)
}

func (x *expansion) tildeHome(prefix string) string {
	if prefix == "~" {
		return x.env.Home()
	}
	switch prefix {
	case "~+":
		return x.env.Dir()
	}
	return ""
}

func splitIFS(s, ifs string) []string {
	if ifs == "" {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	started := false
	for i := 0; i < len(s); {
		c := s[i]
		if strings.IndexByte(ifs, c) < 0 {
			cur.WriteByte(c)
			started = true
			i++
			continue
		}
		ws := isIFSWhite(c)
		j := i
		if ws {
			for j < len(s) && isIFSWhite(s[j]) && strings.IndexByte(ifs, s[j]) >= 0 {
				j++
			}
			if j < len(s) && strings.IndexByte(ifs, s[j]) >= 0 && !isIFSWhite(s[j]) {
				j++
			}
		} else {
			j++
		}
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
		_ = ws
		i = j
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

func isIFSWhite(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }

func (x *expansion) parameter(seg Segment) (string, error) {
	if seg.Kind == SegVar {
		if seg.Inner == "@" || seg.Inner == "*" {
			return strings.Join(x.env.Args(), " "), nil
		}
		value, _ := x.env.Lookup(seg.Inner)
		return value, nil
	}
	return x.braced(seg.Inner, seg.Mode)
}

type bracedOp struct {
	name string
	op   string
	arg  string
}

func (x *expansion) parseBraced(expr string) (bracedOp, error) {
	if expr == "" {
		return bracedOp{}, runtimeErrf(Pos{}, "bad substitution: ${}")
	}
	if strings.HasPrefix(expr, "#") && len(expr) > 1 {
		rest := expr[1:]
		if !strings.HasPrefix(rest, "#") && isBracedPlainName(rest) {
			return bracedOp{name: rest, op: "#len"}, nil
		}
	}
	if strings.HasPrefix(expr, "!") && len(expr) > 1 && isBracedPlainName(expr[1:]) {
		return bracedOp{name: expr[1:], op: "!"}, nil
	}
	if name, op, arg, ok := splitBracedSuffix(expr); ok {
		return bracedOp{name: name, op: op, arg: arg}, nil
	}
	return bracedOp{}, runtimeErrf(Pos{}, "%s: bad substitution", expr)
}

func isBracedPlainName(expr string) bool {
	if expr == "" {
		return false
	}
	if isBracedSpecialName(expr) {
		return true
	}
	for i := 0; i < len(expr); i++ {
		if !isBracedNameRune(expr[i]) {
			return false
		}
	}
	return true
}

func splitBracedSuffix(expr string) (string, string, string, bool) {
	start := 0
	if !isBracedNameRune(expr[0]) {
		if isBracedSpecialName(expr) {
			return expr, "", "", true
		}
		return "", "", "", false
	}
	i := 0
	for i < len(expr) && isBracedNameRune(expr[i]) {
		i++
	}
	start = i
	if start == len(expr) {
		return expr, "", "", true
	}
	rest := expr[start:]
	for _, op := range []string{":-", ":=", ":?", ":+", "##", "%%", "//", "/#", "^^", ",,"} {
		if strings.HasPrefix(rest, op) {
			return expr[:start], op, rest[len(op):], true
		}
	}
	switch rest[0] {
	case '-', '=', '?', '+', '#', '%', '/', '^', ',':
		return expr[:start], rest[:1], rest[1:], true
	case ':':
		return expr[:start], ":", rest[1:], true
	}
	return "", "", "", false
}

func isBracedNameRune(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isBracedSpecialName(expr string) bool {
	if expr == "" {
		return false
	}
	switch expr {
	case "@", "*", "?", "$", "!", "#", "-":
		return true
	}
	_, ok := numericName(expr)
	return ok
}

func (x *expansion) braced(expr string, mode WordMode) (string, error) {
	spec, err := x.parseBraced(expr)
	if err != nil {
		return "", err
	}
	if spec.name == "@" || spec.name == "*" {
		if spec.op == "#len" {
			return itoa(len(x.env.Args())), nil
		}
		return strings.Join(x.env.Args(), " "), nil
	}
	value, set := x.env.Lookup(spec.name)
	switch spec.op {
	case "":
		return value, nil
	case "#len":
		return itoa(len([]rune(value))), nil
	case ":":
		return x.substringOp(value, spec.arg)
	case "^^":
		return x.caseOp(value, spec.arg, true, false), nil
	case ",,":
		return x.caseOp(value, spec.arg, false, false), nil
	case "^":
		return x.caseOp(value, spec.arg, true, true), nil
	case ",":
		return x.caseOp(value, spec.arg, false, true), nil
	case "!":
		if !set {
			return "", runtimeErrf(Pos{}, "%s: invalid indirect expansion", spec.name)
		}
		target, ok := x.env.Lookup(value)
		if !ok {
			return "", runtimeErrf(Pos{}, "%s: invalid indirect expansion", value)
		}
		return target, nil
	case ":-":
		if !set || value == "" {
			return x.expandArbitrary(spec.arg)
		}
		return value, nil
	case "-":
		if !set {
			return x.expandArbitrary(spec.arg)
		}
		return value, nil
	case ":=":
		if !set || value == "" {
			v, err := x.expandArbitrary(spec.arg)
			if err != nil {
				return "", err
			}
			x.env.Set(spec.name, v, false)
			return v, nil
		}
		return value, nil
	case "=":
		if !set {
			v, err := x.expandArbitrary(spec.arg)
			if err != nil {
				return "", err
			}
			x.env.Set(spec.name, v, false)
			return v, nil
		}
		return value, nil
	case ":?", "?":
		bad := !set || (spec.op == ":?" && value == "")
		if bad {
			msg, err := x.expandArbitrary(spec.arg)
			if err != nil {
				return "", err
			}
			if msg == "" {
				msg = "parameter null or not set"
			}
			return "", runtimeErrf(Pos{}, "%s: %s", spec.name, msg)
		}
		return value, nil
	case ":+":
		if set && value != "" {
			return x.expandArbitrary(spec.arg)
		}
		return "", nil
	case "+":
		if set {
			return x.expandArbitrary(spec.arg)
		}
		return "", nil
	case "#":
		pat, err := x.expandArbitrary(spec.arg)
		if err != nil {
			return "", err
		}
		return trimPattern(value, pat, 1)
	case "##":
		pat, err := x.expandArbitrary(spec.arg)
		if err != nil {
			return "", err
		}
		return trimPattern(value, pat, 2)
	case "%":
		pat, err := x.expandArbitrary(spec.arg)
		if err != nil {
			return "", err
		}
		return trimPattern(value, pat, 3)
	case "%%":
		pat, err := x.expandArbitrary(spec.arg)
		if err != nil {
			return "", err
		}
		return trimPattern(value, pat, 4)
	case "/":
		pat, err := x.expandArbitrary(spec.arg)
		if err != nil {
			return "", err
		}
		return replacePattern(value, pat, false)
	case "//":
		pat, err := x.expandArbitrary(spec.arg)
		if err != nil {
			return "", err
		}
		return replacePattern(value, pat, true)
	case "/#":
		pat, err := x.expandArbitrary(spec.arg)
		if err != nil {
			return "", err
		}
		return replacePrefixPattern(value, pat)
	}
	return "", runtimeErrf(Pos{}, "%s: bad substitution", expr)
}

func trimPattern(value, spec string, kind int) (string, error) {
	pattern, repl := spec, ""
	set := -1
	switch kind {
	case 1, 2:
		for k := 0; k <= len(value); k++ {
			ok, err := patternMatches(pattern, value[:k])
			if err != nil {
				return "", err
			}
			if ok {
				set = k
				if kind == 1 {
					return repl + value[k:], nil
				}
			}
		}
	case 3, 4:
		for k := len(value); k >= 0; k-- {
			ok, err := patternMatches(pattern, value[k:])
			if err != nil {
				return "", err
			}
			if ok {
				set = k
				if kind == 3 {
					return value[:k] + repl, nil
				}
			}
		}
	}
	if set < 0 {
		return value, nil
	}
	if kind == 2 {
		return repl + value[set:], nil
	}
	return value[:set] + repl, nil
}

func replacePattern(value, spec string, all bool) (string, error) {
	pattern, repl := spec, ""
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		pattern, repl = spec[:i], spec[i+1:]
	}
	if pattern == "" {
		return value, nil
	}
	var b strings.Builder
	i := 0
	for i < len(value) {
		n, err := longestPatternMatch(pattern, value[i:])
		if err != nil {
			return "", err
		}
		if n > 0 {
			b.WriteString(repl)
			i += n
			if !all {
				b.WriteString(value[i:])
				return b.String(), nil
			}
			continue
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String(), nil
}

func replacePrefixPattern(value, spec string) (string, error) {
	pattern, repl := spec, ""
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		pattern, repl = spec[:i], spec[i+1:]
	}
	for k := len(value); k >= 0; k-- {
		ok, err := patternMatches(pattern, value[:k])
		if err != nil {
			return "", err
		}
		if ok {
			return repl + value[k:], nil
		}
	}
	return value, nil
}

func longestPatternMatch(pattern, s string) (int, error) {
	for n := len(s); n > 0; n-- {
		ok, err := patternMatches(pattern, s[:n])
		if err != nil {
			return 0, err
		}
		if ok {
			return n, nil
		}
	}
	return 0, nil
}

func patternMatches(pattern, s string) (bool, error) {
	if !hasGlobMeta(pattern) {
		return pattern == s, nil
	}
	return matchGlob(pattern, s), nil
}

func (x *expansion) expandToString(w Word) (string, error) {
	var b strings.Builder
	segs := x.applyTilde(w)
	for _, seg := range segs {
		if x.env.NounSet && seg.Kind == SegVar {
			if _, set := x.env.Lookup(seg.Inner); !set {
				if name, ok := expandUnboundName(seg); ok {
					return "", x.runs.unboundAt(name, seg.Pos)
				}
			}
		}
		switch seg.Kind {
		case SegLiteral:
			b.WriteString(seg.Text)
		case SegVar, SegBraced:
			if seg.Inner == "@" {
				b.WriteString(strings.Join(x.env.Args(), " "))
				continue
			}
			if seg.Inner == "*" {
				b.WriteString(x.env.JoinArgs())
				continue
			}
			v, err := x.parameter(seg)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
		case SegArith:
			v, err := x.arith(seg.Inner)
			if err != nil {
				return "", err
			}
			b.WriteString(itoa(v))
		case SegCmdSub:
			v, err := x.commandSubstitution(seg)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
		}
	}
	return b.String(), nil
}

func (x *expansion) expandArbitrary(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	if x.depth >= maxNestDepth {
		return "", &SyntaxError{Msg: "参数展开嵌套过深（上限 " + itoa(maxNestDepth) + " 层）"}
	}
	x.depth++
	defer func() { x.depth-- }()
	segs, err := lexWordSegments(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, seg := range segs {
		switch seg.Kind {
		case SegLiteral:
			b.WriteString(seg.Text)
		case SegVar, SegBraced:
			v, err := x.parameter(seg)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
		case SegArith:
			v, err := x.arith(seg.Inner)
			if err != nil {
				return "", err
			}
			b.WriteString(itoa(v))
		case SegCmdSub:
			v, err := x.commandSubstitution(seg)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
		}
	}
	return b.String(), nil
}
