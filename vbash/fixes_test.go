package vbash

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestHeredocBodyCommandSubstitution(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "cat <<EOF\ncmd=[$(echo inner)]\nnested=[$(echo $(echo deep))]\nEOF")
	if out.code != 0 {
		t.Fatalf("heredoc 体内 $( ) 应展开，退出码 %d，stderr %q", out.code, out.stderr)
	}
	want := "cmd=[inner]\nnested=[deep]\n"
	if out.stdout != want {
		t.Errorf("输出为 %q，期望 %q", out.stdout, want)
	}
}

func TestHeredocBodyArithmetic(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "x=5\ncat <<EOF\n[$((2+3))] [$((x*2))]\nEOF")
	if out.stdout != "[5] [10]\n" {
		t.Errorf("输出为 %q", out.stdout)
	}
}

func TestHeredocBodyBackslashEscapes(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "cat <<EOF\na\\$b \\\\c \\`d e\\f\nEOF")
	want := "a$b \\c `d e\\f\n"
	if out.stdout != want {
		t.Errorf("输出为 %q，期望 %q", out.stdout, want)
	}
}

func TestHeredocBodyLineContinuation(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "cat <<EOF\nline1 \\\nline2\nEOF")
	if out.stdout != "line1 line2\n" {
		t.Errorf("行尾反斜杠应接成一行，输出 %q", out.stdout)
	}
}

func TestHeredocBodyQuotesAreLiteral(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "cat <<EOF\nhe said \"hi\" and 'yo'\nEOF")
	if out.stdout != "he said \"hi\" and 'yo'\n" {
		t.Errorf("体内引号应按字面输出，实际 %q", out.stdout)
	}
}

func TestHeredocQuotedBodyStillLiteral(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "cat <<'EOF'\n[$(echo inner)] [$((2+3))]\nEOF")
	if out.code != 0 {
		t.Fatalf("引用终止符时体内不展开，退出码 %d stderr %q", out.code, out.stderr)
	}
	if out.stdout != "[$(echo inner)] [$((2+3))]\n" {
		t.Errorf("输出为 %q", out.stdout)
	}
}

func TestTestBuiltinNegativeIntegers(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "[ -2 -lt 0 ] && echo neg\n[ -2 -eq -2 ] && echo eq\n[ 0 -gt -1 ] && echo gt")
	if out.stdout != "neg\neq\ngt\n" {
		t.Errorf("负数比较失败，输出 %q stderr %q", out.stdout, out.stderr)
	}
	if out.stderr != "" {
		t.Errorf("不应有诊断: %q", out.stderr)
	}
}

func TestTestBuiltinLeadingZeroIsDecimal(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "[ 010 -eq 10 ] && echo decimal")
	if out.stdout != "decimal\n" {
		t.Errorf("test 的 010 应按十进制处理，输出 %q", out.stdout)
	}
}

func TestTestBuiltinNonNumericDiagnostic(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "[ abc -eq 3 ]")
	if out.code != 2 {
		t.Errorf("退出码应为 2，实际 %d", out.code)
	}
	if !strings.Contains(out.stderr, "abc: integer expected") {
		t.Errorf("诊断文案不符: %q", out.stderr)
	}
}

func TestExitNegativeWraps(t *testing.T) {
	dir := tempDir(t)
	if out := runScript(t, dir, "exit -1"); out.code != 255 {
		t.Errorf("exit -1 应为 255，实际 %d", out.code)
	}
	if out := runScript(t, dir, "exit +3"); out.code != 3 {
		t.Errorf("exit +3 应为 3，实际 %d", out.code)
	}
}

func TestExitNonNumericContinues(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "exit abc; echo after")
	if out.stdout != "after\n" {
		t.Errorf("非数值参数不应终止脚本，输出 %q", out.stdout)
	}
	if !strings.Contains(out.stderr, "exit: abc: numeric argument required") {
		t.Errorf("缺少诊断: %q", out.stderr)
	}
}

func TestExitTooManyArguments(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "exit 1 2; echo after")
	if out.code != 1 {
		t.Errorf("exit 1 2 退出码应为 1，实际 %d", out.code)
	}
	if out.stdout != "" {
		t.Errorf("参数过多应终止脚本，输出 %q", out.stdout)
	}
	if !strings.Contains(out.stderr, "exit: too many arguments") {
		t.Errorf("缺少诊断: %q", out.stderr)
	}
}

