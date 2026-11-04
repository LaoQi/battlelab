package main

import (
	"context"
	"os"

	"github.com/LaoQi/battlelab/vbash"
)

func main() {
	args := os.Args[1:]
	cmd := ""
	hasC := false
	rest := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c":
			hasC = true
			if i+1 < len(args) {
				cmd = args[i+1]
				i++
			}
		default:
			rest = append(rest, args[i])
		}
	}
	if !hasC {
		os.Exit(2)
	}
	dir, _ := os.Getwd()
	home := os.Getenv("HOME")
	cfg := vbash.Config{
		Dir:       dir,
		Home:      home,
		Env:       os.Environ(),
		Args:      rest,
		Arg0:      "bash",
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Stdin:     os.Stdin,
		ShellName: "/bin/bash",
	}
	code, _ := vbash.RunScript(context.Background(), cmd, cfg)
	os.Exit(code)
}
