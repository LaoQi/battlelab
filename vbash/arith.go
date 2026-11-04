package vbash

import (
	"strings"
)

type arithParser struct {
	src   []rune
	i     int
	env   *Env
	expn  *expansion
	depth int
}

func (x *expansion) arith(expr string) (int, error) {
	a := &arithParser{src: []rune(expr), env: x.env, expn: x}
	v, err := a.parseComma()
	if err != nil {
		return 0, err
	}
	a.skipSpace()
	if !a.eof() {
		return 0, runtimeErrf(Pos{}, "%s: syntax error in expression", strings.TrimSpace(expr))
	}
	return v, nil
}

func (a *arithParser) eof() bool { return a.i >= len(a.src) }

func (a *arithParser) peek() rune {
	if a.eof() {
		return 0
	}
	return a.src[a.i]
}

func (a *arithParser) skipSpace() {
	for !a.eof() && (a.src[a.i] == ' ' || a.src[a.i] == '\t' || a.src[a.i] == '\n') {
		a.i++
	}
}

func (a *arithParser) accept(tok string) bool {
	a.skipSpace()
	r := []rune(tok)
	if a.i+len(r) > len(a.src) {
		return false
	}
	for k, c := range r {
		if a.src[a.i+k] != c {
			return false
		}
	}
	a.i += len(r)
	return true
}

func (a *arithParser) parseComma() (int, error) {
	v, err := a.parseAssign()
	if err != nil {
		return 0, err
	}
	for {
		a.skipSpace()
		if a.eof() || a.peek() != ',' {
			return v, nil
		}
		a.i++
		v, err = a.parseAssign()
		if err != nil {
			return 0, err
		}
	}
}

func (a *arithParser) parseAssign() (int, error) {
	save := a.i
	a.skipSpace()
	name := a.readName()
	if name != "" {
		a.skipSpace()
		for _, op := range []string{"<<=", ">>=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "="} {
			if a.accept(op) {
				rhs, err := a.parseAssign()
				if err != nil {
					return 0, err
				}
				old, _ := a.env.Lookup(name)
				oldV := 0
				if v, ok := parseInt(old); ok {
					oldV = v
				}
				var res int
				switch op {
				case "=":
					res = rhs
				case "+=":
					res = oldV + rhs
				case "-=":
					res = oldV - rhs
				case "*=":
					res = oldV * rhs
				case "/=":
					if rhs == 0 {
						return 0, runtimeErrf(Pos{}, "division by 0")
					}
					res = oldV / rhs
				case "%=":
					if rhs == 0 {
						return 0, runtimeErrf(Pos{}, "division by 0")
					}
					res = oldV % rhs
				case "<<=":
					res = oldV << uint(rhs)
				case ">>=":
					res = oldV >> uint(rhs)
				case "&=":
					res = oldV & rhs
				case "|=":
					res = oldV | rhs
				case "^=":
					res = oldV ^ rhs
				}
				a.env.Set(name, itoa(res), false)
				return res, nil
			}
		}
	}
	a.i = save
	return a.parseTernary()
}

func (a *arithParser) readName() string {
	a.skipSpace()
	start := a.i
	if a.eof() || !isArithNameStart(a.src[a.i]) {
		return ""
	}
	for !a.eof() && (isArithNameStart(a.src[a.i]) || (a.src[a.i] >= '0' && a.src[a.i] <= '9')) {
		a.i++
	}
	return string(a.src[start:a.i])
}

func isArithNameStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func (a *arithParser) parseTernary() (int, error) {
	cond, err := a.parseBinary(0)
	if err != nil {
		return 0, err
	}
	a.skipSpace()
	if a.eof() || a.peek() != '?' {
		return cond, nil
	}
	a.i++
	then, err := a.parseAssign()
	if err != nil {
		return 0, err
	}
	if !a.accept(":") {
		return 0, runtimeErrf(Pos{}, "expected ':' in ternary")
	}
	other, err := a.parseAssign()
	if err != nil {
		return 0, err
	}
	if cond != 0 {
		return then, nil
	}
	return other, nil
}

type arithOp struct {
	tok   string
	prec  int
	right bool
}

var arithOps = []arithOp{
	{"||", 1, false}, {"&&", 2, false},
	{"|", 3, false}, {"^", 4, false}, {"&", 5, false},
	{"==", 6, false}, {"!=", 6, false},
	{"<=", 7, false}, {">=", 7, false}, {"<", 7, false}, {">", 7, false},
	{"<<", 8, false}, {">>", 8, false},
	{"+", 9, false}, {"-", 9, false},
	{"*", 10, false}, {"/", 10, false}, {"%", 10, false},
}

func (a *arithParser) parseBinary(minPrec int) (int, error) {
	left, err := a.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		a.skipSpace()
		op, prec, ok := a.peekOp()
		if !ok || prec < minPrec {
			return left, nil
		}
		a.i += len([]rune(op))
		right, err := a.parseBinary(prec + 1)
		if err != nil {
			return 0, err
		}
		left, err = applyArith(op, left, right)
		if err != nil {
			return 0, err
		}
	}
}

func (a *arithParser) peekOp() (string, int, bool) {
	rest := string(a.src[a.i:])
	for _, op := range arithOps {
		if strings.HasPrefix(rest, op.tok) {
			if op.tok == "|" && strings.HasPrefix(rest, "||") {
				continue
			}
			if op.tok == "&" && strings.HasPrefix(rest, "&&") {
				continue
			}
			if op.tok == "<" && (strings.HasPrefix(rest, "<<") || strings.HasPrefix(rest, "<=")) {
				continue
			}
			if op.tok == ">" && (strings.HasPrefix(rest, ">>") || strings.HasPrefix(rest, ">=")) {
				continue
			}
			return op.tok, op.prec, true
		}
	}
	return "", 0, false
}

