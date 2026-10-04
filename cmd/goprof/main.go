// Command goprof collects pprof profiles from a running Go app.
//
// Usage:
//
//	goprof collect --addr http://localhost:6060 --type heap --output profiles/heap.pb.gz
//	goprof collect --addr http://localhost:6060 --type cpu --seconds 30 --output profiles/cpu.pb.gz
//	goprof list --dir profiles
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"goprof/collector"
)

const version = "v0.1.0"

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
	case "version", "--version", "-v":
		fmt.Println("goprof", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`goprof - tiny profiler client for Go pprof endpoints

Commands:
  collect   fetch a profile from /debug/pprof
  list      list local profile files
  version   print version

Examples:
  goprof collect --addr http://localhost:6060 --type heap --output profiles/heap.pb.gz
  goprof collect --addr http://localhost:6060 --type cpu --seconds 30 --output profiles/cpu.pb.gz
  goprof list --dir profiles`)
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
		fmt.Fprintf(os.Stderr, "unknown --type %q, using %q\n", t, t)
		return t
	}
}

func normalizeAddr(a string) string {
	a = strings.TrimSpace(a)
	if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
		a = "http://" + a
	}
	return strings.TrimSuffix(a, "/")
}
