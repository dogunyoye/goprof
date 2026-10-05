package runner

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func memOnly(cmd []string) Options {
	o := DefaultOptions(cmd)
	o.CPU = false
	o.Mem = true
	o.MemInterval = 50 * time.Millisecond
	o.OutputDir = ""
	return o
}

func TestRunNoCommand(t *testing.T) {
	if _, err := Run(DefaultOptions(nil)); err == nil {
		t.Fatal("expected error with no command")
	}
}

func TestRunNothingEnabled(t *testing.T) {
	o := DefaultOptions([]string{"true"})
	o.CPU, o.Mem = false, false
	if _, err := Run(o); err == nil {
		t.Fatal("expected nothing-to-do error")
	}
}

func TestRunBadBinary(t *testing.T) {
	o := memOnly([]string{"/nonexistent-binary-xyz"})
	o.OutputDir = t.TempDir()
	if _, err := Run(o); err == nil {
		t.Fatal("expected start error")
	}
}

func TestRunEchoPassthrough(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := memOnly([]string{"sh", "-c", "echo hello-out; echo hello-err >&2"})
	o.OutputDir = t.TempDir()
	o.Stdout, o.Stderr = &stdout, &stderr
	s, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.ExitedEarly || s.ExitCode != 0 {
		t.Fatalf("unexpected exit: %+v", s)
	}
	if !strings.Contains(stdout.String(), "hello-out") {
		t.Fatalf("stdout not passed through: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "hello-err") {
		t.Fatalf("stderr not passed through: %q", stderr.String())
	}
	if !s.HaveMem {
		t.Fatal("expected memory stats")
	}
	if _, err := os.Stat(s.Mem.CSV); err != nil {
		t.Fatalf("expected rss.csv: %v", err)
	}
}

func TestRunExitCodePropagates(t *testing.T) {
	o := memOnly([]string{"sh", "-c", "sleep 0.3; exit 3"})
	o.OutputDir = t.TempDir()
	s, err := Run(o)
	if err != nil {
		t.Fatalf("child nonzero exit is not a Run error: %v", err)
	}
	if !s.ExitedEarly || s.ExitCode != 3 {
		t.Fatalf("expected exit 3, got %+v", s)
	}
}

func TestRunInstantExitStillPropagates(t *testing.T) {
	// Too fast to sample: no artifacts, but the run itself succeeded.
	o := memOnly([]string{"sh", "-c", "exit 7"})
	o.OutputDir = t.TempDir()
	s, err := Run(o)
	if err != nil {
		t.Fatalf("instant child exit is not a Run error: %v", err)
	}
	if !s.ExitedEarly || s.ExitCode != 7 {
		t.Fatalf("expected exit 7, got %+v", s)
	}
}

func TestRunEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer
	o := memOnly([]string{"sh", "-c", "echo $GOPROF_TEST_ENV; pwd"})
	o.OutputDir = t.TempDir()
	o.Dir = dir
	o.Env = []string{"GOPROF_TEST_ENV=env-works"}
	o.Stdout = &stdout
	if _, err := Run(o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "env-works") {
		t.Fatalf("env not passed: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), dir) {
		t.Fatalf("dir not applied: %q (want %s)", stdout.String(), dir)
	}
}

func TestRunTimeoutKillsChild(t *testing.T) {
	o := memOnly([]string{"sleep", "30"})
	o.OutputDir = t.TempDir()
	o.Seconds = 1
	o.MemInterval = 100 * time.Millisecond
	start := time.Now()
	s, err := Run(o)
	el := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.TimedOut || s.ExitedEarly || s.Detached {
		t.Fatalf("expected timeout-kill, got %+v", s)
	}
	if el > 15*time.Second {
		t.Fatalf("took too long: %s", el)
	}
	if s.HaveMem && s.Mem.Samples < 2 {
		t.Fatalf("expected mem samples, got %+v", s.Mem)
	}
	// Child must be gone (we killed it).
	if err := killCheck(s.PID); err == nil {
		t.Fatalf("child pid %d still alive after timeout kill", s.PID)
	}
}

func TestRunDetachLeavesChild(t *testing.T) {
	o := memOnly([]string{"sleep", "30"})
	o.OutputDir = t.TempDir()
	o.Seconds = 1
	o.MemInterval = 100 * time.Millisecond
	o.Detach = true
	s, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.TimedOut || !s.Detached || s.ExitCode != -1 {
		t.Fatalf("expected detached run, got %+v", s)
	}
	if err := killCheck(s.PID); err != nil {
		t.Fatalf("detached child should still run: %v", err)
	}
	// Cleanup: terminate the leftover.
	terminateTestChild(t, s.PID)
}

func TestRunCPUNeedsPerf(t *testing.T) {
	if PerfAvailableForTest() {
		t.Skip("perf present; failure path not applicable")
	}
	o := DefaultOptions([]string{"sleep", "5"})
	o.Mem = false
	o.Seconds = 1
	o.OutputDir = t.TempDir()
	if _, err := Run(o); err == nil {
		t.Fatal("expected perf-missing error")
	} else if !strings.Contains(err.Error(), "perf") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCPUFlameEndToEnd(t *testing.T) {
	if !PerfAvailableForTest() {
		t.Skip("perf not installed")
	}
	// Burn user-space CPU so perf has user stacks to collapse.
	o := DefaultOptions([]string{"sh", "-c", "i=0; while [ $i -lt 400000 ]; do i=$((i+1)); :; done"})
	o.Mem = false
	o.Seconds = 20 // cap; the loop exits first
	o.OutputDir = t.TempDir()
	s, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.ExitedEarly {
		t.Fatalf("expected child to finish first: %+v", s)
	}
	if s.SVG == "" || s.Samples == 0 {
		t.Fatalf("expected flame artifacts: %+v", s)
	}
	if _, err := os.Stat(s.SVG); err != nil {
		t.Fatalf("missing svg: %v", err)
	}
}

func TestExitCodeMapping(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Fatalf("nil -> %d", got)
	}
	if got := exitCode(errors.New("boom")); got != 1 {
		t.Fatalf("generic -> %d", got)
	}
}

func TestNormalizePprofAddr(t *testing.T) {
	if got := normalizePprofAddr("localhost:6060"); got != "http://localhost:6060" {
		t.Fatalf("got %q", got)
	}
	if got := normalizePprofAddr("http://x:1/"); got != "http://x:1" {
		t.Fatalf("got %q", got)
	}
}

func TestWaitPprofReadyTimeout(t *testing.T) {
	// Nothing listens here; must time out, not hang.
	if err := waitPprofReady("http://127.0.0.1:1", 500*time.Millisecond); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRunPprofModeAgainstExample(t *testing.T) {
	// Live end-to-end of endpoint mode using this repo's example app,
	// which serves pprof on :6060 for ~12s.
	if !portFree("localhost:6060") {
		t.Skip("port 6060 busy")
	}
	o := DefaultOptions([]string{"go", "run", "./example"})
	o.CPU = false
	o.Mem = true
	o.MemInterval = 500 * time.Millisecond
	o.PprofAddr = "localhost:6060"
	o.PprofWait = 30 * time.Second
	o.PprofSeconds = 5
	o.OutputDir = t.TempDir()
	o.Dir = repoRoot(t)
	s, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v (warnings: %v)", err, s.Warnings)
	}
	if s.PprofHeap == "" {
		t.Fatalf("expected heap artifact (warnings: %v)", s.Warnings)
	}
	if s.PprofHeapSVG == "" {
		t.Fatalf("expected heap flamegraph (warnings: %v)", s.Warnings)
	}
	if !s.HaveMem {
		t.Fatalf("expected mem stats (warnings: %v)", s.Warnings)
	}
}
