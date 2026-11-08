package vbash

import "strconv"

func expandUnboundName(seg Segment) (string, bool) {
	if seg.Kind != SegVar {
		return "", false
	}
	switch seg.Inner {
	case "", "?", "#", "@", "*", "$", "-", "0":
		return "", false
	}
	if seg.Inner == "!" {
		return "$!", true
	}
	if n, ok := numericName(seg.Inner); ok {
		return "$" + strconv.Itoa(n), true
	}
	return seg.Inner, true
}

func bracedUnboundName(spec bracedOp) (string, bool) {
	if bracedOpSuppliesDefault(spec) {
		return "", false
	}
	switch spec.name {
	case "", "?", "#", "@", "*", "$", "-", "0":
		return "", false
	}
	if spec.name == "!" {
		return "!", true
	}
	return spec.name, true
}

func bracedOpSuppliesDefault(spec bracedOp) bool {
	switch spec.op {
	case ":-", "-", ":=", "=", ":+", "+", "?", ":?":
		return true
	}
	return false
}
