package vbash

import (
	"strings"
	"unicode"
)

func shellQuoteEscaped(s string) string {
	if s == "" {
		return "''"
	}
	var b strings.Builder
	needDollar := false
	for _, r := range s {
		if !printableRune(r) {
			needDollar = true
			break
		}
	}
	if needDollar {
		b.Reset()
		b.WriteString("$'")
		for _, r := range s {
			switch r {
			case '\a':
				b.WriteString(`\a`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			case '\v':
				b.WriteString(`\v`)
			case '\\':
				b.WriteString(`\\`)
			case '\'':
				b.WriteString(`\'`)
			default:
				if printableRune(r) {
					b.WriteRune(r)
					continue
				}
				for _, by := range []byte(string(r)) {
					b.WriteString(`\` + octal3(int(by)))
				}
			}
		}
		b.WriteString("'")
		return b.String()
	}
	for _, r := range s {
		if quoteSafeRune(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('\\')
		b.WriteRune(r)
	}
	return b.String()
}

func quoteSafeRune(r rune) bool {
	switch r {
	case '%', '+', '-', '.', '/', ':', '=', '@', '_':
		return true
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	return r >= 0x80 && printableRune(r)
}

func printableRune(r rune) bool {
	return unicode.IsPrint(r)
}

func octal3(n int) string {
	out := []byte{'0', '0', '0'}
	out[2] = byte('0' + n%8)
	out[1] = byte('0' + (n/8)%8)
	out[0] = byte('0' + n/64%8)
	return string(out)
}
