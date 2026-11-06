package vbash

import "strings"

func (x *expansion) substringOp(value string, arg string) (string, error) {
	offText, lenText := arg, ""
	haveLen := false
	if i := strings.IndexByte(arg, ':'); i >= 0 {
		offText, lenText = arg[:i], arg[i+1:]
		haveLen = true
	}
	off, err := x.subscriptValue(offText)
	if err != nil {
		return "", err
	}
	runes := []rune(value)
	if off < 0 {
		off += len(runes)
		if off < 0 {
			return "", nil
		}
	}
	if off > len(runes) {
		return "", nil
	}
	rest := runes[off:]
	if !haveLen {
		return string(rest), nil
	}
	n, err := x.subscriptValue(lenText)
	if err != nil {
		return "", err
	}
	if n < 0 {
		n += len(runes)
		if n < 0 {
			return "", runtimeErrf(Pos{}, "%s: substring expression < 0", strings.TrimSpace(lenText))
		}
		n -= off
		if n <= 0 {
			return "", nil
		}
	}
	if n > len(rest) {
		n = len(rest)
	}
	return string(rest[:n]), nil
}

func (x *expansion) subscriptValue(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, runtimeErrf(Pos{}, "substring expression < 0")
	}
	expanded, err := x.expandArbitrary(text)
	if err != nil {
		return 0, err
	}
	return x.arith(strings.TrimSpace(expanded))
}

func (x *expansion) caseOp(value, arg string, upper, single bool) string {
	pattern := ""
	if strings.HasPrefix(arg, "[") && strings.HasSuffix(arg, "]") {
		pattern = arg[1 : len(arg)-1]
	}
	runes := []rune(value)
	for i, r := range runes {
		if pattern != "" && !matchGlob(pattern, string(r)) {
			continue
		}
		if upper {
			runes[i] = toUpperRune(r)
		} else {
			runes[i] = toLowerRune(r)
		}
		if single {
			break
		}
	}
	return string(runes)
}

func toUpperRune(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 'a' + 'A'
	}
	return r
}

func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r - 'A' + 'a'
	}
	return r
}
