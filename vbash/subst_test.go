package vbash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileSubstitution(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runScript(t, dir, `echo "[$(<in.txt)]"; x=$(<in.txt); echo "[$x]"`)
	want := "[hello\nworld]\n[hello\nworld]\n"
	if got.stdout != want {
		t.Fatalf("got %q want %q", got.stdout, want)
	}
}

func TestReadFileSubstitutionMissing(t *testing.T) {
	dir := t.TempDir()
	got := runScript(t, dir, `echo "[$(<missing.txt)]"`)
	if got.stdout != "[]\n" {
		t.Fatalf("got %q", got.stdout)
	}
	if got.stderr == "" {
		t.Fatal("缺文件时应打诊断")
	}
}
