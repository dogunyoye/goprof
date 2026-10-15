// Command goprof profiles Go applications: instrumented (collect),
// offline (flame), by attaching to a running process (attach/ps),
// or by launching the app under the profiler (run).
//
// Usage:
//
//	goprof run --seconds 30 -- ./myapp --serve
//	goprof attach --pid 1234 --seconds 30
//	goprof collect --addr http://localhost:6060 --type heap --output profiles/heap.pb.gz
//	goprof flame --input profiles/cpu.pb.gz --output profiles/flame.svg
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"goprof/attach"
	"goprof/collector"
	"goprof/flamegraph"
	"goprof/runner"
)

const version = "v0.9.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "collect":
		runCollect(os.Args[2:])
	case "list":
		runList(os.Args[2:])
	case "flame":
		runFlame(os.Args[2:])
	case "ps":
		runPs(os.Args[2:])
	case "attach":
		runAttach(os.Args[2:])
	case "run":
		runRun(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("goprof", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`goprof - profiler for Go applications

Commands:
  run       launch an app and profile it in the same command
  attach    profile a running process without instrumentation (Linux perf + RSS)
  ps        list processes (find attach targets)
  collect   fetch a profile from /debug/pprof (instrumented apps)
  list      list local profile files
  flame     convert a saved profile to a flamegraph SVG (+ optional folded)
  version   print version

Examples:
  goprof run --seconds 30 -- ./myapp --serve
  goprof run --seconds 10 -- ./main.go --serve
  goprof run --pprof-addr localhost:6060 -- go run ./examples/starter
  goprof attach --pid 1234 --seconds 30
  goprof collect --addr http://localhost:6060 --type heap --output profiles/heap.pb.gz
  goprof flame --input profiles/cpu.pb.gz --output profiles/flame.svg
  goprof ps --go-only`)
}

func runCollect(args []string) {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	addr := fs.String("addr", "http://localhost:6060", "base address of pprof server")
	typ := fs.String("type", "heap", "profile type: cpu, heap, goroutine, block, mutex, trace")
	seconds := fs.Int("seconds", 30, "capture duration for cpu/trace")
	output := fs.String("output", "", "output file path")
	_ = fs.Parse(args)

	profileType := normalizeType(*typ)
	if *output == "" {
		fmt.Fprintln(os.Stderr, "collect: --output is required")
		os.Exit(2)
	}
	addrNorm := normalizeAddr(*addr)

	fmt.Printf("collecting %s from %s -> %s\n", profileType, addrNorm, *output)
	if err := collector.FetchRemote(addrNorm, profileType, *seconds, *output); err != nil {
		fmt.Fprintln(os.Stderr, "collect:", err)
		os.Exit(1)
	}
	fmt.Println("saved", *output)
	fmt.Printf("view with: go tool pprof %s\n", *output)
}

func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dir := fs.String("dir", "profiles", "directory with profile files")
	_ = fs.Parse(args)

	profiles, err := collector.List(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list:", err)
		os.Exit(1)
	}
	if len(profiles) == 0 {
		fmt.Printf("no profiles in %s\n", *dir)
		return
	}
	fmt.Printf("%-32s %10s  %s\n", "NAME", "SIZE", "PATH")
	for _, p := range profiles {
		fmt.Printf("%-32s %10d  %s\n", p.Name, p.Size, p.Path)
	}
}

func normalizeType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "cpu", "profile":
		return collector.ProfileCPU
	case "heap", "mem":
		return collector.ProfileHeap
	case "goroutine":
		return collector.ProfileGoroutine
	case "block":
		return collector.ProfileBlock
	case "mutex":
		return collector.ProfileMutex
	case "trace":
		return collector.ProfileTrace
	default:
		fmt.Fprintf(os.Stderr, "collect: unknown --type %q (want cpu, heap, goroutine, block, mutex, trace)\n", t)
		os.Exit(2)
		return ""
	}
}

func normalizeAddr(a string) string {
	a = strings.TrimSpace(a)
	lower := strings.ToLower(a)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		a = "http://" + a
	}
	return strings.TrimRight(a, "/")
}

