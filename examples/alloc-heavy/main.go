// Example alloc-heavy: allocation churn plus a slow leak.
//
// What to look for: heap flamegraph dominated by makeChunks/append paths,
// and steadily growing RSS (watch `goprof attach/run` memory stats).
//
// Try:
//
//	go run ./examples/alloc-heavy
//	go run ./cmd/goprof collect --addr localhost:6062 --type heap --output profiles/alloc-heap.pb.gz
//	go run ./cmd/goprof flame --input profiles/alloc-heap.pb.gz --output profiles/flame-alloc.svg
//	go run ./cmd/goprof run --pprof-addr localhost:6062 --pprof-seconds 10 --no-cpu -- ./examples/alloc-heavy -duration 20s
package main

import (
	"flag"
	"fmt"
	"time"

	"goprof/profiler"
)

func main() {
	duration := flag.Duration("duration", 20*time.Second, "how long to churn")
	pprofAddr := flag.String("pprof", "localhost:6062", "pprof endpoint (empty disables)")
	flag.Parse()

	fmt.Println("=== goprof example: alloc-heavy ===")
	fmt.Println("what: 1MB churn per tick, 10% retained in a never-cleared cache (slow leak)")
	fmt.Println("look: heap flamegraph in makeChunks; RSS climbs for the whole run")
	if *pprofAddr != "" {
		fmt.Printf("pprof: http://%s/debug/pprof/\n", *pprofAddr)
		profiler.StartPprofServer(*pprofAddr)
	}

	deadline := time.Now().Add(*duration)
	var cache [][]byte // the "leak": retained forever
	tick := 0
	for time.Now().Before(deadline) {
		buf := makeChunks(256, 4096) // 256 x 4KB = 1MB per tick
		if tick%10 == 0 {
			cache = append(cache, buf...) // retain 10%
		}
		tick++
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Printf("done: %d ticks, cache holds %d chunks (~%.1f MB leaked)\n",
		tick, len(cache), float64(len(cache)*4096)/(1<<20))
}

func makeChunks(n, size int) [][]byte {
	out := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, make([]byte, size))
	}
	return out
}
