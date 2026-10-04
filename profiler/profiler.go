// Package profiler provides a tiny runtime/pprof wrapper for Go apps.
//
// Basic usage:
//
//	p := profiler.New(profiler.DefaultConfig())
//	if err := p.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer p.Stop() // writes heap profile, stops CPU profile
package profiler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

// Profiler manages CPU + heap capture backed by runtime/pprof.
type Profiler struct {
	cfg Config

	mu      sync.Mutex
	started bool
	cpuFile *os.File
}

// New creates a Profiler from cfg (defaults applied).
func New(cfg Config) *Profiler {
	_ = cfg.Validate() // keep zero-value usable; Start re-validates
	return &Profiler{cfg: cfg}
}

// Start begins profiling per Config.
//   - creates OutputDir
//   - sets MemProfileRate
//   - starts CPU profile to OutputDir/CPUFileName (if enabled)
//   - starts pprof HTTP endpoint (if ServerAddr set)
func (p *Profiler) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return fmt.Errorf("profiler: already started")
	}
	if err := p.cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(p.cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("profiler: mkdir %s: %w", p.cfg.OutputDir, err)
	}

	if p.cfg.MemProfileRate != 0 {
		runtime.MemProfileRate = p.cfg.MemProfileRate
	} else {
		runtime.MemProfileRate = 4096
	}

	if p.cfg.EnableCPU {
		f, err := os.Create(filepath.Join(p.cfg.OutputDir, p.cfg.CPUFileName))
		if err != nil {
			return fmt.Errorf("profiler: create cpu profile: %w", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return fmt.Errorf("profiler: start cpu profile: %w", err)
		}
		p.cpuFile = f
	}

	if p.cfg.ServerAddr != "" {
		StartPprofServer(p.cfg.ServerAddr) // non-blocking, logs on failure
	}

	p.started = true
	return nil
}

// Stop ends CPU profiling and writes the heap profile.
// Safe to call once; second call returns an error.
func (p *Profiler) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return fmt.Errorf("profiler: not started")
	}

	if p.cpuFile != nil {
		pprof.StopCPUProfile()
		p.cpuFile.Close()
		p.cpuFile = nil
	}

	if p.cfg.EnableHeap {
		// Unlock while writing heap so SnapshotHeap can reuse the lock path.
		// Copy values to avoid holding mu during I/O.
		cfg := p.cfg
		p.mu.Unlock()
		err := WriteHeapProfile(filepath.Join(cfg.OutputDir, cfg.HeapFileName))
		p.mu.Lock()
		if err != nil {
			return err
		}
	}

	p.started = false
	return nil
}

// SnapshotHeap writes a heap profile immediately without stopping.
// Useful for long-running services: snapshot on SIGUSR1, interval, etc.
func (p *Profiler) SnapshotHeap(outputPath string) error {
	if outputPath == "" {
		return fmt.Errorf("profiler: output path required")
	}
	return WriteHeapProfile(outputPath)
}

// CaptureCPU samples CPU for dur and writes to outputPath.
// Standalone helper (does not require Start). Only one CPU profile
// can be active at a time in a process.
func CaptureCPU(dur time.Duration, outputPath string) error {
	sc := SampleConfig{Duration: dur, OutputPath: outputPath}
	if err := sc.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("profiler: mkdir: %w", err)
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("profiler: create file: %w", err)
	}
	defer f.Close()

	if err := pprof.StartCPUProfile(f); err != nil {
		return fmt.Errorf("profiler: start cpu profile: %w", err)
	}
	time.Sleep(dur)
	pprof.StopCPUProfile()
	return nil
}

// WriteHeapProfile triggers GC then dumps the heap to path.
func WriteHeapProfile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("profiler: mkdir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("profiler: create heap file: %w", err)
	}
	defer f.Close()

	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		return fmt.Errorf("profiler: write heap profile: %w", err)
	}
	return nil
}

// Running reports whether Start has been called without Stop.
func (p *Profiler) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started
}
