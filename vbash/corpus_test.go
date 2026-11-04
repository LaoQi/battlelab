package vbash

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

type benchCase struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

func loadBenchCommands(t *testing.T) []benchCase {
	t.Helper()
	b, err := os.ReadFile("testdata/bench_commands.json")
	if err != nil {
		t.Skipf("缺少语料清单（由基准用例导出）: %v", err)
	}
	var out []benchCase
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func lexOne(cmd string) error {
	l := newLexer(cmd)
	for i := 0; i < 1_000_000; i++ {
		tok, err := l.Next()
		if err != nil {
			return err
		}
		if tok.Kind == TokEOF {
			return nil
		}
	}
	return errors.New("词法器未在合理步数内结束")
}

func TestCorpusLexes(t *testing.T) {
	cases := loadBenchCommands(t)
	if len(cases) == 0 {
		t.Fatal("语料清单为空")
	}
	var unsupported, malformed int
	for _, c := range cases {
		err := lexOne(c.Command)
		if err == nil {
			continue
		}
		var ue *UnsupportedError
		if errors.As(err, &ue) {
			unsupported++
			t.Logf("按设计不支持 %s: %v", c.Name, err)
			continue
		}
		var se *SyntaxError
		if errors.As(err, &se) {
			malformed++
			t.Logf("按预期语法错误 %s: %v", c.Name, err)
			continue
		}
		t.Errorf("%s 词法失败: %v（命令 %q）", c.Name, err, c.Command)
	}
	t.Logf("共扫描 %d 条用例：按设计不支持 %d 条，按预期语法错误 %d 条", len(cases), unsupported, malformed)
}
