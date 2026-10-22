package shell

import (
	"io"
	"os"
	"os/exec"
)

// Release 归还借出的终端；必须幂等（同一租约可安全地多次调用）。
type Lease interface {
	Stdin() *os.File
	Release()
}

type Console interface {
	LendStdin() (Lease, error)
	LendFull(cmd *exec.Cmd, capture io.Writer) (Lease, error)
}

type nullLease struct{}

func (nullLease) Stdin() *os.File { return nil }
func (nullLease) Release()        {}

type nullConsole struct{}

func (nullConsole) LendStdin() (Lease, error) { return nullLease{}, nil }

func (nullConsole) LendFull(cmd *exec.Cmd, capture io.Writer) (Lease, error) {
	return nil, ErrNoLend
}
