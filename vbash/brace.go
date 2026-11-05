package vbash

import (
	"strconv"
	"strings"
)

const maxBraceFields = 16384

type braceChar struct {
	ch     byte
	quoted bool
}

type braceSeg struct {
	chars []braceChar
	ok    bool
	keep  bool
}

func (e *braceLimits) word(w Word) ([]Word, error) {
	segs := flattenBraceSegments(w.Segments)
	if segs == nil {
		return []Word{w}, nil
	}
	e.pos = w.Pos
	alts, ok, err := e.alternates(segs)
	if err != nil {
		return nil, err
	}
	if !ok || len(alts) == 0 {
		return []Word{w}, nil
	}
	e.fields += len(alts)
	out := make([]Word, 0, len(alts))
	for _, alt := range alts {
		out = append(out, rebuildBraceWord(alt))
	}
	return out, nil
}

func flattenBraceSegments(segs []Segment) []braceSeg {
	out := make([]braceSeg, 0, len(segs))
	for _, seg := range segs {
		if seg.Kind != SegLiteral {
			out = append(out, braceSeg{})
			continue
		}
		bs := braceSeg{ok: true}
		for i := 0; i < len(seg.Text); i++ {
			bs.chars = append(bs.chars, braceChar{ch: seg.Text[i], quoted: seg.Mode != ModeUnquoted})
		}
		out = append(out, bs)
	}
	return out
}

func concatBraceChars(segs []braceSeg) []braceChar {
	var out []braceChar
	for _, seg := range segs {
		if !seg.ok {
			out = append(out, braceChar{ch: 0, quoted: true})
			continue
		}
		out = append(out, seg.chars...)
	}
	return out
}

type braceLimits struct {
	pos    Pos
	depth  int
	fields int
}

func (e *braceLimits) free() int {
	return maxBraceFields - e.fields
}

func (e *braceLimits) alternates(segs []braceSeg) ([][]braceSeg, bool, error) {
	all := concatBraceChars(segs)
	res, ok, err := e.raw(all)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	out := make([][]braceSeg, 0, len(res))
	for _, chars := range res {
		out = append(out, []braceSeg{{chars: chars, ok: true, keep: true}})
	}
	return out, true, nil
}

func (e *braceLimits) raw(all []braceChar) ([][]braceChar, bool, error) {
	pairs := bracePairs(all)
	for start := 0; start < len(all); start++ {
		if all[start].quoted || all[start].ch != '{' {
			continue
		}
		close, ok := pairs[start]
		if !ok {
			continue
		}
		res, hit, err := e.alternateAt(all, start, close)
		if err != nil {
			return nil, false, err
		}
		if hit {
			return res, true, nil
		}
	}
	return nil, false, nil
}

func (e *braceLimits) alternateAt(all []braceChar, open int, close int) ([][]braceChar, bool, error) {
	parts, isList, over := braceSplitChars(all[open+1:close], e.free())
	if over {
		return nil, false, e.tooManyFields()
	}
	if !isList {
		return nil, false, nil
	}
	head := all[:open]
	tail := all[close+1:]
	var out [][]braceChar
	skipped := 0
	for _, part := range parts {
		if len(part.chars) == 0 && len(head) == 0 && len(tail) == 0 && !part.keep {
			skipped++
			continue
		}
		cur := make([]braceChar, 0, len(head)+len(part.chars)+len(tail))
		cur = append(cur, head...)
		cur = append(cur, part.chars...)
		cur = append(cur, tail...)
		if err := e.enter(); err != nil {
			return nil, false, err
		}
		sub, ok, err := e.raw(cur)
		e.depth--
		if err != nil {
			return nil, false, err
		}
		if !ok {
			sub = [][]braceChar{cur}
		}
		if len(out)+len(sub) > e.free() {
			return nil, false, e.tooManyFields()
		}
		out = append(out, sub...)
	}
	if len(out) == 0 && skipped > 0 {
		out = append(out, nil)
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, true, nil
}

func (e *braceLimits) enter() error {
	if e.depth >= maxNestDepth {
		return runtimeErrf(e.pos, "大括号展开嵌套过深（上限 %d 层）", maxNestDepth)
	}
	e.depth++
	return nil
}

func (e *braceLimits) tooManyFields() error {
	return runtimeErrf(e.pos, "大括号展开字段过多（上限 %d 个）", maxBraceFields)
}

func bracePairs(all []braceChar) map[int]int {
	pairs := make(map[int]int, len(all)/8)
	var stack []int
	for i := 0; i < len(all); i++ {
		if all[i].quoted {
			continue
		}
		switch all[i].ch {
		case '{':
			stack = append(stack, i)
		case '}':
			if len(stack) == 0 {
				continue
			}
			pairs[stack[len(stack)-1]] = i
			stack = stack[:len(stack)-1]
		}
	}
	return pairs
}

type braceSplitItem struct {
	chars []braceChar
	keep  bool
}

func braceSplitChars(body []braceChar, limit int) ([]braceSplitItem, bool, bool) {
	var items []braceSplitItem
	var cur []braceChar
	depth := 0
	hasComma := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c.quoted {
			cur = append(cur, c)
			continue
		}
		switch c.ch {
		case '\\':
			if i+1 < len(body) {
				cur = append(cur, body[i+1])
				i++
				continue
			}
			cur = append(cur, c)
		case '{':
			depth++
			cur = append(cur, c)
		case '}':
			depth--
			cur = append(cur, c)
		case ',':
			if depth == 0 {
				hasComma = true
				items = append(items, braceSplitItem{chars: cur})
				cur = nil
				continue
			}
			cur = append(cur, c)
		default:
			cur = append(cur, c)
		}
	}
	if hasComma {
		if cur != nil || len(items) > 0 {
			items = append(items, braceSplitItem{chars: cur})
		}
		if len(items) > limit {
			return nil, false, true
		}
		return items, true, false
	}
	items, ok, over := braceSequenceChars(body, limit)
	return items, ok, over
}

