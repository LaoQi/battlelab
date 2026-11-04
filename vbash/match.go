package vbash

import "strings"

func matchGlob(pattern, name string) bool {
	return matchRunes([]rune(pattern), []rune(name))
}

func matchRunes(p, s []rune) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if matchRunes(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			p, s = p[1:], s[1:]
		case '[':
			if len(s) == 0 {
				return false
			}
			ok, rest, valid := matchClass(p, s[0])
			if !valid {
				if s[0] != '[' {
					return false
				}
				p, s = p[1:], s[1:]
				continue
			}
			if !ok {
				return false
			}
			p, s = rest, s[1:]
		case '\\':
			if len(p) > 1 {
				p = p[1:]
			}
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			p, s = p[1:], s[1:]
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return len(s) == 0
}

func matchClass(p []rune, c rune) (bool, []rune, bool) {
	i := 1
	negate := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		negate = true
		i++
	}
	matched := false
	start := i
	for i < len(p) && (p[i] != ']' || i == start) {
		if i+2 < len(p) && p[i+1] == '-' && p[i+2] != ']' {
			if c >= p[i] && c <= p[i+2] {
				matched = true
			}
			i += 3
			continue
		}
		if p[i] == c {
			matched = true
		}
		i++
	}
	if i >= len(p) {
		return false, nil, false
	}
	if negate {
		matched = !matched
	}
	return matched, p[i+1:], true
}

func globMatchPath(pattern, name string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	return matchGlob(pattern, name)
}