func TestBreakOutsideLoopContinues(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "echo a; break; echo b")
	if out.stdout != "a\nb\n" {
		t.Errorf("循环外 break 不应吞掉后续命令，输出 %q", out.stdout)
	}
	if !strings.Contains(out.stderr, "only meaningful in a `for', `while', or `until' loop") {
		t.Errorf("缺少诊断: %q", out.stderr)
	}
	if out.code != 0 {
		t.Errorf("退出码应为 0，实际 %d", out.code)
	}
}

func TestBreakInFunctionInsideLoop(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "f() { break; echo body; }\nfor i in 1 2; do f; echo \"loop$i\"; done")
	if out.stdout != "body\nloop1\nbody\nloop2\n" {
		t.Errorf("函数内 break 不应中断外层循环，输出 %q", out.stdout)
	}
}

func TestForWithoutIn(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -- a b\nfor x; do echo $x; done")
	if out.stdout != "a\nb\n" {
		t.Errorf("省略 in 应遍历位置参数，输出 %q", out.stdout)
	}
}

func TestEchoBackslashC(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "echo -e \"x\\cy\"; echo tail")
	if out.stdout != "xtail\n" {
		t.Errorf("\\c 应抑制换行，输出 %q", out.stdout)
	}
}

func TestEchoOctalEscape(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "echo -e \"a\\0101b\"")
	if out.stdout != "aAb\n" {
		t.Errorf("\\0NNN 应解释为八进制，输出 %q", out.stdout)
	}
}

func TestStarJoinsWithIFSFirstChar(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "IFS=,\nset -- a b c\necho \"[$*] [$@]\"")
	if out.stdout != "[a,b,c] [a b c]\n" {
		t.Errorf("输出为 %q", out.stdout)
	}
}

func TestArithEmbeddedExpansions(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "v=4\necho $(( ${v} + 1 )) $(( 1 + $(( 2 )) )) $(( $(echo 3) + 1 ))")
	if out.stdout != "5 3 4\n" {
		t.Errorf("算术内嵌展开失败，输出 %q stderr %q", out.stdout, out.stderr)
	}
}

func TestSetPipefail(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -o pipefail\nfalse | true\necho $?")
	if out.stdout != "1\n" {
		t.Errorf("pipefail 未生效，输出 %q", out.stdout)
	}
}

func TestSetNounset(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -u\necho \"$nope\"\necho unreached")
	if out.code != 127 {
		t.Errorf("未绑定变量退出码应为 127，实际 %d", out.code)
	}
	if !strings.Contains(out.stderr, "nope: unbound variable") {
		t.Errorf("缺少诊断: %q", out.stderr)
	}
	if strings.Contains(out.stdout, "unreached") {
		t.Errorf("应终止脚本: %q", out.stdout)
	}
}

func TestSetNounsetDefaultExpansionStillWorks(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -u\necho \"[${nope:-fallback}]\"")
	if out.stdout != "[fallback]\n" {
		t.Errorf(":- 应绕过 nounset，输出 %q stderr %q", out.stdout, out.stderr)
	}
}

func TestSetOptionClustersKeepPositional(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -- a b\nset -uo pipefail\necho \"[$1] $#\"")
	if out.stdout != "[a] 2\n" {
		t.Errorf("选项簇不应清空位置参数，输出 %q", out.stdout)
	}
}

func TestDollarDollarIsHostPid(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "echo $(( $$ > 0 ))")
	if out.stdout != "1\n" {
		t.Errorf("$$ 应为非零宿主 pid，输出 %q", out.stdout)
	}
}

func TestKillRefusesSelfAndGroup(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "kill $$; echo rc=$?\nkill 0; echo rc=$?")
	if !strings.Contains(out.stdout, "rc=1\nrc=1") {
		t.Errorf("kill $$/kill 0 应被拒绝，输出 %q stderr %q", out.stdout, out.stderr)
	}
}

func TestKillSignalLookup(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "kill -l 9; kill -l KILL; kill -l TERM")
	if out.stdout != "KILL\n9\n15\n" {
		t.Errorf("信号名查表失败，输出 %q", out.stdout)
	}
}

