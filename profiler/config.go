package profiler

import (
	"errors"
	"time"
)

// Config controls what the Profiler captures and where it writes.
type Config struct {
	// OutputDir is where profile files are written. Created if missing.
	OutputDir string
	// CPUFileName is the CPU profile filename. Default: cpu.pb.gz
	CPUFileName string
	// HeapFileName is the heap profile filename. Default: mem.pb.gz
	HeapFileName string

	// EnableCPU turns on runtime/pprof CPU sampling on Start.
	EnableCPU bool
	// EnableHeap turns on heap capture on Stop and SnapshotHeap.
	// Heap profiling via runtime has near-zero overhead; leave true.
	EnableHeap bool

	// MemProfileRate sets runtime.MemProfileRate (default 4096).
	// 0 disables, 1 captures every allocation (expensive, test only).
	MemProfileRate int

	// ServerAddr optionally exposes net/http/pprof, e.g. "localhost:6060".
	// Empty disables the HTTP endpoint. Never expose publicly without auth.
	ServerAddr string
}

// DefaultConfig returns a sane v0.1 config: CPU+heap to ./profiles.
func DefaultConfig() Config {
	return Config{
		OutputDir:      "profiles",
		CPUFileName:    "cpu.pb.gz",
		HeapFileName:   "mem.pb.gz",
		EnableCPU:      true,
		EnableHeap:     true,
		MemProfileRate: 4096,
		ServerAddr:     "",
	}
}

// Validate fills defaults and checks the config.
func (c *Config) Validate() error {
	if c.OutputDir == "" {
		c.OutputDir = "profiles"
	}
	if c.CPUFileName == "" {
		c.CPUFileName = "cpu.pb.gz"
	}
	if c.HeapFileName == "" {
		c.HeapFileName = "mem.pb.gz"
	}
	if c.MemProfileRate < 0 {
		return errors.New("profiler: MemProfileRate cannot be negative")
	}
	if !c.EnableCPU && !c.EnableHeap && c.ServerAddr == "" {
		return errors.New("profiler: nothing to do (enable CPU, heap, or ServerAddr)")
	}
	return nil
}

// SampleConfig is a one-shot CPU capture request.
type SampleConfig struct {
	// Duration is how long to sample CPU. pprof recommends >= 10s for real apps.
	Duration time.Duration
	// OutputPath is the destination file, e.g. "profiles/cpu-30s.pb.gz".
	OutputPath string
}

// Validate checks a SampleConfig.
func (s *SampleConfig) Validate() error {
	if s.Duration <= 0 {
		return errors.New("profiler: Duration must be > 0")
	}
	if s.OutputPath == "" {
		return errors.New("profiler: OutputPath is required")
	}
	return nil
}