func braceSequenceChars(body []braceChar, limit int) ([]braceSplitItem, bool, bool) {
	parts := strings.Split(charsToString(body), "..")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, false, false
	}
	step := 1
	if len(parts) == 3 {
		n, err := strconv.Atoi(parts[2])
		if err != nil || n == 0 {
			return nil, false, false
		}
		step = n
		if step < 0 {
			step = -step
		}
	}
	if from, err := strconv.Atoi(parts[0]); err == nil {
		to, terr := strconv.Atoi(parts[1])
		if terr != nil {
			return nil, false, false
		}
		width := 0
		if paddedNum(parts[0]) || paddedNum(parts[1]) {
			width = len(strings.TrimPrefix(parts[0], "-"))
			if w := len(strings.TrimPrefix(parts[1], "-")); w > width {
				width = w
			}
		}
		var out []braceSplitItem
		if from <= to {
			for v := from; v <= to; v += step {
				if len(out) >= limit {
					return nil, false, true
				}
				out = append(out, braceSplitItem{chars: stringToChars(padNumberWidth(v, width)), keep: true})
			}
		} else {
			for v := from; v >= to; v -= step {
				if len(out) >= limit {
					return nil, false, true
				}
				out = append(out, braceSplitItem{chars: stringToChars(padNumberWidth(v, width)), keep: true})
			}
		}
		return out, true, false
	}
	if len(parts[0]) != 1 || len(parts[1]) != 1 {
		return nil, false, false
	}
	a, b := rune(parts[0][0]), rune(parts[1][0])
	if !isBraceLetter(a) || !isBraceLetter(b) {
		return nil, false, false
	}
	var out []braceSplitItem
	if a <= b {
		for c := int(a); c <= int(b); c += step {
			if len(out) >= limit {
				return nil, false, true
			}
			out = append(out, braceLetterItem(c))
		}
		return out, true, false
	}
	for c := int(a); c >= int(b); c -= step {
		if len(out) >= limit {
			return nil, false, true
		}
		out = append(out, braceLetterItem(c))
	}
	return out, true, false
}

func braceLetterItem(c int) braceSplitItem {
	if c == '\\' {
		return braceSplitItem{keep: true}
	}
	return braceSplitItem{chars: stringToChars(string(rune(c))), keep: true}
}

func charsToString(chars []braceChar) string {
	var b strings.Builder
	for _, c := range chars {
		b.WriteByte(c.ch)
	}
	return b.String()
}

func stringToChars(s string) []braceChar {
	out := make([]braceChar, 0, len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, braceChar{ch: s[i]})
	}
	return out
}

func rebuildBraceWord(segs []braceSeg) Word {
	var words []Segment
	text := ""
	for _, seg := range segs {
		if !seg.ok {
			continue
		}
		var cur strings.Builder
		for _, c := range seg.chars {
			cur.WriteByte(c.ch)
		}
		if cur.Len() == 0 {
			continue
		}
		words = append(words, Segment{Kind: SegLiteral, Text: cur.String(), Mode: ModeUnquoted})
		text += cur.String()
	}
	if text == "" {
		words = []Segment{{Kind: SegLiteral, Text: "", Mode: ModeDoubleQuoted}}
	}
	return Word{Segments: words, Text: text}
}

func paddedNum(s string) bool {
	s = strings.TrimPrefix(s, "-")
	return len(s) > 1 && s[0] == '0'
}

func padNumberWidth(v int, width int) string {
	if width <= 0 {
		return strconv.Itoa(v)
	}
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	s := strconv.Itoa(v)
	for len(s) < width {
		s = "0" + s
	}
	return sign + s
}

func isBraceLetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
