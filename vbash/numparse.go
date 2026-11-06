package vbash

import "strings"

func parseArithInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	neg := false
	switch s[0] {
	case '-':
		neg = true
		s = s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	base := 10
	text := s
	if len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base = 16
		text = s[2:]
	} else if len(s) > 1 && s[0] == '0' && s[1] != '8' && s[1] != '9' {
		base = 8
		text = s[1:]
	}
	n, ok := parseDigits(text, base)
	if !ok {
		return 0, false
	}
	if neg {
		return -n, true
	}
	return n, true
}

func parseDigits(text string, base int) (int, bool) {
	if text == "" {
		return 0, false
	}
	n := 0
	for _, r := range text {
		var d int
		switch {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r >= 'a' && r <= 'z':
			d = int(r-'a') + 10
		case r >= 'A' && r <= 'Z':
			d = int(r-'A') + 10
		default:
			return 0, false
		}
		if d >= base {
			return 0, false
		}
		n = n*base + d
	}
	return n, true
}

func parseDecimalInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	neg := false
	switch s[0] {
	case '-':
		neg = true
		s = s[1:]
	case '+':
		s = s[1:]
	}
	n, ok := parseDigits(s, 10)
	if !ok {
		return 0, false
	}
	if neg {
		return -n, true
	}
	return n, true
}

func parseInt(s string) (int, bool) { return parseArithInt(s) }