func applyArith(op string, l, r int) (int, error) {
	switch op {
	case "||":
		if l != 0 || r != 0 {
			return 1, nil
		}
		return 0, nil
	case "&&":
		if l != 0 && r != 0 {
			return 1, nil
		}
		return 0, nil
	case "|":
		return l | r, nil
	case "^":
		return l ^ r, nil
	case "&":
		return l & r, nil
	case "==":
		return btoi(l == r), nil
	case "!=":
		return btoi(l != r), nil
	case "<":
		return btoi(l < r), nil
	case ">":
		return btoi(l > r), nil
	case "<=":
		return btoi(l <= r), nil
	case ">=":
		return btoi(l >= r), nil
	case "<<":
		return l << uint(r), nil
	case ">>":
		return l >> uint(r), nil
	case "+":
		return l + r, nil
	case "-":
		return l - r, nil
	case "*":
		return l * r, nil
	case "/":
		if r == 0 {
			return 0, runtimeErrf(Pos{}, "division by 0")
		}
		return l / r, nil
	case "%":
		if r == 0 {
			return 0, runtimeErrf(Pos{}, "division by 0")
		}
		return l % r, nil
	}
	return 0, runtimeErrf(Pos{}, "unsupported operator %s", op)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *arithParser) parseUnary() (int, error) {
	a.skipSpace()
	if a.eof() {
		return 0, runtimeErrf(Pos{}, "syntax error: operand expected")
	}
	switch a.peek() {
	case '+':
		a.i++
		return a.parseUnary()
	case '-':
		a.i++
		v, err := a.parseUnary()
		if err != nil {
			return 0, err
		}
		return -v, nil
	case '!':
		a.i++
		v, err := a.parseUnary()
		if err != nil {
			return 0, err
		}
		return btoi(v == 0), nil
	case '~':
		a.i++
		v, err := a.parseUnary()
		if err != nil {
			return 0, err
		}
		return ^v, nil
	}
	return a.parsePrimary()
}

func (a *arithParser) parsePrimary() (int, error) {
	a.skipSpace()
	if a.eof() {
		return 0, runtimeErrf(Pos{}, "syntax error: operand expected")
	}
	if a.accept("(") {
		if a.depth >= maxNestDepth {
			return 0, runtimeErrf(Pos{}, "算术表达式嵌套过深（上限 %d 层）", maxNestDepth)
		}
		a.depth++
		v, err := a.parseComma()
		a.depth--
		if err != nil {
			return 0, err
		}
		if !a.accept(")") {
			return 0, runtimeErrf(Pos{}, "missing ')' in arithmetic expression")
		}
		return v, nil
	}
	if a.peek() >= '0' && a.peek() <= '9' {
		start := a.i
		for !a.eof() && ((a.src[a.i] >= '0' && a.src[a.i] <= '9') || a.src[a.i] == 'x' || a.src[a.i] == 'X' ||
			(a.src[a.i] >= 'a' && a.src[a.i] <= 'f') || (a.src[a.i] >= 'A' && a.src[a.i] <= 'F')) {
			a.i++
		}
		v, ok := parseInt(string(a.src[start:a.i]))
		if !ok {
			return 0, runtimeErrf(Pos{}, "invalid number %s", string(a.src[start:a.i]))
		}
		return v, nil
	}
	if a.peek() == '$' {
		start := a.i
		a.i++
		if a.peek() == '{' {
			depth := 0
			for !a.eof() {
				if a.src[a.i] == '}' && depth == 0 {
					a.i++
					break
				}
				if a.src[a.i] == '{' {
					depth++
				} else if a.src[a.i] == '}' {
					depth--
				}
				a.i++
			}
		} else if a.peek() == '(' {
			depth := 0
			for !a.eof() {
				if a.src[a.i] == ')' && depth == 0 {
					a.i++
					break
				}
				if a.src[a.i] == '(' {
					depth++
				} else if a.src[a.i] == ')' {
					depth--
				}
				a.i++
			}
		} else {
			for !a.eof() && (isArithNameStart(a.src[a.i]) || (a.src[a.i] >= '0' && a.src[a.i] <= '9')) {
				a.i++
			}
		}
		text := string(a.src[start:a.i])
		segs, err := lexWordSegments(text)
		if err != nil {
			return 0, err
		}
		var b strings.Builder
		for _, seg := range segs {
			if seg.Kind == SegLiteral {
				b.WriteString(seg.Text)
				continue
			}
			v, err := a.expn.parameter(seg)
			if err != nil {
				return 0, err
			}
			b.WriteString(v)
		}
		inner := strings.TrimSpace(b.String())
		if inner == "" {
			return 0, nil
		}
		sub, err := a.expn.arith(inner)
		if err != nil {
			return 0, err
		}
		return sub, nil
	}
	name := a.readName()
	if name != "" {
		if a.accept("[") {
			idx, err := a.parseComma()
			if err != nil {
				return 0, err
			}
			if !a.accept("]") {
				return 0, runtimeErrf(Pos{}, "missing ']' in array subscript")
			}
			_ = idx
			return 0, nil
		}
		value, ok := a.env.Lookup(name)
		if !ok || value == "" {
			return 0, nil
		}
		inner, err := a.expn.arith(value)
		if err != nil {
			return 0, err
		}
		return inner, nil
	}
	return 0, runtimeErrf(Pos{}, "syntax error near unexpected token %q", string(a.peek()))
}

func parseInt(s string) (int, bool) {
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
	n := 0
	for _, r := range text {
		var d int
		switch {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r >= 'a' && r <= 'f':
			d = int(r-'a') + 10
		case r >= 'A' && r <= 'F':
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
