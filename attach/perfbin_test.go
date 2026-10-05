package attach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPerfBinaryEnvOverride(t *testing.T) {
	t.Setenv("GOPROF_PERF_BIN", "/bin/true")
	got, err := perfBinary()
	if err != nil || got != "/bin/true" {
		t.Fatalf("env override: %q %v", got, err)
	}
}

func TestPerfBinaryEnvMissing(t *testing.T) {
	t.Setenv("GOPROF_PERF_BIN", "/nonexistent-perf-xyz")
	if _, err := perfBinary(); err == nil {
		t.Fatal("expected error for bad GOPROF_PERF_BIN")
	}
}

func TestPerfBinarySkipsBrokenWrapper(t *testing.T) {
	t.Setenv("GOPROF_PERF_BIN", "")
	dir := t.TempDir()

	// Fake broken wrapper first on PATH: prints the WSL miss, exits 1.
	fakeWrapper := filepath.Join(dir, "perf")
	if err := os.WriteFile(fakeWrapper,
		[]byte("#!/bin/sh\necho 'WARNING: perf not found for kernel 6.18.33.2-microsoft' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake versioned binary that answers --version.
	toolsDir := filepath.Join(dir, "tools-9.9")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeReal := filepath.Join(toolsDir, "perf")
	if err := os.WriteFile(fakeReal,
		[]byte("#!/bin/sh\necho 'perf version 9.9'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldGlobs := versionedPerfGlobs
	versionedPerfGlobs = []string{filepath.Join(dir, "tools-*/perf")}
	defer func() { versionedPerfGlobs = oldGlobs }()

	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	got, err := perfBinary()
	if err != nil {
		t.Fatalf("perfBinary: %v", err)
	}
	if got != fakeReal {
		t.Fatalf("expected versioned fallback %q, got %q", fakeReal, got)
	}
}

func TestPerfBinaryPrefersWorkingWrapper(t *testing.T) {
	t.Setenv("GOPROF_PERF_BIN", "")
	dir := t.TempDir()
	fakeWrapper := filepath.Join(dir, "perf")
	if err := os.WriteFile(fakeWrapper,
		[]byte("#!/bin/sh\necho 'perf version 9.9'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldGlobs := versionedPerfGlobs
	versionedPerfGlobs = []string{filepath.Join(dir, "nothing-here-*/perf")}
	defer func() { versionedPerfGlobs = oldGlobs }()

	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	got, err := perfBinary()
	if err != nil || got != "perf" {
		t.Fatalf("expected PATH wrapper, got %q %v", got, err)
	}
}

func TestRecordArgsEvent(t *testing.T) {
	plain := recordArgs(123, 99, "dwarf", "/tmp/p.data", "")
	for _, a := range plain {
		if a == "-e" {
			t.Fatalf("default must not pass -e: %v", plain)
		}
	}
	with := recordArgs(123, 99, "dwarf", "/tmp/p.data", "cpu-clock")
	want := []string{"record", "-e", "cpu-clock", "-F", "99", "-p", "123",
		"-g", "--call-graph", "dwarf", "-o", "/tmp/p.data"}
	if strings.Join(with, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", with, want)
	}
}

func TestIsWrapperMiss(t *testing.T) {
	if !isWrapperMiss("WARNING: perf not found for kernel 6.18.33.2-microsoft") {
		t.Fatal("expected wrapper miss")
	}
	if isWrapperMiss("perf version 6.8.12") {
		t.Fatal("version line is not a miss")
	}
	if isWrapperMiss("") {
		t.Fatal("empty is not a miss")
	}
}

func TestIsPMUFailure(t *testing.T) {
	for _, s := range []string{
		"PMU Hardware doesn't support sampling/overflow-interrupts",
		"cycles: PMU Hardware doesn't support sampling",
		"Error: No such device",
		"event not supported",
	} {
		if !isPMUFailure(s) {
			t.Fatalf("expected PMU failure: %q", s)
		}
	}
	if isPMUFailure("perf record: Captured and wrote 1MB") {
		t.Fatal("success line is not a PMU failure")
	}
}

func TestPerfTailHints(t *testing.T) {
	if got := perfTail("WARNING: perf not found for kernel 6.18.33.2-microsoft"); !strings.Contains(got, "ln -s") {
		t.Fatalf("wrapper hint missing:\n%s", got)
	}
	if got := perfTail("cycles: PMU Hardware doesn't support sampling"); !strings.Contains(got, "cpu-clock") {
		t.Fatalf("pmu hint missing:\n%s", got)
	}
	if got := perfTail("Permission error"); !strings.Contains(got, "perf_event_paranoid") {
		t.Fatalf("permission hint missing:\n%s", got)
	}
}

func TestRecordingPersist(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "perf.data")
	content := []byte("fake-perf-data-12345")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Recording{dataPath: src, tmpDir: tmpDir, waited: make(chan error, 1)}
	dstDir := filepath.Join(t.TempDir(), "run")
	got, err := r.Persist(dstDir)
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if got != filepath.Join(dstDir, "perf.data") {
		t.Fatalf("unexpected path %q", got)
	}
	back, err := os.ReadFile(got)
	if err != nil || string(back) != string(content) {
		t.Fatalf("content mismatch: %v %q", err, back)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatal("expected temp dir cleaned up")
	}
	if r.DataPath() != got {
		t.Fatalf("DataPath should track the copy, got %q", r.DataPath())
	}
}

func TestRecordingPersistMissing(t *testing.T) {
	r := &Recording{dataPath: filepath.Join(t.TempDir(), "nope.data"), waited: make(chan error, 1)}
	if _, err := r.Persist(t.TempDir()); err == nil {
		t.Fatal("expected error for missing source")
	}
}
