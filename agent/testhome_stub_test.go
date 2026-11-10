//go:build !linux && !darwin && !windows

package agent

func setTestHome(home string) error { return nil }
