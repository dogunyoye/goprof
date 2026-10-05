// Package runner launches an application and profiles it in the same
// command: the child runs with stdio connected while perf samples its
// CPU, /proc RSS is polled, and (optionally) its pprof HTTP endpoint is
// fetched. No instrumentation of the target is required for the
// external (perf+RSS) mode; the pprof mode additionally converts the
// endpoint's profiles to flamegraphs.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"goprof/attach"
	"goprof/flamegraph"
)

// ErrInterrupted is returned (with a partial Summary) when SIGINT or
// SIGTERM arrives mid-run.
var ErrInterrupted = errors.New("runner: interrupted")

// Options controls Run. Command is required; at least one profiling
// method (CPU, Mem, PprofAddr) must be enabled.
type Options struct {
	// Command is argv of the app to profile, e.g. {"./myapp", "--serve"}.
	Command []string
	// Dir sets the child's working directory ("" = inherit).
	Dir string
	// Env adds KEY=VAL pairs to the child's environment.
	Env []string
	// Seconds caps the sampling window (0 = until the child exits).
	Seconds int
	// Freq is perf samples/sec. Default 99.
	Freq int
	// Callgraph is "dwarf" (default, reliable for Go) or "fp".
	Callgraph string
	// OutputDir holds the run directory. Default "profiles".
	OutputDir string
	// Prefix names the run directory. Default "run-<cmd>-<timestamp>".
	Prefix string
	// CPU enables perf sampling (default true; requires perf).
	CPU bool
	// IncludeKernel keeps kernel frames in the flamegraph.
	IncludeKernel bool
	// Mem enables concurrent RSS sampling (default true).
	Mem bool
	// MemInterval is the RSS poll period. Default 500ms.
	MemInterval time.Duration
	// PprofAddr (e.g. "localhost:6060") enables endpoint mode: after
	// the child serves /debug/pprof, heap (+ optional CPU) profiles
	// are fetched and rendered. "" disables.
	PprofAddr string
	// PprofWait caps endpoint readiness polling. Default 15s.
	PprofWait time.Duration
	// PprofSeconds is the endpoint CPU fetch duration. Default 10.
	PprofSeconds int
	// Detach leaves a still-running child alive when the window ends
	// (default: terminate it).
	Detach bool
	// Title overrides the SVG title.
	Title string
	// FlameOpts tunes SVG rendering; zero values take defaults.
	FlameOpts flamegraph.Options
	// Stdout/Stderr receive the child's output (default os.Stdout/Err).
	Stdout io.Writer
	Stderr io.Writer
}

// Summary describes a run.
type Summary struct {
	PID         int
	ExitCode    int  // child status (128+sig if signaled); -1 if detached/unknown
	ExitedEarly bool // child finished before the window end
	TimedOut    bool // window elapsed with the child still alive
	Detached    bool // child left running (--detach)
	Interrupted bool // stopped by SIGINT/SIGTERM
	Duration    time.Duration
	Warnings    []string
	// External (perf) artifacts.
	PerfData string
	Folded   string
	SVG      string
	Samples  int
	Stacks   int
	Frames   int
	// Memory artifacts.
	Mem     attach.MemStats
	HaveMem bool
	// Endpoint (pprof) artifacts.
	PprofCPU     string
	PprofHeap    string
	PprofCPUSVG  string
	PprofHeapSVG string
}

// DefaultOptions profiles both CPU and memory until the child exits.
func DefaultOptions(command []string) Options {
	return Options{
		Command:      command,
		Freq:         99,
		Callgraph:    attach.CallgraphDwarf,
		OutputDir:    "profiles",
		CPU:          true,
		Mem:          true,
		MemInterval:  500 * time.Millisecond,
		PprofWait:    15 * time.Second,
		PprofSeconds: 10,
	}
}

