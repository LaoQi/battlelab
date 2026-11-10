package vbash

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type runOut struct {
	code   int
	stdout string
	stderr string
}

func runScript(t *testing.T, dir, src string, args ...string) runOut {
	t.Helper()
	var out, errb strings.Builder
	cfg := Config{
		Dir:       dir,
		Home:      dir,
		Env:       []string{testPath},
		Args:      args,
		Stdout:    &out,
		Stderr:    &errb,
		Stdin:     strings.NewReader(""),
		ShellName: "/bin/bash",
	}
	code, _ := RunScript(context.Background(), src, cfg)
	return runOut{code: code, stdout: out.String(), stderr: errb.String()}
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "vbash-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestRunEchoAndPrintf(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `echo hello world; printf '%s-%s\n' foo bar`)
	if got.stdout != "hello world\nfoo-bar\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
	if got.code != 0 {
		t.Errorf("退出码为 %d", got.code)
	}
}

func TestRunEchoNoNewline(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `echo -n a; echo -n b`)
	if got.stdout != "ab" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunPrintfEscapes(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `printf 'a\tb\n'; printf 'n=%d\n' 42; printf '\101\102\n'`)
	if got.stdout != "a\tb\nn=42\nAB\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunAndOrShortCircuit(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `false && echo never; echo a; true || echo never2; echo b`)
	if got.stdout != "a\nb\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunExitStatus(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `true; echo $?; false; echo $?`)
	if got.stdout != "0\n1\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunPipeline(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `printf 'a\nb\na\n' | grep a | wc -l`)
	if strings.TrimSpace(got.stdout) != "2" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunPipelineExitCodeIsLast(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `false | true; echo rc=$?`)
	if got.stdout != "rc=0\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunRedirects(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `echo one > f.txt; echo two >> f.txt; cat f.txt; echo x > g.txt; cat g.txt`)
	if got.stdout != "one\ntwo\nx\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunRedirectOrder(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `sh -c 'echo out; echo err >&2' 2>/dev/null >o.txt; cat o.txt`)
	if got.stdout != "out\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
	if got.stderr != "" {
		t.Errorf("stderr 应为空，得到 %q", got.stderr)
	}
}

func TestRunHeredoc(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, "cat > f.txt <<'EOF'\n# title\nbody\nEOF\nwc -l < f.txt")
	if strings.TrimSpace(got.stdout) != "2" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunHeredocExpand(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, "X=val\ncat <<EOF\ngot $X\nEOF")
	if !strings.Contains(got.stdout, "got val") {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunCDChangesDir(t *testing.T) {
	dir := tempDir(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "x.txt"), []byte("inner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `cd sub; cat x.txt; pwd`)
	lines := strings.Split(strings.TrimSpace(got.stdout), "\n")
	if lines[0] != "inner" {
		t.Errorf("输出为 %q", got.stdout)
	}
	if !strings.HasSuffix(lines[1], "/sub") {
		t.Errorf("pwd 为 %q", lines[1])
	}
}

func TestRunCDFailure(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `cd nope && echo never; echo rc=$?`)
	if got.stdout != "rc=1\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
	if !strings.Contains(got.stderr, "cd: nope: No such file or directory") {
		t.Errorf("stderr 为 %q", got.stderr)
	}
}

func TestRunVarExpansion(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `X=abc; echo "$X"; echo '$X'; echo "${X}def"`)
	if got.stdout != "abc\n$X\nabcdef\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunVarWordSplitting(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `X="a  b"; printf '[%s]' $X; echo; printf '[%s]' "$X"; echo`)
	if got.stdout != "[a][b]\n[a  b]\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunCmdSubstitution(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `N=$(printf 'a\nb\nc\n' | wc -l); echo "n=$N"; echo "$(echo hi)"`)
	if got.stdout != "n=3\nhi\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunArithmetic(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `echo $((1+2*3)); N=4; echo $((N*N))`)
	if got.stdout != "7\n16\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunBraceParams(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `X=abcdef; echo "${#X}"; F=a/b/c.txt; echo "${F##*/}"; echo "${F%.*}"; Y=a-b; echo "${Y/-/_}"`)
	if got.stdout != "6\nc.txt\na/b/c\na_b\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunExportVisibility(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `export FOO=bar; sh -c 'echo "$FOO"'; BAZ=1 sh -c 'echo "$BAZ"'`)
	if got.stdout != "bar\n1\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `definitely_not_a_command_xyz 2>/dev/null; echo rc=$?`)
	if got.stdout != "rc=127\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunDevNull(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `echo hidden > /dev/null; echo shown`)
	if got.stdout != "shown\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunTestBuiltin(t *testing.T) {
	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `[ -f f.txt ] && echo yes; [ -d f.txt ] || echo notdir; test -z "" && echo empty`)
	if got.stdout != "yes\nnotdir\nempty\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunSetArgsSpecialParams(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `set -- a b c; echo $#; echo "$1"; printf '[%s]' "$@"; echo`)
	if got.stdout != "3\na\n[a][b][c]\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunRead(t *testing.T) {
	dir := tempDir(t)
	var out, errb strings.Builder
	cfg := Config{
		Dir: dir, Home: dir, Env: []string{"PATH=/usr/bin:/bin"},
		Stdout: &out, Stderr: &errb, Stdin: strings.NewReader("line one\nline two\n"),
		ShellName: "/bin/bash",
	}
	code, _ := RunScript(context.Background(), `read -r a; read -r b; echo "$a|$b"`, cfg)
	if code != 0 || out.String() != "line one|line two\n" {
		t.Errorf("code=%d out=%q", code, out.String())
	}
}

