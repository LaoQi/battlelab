package vbash

import (
	"strings"
	"testing"
	"time"
)

func checkNoPanic(t *testing.T, src string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("解析 %q 触发 panic: %v", src, r)
		}
	}()
	list, err := Parse(src)
	if err == nil {
		if checkErr := checkTree(list); checkErr != nil {
			t.Errorf("解析 %q 的 AST 不完整: %v", src, checkErr)
		}
	}
	lexAllNoFatal(t, src)
}

func TestFuzzSnippets(t *testing.T) {
	atoms := []string{
		"echo", "a", "b", "$X", "${Y}", "$( echo hi )", "$((1+2))", "'q p'", `"d q"`,
		"|", "&&", "||", ";", "&", ">", ">>", "<", "2>&1", "<<'EOF'", "EOF", "\n",
		"(", ")", "{", "}", "\\", "#", "=", "X=1", "!", "*", "?", "~", "$?", "$@",
		"\t", "[", "]", "[[", "$'\\t'", "$", "${", "$(", "}", ";;", "<<-",
	}
	seed := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}
	deadline := time.Now().Add(2 * time.Second)
	for i := 0; i < 5000; i++ {
		n := int(next() % 10)
		var b strings.Builder
		for j := 0; j < n; j++ {
			b.WriteString(atoms[int(next()%uint64(len(atoms)))])
			if next()%2 == 0 {
				b.WriteByte(' ')
			}
		}
		checkNoPanic(t, b.String())
		if i%500 == 0 && time.Now().After(deadline) {
			return
		}
	}
}
