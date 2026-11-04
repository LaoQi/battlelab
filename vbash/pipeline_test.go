package vbash

import "testing"

func TestPipelineRepeatedNoLeak(t *testing.T) {
	dir := tempDir(t)
	for i := 0; i < 100; i++ {
		got := runScript(t, dir, `printf 'a\nb\na\n' | grep a | wc -l`)
		if got.stdout != "2\n" {
			t.Fatalf("第 %d 轮输出为 %q", i, got.stdout)
		}
	}
}

func TestPipelineThreeStages(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `printf 'x\ny\n' | cat | cat | cat`)
	if got.stdout != "x\ny\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestPipelineLargeThroughput(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `seq 1 50000 | wc -l`)
	if got.stdout != "50000\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestPipelineConsumerReadsInput(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `printf '1\n2\n3\n' | while read x; do echo "<$x>"; done`)
	if got.stdout != "<1>\n<2>\n<3>\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}
