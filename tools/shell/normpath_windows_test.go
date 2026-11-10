//go:build windows

package shell

import "strings"

func normShellPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if len(p) > 2 && p[1] == ':' && p[2] == '/' {
		return "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}
