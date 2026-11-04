package vbash

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

func (r *Runner) runExternal(argv []string, io *IO, pos Pos) (int, error) {
	name := argv[0]
	if strings.Contains(name, "/") {
		full := r.absPath(name)
		if st, err := os.Stat(full); err == nil && st.IsDir() {
			r.diagf(io.Err, pos, "%s: Is a directory", name)
			return 126, nil
		}
		if _, err := os.Stat(full); err == nil {
			if err := checkExecutable(full); err != nil {
				r.diagf(io.Err, pos, "%s: %s", name, err.Error())
				return 126, nil
			}
			return r.spawn(full, argv, io, pos)
		}
	}
	resolved, err := r.lookPath(name)
	if err != nil {
		r.diagf(io.Err, pos, "%s: command not found", name)
		return 127, nil
	}
	return r.spawn(resolved, argv, io, pos)
}

func checkExecutable(full string) error {
	st, err := os.Stat(full)
	if err != nil {
		return errors.New(errText(err))
	}
	if st.IsDir() {
		return errors.New("Is a directory")
	}
	if st.Mode()&0o111 == 0 {
		return errors.New("Permission denied")
	}
	return nil
}

func (r *Runner) lookPath(name string) (string, error) {
	if r.Exec != nil {
		return name, nil
	}
	if strings.Contains(name, "/") {
		return r.absPath(name), nil
	}
	pathVar, ok := r.Env.Lookup("PATH")
	if !ok {
		pathVar = "/usr/local/bin:/usr/bin:/bin"
	}
	for _, dir := range strings.Split(pathVar, ":") {
		if dir == "" {
			dir = "."
		}
		candidate := dir
		if !strings.HasPrefix(dir, "/") {
			candidate = r.absPath(dir)
		}
		full := path.Join(candidate, name)
		st, err := os.Stat(full)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		return full, nil
	}
	return "", os.ErrNotExist
}

func (r *Runner) spawn(full string, argv []string, io *IO, pos Pos) (int, error) {
	if r.Exec != nil {
		code, err := r.Exec(r.Ctx, r, argv, io)
		if err != nil {
			return code, err
		}
		return code, nil
	}
	cmd := exec.CommandContext(r.Ctx, full, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Dir = r.Env.Dir()
	cmd.Env = r.Env.Environ()
	cmd.Stdin = io.In
	cmd.Stdout = io.Out
	cmd.Stderr = io.Err
	if r.ConfigureCmd != nil {
		r.ConfigureCmd(cmd)
	}
	if r.CancelCmd != nil {
		cmd.Cancel = func() error { return r.CancelCmd(cmd) }
		cmd.WaitDelay = waitDelay
	}
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitCodeOf(exitErr)
		if code > 128 {
			r.diagSignal(io.Err, pos, code-128, argv)
		}
		return code, nil
	}
	if r.Ctx.Err() != nil {
		return 130, r.Ctx.Err()
	}
	r.diagf(io.Err, pos, "%s: %s", argv[0], errText(err))
	return 1, nil
}

func (r *Runner) diagSignal(errw io.Writer, pos Pos, signum int, argv []string) {
	if errw == nil || signum <= 0 {
		return
	}
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		if strings.ContainsAny(a, " \t$'\"") || a == "" {
			parts = append(parts, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
			continue
		}
		parts = append(parts, a)
	}
	fmt.Fprintf(errw, "%-26s %s\n", signalName(signum), strings.Join(parts, " "))
}

const waitDelay = 2 * time.Second

func exitCodeOf(err *exec.ExitError) int {
	code := err.ExitCode()
	if code >= 0 {
		return code
	}
	if status, ok := err.Sys().(interface{ Signal() os.Signal }); ok {
		_ = status
	}
	return 128 + signalNumber(err)
}

func signalNumber(err *exec.ExitError) int {
	return sysSignalNumber(err)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
