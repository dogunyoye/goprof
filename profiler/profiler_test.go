package profiler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartStopWritesProfiles(t *testing.T) {
	dir := t.TempDir()
	p := New(Config{
		OutputDir:      dir,
		CPUFileName:    "cpu.pb.gz",
		HeapFileName:   "mem.pb.gz",
		EnableCPU:      true,
		EnableHeap:     true,
		MemProfileRate: 4096,
	})
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Do a little work so CPU profile is non-trivial.
	busy := 0
	for i := 0; i < 1_000_000; i++ {
		busy += i % 7
	}
	_ = busy
	time.Sleep(100 * time.Millisecond)

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, name := range []string{"cpu.pb.gz", "mem.pb.gz"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
		if fi.Size() == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

func TestDoubleStartFails(t *testing.T) {
	p := New(Config{OutputDir: t.TempDir(), EnableCPU: false, EnableHeap: false, ServerAddr: "localhost:0"})
	// ServerAddr-only config passes validation; Start should succeed once.
	cfg := DefaultConfig()
	cfg.OutputDir = t.TempDir()
	cfg.ServerAddr = ""
	p = New(cfg)
	if err := p.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer p.Stop()
	if err := p.Start(); err == nil {
		t.Fatal("expected second Start to fail")
	}
}

func TestCaptureCPU(t *testing.T) {
	out := filepath.Join(t.TempDir(), "cpu-short.pb.gz")
	if err := CaptureCPU(100*time.Millisecond, out); err != nil {
		t.Fatalf("CaptureCPU: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty profile, stat=%v size=%v", err, fi.Size())
	}
}

func TestWriteHeapProfile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "heap.pb.gz")
	if err := WriteHeapProfile(out); err != nil {
		t.Fatalf("WriteHeapProfile: %v", err)
	}
}
