package vbash

import (
	"bufio"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
)

type IO struct {
	In    io.Reader
	Out   io.Writer
	Err   io.Writer
	Extra map[int]io.Writer
	Shut  map[int]bool
}

func (x *IO) closed(fd int) bool {
	if x.Shut == nil {
		return false
	}
	return x.Shut[fd]
}

func (x *IO) writer(fd int) (io.Writer, bool) {
	if x.closed(fd) {
		return nil, false
	}
	switch fd {
	case 0:
		if w, ok := x.In.(io.Writer); ok {
			return w, true
		}
		return nil, false
	case 1:
		return x.Out, true
	case 2:
		return x.Err, true
	}
	if x.Extra == nil {
		return nil, false
	}
	w, ok := x.Extra[fd]
	return w, ok
}

func (x *IO) withWriter(fd int, w io.Writer) {
	if x.Shut != nil {
		delete(x.Shut, fd)
	}
	switch fd {
	case 0:
		if r, ok := w.(io.Reader); ok {
			x.In = r
			return
		}
	case 1:
		x.Out = w
		return
	case 2:
		x.Err = w
		return
	}
	if x.Extra == nil {
		x.Extra = map[int]io.Writer{}
	}
	x.Extra[fd] = w
}

func (x *IO) shutFd(fd int) {
	switch fd {
	case 0:
		x.In = emptyReader()
	case 1:
		x.Out = errWriter{}
	case 2:
		x.Err = errWriter{}
	default:
		if x.Extra != nil {
			delete(x.Extra, fd)
		}
	}
	if x.Shut == nil {
		x.Shut = map[int]bool{}
	}
	x.Shut[fd] = true
}

type closedFdError struct{ fd int }

func (e closedFdError) Error() string { return "Bad file descriptor" }

type errWriter struct{ fd int }

func (w errWriter) Write([]byte) (int, error) { return 0, closedFdError{w.fd} }

func (x *IO) extraFds() []int {
	if len(x.Extra) == 0 {
		return nil
	}
	out := make([]int, 0, len(x.Extra))
	for fd := range x.Extra {
		if fd >= 3 {
			out = append(out, fd)
		}
	}
	sort.Ints(out)
	return out
}

func (x *IO) bufferedInput() *bufio.Reader {
	if br, ok := x.In.(*bufio.Reader); ok {
		return br
	}
	br := bufio.NewReader(x.In)
	x.In = br
	return br
}

func (x *IO) clone() *IO {
	c := &IO{In: x.In, Out: x.Out, Err: x.Err}
	if len(x.Shut) > 0 {
		c.Shut = make(map[int]bool, len(x.Shut))
		for k, v := range x.Shut {
			c.Shut[k] = v
		}
	}
	if len(x.Extra) > 0 {
		c.Extra = make(map[int]io.Writer, len(x.Extra))
		for k, v := range x.Extra {
			c.Extra[k] = v
		}
	}
	return c
}

func (x *IO) WriteString(w io.Writer, s string) error {
	if w == nil {
		return nil
	}
	_, err := io.WriteString(w, s)
	if err != nil && isBrokenPipe(err) {
		return nil
	}
	return err
}

func isBrokenPipe(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	return strings.Contains(err.Error(), "broken pipe")
}

type nopReader struct{}

func (nopReader) Read([]byte) (int, error) { return 0, io.EOF }

func emptyReader() io.Reader { return nopReader{} }

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func devNull() io.Writer { return discard{} }

func stringReader(s string) io.Reader { return strings.NewReader(s) }

func stdIO() *IO {
	return &IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}
