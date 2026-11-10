//go:build windows

package vbash

import "os"

var testPath = "PATH=" + os.Getenv("PATH")
