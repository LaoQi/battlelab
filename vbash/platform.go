package vbash

import (
	"os"
	"os/exec"
	"path"
	"strings"
)

type platformOps struct {
	DevNull      []string
	DevNullFold  bool
	DefaultPath  string
	PathSep      string
	SplitPath    func(string) []string
	ExecSuffixes []string
	FromHost     func(string) string
	ToHost       func(cwd, target string) string
	Display      func(string) string
	IsExec       func(name string, st os.FileInfo) bool
	MatchName    func(pattern, name string) bool
	Argv0        func(string) string
	// Arg 把 argv[1:] 里的内部形态绝对路径翻成宿主形态；ok=false 表示原样。
	// nil 表示该平台不需要（POSIX）。
	Arg              func(string) (string, bool)
	DriveOf          func(string) (string, bool)
	ExitCode         func(*exec.ExitError) int
	SignalNumber     func(*exec.ExitError) int
	SignalName       func(int) string
	DeliverSignal    func(pid, sig int) bool
	SetProcessGroup  func(*exec.Cmd)
	KillProcessGroup func(pid int, sig int) error
	CanReap          bool
}

func fillPlatform(p platformOps) platformOps {
	if p.DevNull == nil {
		p.DevNull = []string{"/dev/null"}
	}
	if p.DefaultPath == "" {
		p.DefaultPath = "/usr/local/bin:/usr/bin:/bin"
	}
	if p.PathSep == "" {
		p.PathSep = ":"
	}
	if p.SplitPath == nil {
		p.SplitPath = func(s string) []string { return strings.Split(s, ":") }
	}
	if p.ExecSuffixes == nil {
		p.ExecSuffixes = []string{""}
	}
	if p.FromHost == nil {
		p.FromHost = identityPath
	}
	if p.ToHost == nil {
		p.ToHost = func(_, target string) string { return target }
	}
	if p.Display == nil {
		p.Display = identityPath
	}
	if p.IsExec == nil {
		p.IsExec = func(_ string, st os.FileInfo) bool { return !st.IsDir() && st.Mode()&0o111 != 0 }
	}
	if p.Argv0 == nil {
		p.Argv0 = identityPath
	}
	if p.MatchName == nil {
		p.MatchName = matchGlob
	}
	if p.SignalNumber == nil {
		p.SignalNumber = func(*exec.ExitError) int { return 1 }
	}
	if p.DeliverSignal == nil {
		p.DeliverSignal = func(int, int) bool { return false }
	}
	if p.ExitCode == nil {
		p.ExitCode = func(err *exec.ExitError) int {
			if code := err.ExitCode(); code >= 0 {
				return code
			}
			return 128 + p.SignalNumber(err)
		}
	}
	if p.SignalName == nil {
		p.SignalName = func(int) string { return "Terminated" }
	}
	if p.SetProcessGroup == nil {
		p.SetProcessGroup = func(*exec.Cmd) {}
	}
	if p.KillProcessGroup == nil {
		p.KillProcessGroup = func(int, int) error { return os.ErrProcessDone }
	}
	return p
}

func identityPath(s string) string { return s }

func isDevNull(target string) bool {
	for _, n := range plat.DevNull {
		if plat.DevNullFold {
			if strings.EqualFold(target, n) {
				return true
			}
			continue
		}
		if target == n {
			return true
		}
	}
	return false
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isASCIILetterRune 判 ASCII 字母。byte 版本对 rune 会截断（š 的低字节是 'a'），
// 凡输入是 rune 的判定必须走这里。
func isASCIILetterRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// DisplayPath 把内部形态路径渲染成对外形态：Windows 上是宿主形态（正斜杠），
// POSIX 上恒等。只用于脚本可见的输出面（pwd、$PWD、cd -、~、command -v/-V、
// export/set 列表）；内部消费者（cd - 的目标、PATH 查找）必须继续用内部形态。
func DisplayPath(p string) string { return plat.Display(p) }

// HostPathArg 把内部形态路径参数（Windows 的 /d/...、//server/share、/tmp）
// 翻成宿主形态，供解释器以外的调用方使用（如 tools/shell 的 cwd 参数）。
// POSIX 或非内部形态返回 ok=false，调用方保持原值。
func HostPathArg(s string) (string, bool) {
	if plat.Arg == nil {
		return "", false
	}
	return plat.Arg(s)
}

func isPathLike(name string) bool {
	return strings.ContainsRune(plat.FromHost(name), '/')
}

func absInternal(dir, target string) string {
	t := plat.FromHost(target)
	if strings.HasPrefix(t, "/") {
		return path.Clean(t)
	}
	if plat.DriveOf != nil {
		if d, ok := plat.DriveOf(t); ok {
			return path.Clean("/" + d)
		}
	}
	d := plat.FromHost(dir)
	if d == "" {
		d = "/"
	}
	return path.Clean(d + "/" + t)
}

func (r *Runner) posixPath(target string) string {
	return absInternal(r.Env.Dir(), target)
}

func (r *Runner) hostPath(target string) string {
	if isDevNull(target) {
		return os.DevNull
	}
	p := r.posixPath(target)
	if isDevNull(p) {
		return os.DevNull
	}
	return plat.ToHost(r.Env.Dir(), p)
}

// HostDir 返回当前工作目录的**宿主形态**。会话宿主进程用它设 exec.Cmd.Dir：
// 协议里 cwd 一律宿主形态，宿主侧因此不需要任何路径形态知识。
func (r *Runner) HostDir() string {
	if r.Env.Dir() == "" {
		return ""
	}
	return plat.ToHost(r.Env.Dir(), r.Env.Dir())
}

func resolveSymlinks(cwd, p string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	cur := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = path.Join(cur, part)
		st, err := os.Lstat(plat.ToHost(cwd, cur))
		if err != nil {
			return p, err
		}
		if st.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(plat.ToHost(cwd, cur))
		if err != nil {
			return p, err
		}
		target = plat.FromHost(target)
		if strings.HasPrefix(target, "/") {
			cur = target
			continue
		}
		cur = path.Join(path.Dir(cur), target)
	}
	return path.Clean(cur), nil
}