func TestPrintfPercentQ(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "printf '%q %q\\n' 'a b' 'ab'")
	if out.stdout != "a\\ b ab\n" {
		t.Errorf("%%q 应与 bash 逐字节一致，输出 %q", out.stdout)
	}
}

func TestSubstringExpansion(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "v=abcdef\necho \"${v:1}\" \"${v:1:3}\" \"${v: -2}\" \"${v:2:-1}\"")
	if out.stdout != "bcdef bcd ef cde\n" {
		t.Errorf("子串展开失败，输出 %q stderr %q", out.stdout, out.stderr)
	}
}

func TestSubstringNegativeBeyondLength(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "v=abc\necho \"${v:0:-4}\"")
	if out.code == 0 {
		t.Errorf("负长度越界应报错，实际退出码 0，输出 %q", out.stdout)
	}
}

func TestCaseConversionExpansion(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "v=aBc\necho \"${v^^} ${v,,} ${v^} ${v,}\"")
	if out.stdout != "ABC abc ABc aBc\n" {
		t.Errorf("大小写转换失败，输出 %q", out.stdout)
	}
}

func TestIndirectExpansion(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "x=hi\nv=x\necho \"${!v}\"")
	if out.stdout != "hi\n" {
		t.Errorf("间接展开失败，输出 %q", out.stdout)
	}
}

func TestArithBaseConstant(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "echo $((2#101)) $((16#ff)) $((8#17))")
	if out.stdout != "5 255 15\n" {
		t.Errorf("base#num 失败，输出 %q", out.stdout)
	}
}

func TestArithIncrement(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "n=5\necho $((n++)) $n\necho $((n--)) $n\necho $((++n)) $n")
	if out.stdout != "5 6\n6 5\n6 6\n" {
		t.Errorf("自增语义不符，输出 %q", out.stdout)
	}
}

func TestArithPower(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "echo $((2**10)) $((2**3**2)) $((1+2**3))")
	if out.stdout != "1024 512 9\n" {
		t.Errorf("幂运算失败，输出 %q", out.stdout)
	}
}

func TestAssignPrefixKeepsBuiltinSideEffects(t *testing.T) {
	dir := tempDir(t)
	if err := os.WriteFile(dir+"/f", []byte("a:b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runScript(t, dir, "IFS=: read p q < f\necho \"[$p][$q]\"")
	if out.stdout != "[a][b]\n" {
		t.Errorf("赋值前缀不得吞掉 read 写入的变量，输出 %q stderr %q", out.stdout, out.stderr)
	}
}

func TestAssignPrefixItselfDoesNotPersist(t *testing.T) {
	dir := tempDir(t)
	if err := os.WriteFile(dir+"/f", []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runScript(t, dir, "unset FOO p\nFOO=bar read p < f\necho \"p=[$p] FOO=[$FOO]\"")
	if out.stdout != "p=[x] FOO=[]\n" {
		t.Errorf("前缀变量应不持久、内建写入应保留，输出 %q", out.stdout)
	}
}

func TestAssignPrefixExportSideEffect(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "FOO=bar export E=1\necho \"E=[$E] FOO=[$FOO]\"")
	if out.stdout != "E=[1] FOO=[]\n" {
		t.Errorf("export 的副作用应保留，输出 %q", out.stdout)
	}
}

func TestAssignPrefixRestoresPreviousValue(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "V=prev\nV=pre true\necho \"V=[$V]\"")
	if out.stdout != "V=[prev]\n" {
		t.Errorf("前缀结束后应恢复原值，输出 %q", out.stdout)
	}
}

func TestAssignPrefixLeftToRight(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "unset a b\na=1 b=$((a+1)) sh -c 'echo $a $b'")
	if out.stdout != "1 2\n" {
		t.Errorf("前缀应按左到右求值，输出 %q stderr %q", out.stdout, out.stderr)
	}
}

func TestPrintfVAssigns(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "printf -v v '%s-%s' a b\nprintf -v n '%03d' 7\necho \"[$v][$n]\"")
	if out.stdout != "[a-b][007]\n" {
		t.Errorf("printf -v 应写变量且不输出，输出 %q", out.stdout)
	}
}

