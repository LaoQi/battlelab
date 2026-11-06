package vbash

import "strings"

func (a *arithParser) dollarOperand() (int, bool, error) {
	start := a.i
	a.i++
	switch {
	case a.peek() == '{':
		a.i = skipDelimited(a.src, a.i, '{', '}')
	case a.peek() == '(' && a.peekAt(1) == '(':
		a.i = skipArithSub(a.src, a.i)
	case a.peek() == '(':
		a.i = skipDelimited(a.src, a.i, '(', ')')
	default:
		if a.peek() != 0 && isSpecialParam(a.peek()) {
			a.i++
			break
		}
		for !a.eof() && (isArithNameStart(a.src[a.i]) || (a.src[a.i] >= '0' && a.src[a.i] <= '9')) {
			a.i++
		}
	}
	text := string(a.src[start:a.i])
	if text == "$" {
		a.i = start
		return 0, false, nil
	}
	value, err := a.expn.expandArbitrary(text)
	if err != nil {
		return 0, true, err
	}
	inner := strings.TrimSpace(value)
	if inner == "" {
		return 0, true, nil
	}
	sub, err := a.expn.arith(inner)
	if err != nil {
		return 0, true, err
	}
	return sub, true, nil
}

func (a *arithParser) peekAt(n int) rune {
	if a.i+n >= len(a.src) {
		return 0
	}
	return a.src[a.i+n]
}

func skipDelimited(src []rune, i int, open, close rune) int {
	depth := 1
	i++
	for i < len(src) {
		switch src[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return i
}

func skipArithSub(src []rune, i int) int {
	depth := 0
	i += 2
	for i < len(src) {
		switch src[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				if i+1 < len(src) && src[i+1] == ')' {
					return i + 2
				}
				return i + 1
			}
			depth--
		}
		i++
	}
	return i
}
