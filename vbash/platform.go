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
	ExecSuffixes []string
	FromHost     func(string) string
	ToHost       func(cwd, target string) string
	IsExec       func(name string, st os.FileInfo) bool
	MatchName    func(pattern, name string) bool
	Argv0        func(string) string
	DriveOf      func(string) (string, bool)
	ExitCode     func(*exec.ExitError) int
	SignalNumber func(*exec.ExitError) int
	SignalName   func(int) string
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
	if p.ExecSuffixes == nil {
		p.ExecSuffixes = []string{""}
	}
	if p.FromHost == nil {
		p.FromHost = identityPath
	}
	if p.ToHost == nil {
		p.ToHost = func(_, target string) string { return target }
	}
	if p.IsExec == nil {
		p.IsExec = func(_ string, st os.FileInfo) bool { return st.Mode()&0o111 != 0 }
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

func (r *Runner) hostDir() string {
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
