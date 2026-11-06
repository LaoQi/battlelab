package vbash

import (
	"math"
	"strconv"
	"strings"
)

type printfSpec struct {
	alt       bool
	zero      bool
	left      bool
	space     bool
	plus      bool
	width     int
	prec      int
	hasWidth  bool
	hasPrec   bool
	verb      byte
	exhausted bool
}

func formatPrintf(format string, values []string) (string, string) {
	var b strings.Builder
	args := newPrintfArgs(values)
	warn := ""
	for {
		consumed := false
		i := 0
		for i < len(format) {
			c := format[i]
			if c == '\\' && i+1 < len(format) {
				n, ok := printfEscape(format, i)
				if ok {
					b.WriteString(n)
					i = skipEscape(format, i)
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
			spec, next, ok := parsePrintfSpec(format, i)
			if !ok {
				b.WriteByte(c)
				i++
				continue
			}
			i = next
			if spec.verb == '*' {
				continue
			}
			text, missing, bad := args.take(spec)
			if !missing {
				consumed = true
			}
			if bad != "" && warn == "" {
				warn = bad
			}
			b.WriteString(text)
		}
		if args.done() || !consumed {
			break
		}
	}
	return b.String(), warn
}

func skipEscape(format string, i int) int {
	next := format[i+1]
	switch next {
	case '0', '1', '2', '3', '4', '5', '6', '7':
		j := i + 1
		for k := 0; k < 3 && j < len(format) && format[j] >= '0' && format[j] <= '7'; k++ {
			j++
		}
		return j
	case 'x':
		j := i + 2
		n := 0
		for n < 2 && j < len(format) && isHexDigit(format[j]) {
			j++
			n++
		}
		if n == 0 {
			return i + 1
		}
		return j
	}
	return i + 2
}

func printfEscape(format string, i int) (string, bool) {
	switch format[i+1] {
	case 'n':
		return "\n", true
	case 't':
		return "\t", true
	case 'r':
		return "\r", true
	case '\\':
		return "\\", true
	case '"':
		return "\"", true
	case 'a':
		return "\a", true
	case 'b':
		return "\b", true
	case 'f':
		return "\f", true
	case 'v':
		return "\v", true
	case 'e':
		return "\x1b", true
	case '0', '1', '2', '3', '4', '5', '6', '7':
		j := i + 1
		val := 0
		n := 0
		for n < 3 && j < len(format) && format[j] >= '0' && format[j] <= '7' {
			val = val*8 + int(format[j]-'0')
			j++
			n++
		}
		return string([]byte{byte(val)}), true
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
			return "", false
		}
		return string([]byte{byte(val)}), true
	}
	return "", false
}

type printfArgs struct {
	values []string
	pos    int
}

func newPrintfArgs(values []string) *printfArgs {
	return &printfArgs{values: values}
}

func (a *printfArgs) next() (string, bool) {
	if a.pos >= len(a.values) {
		return "", true
	}
	v := a.values[a.pos]
	a.pos++
	return v, false
}

func (a *printfArgs) done() bool { return a.pos >= len(a.values) }

func (a *printfArgs) take(spec *printfSpec) (string, bool, string) {
	arg, missing := a.next()
	if missing {
		return applyPrintfSpec(spec, zeroValue(spec.verb)), true, ""
	}
	return applyPrintfSpec(spec, arg), false, invalidNumberMsg(spec, arg)
}

func invalidNumberMsg(spec *printfSpec, arg string) string {
	switch spec.verb {
	case 'd', 'i', 'u', 'o', 'x', 'X':
		_, st := parsePrintfInt(arg)
		return intErrMsg(arg, st)
	case 'f', 'F', 'e', 'E', 'g', 'G':
		if _, ok := parsePrintfFloat(arg); !ok {
			return arg + ": invalid number"
		}
	}
	return ""
}

func intErrMsg(arg string, st intParseStatus) string {
	switch st {
	case intOctalInvalid:
		return arg + ": invalid octal number"
	case intRange:
		return arg + ": Numerical result out of range"
	case intInvalid:
		return arg + ": invalid number"
	}
	return ""
}

func parsePrintfSpec(format string, start int) (*printfSpec, int, bool) {
	spec := &printfSpec{prec: -1}
	i := start + 1
	for i < len(format) {
		switch format[i] {
		case '-':
			spec.left = true
		case '+':
			spec.plus = true
		case ' ':
			spec.space = true
		case '#':
			spec.alt = true
		case '0':
			spec.zero = true
		case '\'':
			i++
			continue
		default:
			goto width
		}
		i++
	}
width:
	for i < len(format) && format[i] >= '0' && format[i] <= '9' {
		spec.width = spec.width*10 + int(format[i]-'0')
		spec.hasWidth = true
		i++
	}
	if i < len(format) && format[i] == '.' {
		i++
		spec.prec = 0
		spec.hasPrec = true
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			spec.prec = spec.prec*10 + int(format[i]-'0')
			i++
		}
	}
	if i >= len(format) {
		return nil, start + 1, false
	}
	spec.verb = format[i]
	if spec.verb == '*' {
		return spec, i + 1, true
	}
	switch spec.verb {
	case 's', 'd', 'i', 'u', 'o', 'x', 'X', 'c', 'f', 'F', 'e', 'E', 'g', 'G', 'b', 'q', 'a', 'A':
		return spec, i + 1, true
	}
	return nil, start + 1, false
}