func TestPrintfVInvalidIdentifier(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "printf -v '1bad' x\necho \"rc=$?\"")
	if out.code != 0 || out.stdout != "rc=1\n" {
		t.Errorf("坏标识符应返回 1 并继续，输出 %q rc=%d", out.stdout, out.code)
	}
	if !strings.Contains(out.stderr, "not a valid identifier") {
		t.Errorf("缺少诊断: %q", out.stderr)
	}
}

func TestNounsetBracedUnbound(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -u\necho \"${nope}\"\necho unreached")
	if out.code != 127 {
		t.Errorf("${VAR} 未绑定应退出 127，实际 %d", out.code)
	}
	if !strings.Contains(out.stderr, "nope: unbound variable") {
		t.Errorf("缺少诊断: %q", out.stderr)
	}
	if strings.Contains(out.stdout, "unreached") {
		t.Errorf("应终止脚本: %q", out.stdout)
	}
}

func TestNounsetBracedOps(t *testing.T) {
	dir := tempDir(t)
	for _, src := range []string{"${nope^^}", "${#nope}", "${nope:0:2}", "${nope#x}", "${nope/x/y}"} {
		out := runScript(t, dir, "set -u\necho \""+src+"\"")
		if out.code != 127 {
			t.Errorf("%s 应触发 nounset，退出码 %d stdout %q", src, out.code, out.stdout)
		}
	}
}

func TestNounsetDefaultOpsBypass(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -u\necho \"[${nope:-d}][${nope-d}][${nope:+x}][${nope+x}]\"")
	if out.stdout != "[d][d][][]\n" {
		t.Errorf("带默认值的形态应绕过 nounset，输出 %q stderr %q", out.stdout, out.stderr)
	}
}

func TestNounsetBangNameHasDollar(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "set -u\necho $!")
	if !strings.Contains(out.stderr, "$!: unbound variable") {
		t.Errorf("$! 未绑定应报 $!:，实际 %q", out.stderr)
	}
	out = runScript(t, dir, "set -u\necho \"${!}\"")
	if !strings.Contains(out.stderr, "!: unbound variable") {
		t.Errorf("${!} 未绑定应报 !:，实际 %q", out.stderr)
	}
}

func TestBrokenPipeWriteIsSilent(t *testing.T) {
	dir := tempDir(t)
	for _, src := range []string{
		"echo hi | true",
		"printf 'x\\n' | true",
		"yes 2>/dev/null | head -2",
		"for i in 1 2 3; do echo $i; done | head -1",
	} {
		out := runScript(t, dir, src)
		if strings.Contains(out.stderr, "broken pipe") {
			t.Errorf("%q 不应产生 broken pipe 诊断: %q", src, out.stderr)
		}
		if out.code != 0 {
			t.Errorf("%q 退出码应为 0，实际 %d stderr %q", src, out.code, out.stderr)
		}
	}
}

func TestBrokenPipeKeepsRealWriteErrors(t *testing.T) {
	dir := tempDir(t)
	out := runScript(t, dir, "exec 3>/dev/null\nexec 3>&-\necho hi >&3")
	if !strings.Contains(out.stderr, "Bad file descriptor") && !strings.Contains(out.stderr, "write error") {
		t.Errorf("真实写错误仍应报出，实际 %q", out.stderr)
	}
}

// read 内建曾经在读 nil stdin 时 panic：NewRunner 把 cfg.Stdin 原样塞进 IO.In，
// IO.bufferedInput 用 bufio.NewReader(nil) 包成非 nil 的 *bufio.Reader，
// readLine 里的 nil 守卫因此失效，ReadString 现场踩空指针。
// 口径：未注入 Stdin 等同空输入（读即 EOF，等价 /dev/null），不 panic。
func TestNilStdinIsEmptyInput(t *testing.T) {
	dir := tempDir(t)
	cfg := Config{
		Dir:       dir,
		Home:      dir,
		Env:       []string{"PATH=/usr/local/bin:/usr/bin:/bin"},
		ShellName: "/bin/bash",
		// Stdout/Stderr/Stdin 全不注入：既要能跑，也不能 panic
	}
	var out, errb strings.Builder
	cfg.Stdout, cfg.Stderr = &out, &errb

	code, err := RunScript(context.Background(), `read x; echo "got=[$x]"`, cfg)
	if err != nil {
		t.Fatalf("nil stdin 下 read 不应返回错误: %v", err)
	}
	if code != 0 {
		t.Errorf("read 遇 EOF 应返回 0（变量置空），实际退出码 %d，stderr %q", code, errb.String())
	}
	if want := "got=[]\n"; out.String() != want {
		t.Errorf("输出为 %q，期望 %q", out.String(), want)
	}
}

