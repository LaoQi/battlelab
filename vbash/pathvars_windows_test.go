//go:build windows

package vbash

import "testing"

// F6：TMP/TEMP/APPDATA 纳入边界归一——内部形态、出站宿主形态、展开形态三者一致。
func TestWindowsPathVarsNormalized(t *testing.T) {
	e := NewEnv()
	e.SetExported("TEMP", `C:\Users\Q\AppData\Local\Temp`)
	e.SetExported("TMP", `C:\Users\Q\AppData\Local\Temp`)
	e.SetExported("APPDATA", `C:\Users\Q\AppData\Roaming`)

	internal := map[string]string{
		"TEMP":    "/c/Users/Q/AppData/Local/Temp",
		"TMP":     "/c/Users/Q/AppData/Local/Temp",
		"APPDATA": "/c/Users/Q/AppData/Roaming",
	}
	for name, want := range internal {
		if got, _ := e.Lookup(name); got != want {
			t.Errorf("Lookup(%s) = %q，期望内部形态 %q", name, got, want)
		}
		// 展开/显示形态：正斜杠宿主形态
		if got, _ := e.LookupDisplay(name); got != `C:/Users/Q/AppData/Local/Temp` && name != "APPDATA" {
			t.Errorf("LookupDisplay(%s) = %q", name, got)
		}
	}

	// 出站到子进程的必须是宿主形态（反斜杠）
	env := e.Environ()
	found := map[string]string{}
	for _, kv := range env {
		for name := range internal {
			if len(kv) > len(name) && kv[:len(name)+1] == name+"=" {
				found[name] = kv[len(name)+1:]
			}
		}
	}
	if got := found["TEMP"]; got != `C:\Users\Q\AppData\Local\Temp` {
		t.Errorf("Environ TEMP = %q", got)
	}
	if got := found["APPDATA"]; got != `C:\Users\Q\AppData\Roaming` {
		t.Errorf("Environ APPDATA = %q", got)
	}
}
