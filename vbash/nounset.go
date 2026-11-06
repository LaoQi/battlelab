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
	if n, ok := numericName(seg.Inner); ok {
		return "$" + strconv.Itoa(n), true
	}
	return seg.Inner, true
}
