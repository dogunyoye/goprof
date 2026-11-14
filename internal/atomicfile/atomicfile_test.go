package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteNewFileMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "out.txt")
	if err := Write(p, func(f *os.File) error {
		_, err := f.WriteString("hi")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "hi" {
		t.Fatalf("content %q, err %v", b, err)
	}
	if runtime.GOOS == "windows" {
		return // Windows has no Unix permission bits.
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v, want 0644", fi.Mode().Perm())
	}
}

func TestWritePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permission bits on Windows")
	}
	p := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(p, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, func(f *os.File) error { _, err := f.WriteString("new"); return err }); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, want 0640", fi.Mode().Perm())
	}
}

func TestWriteFailureLeavesOriginal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := Write(p, func(f *os.File) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "old" {
		t.Fatalf("content %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}
