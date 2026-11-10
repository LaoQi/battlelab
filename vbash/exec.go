package vbash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (r *Runner) runExternal(argv []string, io *IO, pos Pos) (int, error) {
	full, code, ok := r.resolveExternal(argv[0], io, pos)
	if !ok {
		return code, nil
	}
	return r.spawn(full, argv, io, pos)
}

func (r *Runner) resolveExternal(name string, io *IO, pos Pos) (string, int, bool) {
	if r.Exec != nil {
		return name, 0, true
	}
	if isPathLike(name) {
		host := r.hostPath(name)
		st, err := os.Stat(host)
		if err == nil && st.IsDir() {
			r.diagf(io.Err, pos, "%s: Is a directory", name)
			return "", 126, false
		}
		if err == nil {
			if !plat.IsExec(host, st) {
				r.diagf(io.Err, pos, "%s: Permission denied", name)
				return "", 126, false
			}
			return host, 0, true
		}
		if os.IsNotExist(err) {
			r.diagf(io.Err, pos, "%s: No such file or directory", name)
			return "", 127, false
		}
		if os.IsPermission(err) {
			r.diagf(io.Err, pos, "%s: Permission denied", name)
			return "", 126, false
		}
	}
	resolved, err := r.lookPath(name)
	if err != nil {
		r.diagf(io.Err, pos, "%s: command not found", name)
		return "", 127, false
	}
	return resolved, 0, true
}

func (r *Runner) lookPath(name string) (string, error) {
	if r.Exec != nil {
		return name, nil
	}
	if isPathLike(name) {
		host := r.hostPath(name)
		st, err := os.Stat(host)
		if err != nil {
			return "", err
		}
		if !plat.IsExec(host, st) {
			return "", os.ErrPermission
		}
		return host, nil
	}
	pathVar, ok := r.Env.Lookup("PATH")
	if !ok {
		pathVar = plat.DefaultPath
	}
	names := plat.ExecSuffixes
	if hasAnySuffix(name, names) {
		names = []string{""}
	}
	for _, dir := range strings.Split(pathVar, ":") {
		if dir == "" {
			dir = "."
		}
		for _, suffix := range names {
			full := plat.ToHost(r.Env.Dir(), absInternal(r.Env.Dir(), dir+"/"+name+suffix))
			st, err := os.Stat(full)
			if err != nil || !plat.IsExec(full, st) {
				continue
			}
			return full, nil
		}
	}
	return "", os.ErrNotExist
}

func hasAnySuffix(name string, suffixes []string) bool {
	lower := strings.ToLower(name)
	for _, s := range suffixes {
		if s != "" && strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

func (r *Runner) spawn(full string, argv []string, io *IO, pos Pos) (int, error) {
	if r.Exec != nil {
		code, err := r.Exec(r.Ctx, r, argv, io)
		if err != nil {
			return code, err
		}
		return code, nil
	}
	if fds := io.extraFds(); len(fds) > 0 && !r.warnedFd {
		r.warnedFd = true
		r.diagf(io.Err, pos, "fd %s: not passed to external commands (vbash limitation)", joinInts(fds))
	}
	if io.closed(0) && !r.warnedFd {
		r.warnedFd = true
		r.diagf(io.Err, pos, "fd 0: closed stdin not passed to external commands (vbash limitation)")
	}
	cmd := r.newCmd(r.Ctx, full, argv, io)
	err := func() error {
		if err := cmd.Start(); err != nil {
			return err
		}
		if r.bg {
			done := make(chan struct{})
			defer close(done)
			r.fence.register(cmd.Process.Pid, done)
		}
		return cmd.Wait()
	}()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := plat.ExitCode(exitErr)
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

func (r *Runner) newCmd(ctx context.Context, full string, argv []string, io *IO) *exec.Cmd {
	cmd := exec.CommandContext(ctx, full, argv[1:]...)
	cmd.Args[0] = plat.Argv0(argv[0])
	cmd.Dir = r.hostDir()
	cmd.Env = r.Env.Environ()
	cmd.Stdin = io.In
	cmd.Stdout = io.Out
	cmd.Stderr = io.Err
	plat.SetProcessGroup(cmd)
	if r.ConfigureCmd != nil {
		r.ConfigureCmd(cmd, CmdState{Background: r.bg, InPipeline: r.inPipe})
	}
	if r.CancelCmd != nil {
		cmd.Cancel = func() error { return r.CancelCmd(cmd) }
		cmd.WaitDelay = waitDelay
	}
	return cmd
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
	fmt.Fprintf(errw, "%-26s %s\n", plat.SignalName(signum), strings.Join(parts, " "))
}

const waitDelay = 2 * time.Second

func joinInts(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, itoa(n))
	}
	return strings.Join(parts, ",")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
