package attach

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"goprof/flamegraph"
)

// Options controls Attach. Use DefaultOptions, then set PID or Name.
type Options struct {
	// Target: exactly one of PID / Name (substring match).
	PID  int
	Name string
	// Seconds is the sampling window. Default 30.
	Seconds int
	// Freq is perf samples/sec. Default 99.
	Freq int
	// Callgraph is "dwarf" (default, reliable for Go) or "fp".
	Callgraph string
	// OutputDir holds all artifacts. Default "profiles".
	OutputDir string
	// Prefix names the artifacts. Default "attach-<exe>-<pid>-<timestamp>".
	Prefix string
	// CPU enables perf sampling (default true; requires perf).
	CPU bool
	// IncludeKernel keeps kernel frames in the flamegraph (default false).
	IncludeKernel bool
	// Mem enables concurrent RSS sampling (default true).
	Mem bool
	// MemInterval is the RSS poll period. Default 500ms.
	MemInterval time.Duration
	// Title overrides the SVG title.
	Title string
	// FlameOpts tunes the SVG renderer (widths, depth). Zero values
	// fall back to flamegraph defaults.
	FlameOpts flamegraph.Options
}

// DefaultOptions returns an attach config that samples both CPU and
// memory for 30s.
func DefaultOptions() Options {
	return Options{
		Seconds:     30,
		Freq:        99,
		Callgraph:   CallgraphDwarf,
		OutputDir:   "profiles",
		CPU:         true,
		Mem:         true,
		MemInterval: 500 * time.Millisecond,
	}
}

// Summary describes an attach run.
type Summary struct {
	Proc     ProcInfo
	Warnings []string
	PerfData string // raw perf.data (kept for `perf report`)
	Folded   string // collapsed stacks
	SVG      string // flamegraph (CPU only)
	Samples  int    // perf samples kept
	Stacks   int
	Frames   int
	Mem      MemStats
	HaveMem  bool
}

// Attach profiles a running process for Seconds without touching its
// code: CPU via perf (folded stacks + flamegraph SVG) and RSS via
// /proc polling (CSV + stats). At least one of CPU/Mem must be on.
func Attach(opts Options) (Summary, error) {
	var s Summary
	if runtime.GOOS != "linux" {
		return s, fmt.Errorf("attach: external attach requires Linux (have %s); use the profiler library + collect instead", runtime.GOOS)
	}
	if !opts.CPU && !opts.Mem {
		return s, fmt.Errorf("attach: nothing to do (both CPU and mem sampling are off)")
	}
	if opts.Seconds <= 0 {
		opts.Seconds = 30
	}

	// Resolve target.
	var (
		proc ProcInfo
		err  error
	)
	switch {
	case opts.PID > 0:
		proc, err = FindByPID(opts.PID)
	case opts.Name != "":
		proc, err = FindByName(opts.Name)
	default:
		return s, fmt.Errorf("attach: set --pid or --name")
	}
	if err != nil {
		return s, err
	}
	s.Proc = proc
	if !proc.IsGo {
		s.Warnings = append(s.Warnings,
			fmt.Sprintf("pid %d does not look like a Go binary (no Go build info in %q); continuing anyway", proc.PID, proc.Exe))
	}

	if opts.OutputDir == "" {
		opts.OutputDir = "profiles"
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return s, fmt.Errorf("attach: mkdir %s: %w", opts.OutputDir, err)
	}
	prefix := opts.Prefix
	if prefix == "" {
		prefix = fmt.Sprintf("attach-%s-%d-%s",
			safeName(proc.ExeBase()), proc.PID,
			time.Now().Format("20060102-150405"))
	}
	workDir := filepath.Join(opts.OutputDir, prefix)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return s, fmt.Errorf("attach: mkdir %s: %w", workDir, err)
	}

	duration := time.Duration(opts.Seconds) * time.Second

	// Memory samples concurrently with perf.
	var (
		wg       sync.WaitGroup
		memStats MemStats
		memErr   error
	)
	if opts.Mem {
		interval := opts.MemInterval
		if interval <= 0 {
			interval = 500 * time.Millisecond
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			memStats, memErr = SampleMemory(proc.PID, interval, duration,
				filepath.Join(workDir, "rss.csv"))
		}()
	}

	// CPU in the foreground (perf blocks for the window).
	if opts.CPU {
		dataPath, err := RecordCPU(proc.PID, opts.Seconds, opts.Freq, opts.Callgraph, workDir)
		if err != nil {
			// Stop memory early on CPU failure? No — let it finish;
			// but surface the CPU error regardless.
			wg.Wait()
			// Preserve any memory stats collected for diagnosis.
			if opts.Mem && memErr == nil {
				s.Mem, s.HaveMem = memStats, true
			}
			return s, err
		}
		s.PerfData = dataPath

		foldedPath := filepath.Join(workDir, "cpu.folded")
		n, err := ScriptToFolded(dataPath, foldedPath, proc.PID, opts.IncludeKernel)
		if err != nil {
			wg.Wait()
			if opts.Mem && memErr == nil {
				s.Mem, s.HaveMem = memStats, true
			}
			return s, err
		}
		s.Samples = n
		s.Folded = foldedPath

		title := opts.Title
		if title == "" {
			title = fmt.Sprintf("attach %s (pid %d) — cpu", proc.ExeBase(), proc.PID)
		}
		fopts := opts.FlameOpts
		if fopts.Width == 0 {
			fopts = flamegraph.DefaultOptions()
			fopts.Title = title
		} else if fopts.Title == "" {
			fopts.Title = title
		}
		fs, err := flamegraph.GenerateFromFolded(foldedPath,
			filepath.Join(workDir, "flame.svg"), fopts)
		if err != nil {
			wg.Wait()
			if opts.Mem && memErr == nil {
				s.Mem, s.HaveMem = memStats, true
			}
			return s, err
		}
		s.SVG = fs.SVG
		s.Stacks = fs.Stacks
		s.Frames = fs.Frames
	}

	wg.Wait()
	if opts.Mem {
		if memErr != nil {
			if s.SVG == "" && s.Folded == "" {
				return s, memErr // total failure
			}
			s.Warnings = append(s.Warnings, "memory sampling: "+memErr.Error())
		} else {
			s.Mem, s.HaveMem = memStats, true
			if !memStats.Completed {
				s.Warnings = append(s.Warnings, "target exited before the memory window completed (partial series kept)")
			}
		}
	}
	return s, nil
}

func safeName(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			b = append(b, c)
		} else {
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		return "proc"
	}
	if len(b) > 40 {
		b = b[:40]
	}
	return string(b)
}
