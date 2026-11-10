//go:build windows

package agent

import "os"

func setTestHome(home string) error { return os.Setenv("USERPROFILE", home) }