// Run launches the command and profiles it simultaneously. It returns
// a Summary; a non-nil error means profiling itself failed (child
// startup, perf, or endpoint problems). A child that merely exits
// nonzero is not an error — see Summary.ExitCode.
func Run(opts Options) (Summary, error) {
	var s Summary
	if runtime.GOOS != "linux" {
		return s, fmt.Errorf("runner: run requires Linux (have %s)", runtime.GOOS)
	}
	if len(opts.Command) == 0 {
		return s, fmt.Errorf("runner: no command to run (usage: goprof run -- <cmd> [args...])")
	}
	if !opts.CPU && !opts.Mem && opts.PprofAddr == "" {
		return s, fmt.Errorf("runner: nothing to do (enable CPU, mem, or --pprof-addr)")
	}
	if opts.Freq <= 0 {
		opts.Freq = 99
	}
	if opts.MemInterval <= 0 {
		opts.MemInterval = 500 * time.Millisecond
	}

	outDir := opts.OutputDir
	if outDir == "" {
		outDir = "profiles"
	}
	prefix := opts.Prefix
	if prefix == "" {
		prefix = fmt.Sprintf("run-%s-%s", safeName(filepath.Base(opts.Command[0])),
			time.Now().Format("20060102-150405"))
	}
	workDir := filepath.Join(outDir, prefix)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return s, fmt.Errorf("runner: mkdir %s: %w", workDir, err)
	}

	stdout, stderr := opts.Stdout, opts.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	// Interrupt handling for the whole run.
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	start := time.Now()
	child := exec.Command(opts.Command[0], opts.Command[1:]...)
	child.Dir = opts.Dir
	child.Env = append(os.Environ(), opts.Env...)
	child.Stdout = stdout
	child.Stderr = stderr
	child.Stdin = os.Stdin
	if err := child.Start(); err != nil {
		return s, fmt.Errorf("runner: start %q: %w", opts.Command[0], err)
	}
	s.PID = child.Process.Pid
	childDone := make(chan error, 1)
	go func() { childDone <- child.Wait() }()

	// Window: Seconds>0 caps it, else the child's lifetime (or Ctrl-C).
	window := time.Duration(opts.Seconds) * time.Second
	haveDeadline := opts.Seconds > 0
	memDur := 24 * time.Hour // bounded by stopCh in practice
	var deadline <-chan time.Time
	if haveDeadline {
		deadline = time.After(window)
		memDur = window
	}

	// Memory sampler (streaming CSV; stops on stopCh or process exit).
	memStop := make(chan struct{})
	var memClosed bool
	closeMem := func() {
		if !memClosed {
			memClosed = true
			close(memStop)
		}
	}
	type memResult struct {
		stats attach.MemStats
		err   error
	}
	memCh := make(chan memResult, 1)
	if opts.Mem {
		interval := opts.MemInterval
		go func() {
			st, err := attach.SampleMemoryStop(s.PID, interval, memDur,
				filepath.Join(workDir, "rss.csv"), memStop)
			memCh <- memResult{st, err}
		}()
	} else {
		closeMem()
		memCh <- memResult{}
	}

	// CPU recorder. Fail fast when explicitly requested but broken.
	var rec *attach.Recording
	if opts.CPU {
		r, err := attach.StartRecording(s.PID, opts.Freq, opts.Callgraph, workDir)
		if err != nil {
			killErr := terminateChild(child, childDone)
			_ = killErr
			closeMem()
			<-memCh
			return s, err
		}
		rec = r
	}

	// Endpoint fetcher (background; waits for readiness itself).
	pprofCh := make(chan pprofResult, 1)
	if opts.PprofAddr != "" {
		wait := opts.PprofWait
		if wait <= 0 {
			wait = 15 * time.Second
		}
		secs := opts.PprofSeconds
		if secs <= 0 {
			secs = 10
		}
		go func() {
			pprofCh <- fetchEndpoint(opts.PprofAddr, wait, secs, workDir, opts.FlameOpts, opts.Title)
		}()
	} else {
		pprofCh <- pprofResult{}
	}

	// Main wait: child exit, deadline, or interrupt. childDone is the
	// single waiter on the child; everything else coordinates through
	// it (never call child.Wait elsewhere).
	var childErr error
	childExited := false
	select {
	case childErr = <-childDone:
		childExited = true
		s.ExitedEarly = true
	case <-deadline:
		s.TimedOut = true
	case <-ctx.Done():
		s.Interrupted = true
	}

	switch {
	case s.Interrupted:
		childErr = terminateChild(child, childDone)
		childExited = true
	case s.TimedOut && opts.Detach:
		s.Detached = true // leave the child running
	case s.TimedOut:
		childErr = terminateChild(child, childDone)
		childExited = true
	}
	if childExited {
		s.ExitCode = exitCode(childErr)
	} else {
		s.ExitCode = -1
	}
	s.Duration = time.Since(start)

	// Stop profilers and finalize artifacts.
	closeMem()
	mr := <-memCh
	if opts.Mem {
		if mr.err != nil {
			s.Warnings = append(s.Warnings, "memory sampling: "+mr.err.Error())
		} else {
			s.Mem, s.HaveMem = mr.stats, true
			if !mr.stats.Completed {
				s.Warnings = append(s.Warnings, "memory window ended early (target exited or run stopped)")
			}
		}
	}

	if rec != nil {
		if err := rec.Stop(); err != nil {
			rec.Kill()
			s.Warnings = append(s.Warnings, "perf: "+err.Error())
		} else {
			s.PerfData = rec.DataPath()
			foldedPath := filepath.Join(workDir, "cpu.folded")
			n, err := attach.ScriptToFolded(s.PerfData, foldedPath, s.PID, opts.IncludeKernel)
			if err != nil {
				s.Warnings = append(s.Warnings, "perf collapse: "+err.Error())
			} else {
				s.Samples = n
				s.Folded = foldedPath
				title := opts.Title
				if title == "" {
					title = fmt.Sprintf("run %s (pid %d) — cpu",
						filepath.Base(opts.Command[0]), s.PID)
				}
				fopts := flamegraph.DefaultOptions()
				if opts.FlameOpts.Width != 0 {
					fopts = opts.FlameOpts
				}
				if fopts.Title == "" {
					fopts.Title = title
				}
				fs, err := flamegraph.GenerateFromFolded(foldedPath,
					filepath.Join(workDir, "flame.svg"), fopts)
				if err != nil {
					s.Warnings = append(s.Warnings, "flamegraph: "+err.Error())
				} else {
					s.SVG = fs.SVG
					s.Stacks = fs.Stacks
					s.Frames = fs.Frames
				}
			}
		}
	}

	pr := <-pprofCh
	s.Warnings = append(s.Warnings, pr.warnings...)
	if opts.PprofAddr != "" {
		if pr.err != nil {
			s.Warnings = append(s.Warnings, "pprof endpoint: "+pr.err.Error())
		} else {
			s.PprofCPU, s.PprofHeap = pr.cpu, pr.heap
			s.PprofCPUSVG, s.PprofHeapSVG = pr.cpuSVG, pr.heapSVG
		}
	}

	if s.Interrupted {
		return s, ErrInterrupted
	}
	if s.SVG == "" && s.Folded == "" && !s.HaveMem &&
		s.PprofCPU == "" && s.PprofHeap == "" {
		// A child that ran to completion is a successful run even when
		// it was too short-lived to sample — say so, keep its exit code.
		if s.ExitedEarly {
			s.Warnings = append(s.Warnings, "no profiling artifacts produced (target exited before any sample)")
			return s, nil
		}
		return s, fmt.Errorf("runner: no profiling artifacts produced (target may have exited instantly)%s",
			joinWarnings(s.Warnings))
	}
	return s, nil
}

func joinWarnings(ws []string) string {
	if len(ws) == 0 {
		return ""
	}
	out := "; warnings:"
	for _, w := range ws {
		out += " [" + w + "]"
	}
	return out
}

// terminateChild SIGTERMs the child and escalates to SIGKILL after a
// 3s grace, returning its Wait status. It consumes the single pending
// value from childDone (blocking after SIGKILL guarantees delivery).
func terminateChild(child *exec.Cmd, childDone chan error) error {
	_ = child.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-childDone:
		return err
	case <-time.After(3 * time.Second):
	}
	_ = child.Process.Kill()
	return <-childDone
}

// exitCode maps a Wait error to a process exit code, using the
// 128+signal convention for signaled processes.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if st, ok := ee.Sys().(syscall.WaitStatus); ok {
			if st.Signaled() {
				return 128 + int(st.Signal())
			}
			return st.ExitStatus()
		}
	}
	return 1
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
		return "cmd"
	}
	if len(b) > 40 {
		b = b[:40]
	}
	return string(b)
}
