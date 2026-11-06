package vbash

import (
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
