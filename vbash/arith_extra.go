package vbash

import "strings"

func (a *arithParser) hasPrefix(tok string) bool {
	return strings.HasPrefix(string(a.src[a.i:]), tok)
}

func (a *arithParser) readBaseConstant() (int, bool, error) {
	i := a.i
	for i < len(a.src) && a.src[i] >= '0' && a.src[i] <= '9' {
		i++
	}
	if i >= len(a.src) || a.src[i] != '#' {
		return 0, false, nil
	}
	baseText := string(a.src[a.i:i])
	base, ok := parseDigits(baseText, 10)
	if !ok || base < 2 || base > 64 {
		return 0, true, runtimeErrf(Pos{}, "%s#: invalid integer constant", baseText)
	}
	j := i + 1
	if j < len(a.src) && (a.src[j] == '-' || a.src[j] == '+') {
		return 0, true, runtimeErrf(Pos{}, "%s#: invalid integer constant", baseText)
	}
	digitStart := j
	for j < len(a.src) && baseDigitValue(a.src[j]) < base && baseDigitValue(a.src[j]) >= 0 {
		j++
	}
	if j == digitStart {
		return 0, true, runtimeErrf(Pos{}, "%s#: invalid integer constant", baseText)
	}
	text := string(a.src[digitStart:j])
	n, ok := parseDigits(text, base)
	if !ok {
		return 0, true, runtimeErrf(Pos{}, "%s#%s: value too great for base", baseText, text)
	}
	a.i = j
	return n, true, nil
}

func baseDigitValue(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'z':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'Z':
		return int(r-'A') + 10
	case r == '@':
		return 62
	case r == '_':
		return 63
	}
	return -1
}

func (a *arithParser) applyPow() (int, error) {
	left, err := a.parsePostfix()
	if err != nil {
		return 0, err
	}
	save := a.i
	a.skipSpace()
	if !a.hasPrefix("**") {
		a.i = save
		return left, nil
	}
	a.i += 2
	right, err := a.applyPow()
	if err != nil {
		return 0, err
	}
	if right < 0 {
		return 0, runtimeErrf(Pos{}, "exponent less than 0")
	}
	res := 1
	for i := 0; i < right; i++ {
		res *= left
	}
	return res, nil
}

func (a *arithParser) parsePostfix() (int, error) {
	if a.peek() == '+' || a.peek() == '-' {
		sign := a.peek()
		if sign == '+' && a.peekAt(1) == '+' || sign == '-' && a.peekAt(1) == '-' {
			return a.parsePreIncrement()
		}
	}
	start := a.i
	v, err := a.parseUnary()
	if err != nil {
		return 0, err
	}
	name := ""
	if a.i > start {
		name = strings.TrimSpace(string(a.src[start:a.i]))
	}
	if !validAssignName(name) {
		name = ""
	}
	save := a.i
	a.skipSpace()
	if !a.hasPrefix("++") && !a.hasPrefix("--") {
		a.i = save
		return v, nil
	}
	if name == "" {
		a.i = save
		return v, nil
	}
	op := string(a.src[a.i : a.i+2])
	a.i += 2
	if op == "++" {
		a.env.Set(name, itoa(v+1), false)
	} else {
		a.env.Set(name, itoa(v-1), false)
	}
	return v, nil
}

func (a *arithParser) parsePreIncrement() (int, error) {
	op := string(a.src[a.i : a.i+2])
	a.i += 2
	name := a.readName()
	if name == "" {
		return 0, runtimeErrf(Pos{}, "syntax error: operand expected")
	}
	cur := 0
	if text, ok := a.env.Lookup(name); ok {
		if v, ok := parseArithInt(text); ok {
			cur = v
		}
	}
	if op == "++" {
		cur++
	} else {
		cur--
	}
	a.env.Set(name, itoa(cur), false)
	return cur, nil
}
