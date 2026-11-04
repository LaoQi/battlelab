package vbash

import (
	"bufio"
	"io"
	"os"
	"strings"
)

type IO struct {
	In    io.Reader
	Out   io.Writer
	Err   io.Writer
	Extra map[int]io.Writer
}

func (x *IO) writer(fd int) (io.Writer, bool) {
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
	if len(x.Extra) > 0 {
		c.Extra = make(map[int]io.Writer, len(x.Extra))
		for k, v := range x.Extra {
			c.Extra[k] = v
		}
	}
	return c
}

func (x *IO) WriteString(w io.Writer, s string) {
	if w == nil {
		return
	}
	io.WriteString(w, s)
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