func zeroValue(verb byte) string {
	switch verb {
	case 'd', 'i', 'u', 'o', 'x', 'X', 'f', 'F', 'e', 'E', 'g', 'G', 'a', 'A':
		return "0"
	}
	return ""
}

func applyPrintfSpec(spec *printfSpec, arg string) string {
	switch spec.verb {
	case 's':
		return padString(spec, arg)
	case 'c':
		if arg == "" {
			return padString(spec, "")
		}
		return padString(spec, arg[:1])
	case 'b':
		decoded, _ := decodeEchoEscapes(arg)
		return padString(spec, decoded)
	case 'q':
		if arg == "" {
			return "''"
		}
		if strings.IndexFunc(arg, func(r rune) bool { return !isSafeChar(r) }) < 0 {
			return arg
		}
		return shellQuoteEscaped(arg)
	case 'd', 'i', 'u', 'o', 'x', 'X':
		n, _ := parsePrintfInt(arg)
		return padNumber(spec, n)
	case 'f', 'F', 'e', 'E', 'g', 'G':
		return padFloat(spec, printfFloatValue(arg))
	case 'a', 'A':
		return padString(spec, arg)
	}
	return arg
}

type intParseStatus int

const (
	intOK intParseStatus = iota
	intInvalid
	intOctalInvalid
	intRange
)

func parsePrintfInt(arg string) (int64, intParseStatus) {
	s := strings.TrimLeft(arg, " \t\n")
	if s == "" {
		return 0, intInvalid
	}
	if s[0] == '\'' || s[0] == '"' {
		if len(s) > 1 {
			return int64(s[1]), intOK
		}
		return 0, intInvalid
	}
	neg := false
	i := 0
	if s[i] == '-' {
		neg = true
		i++
	} else if s[i] == '+' {
		i++
	}
	base := 10
	if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
		base = 16
		i += 2
	} else if i < len(s) && s[i] == '0' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
		base = 8
		i++
	}
	start := i
	var n uint64
	overflow := false
	for ; i < len(s); i++ {
		c := s[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case base == 16 && c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case base == 16 && c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			goto rest
		}
		if d >= uint64(base) {
			if base == 8 {
				return 0, intOctalInvalid
			}
			return 0, intInvalid
		}
		if n > (math.MaxUint64-d)/uint64(base) {
			overflow = true
		} else {
			n = n*uint64(base) + d
		}
	}
rest:
	if i == start {
		return 0, intInvalid
	}
	if i != len(s) {
		if strings.TrimLeft(s[i:], " \t\n") != "" {
			if neg {
				return -int64(n), intInvalid
			}
			return int64(n), intInvalid
		}
	}
	if overflow {
		if neg {
			return math.MinInt64, intRange
		}
		if n > math.MaxInt64 {
			return math.MaxInt64, intRange
		}
	}
	if neg {
		return -int64(n), intOK
	}
	return int64(n), intOK
}

func parsePrintfFloat(arg string) (float64, bool) {
	s := strings.TrimLeft(arg, " \t\n")
	if s == "" {
		return 0, false
	}
	s = strings.TrimRight(s, " \t\n")
	v, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return v, true
	}
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		return v, false
	}
	return 0, false
}

func printfFloatValue(arg string) float64 {
	if v, ok := parsePrintfFloat(arg); ok {
		return v
	}
	s := strings.TrimLeft(arg, " \t\n")
	for i := len(s); i > 0; i-- {
		if v, err := strconv.ParseFloat(s[:i], 64); err == nil {
			return v
		}
	}
	return 0
}

