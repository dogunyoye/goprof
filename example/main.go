// Example app instrumented with goprof.
//
// Run:
//
//	go run ./example
//
// Then in another terminal:
//
//	go tool pprof profiles/cpu.pb.gz
//	go tool pprof profiles/mem.pb.gz
//
// Flamegraphs are generated automatically on shutdown:
// profiles/flame-cpu.svg, profiles/flame-mem.svg (open in a browser).
//
// Or expose pprof over HTTP by setting ServerAddr and:
//
//	go run ./cmd/goprof collect --addr localhost:6060 --type heap --output profiles/remote-heap.pb.gz
package main

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"goprof/flamegraph"
	"goprof/profiler"
)

func main() {
	cfg := profiler.DefaultConfig()
	cfg.OutputDir = "profiles"
	cfg.ServerAddr = "localhost:6060" // try: go tool pprof http://localhost:6060/debug/pprof/heap
	p := profiler.New(cfg)

	if err := p.Start(); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("profiles written to ./profiles — view with: go tool pprof profiles/cpu.pb.gz")
		for _, fc := range []struct{ in, out string }{
			{"profiles/cpu.pb.gz", "profiles/flame-cpu.svg"},
			{"profiles/mem.pb.gz", "profiles/flame-mem.svg"},
		} {
			if s, err := flamegraph.Generate(fc.in, fc.out, flamegraph.DefaultOptions()); err != nil {
				log.Printf("flamegraph %s: %v", fc.in, err)
			} else {
				fmt.Printf("flamegraph: %s (%d stacks, %d frames) — open in a browser\n", s.SVG, s.Stacks, s.Frames)
			}
		}
	}()

	fmt.Println("profiling for ~12s (pprof at http://localhost:6060/debug/pprof/)...")
	// Simulate CPU + allocation pressure.
	deadline := time.Now().Add(12 * time.Second)
	var sink [][]byte
	for time.Now().Before(deadline) {
		burnCPU(200000)
		// Allocate ~ small churn so heap profile is non-empty.
		sink = append(sink, make([]byte, 8<<10))
		if len(sink) > 200 {
			sink = sink[50:]
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Mid-run heap snapshot example.
	if err := p.SnapshotHeap("profiles/mem-snapshot.pb.gz"); err != nil {
		log.Printf("snapshot: %v", err)
	} else {
		fmt.Println("wrote profiles/mem-snapshot.pb.gz")
	}

	_ = sink
	rand.Shuffle(1, nil)
}

func burnCPU(n int) float64 {
	x := 0.0
	for i := 0; i < n; i++ {
		x += float64(i%97) * 1.0001
	}
	return x
}
