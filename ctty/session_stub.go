//go:build !linux && !darwin

package ctty

import (
	"errors"
	"os"
)

var errSessionUnsupported = errors.New("ctty: session control unsupported on this platform")

func BecomeSessionLeader(*os.File) error { return errSessionUnsupported }

func SetControllingTTY(*os.File) error { return errSessionUnsupported }

func SetForeground(*os.File, int) error { return errSessionUnsupported }

func ForegroundGroup(*os.File) (int, error) { return 0, errSessionUnsupported }

func IgnoreTTOU() {}