func TestNilStdinExitCodeMatchesDevNull(t *testing.T) {
	dir := tempDir(t)
	base := Config{Dir: dir, Home: dir, Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin"}, ShellName: "/bin/bash"}
	var nilOut, nilErr strings.Builder
	cfgNil := base
	cfgNil.Stdout, cfgNil.Stderr = &nilOut, &nilErr

	var devOut, devErr strings.Builder
	cfgDev := base
	cfgDev.Stdout, cfgDev.Stderr = &devOut, &devErr
	cfgDev.Stdin = strings.NewReader("")

	for _, src := range []string{
		`read a; read b; read c; echo done`,
		`while read -r l; do echo "L:$l"; done; echo eof`,
		`read -r x; echo "rc=$?"`,
	} {
		nilOut.Reset()
		devOut.Reset()
		codeNil, errNil := RunScript(context.Background(), src, cfgNil)
		codeDev, errDev := RunScript(context.Background(), src, cfgDev)
		if codeNil != codeDev || (errNil == nil) != (errDev == nil) {
			t.Errorf("%q: nil stdin(%d,%v) 与空 reader(%d,%v) 不一致", src, codeNil, errNil, codeDev, errDev)
		}
		if nilOut.String() != devOut.String() {
			t.Errorf("%q: nil stdin 输出 %q，空 reader 输出 %q", src, nilOut.String(), devOut.String())
		}
	}
}

// Run 与 NewRunner 是另外两条独立入口，nil stdin 同样不能 panic。
func TestNilStdinOtherEntryPoints(t *testing.T) {
	dir := tempDir(t)
	cfg := Config{Dir: dir, Home: dir, Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin"}, ShellName: "/bin/bash"}

	var out strings.Builder
	cfg.Stdout = &out
	res, err := Run(context.Background(), `read x; echo ok`, cfg)
	if err != nil {
		t.Fatalf("Run 在 nil stdin 下不应报错: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "ok\n" {
		t.Errorf("Run 结果 %+v", res)
	}

	// NewRunner 这条入口只验不 panic 与退出码：它的输出由 Runner.IO 承载，不收集。
	r := NewRunner(context.Background(), cfg)
	list, perr := Parse(`read y; echo ok`)
	if perr != nil {
		t.Fatalf("解析失败: %v", perr)
	}
	code, err := r.RunList(list)
	if err != nil || code != 0 {
		t.Fatalf("NewRunner: code=%d err=%v", code, err)
	}
}

// IO 字段对外可见，调用方可能在构造之后把 In 置 nil；input 自身也要挡住。
func TestInputNilGuard(t *testing.T) {
	x := &IO{}
	in := x.input()
	if in == nil {
		t.Fatal("input 返回 nil")
	}
	if n, err := in.Read(make([]byte, 4)); n != 0 || err != io.EOF {
		t.Errorf("nil In 应读到 EOF，实际 n=%d err=%v", n, err)
	}

	// fd 0 原样返回：绝不换成 *bufio.Reader（缓冲区会预读、且让外部命令拿不到 *os.File）。
	if _, ok := in.(*bufio.Reader); ok {
		t.Error("input 不得把 fd 0 包成 bufio.Reader")
	}
	x.In = strings.NewReader("a\n")
	if x.input() != x.In {
		t.Error("已注入的 reader 应原样返回")
	}
}

// `read` 不得预读 fd 0 之后的输入：`read x; head -1` 里的 head 要还能读到下一行。
func TestReadDoesNotConsumeNextCommandInput(t *testing.T) {
	dir := tempDir(t)
	cfg := Config{
		Dir: dir, Home: dir, Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin"},
		ShellName: "/bin/bash", Stdin: strings.NewReader("first\nsecond\n"),
	}
	var out strings.Builder
	cfg.Stdout, cfg.Stderr = &out, &out
	if _, err := RunScript(context.Background(), `read -r x; echo "x=$x"; head -1`, cfg); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if want := "x=first\nsecond\n"; out.String() != want {
		t.Errorf("输出 %q，期望 %q（read 预读了后续命令的输入）", out.String(), want)
	}
}
