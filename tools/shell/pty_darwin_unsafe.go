//go:build darwin

package shell

import "unsafe"

func unsafePointer(p *byte) unsafe.Pointer { return unsafe.Pointer(p) }