func runFlame(args []string) {
	fs := flag.NewFlagSet("flame", flag.ExitOnError)
	input := fs.String("input", "", "path to a pprof profile (cpu.pb.gz, mem.pb.gz, …)")
	output := fs.String("output", "", "destination .svg path")
	folded := fs.String("folded", "", "optional path to also write folded stacks (flamegraph.pl/Speedscope compatible)")
	html := fs.String("html", "", "optional path to also write an HTML viewer (sticky search/zoom toolbar + inline SVG)")
	title := fs.String("title", "", "SVG title (default: Flamegraph — <sample type>)")
	width := fs.Int("width", 1200, "SVG canvas width in px")
	rowHeight := fs.Int("row-height", 22, "px height per stack frame")
	minWidth := fs.Float64("min-width", 0.5, "hide frames narrower than this (px)")
	maxDepth := fs.Int("max-depth", 0, "truncate stacks deeper than this (0 = unlimited)")
	sampleIndex := fs.Int("sample-index", -1, "profile value index to render (-1 = auto: cpu time / alloc_space)")
	listSamples := fs.Bool("list-samples", false, "print the profile's sample types and exit")
	format := fs.String("format", "auto", "input format: auto (by extension), pprof, or folded")
	_ = fs.Parse(args)

	if *listSamples {
		if *input == "" {
			fmt.Fprintln(os.Stderr, "flame: --input is required with --list-samples")
			os.Exit(2)
		}
		types, err := flamegraph.SampleTypes(*input)
		if err != nil {
			fmt.Fprintln(os.Stderr, "flame:", err)
			os.Exit(1)
		}
		fmt.Printf("sample types in %s:\n", *input)
		for _, t := range types {
			fmt.Println(" ", t)
		}
		return
	}
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "flame: --input and --output are required")
		fs.Usage()
		os.Exit(2)
	}
	if *width <= 0 || *rowHeight <= 0 {
		fmt.Fprintln(os.Stderr, "flame: --width and --row-height must be positive")
		os.Exit(2)
	}
	if *minWidth < 0 || *maxDepth < 0 {
		fmt.Fprintln(os.Stderr, "flame: --min-width and --max-depth must not be negative")
		os.Exit(2)
	}

	opts := flamegraph.DefaultOptions()
	opts.Title = *title
	opts.Width = *width
	opts.RowHeight = *rowHeight
	opts.MinWidth = *minWidth
	opts.MaxDepth = *maxDepth
	opts.SampleIndex = *sampleIndex
	opts.FoldedOutput = *folded
	opts.HTMLOutput = *html

	mode := strings.ToLower(strings.TrimSpace(*format))
	switch mode {
	case "folded", "pprof":
	case "auto":
		mode = "pprof"
		lower := strings.ToLower(*input)
		if strings.HasSuffix(lower, ".folded") || strings.HasSuffix(lower, ".fold") {
			mode = "folded"
		}
	default:
		fmt.Fprintf(os.Stderr, "flame: unknown --format %q (want auto, pprof, or folded)\n", *format)
		os.Exit(2)
	}

	var (
		s   flamegraph.Summary
		err error
	)
	if mode == "folded" {
		s, err = flamegraph.GenerateFromFolded(*input, *output, opts)
	} else {
		s, err = flamegraph.Generate(*input, *output, opts)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "flame:", err)
		os.Exit(1)
	}
	fmt.Printf("flamegraph: %s -> %s (%d stacks, %d frames, total %d %s)\n",
		s.Input, s.SVG, s.Stacks, s.Frames, s.Total, s.Unit)
	if s.Folded != "" {
		fmt.Println("folded:    ", s.Folded)
	}
	if s.HTML != "" {
		fmt.Println("html:      ", s.HTML)
	}
	fmt.Println("open the .svg (or .html viewer) in a browser (click a frame to zoom, search to highlight)")
}

func runPs(args []string) {
	fs := flag.NewFlagSet("ps", flag.ExitOnError)
	goOnly := fs.Bool("go-only", false, "show only processes that look like Go binaries")
	_ = fs.Parse(args)

	procs, err := attach.ListProcesses(*goOnly)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ps:", err)
		os.Exit(1)
	}
	fmt.Printf("%-8s %-3s  %s\n", "PID", "GO", "COMMAND")
	for _, p := range procs {
		goTag := "-"
		if p.IsGo {
			goTag = "go"
		}
		fmt.Printf("%-8d %-3s  %s\n", p.PID, goTag, p.Display())
	}
}

func runAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	pid := fs.Int("pid", 0, "target process id")
	name := fs.String("name", "", "substring matching comm/exe/cmdline (exactly one match required)")
	seconds := fs.Int("seconds", 30, "sampling window in seconds")
	freq := fs.Int("freq", 99, "perf samples per second")
	callgraph := fs.String("callgraph", "dwarf", "perf unwinding: dwarf (reliable for Go) or fp")
	event := fs.String("event", "", "perf sampling event (default cycles; try cpu-clock on VMs/WSL without PMU)")
	outputDir := fs.String("output-dir", "profiles", "directory for the run artifacts")
	prefix := fs.String("prefix", "", "artifact directory name (default attach-<exe>-<pid>-<timestamp>)")
	noCPU := fs.Bool("no-cpu", false, "disable perf CPU sampling")
	noMem := fs.Bool("no-mem", false, "disable RSS memory sampling")
	memInterval := fs.Duration("mem-interval", 500*time.Millisecond, "RSS poll period")
	includeKernel := fs.Bool("include-kernel", false, "keep kernel frames in the flamegraph")
	title := fs.String("title", "", "SVG title (default: attach <exe> (pid N) — cpu)")
	width := fs.Int("width", 1200, "SVG canvas width in px")
	_ = fs.Parse(args)

	opts := attach.DefaultOptions()
	opts.PID = *pid
	opts.Name = *name
	opts.Seconds = *seconds
	opts.Freq = *freq
	opts.Callgraph = *callgraph
	opts.Event = *event
	opts.OutputDir = *outputDir
	opts.Prefix = *prefix
	opts.CPU = !*noCPU
	opts.Mem = !*noMem
	opts.MemInterval = *memInterval
	opts.IncludeKernel = *includeKernel
	opts.Title = *title
	fopts := flamegraph.DefaultOptions()
	fopts.Width = *width
	opts.FlameOpts = fopts

	fmt.Printf("attaching to %s for %ds (cpu=%v mem=%v)...\n",
		targetDesc(*pid, *name), opts.Seconds, opts.CPU, opts.Mem)
	s, err := attach.Attach(opts)
	for _, w := range s.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("target: pid %d %s\n", s.Proc.PID, s.Proc.Display())
	if s.SVG != "" {
		fmt.Printf("flamegraph: %s (%d samples, %d stacks, %d frames)\n",
			s.SVG, s.Samples, s.Stacks, s.Frames)
		fmt.Printf("folded:     %s\nperf data:  %s (use `perf report -i` for details)\n",
			s.Folded, s.PerfData)
	}
	if s.HaveMem {
		fmt.Printf("memory:     %s (min %d KB, max %d KB, avg %d KB, growth %+d KB over %.1fs)\n",
			s.Mem.CSV, s.Mem.MinKB, s.Mem.MaxKB, s.Mem.AvgKB, s.Mem.GrowthKB, s.Mem.ElapsedSecs)
	}
}

func targetDesc(pid int, name string) string {
	if pid > 0 {
		return fmt.Sprintf("pid %d", pid)
	}
	return fmt.Sprintf("process matching %q", name)
}

// stringSlice collects repeatable KEY=VAL style flags.
type stringSlice []string

func (e *stringSlice) String() string { return strings.Join(*e, ",") }
func (e *stringSlice) Set(v string) error {
	if v == "" {
		return fmt.Errorf("value required (want KEY=VAL)")
	}
	// --go-flag takes bare build flags; --env takes KEY=VAL.
	if strings.HasPrefix(v, "-") {
		*e = append(*e, v)
		return nil
	}
	key, _, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("want KEY=VAL, got %q", v)
	}
	*e = append(*e, v)
	return nil
}

func runRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	seconds := fs.Int("seconds", 0, "sampling window in seconds (0 = until the app exits)")
	freq := fs.Int("freq", 99, "perf samples per second")
	callgraph := fs.String("callgraph", "dwarf", "perf unwinding: dwarf (reliable for Go) or fp")
	event := fs.String("event", "", "perf sampling event (default cycles; try cpu-clock on VMs/WSL without PMU)")
	outputDir := fs.String("output-dir", "profiles", "directory for the run artifacts")
	prefix := fs.String("prefix", "", "artifact directory name (default run-<cmd>-<timestamp>)")
	noCPU := fs.Bool("no-cpu", false, "disable perf CPU sampling")
	noMem := fs.Bool("no-mem", false, "disable RSS memory sampling")
	memInterval := fs.Duration("mem-interval", 500*time.Millisecond, "RSS poll period")
	includeKernel := fs.Bool("include-kernel", false, "keep kernel frames in the flamegraph")
	pprofAddr := fs.String("pprof-addr", "", "fetch the child's pprof endpoint too (e.g. localhost:6060)")
	pprofWait := fs.Duration("pprof-wait", 15*time.Second, "how long to wait for the pprof endpoint")
	pprofSeconds := fs.Int("pprof-seconds", 10, "endpoint CPU profile duration in seconds")
	detach := fs.Bool("detach", false, "leave the app running when the window ends (default: terminate it)")
	title := fs.String("title", "", "SVG title override")
	width := fs.Int("width", 1200, "SVG canvas width in px")
	dir := fs.String("dir", "", "working directory for the app")
	var envs stringSlice
	fs.Var(&envs, "env", "extra child env (KEY=VAL, repeatable)")
	fs.Var(&envs, "e", "shorthand for --env")
	var goFlags stringSlice
	fs.Var(&goFlags, "go-flag", "go build flag for .go/package targets, repeatable (e.g. --go-flag -race)")
	_ = fs.Parse(args)

	cmd := fs.Args()
	if len(cmd) == 0 {
		fmt.Fprintln(os.Stderr, "run: no command given (usage: goprof run [flags] -- <cmd> [args...])")
		os.Exit(2)
	}

	opts := runner.DefaultOptions(cmd)
	opts.Seconds = *seconds
	opts.Freq = *freq
	opts.Callgraph = *callgraph
	opts.Event = *event
	opts.OutputDir = *outputDir
	opts.Prefix = *prefix
	opts.CPU = !*noCPU
	opts.Mem = !*noMem
	opts.MemInterval = *memInterval
	opts.IncludeKernel = *includeKernel
	opts.PprofAddr = *pprofAddr
	opts.PprofWait = *pprofWait
	opts.PprofSeconds = *pprofSeconds
	opts.Detach = *detach
	opts.Title = *title
	opts.Dir = *dir
	opts.Env = []string(envs)
	opts.GoFlags = []string(goFlags)
	fopts := flamegraph.DefaultOptions()
	fopts.Width = *width
	opts.FlameOpts = fopts

	fmt.Printf("running profiling: %s\n", strings.Join(cmd, " "))
	s, err := runner.Run(opts)
	if s.ViaGoRun {
		fmt.Println("(target is Go source, launched via `go run`)")
	}
	for _, w := range s.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	printRunSummary(s)
	if err != nil {
		if errors.Is(err, runner.ErrInterrupted) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Propagate the app's own exit code when it ran to completion.
	if s.ExitedEarly && s.ExitCode != 0 {
		os.Exit(s.ExitCode)
	}
}

func printRunSummary(s runner.Summary) {
	if s.Detached {
		fmt.Printf("app left running (pid %d, --detach)\n", s.PID)
	} else if s.ExitedEarly {
		fmt.Printf("app exited with code %d after %.1fs\n", s.ExitCode, s.Duration.Seconds())
	} else {
		fmt.Printf("window ended after %.1fs (app terminated)\n", s.Duration.Seconds())
	}
	if s.SVG != "" {
		fmt.Printf("flamegraph: %s (%d samples, %d stacks, %d frames)\n",
			s.SVG, s.Samples, s.Stacks, s.Frames)
	}
	if s.Folded != "" {
		fmt.Printf("folded:     %s\nperf data:  %s\n", s.Folded, s.PerfData)
	}
	if s.HaveMem {
		fmt.Printf("memory:     %s (min %d KB, max %d KB, avg %d KB, growth %+d KB over %.1fs)\n",
			s.Mem.CSV, s.Mem.MinKB, s.Mem.MaxKB, s.Mem.AvgKB, s.Mem.GrowthKB, s.Mem.ElapsedSecs)
	}
	if s.PprofHeap != "" {
		fmt.Printf("pprof heap: %s -> %s\n", s.PprofHeap, s.PprofHeapSVG)
	}
	if s.PprofCPU != "" {
		fmt.Printf("pprof cpu:  %s -> %s\n", s.PprofCPU, s.PprofCPUSVG)
	}
}
