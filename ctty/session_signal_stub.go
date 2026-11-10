//go:build !linux && !darwin

package ctty

func signalIgnoreTTOU() {}
