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
	ExecSuffixes: windowsExecSuffixes,
	FromHost:     windowsFromHost,
	ToHost:       windowsToHost,
	IsExec:       windowsIsExec,
	MatchName:    windowsMatchName,
	Argv0:        windowsArgv0,
	DriveOf:      windowsDriveOf,
	SignalName:   windowsSignalName,
	ExitCode:     windowsExitCode,
})

func windowsFromHost(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, `\`, "/")
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

func windowsIsExec(name string, st os.FileInfo) bool {
	if st.IsDir() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	for _, s := range windowsExecSuffixes {
		if s == "" || ext == s {
			return true
		}
	}
	return false
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
