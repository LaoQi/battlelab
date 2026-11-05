package vbash

import (
	"os"
	"strings"
)

func builtinTest(r *Runner, argv []string, io *IO, pos Pos) (int, error) {
	args := argv[1:]
	if argv[0] == "[" {
		if len(args) == 0 || args[len(args)-1] != "]" {
			r.diagfName(io.Err, pos, "[", "missing `]'")
			return 2, nil
		}
		args = args[:len(args)-1]
	}
	ok, err := evalTest(r, args)
	if err != nil {
		if ue, isU := err.(*UnsupportedError); isU {
			return 2, ue
		}
		r.diagfName(io.Err, pos, argv[0], "%s", err.Error())
		return 2, nil
	}
	if ok {
		return 0, nil
	}
	return 1, nil
}

func evalTest(r *Runner, args []string) (bool, error) {
	switch len(args) {
	case 0:
		return false, nil
	case 1:
		return args[0] != "", nil
	case 2:
		if args[0] == "!" {
			return args[1] == "", nil
		}
		return unaryTest(r, args[0], args[1])
	case 3:
		if args[0] == "!" {
			v, err := evalTest(r, args[1:])
			return !v, err
		}
		return binaryTest(args[0], args[1], args[2])
	case 4:
		if args[0] == "!" {
			v, err := evalTest(r, args[1:])
			return !v, err
		}
		if args[0] == "(" && args[3] == ")" {
			return evalTest(r, args[1:3])
		}
	}
	return false, runtimeErrf(Pos{}, "too many arguments")
}

func unaryTest(r *Runner, op, arg string) (bool, error) {
	switch op {
	case "-z":
		return arg == "", nil
	case "-n":
		return arg != "", nil
	case "-e", "-a":
		_, err := os.Stat(r.hostPath(arg))
		return err == nil, nil
	case "-f":
		st, err := os.Stat(r.hostPath(arg))
		return err == nil && st.Mode().IsRegular(), nil
	case "-d":
		st, err := os.Stat(r.hostPath(arg))
		return err == nil && st.IsDir(), nil
	case "-s":
		st, err := os.Stat(r.hostPath(arg))
		return err == nil && st.Size() > 0, nil
	case "-r":
		f, err := os.Open(r.hostPath(arg))
		if err != nil {
			return false, nil
		}
		f.Close()
		return true, nil
	case "-w":
		st, err := os.Stat(r.hostPath(arg))
		return err == nil && st.Mode().Perm()&0o222 != 0, nil
	case "-x":
		host := r.hostPath(arg)
		st, err := os.Stat(host)
		return err == nil && plat.IsExec(host, st), nil
	case "-L", "-h":
		st, err := os.Lstat(r.hostPath(arg))
		return err == nil && st.Mode()&os.ModeSymlink != 0, nil
	}
	return false, runtimeErrf(Pos{}, "%s: unary operator expected", op)
}

func binaryTest(left, op, right string) (bool, error) {
	switch op {
	case "=", "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	case "<":
		return left < right, nil
	case ">":
		return left > right, nil
	case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
		a, ok1 := parseInt(left)
		b, ok2 := parseInt(right)
		if !ok1 || !ok2 {
			return false, runtimeErrf(Pos{}, "integer expression expected")
		}
		switch op {
		case "-eq":
			return a == b, nil
		case "-ne":
			return a != b, nil
		case "-lt":
			return a < b, nil
		case "-le":
			return a <= b, nil
		case "-gt":
			return a > b, nil
		default:
			return a >= b, nil
		}
	}
	return false, runtimeErrf(Pos{}, "%s: binary operator expected", op)
}

var _ = strings.TrimSpace