func padNumber(spec *printfSpec, n int64) string {
	un := uint64(n)
	var digits string
	switch spec.verb {
	case 'o':
		digits = strconv.FormatUint(un, 8)
	case 'x':
		digits = strconv.FormatUint(un, 16)
	case 'X':
		digits = strings.ToUpper(strconv.FormatUint(un, 16))
	case 'u':
		digits = strconv.FormatUint(un, 10)
	default:
		digits = strconv.FormatInt(n, 10)
	}
	sign := ""
	body := digits
	zeroBody := n == 0
	if spec.verb == 'd' || spec.verb == 'i' {
		if n < 0 {
			sign = "-"
			body = digits[1:]
		} else if spec.plus {
			sign = "+"
		} else if spec.space {
			sign = " "
		}
		if body == "0" {
			zeroBody = true
		}
	}
	prefix := ""
	if spec.alt {
		switch spec.verb {
		case 'x':
			if n != 0 {
				prefix = "0x"
			}
		case 'X':
			if n != 0 {
				prefix = "0X"
			}
		case 'o':
			if !strings.HasPrefix(digits, "0") {
				prefix = "0"
			}
		}
	}
	if spec.hasPrec {
		if spec.prec == 0 && zeroBody {
			body = ""
		}
		for len(body) < spec.prec {
			body = "0" + body
		}
	}
	text := sign + prefix + body
	if spec.zero && !spec.left && !spec.hasPrec {
		pad := spec.width - len(text)
		if pad > 0 {
			text = sign + prefix + strings.Repeat("0", pad) + body
		}
	}
	return padString(spec, text)
}

func padFloat(spec *printfSpec, v float64) string {
	var text string
	switch spec.verb {
	case 'f', 'F':
		prec := 6
		if spec.hasPrec {
			prec = spec.prec
		}
		text = strconv.FormatFloat(v, 'f', prec, 64)
		if spec.verb == 'F' {
			text = strings.ToUpper(text)
		}
	case 'e', 'E':
		prec := 6
		if spec.hasPrec {
			prec = spec.prec
		}
		text = strconv.FormatFloat(v, 'e', prec, 64)
		text = expandExponent(text)
		if spec.verb == 'E' {
			text = strings.ToUpper(text)
		}
	case 'g', 'G':
		prec := 6
		if spec.hasPrec {
			if spec.prec == 0 {
				prec = 1
			} else {
				prec = spec.prec
			}
		}
		text = formatG(v, prec, false)
		if spec.verb == 'G' {
			text = strings.ToUpper(text)
		}
	case 'a', 'A':
		text = strconv.FormatFloat(v, 'x', -1, 64)
		if spec.verb == 'A' {
			text = strings.ToUpper(text)
		}
	}
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign = "-"
		text = text[1:]
	} else if spec.plus {
		sign = "+"
	} else if spec.space {
		sign = " "
	}
	if spec.alt && !strings.ContainsAny(text, ".eE") {
		text += "."
	}
	full := sign + text
	if spec.zero && !spec.left {
		if pad := spec.width - len(full); pad > 0 {
			full = sign + strings.Repeat("0", pad) + text
		}
	}
	return padString(spec, full)
}

func expandExponent(s string) string {
	i := strings.IndexAny(s, "eE")
	if i < 0 {
		return s
	}
	mant := s[:i]
	exp := s[i+1:]
	sign := "+"
	if strings.HasPrefix(exp, "-") {
		sign = "-"
		exp = exp[1:]
	} else if strings.HasPrefix(exp, "+") {
		exp = exp[1:]
	}
	for len(exp) < 2 {
		exp = "0" + exp
	}
	return mant + "e" + sign + exp
}

func formatG(v float64, prec int, alt bool) string {
	if v == 0 {
		return "0"
	}
	exp := int(math.Floor(math.Log10(math.Abs(v))))
	text := strconv.FormatFloat(v, 'e', prec-1, 64)
	if exp < -4 || exp >= prec {
		return trimG(expandExponent(text))
	}
	return trimG(strconv.FormatFloat(v, 'f', prec-1-exp, 64))
}

func trimG(s string) string {
	i := strings.IndexAny(s, "eE")
	head, tail := s, ""
	if i >= 0 {
		head, tail = s[:i], s[i:]
	}
	if strings.Contains(head, ".") {
		head = strings.TrimRight(head, "0")
		head = strings.TrimSuffix(head, ".")
	}
	return head + tail
}

func padString(spec *printfSpec, text string) string {
	switch spec.verb {
	case 's', 'c', 'b', 'q', 'a', 'A':
		text = truncString(spec, text)
	}
	return padWidth(spec, text)
}

func truncString(spec *printfSpec, text string) string {
	if !spec.hasPrec {
		return text
	}
	if spec.prec == 0 {
		return ""
	}
	r := []rune(text)
	if len(r) > spec.prec {
		return string(r[:spec.prec])
	}
	return text
}

func padWidth(spec *printfSpec, text string) string {
	if spec.width <= len(text) {
		return text
	}
	pad := strings.Repeat(" ", spec.width-len(text))
	if spec.left {
		return text + pad
	}
	return pad + text
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
