package vbash

import (
	"os"
	"strconv"
	"strings"
)

var killSignals = []struct {
	num  int
	name string
}{
	{1, "HUP"}, {2, "INT"}, {3, "QUIT"}, {4, "ILL"}, {5, "TRAP"}, {6, "ABRT"},
	{7, "BUS"}, {8, "FPE"}, {9, "KILL"}, {10, "USR1"}, {11, "SEGV"}, {12, "USR2"},
	{13, "PIPE"}, {14, "ALRM"}, {15, "TERM"}, {17, "CHLD"}, {18, "CONT"}, {19, "STOP"},
	{20, "TSTP"}, {21, "TTIN"}, {22, "TTOU"}, {23, "URG"}, {28, "WINCH"},
}

func builtinKill(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	sig := 15
	haveSig := false
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "-l" || a == "-L":
			return listSignals(r, args[1:], io), nil
		case a == "-s" || a == "-n":
			if len(args) < 2 {
				r.diagfName(io.Err, pos, "kill", "usage: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ...")
				return 2, nil
			}
			n, ok := parseSignalSpec(args[1])
			if !ok {
				r.diagfName(io.Err, pos, "kill", "%s: invalid signal specification", args[1])
				return 2, nil
			}
			sig = n
			haveSig = true
			args = args[2:]
		case strings.HasPrefix(a, "-") && len(a) > 1:
			n, ok := parseSignalSpec(a[1:])
			if !ok {
				r.diagfName(io.Err, pos, "kill", "%s: invalid signal specification", a[1:])
				return 2, nil
			}
			sig = n
			haveSig = true
			args = args[1:]
		case a == "--":
			args = args[1:]
			goto targets
		default:
			goto targets
		}
	}
targets:
	if len(args) == 0 {
		r.diagfName(io.Err, pos, "kill", "usage: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ...")
		return 2, nil
	}
	_ = haveSig
	status := 0
	for _, a := range args {
		pid, ok := parseDecimalInt(a)
		if !ok {
			r.diagfName(io.Err, pos, "kill", "%s: arguments must be process or job IDs", a)
			status = 1
			continue
		}
		if guardedPid(pid, sig) {
			r.diagf(io.Err, pos, "kill: (%d) - Operation not permitted", pid)
			status = 1
			continue
		}
		if !plat.DeliverSignal(pid, sig) {
			status = 1
		}
	}
	return status, nil
}

func guardedPid(pid, sig int) bool {
	if sig == 0 {
		return pid < 0
	}
	return pid <= 0 || pid == os.Getpid()
}

func parseSignalSpec(s string) (int, bool) {
	s = strings.ToUpper(strings.TrimPrefix(strings.ToUpper(s), "SIG"))
	if s == "" {
		return 0, false
	}
	if n, ok := parseDigits(s, 10); ok {
		return n, true
	}
	for _, sig := range killSignals {
		if sig.name == s {
			return sig.num, true
		}
	}
	return 0, false
}

func listSignals(r *Runner, args []string, io *IO) int {
	if len(args) > 0 {
		arg := args[0]
		if n, ok := parseDecimalInt(arg); ok {
			for _, sig := range killSignals {
				if sig.num == n {
					io.WriteString(io.Out, sig.name+"\n")
					return 0
				}
			}
			return 1
		}
		if sig, found := parseSignalSpec(arg); found {
			io.WriteString(io.Out, strconv.Itoa(sig)+"\n")
			return 0
		}
		return 1
	}
	var b strings.Builder
	for i, sig := range killSignals {
		if i > 0 && i%5 == 0 {
			b.WriteString("\n")
		}
		b.WriteString(pad2(strconv.Itoa(sig.num)) + ") SIG" + sig.name + "\t")
	}
	b.WriteString("\n")
	io.WriteString(io.Out, b.String())
	return 0
}

func pad2(s string) string {
	if len(s) < 2 {
		return " " + s
	}
	return s
}
