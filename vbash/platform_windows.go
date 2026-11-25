//go:build windows

package vbash

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const statusControlCExit = 0xC000013A

var windowsExecSuffixes = []string{".exe", ".cmd", ".bat", ".com", ""}

var plat = fillPlatform(platformOps{
	DevNull:      []string{"/dev/null", "nul"},
	DevNullFold:  true,
	DefaultPath:  "/c/Windows/system32:/c/Windows",
	PathSep:      ";",
	SplitPath:    windowsSplitPath,
	ExecSuffixes: windowsExecSuffixes,
	FromHost:     windowsFromHost,
	ToHost:       windowsToHost,
	Display:      windowsDisplayPath,
	IsExec:       windowsIsExec,
	MatchName:    windowsMatchName,
	Argv0:        windowsArgv0,
	Arg:          windowsHostArg,
	DriveOf:      windowsDriveOf,
	SignalName:   windowsSignalName,
	ExitCode:     windowsExitCode,
})

func windowsFromHost(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, `\`, "/")
	// 内部形态的盘符输入（/D/per）统一小写：cd 之后 pwd/$PWD 的形态不随输入
	// 大小写漂移（目录名本身的大小写仍按输入保留，Windows 上不可区分）。
	if len(s) >= 2 && s[0] == '/' && isASCIILetter(s[1]) && (len(s) == 2 || s[2] == '/') {
		return "/" + strings.ToLower(s[1:2]) + s[2:]
	}
	if len(s) >= 2 && s[1] == ':' && isASCIILetter(s[0]) {
		drive := strings.ToLower(s[:1])
		rest := strings.TrimPrefix(s[2:], "/")
		if rest == "" {
			return "/" + drive
		}
		return "/" + drive + "/" + rest
	}
	return s
}

// windowsSplitPath 把 PATH 字符串切成宿主形态的段数组（尚未 FromHost）。
//
// 两个 Windows 特有形态必须识别：
//   - 盘符冒号：C:/a:D:/b 是两段，不是四段；
//   - 宿主列表分隔符 ;：C:\a;D:\b 是两段。Windows 上 ; 就是 PATH 分隔符
//     （与 filepath.SplitList 一致），内部形态用 :，所以出现 ; 只可能来自
//     宿主形态列表。代价：目录名里字面含 ; 的条目会被切开——Windows 下极
//     罕见，且旧实现同样无法处理，登记为已知限制。
//
// 段首盘符判定（单次左到右扫描，无回溯）：: 位于段内第 2 个位置、前一个是
// ASCII 字母、后一个是 / 或 \ 或串尾时，属盘符冒号，不切。只认这三种完整
// 盘符前缀（X:/、X:\、X: 结尾），于是 a:D:/b 切成 a + D:/b，而 D:x 切成
// D + x —— 后者不制造「意外的 /d/x 命中」，宁可 miss。
func windowsSplitPath(s string) []string {
	if s == "" {
		return []string{""}
	}
	if strings.ContainsRune(s, ';') {
		var out []string
		for _, part := range strings.Split(s, ";") {
			out = append(out, splitDriveColons(part)...)
		}
		return out
	}
	return splitDriveColons(s)
}

func splitDriveColons(s string) []string {
	var out []string
	start, segStart := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] != ':' {
			continue
		}
		if i == segStart+1 && isASCIILetter(s[segStart]) {
			if i+1 == len(s) || s[i+1] == '/' || s[i+1] == '\\' {
				continue // 盘符冒号
			}
		}
		out = append(out, s[start:i])
		start = i + 1
		segStart = i + 1
	}
	return append(out, s[start:])
}

// windowsDisplayPath 把内部形态渲染成对外形态，供 pwd、$PWD、cd - 回显、
// ~ 与 -v/-V 使用。输出**宿主形态但用正斜杠**（D:/per/vis）：原生工具、
// msys 工具、字符串流（写进 go.mod/JSON/脚本）三方都认，且不踩反斜杠转义。
//
// 盘符形态（/d/...）与其它绝对路径（/tmp、/usr/bin）都跟随 windowsToHost 的
// 显式映射，保证 pwd/$PWD 看到的与实际落盘位置一致；UNC 保持 //server/share
// 原样（正斜杠，写进字符串流不会踩反斜杠转义）。
func windowsDisplayPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "//") {
		return p
	}
	if len(p) >= 2 && p[0] == '/' && isASCIILetter(p[1]) && (len(p) == 2 || p[2] == '/') {
		drive := strings.ToUpper(p[1:2])
		if len(p) == 2 {
			return drive + ":/"
		}
		return drive + ":" + p[2:]
	}
	if strings.HasPrefix(p, "/") {
		// 非盘符绝对路径（/tmp、/usr/bin）跟随 windowsToHost 的显式映射，
		// 保证 pwd/$PWD 看到的与实际落盘位置一致；换回正斜杠。
		return strings.ReplaceAll(windowsToHost("", p), `\`, "/")
	}
	return p
}

func windowsToHost(cwd, p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "//") {
		return `\\` + filepath.FromSlash(strings.TrimPrefix(p, "//"))
	}
	if len(p) >= 2 && p[0] == '/' && isASCIILetter(p[1]) && (len(p) == 2 || p[2] == '/') {
		return strings.ToUpper(p[1:2]) + ":" + `\` + filepath.FromSlash(strings.TrimPrefix(p[2:], "/"))
	}
	if strings.HasPrefix(p, "/") {
		// /tmp 显式映射到 %TEMP%：msys 工具、$TEMP 字面量与 vbash 内部的重定向/
		// test/glob 因此看到同一个目录，不再出现「三世界」。
		if p == "/tmp" || strings.HasPrefix(p, "/tmp/") {
			rest := strings.TrimPrefix(p, "/tmp")
			if rest == "" {
				return os.TempDir()
			}
			return filepath.Join(os.TempDir(), filepath.FromSlash(rest))
		}
		// 其它非盘符绝对路径：显式落到进程当前盘的根，而不是留下 \tmp 这种
		// 盘符相对形式（后者会被原生工具按「当前盘」解释，取决于进程启动盘）。
		if root, err := filepath.Abs(`\`); err == nil {
			return filepath.Join(root, filepath.FromSlash(p))
		}
		return filepath.FromSlash(p)
	}
	if filepath.IsAbs(cwd) {
		return filepath.Join(windowsToHost("", cwd), filepath.FromSlash(p))
	}
	return filepath.FromSlash(p)
}

func windowsDriveOf(s string) (string, bool) {
	if len(s) < 2 || s[1] != '/' || !isASCIILetter(s[0]) {
		return "", false
	}
	root := strings.ToUpper(s[:1]) + `:\`
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", false
	}
	return strings.ToLower(s[:1]) + s[1:], true
}

// windowsIsExec 按 PATHEXT 语义判可执行：**后缀必须在清单内**才算。
//
// 不能复用 ExecSuffixes 的空串条目：那个 "" 是「查找时允许不带后缀的候选」
// （`ls` 要先试 `ls.exe`，试不到再试裸名），而**裸名文件本身在 Windows 上不可执行**
// （没有执行位，CreateProcess 只认 PATHEXT 里的类型）。混用两者会把
// `d1/nx` 这种无扩展名普通文件判成可执行。
func windowsIsExec(name string, st os.FileInfo) bool {
	if st.IsDir() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	for _, s := range windowsExecSuffixes {
		if s != "" && ext == s {
			return true
		}
	}
	return false
}

// windowsHostArg 把单个 argv 参数从内部形态翻成宿主形态，供原生工具使用。
//
// 只翻确凿的三种形态：盘符路径（/d/x）、UNC（//server/share）、/tmp（映射到
// %TEMP%）。含 冒号 的值（URL、已是宿主形态的 D:/x、PATH 列表）与非盘符绝对
// 路径（/usr/bin 是 msys 专用）一律不动——后者交给 msys 工具自己解释，翻了反而坏。
func windowsHostArg(s string) (string, bool) {
	if s == "" || strings.ContainsRune(s, ':') {
		return "", false
	}
	if strings.HasPrefix(s, "//") {
		return windowsToHost("", s), true
	}
	if len(s) >= 2 && s[0] == '/' && isASCIILetter(s[1]) && (len(s) == 2 || s[2] == '/') {
		return windowsToHost("", s), true
	}
	if s == "/tmp" || strings.HasPrefix(s, "/tmp/") {
		return windowsToHost("", s), true
	}
	return "", false
}

func windowsArgv0(name string) string {
	if !strings.ContainsRune(name, '/') {
		return name
	}
	return windowsToHost("", name)
}

func windowsMatchName(pattern, name string) bool {
	return matchGlob(strings.ToLower(pattern), strings.ToLower(name))
}

func windowsExitCode(err *exec.ExitError) int {
	code := err.ExitCode()
	if code < 0 {
		return 1
	}
	if uint32(code) == statusControlCExit {
		return 130
	}
	return code & 0xff
}

func windowsSignalName(n int) string {
	switch n {
	case 2:
		return "Interrupt"
	case 9:
		return "Killed"
	}
	return "Terminated"
}
