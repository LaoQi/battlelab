package vbash

import (
	"os"
	"path"
	"sort"
	"strings"
)

func pathMatch(pattern, name string) (bool, error) {
	return path.Match(pattern, name)
}

func matchSimple(pattern, name string) (bool, error) {
	return path.Match(pattern, name)
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func (r *Runner) globExpand(text string) []string {
	if !hasGlobMeta(text) {
		return []string{text}
	}
	dir, file := path.Split(text)
	base := dir
	if base == "" {
		base = "."
	}
	if r.Env.Dir() != "" {
		base = r.posixPath(base)
	}
	entries, err := os.ReadDir(plat.ToHost(r.Env.Dir(), base))
	if err != nil {
		return []string{text}
	}
	var matches []string
	dotOK := strings.HasPrefix(file, ".")
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !dotOK {
			continue
		}
		if plat.MatchName(file, name) {
			matches = append(matches, dir+name)
		}
	}
	if len(matches) == 0 {
		return []string{text}
	}
	sort.Strings(matches)
	return matches
}