func TestRunExit(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `echo before; exit 5; echo after`)
	if got.stdout != "before\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
	if got.code != 5 {
		t.Errorf("退出码为 %d", got.code)
	}
}

func TestRunSubshell(t *testing.T) {
	dir := tempDir(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `( cd sub ); pwd`)
	if strings.HasSuffix(strings.TrimSpace(got.stdout), "/sub") {
		t.Errorf("子 shell 的 cd 不应影响外层，得到 %q", got.stdout)
	}
	got = runScript(t, dir, `( echo sub )`)
	if got.stdout != "sub\n" || got.code != 0 {
		t.Errorf("code=%d out=%q", got.code, got.stdout)
	}
}

func TestRunBraceGroupSharedVars(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `x=1; { x=2; }; echo $x; ( x=3 ); echo $x`)
	if got.stdout != "2\n2\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunFuncDefineAndCall(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `greet() { echo "hi $1"; }; greet world`)
	if got.stdout != "hi world\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunFuncReturnAndLocal(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `f() { local v=inner; echo "$v"; return 3; }; v=outer; f; echo "rc=$?"; echo "$v"`)
	if got.stdout != "inner\nrc=3\nouter\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunForLoop(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `for f in a b c; do echo "== $f"; done`)
	if got.stdout != "== a\n== b\n== c\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunForBreakContinue(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `for i in 1 2 3; do [ "$i" = 2 ] && break; echo $i; done`)
	if got.stdout != "1\n" {
		t.Errorf("break 输出为 %q", got.stdout)
	}
	got = runScript(t, dir, `for i in 1 2 3; do [ "$i" = 2 ] && continue; echo $i; done`)
	if got.stdout != "1\n3\n" {
		t.Errorf("continue 输出为 %q", got.stdout)
	}
}

func TestRunWhileLoop(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `i=0; while [ $i -lt 3 ]; do echo "i=$i"; i=$((i+1)); done`)
	if got.stdout != "i=0\ni=1\ni=2\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunIfElifElse(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `x=2; if [ "$x" = 1 ]; then echo one; elif [ "$x" = 2 ]; then echo two; else echo other; fi`)
	if got.stdout != "two\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunSetMinusE(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `set -e; false; echo unreachable`)
	if got.stdout != "" || got.code != 1 {
		t.Errorf("code=%d out=%q", got.code, got.stdout)
	}
}

func TestRunSyntaxError(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `;`)
	if got.code != 2 {
		t.Errorf("退出码为 %d", got.code)
	}
	if !strings.Contains(got.stderr, "syntax error near unexpected token `;'") {
		t.Errorf("stderr 为 %q", got.stderr)
	}
}

func TestRunEmptyScript(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, "")
	if got.code != 0 || got.stdout != "" {
		t.Errorf("code=%d out=%q", got.code, got.stdout)
	}
}

func TestRunTilde(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `echo ~ | grep -c .`)
	if strings.TrimSpace(got.stdout) != "1" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunSemicolonOnly(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, "echo a; echo b")
	if got.stdout != "a\nb\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunSetMinusEChains(t *testing.T) {
	dir := tempDir(t)
	cases := []struct {
		src  string
		out  string
		code int
	}{
		{"set -e; false && echo x; echo ok", "ok\n", 0},
		{"set -e; true && false; echo bad", "", 1},
		{"set -e; false || true; echo ok", "ok\n", 0},
		{"set -e; true || false; echo ok", "ok\n", 0},
		{"set -e; false && true; echo ok", "ok\n", 0},
	}
	for _, c := range cases {
		got := runScript(t, dir, c.src)
		if got.stdout != c.out || got.code != c.code {
			t.Errorf("%q 得到 out=%q code=%d，期望 out=%q code=%d", c.src, got.stdout, got.code, c.out, c.code)
		}
	}
}

func TestRunSetMinusECondition(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `set -e; if false; then echo x; fi; echo ok`)
	if got.stdout != "ok\n" || got.code != 0 {
		t.Errorf("条件上下文不应触发 errexit: out=%q code=%d", got.stdout, got.code)
	}
}

func TestRunNestedControlFlow(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `for f in a b; do if [ "$f" = a ]; then echo first; else echo rest; fi; done`)
	if got.stdout != "first\nrest\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunWhileReadLines(t *testing.T) {
	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("l1\nl2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `while read -r line; do echo "L:$line"; done < f.txt`)
	if got.stdout != "L:l1\nL:l2\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunForCmdSubList(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `for n in $(printf 'p\nq\n'); do echo "-$n"; done`)
	if got.stdout != "-p\n-q\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunForGlob(t *testing.T) {
	dir := tempDir(t)
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := runScript(t, dir, `for f in *.txt; do echo "$f"; done`)
	if got.stdout != "a.txt\nb.txt\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}

func TestRunBreakDepth(t *testing.T) {
	dir := tempDir(t)
	got := runScript(t, dir, `for i in 1 2; do for j in a b; do break 2; done; echo never; done; echo done`)
	if got.stdout != "done\n" {
		t.Errorf("输出为 %q", got.stdout)
	}
}
