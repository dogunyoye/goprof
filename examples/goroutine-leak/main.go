// Example goroutine-leak: spawns goroutines that block forever,
// simulating the classic "leaked worker per request" bug.
//
// What to look for: the goroutine profile grows linearly; heap grows
// slowly (each leaked goroutine holds 4KB + an 8KB stack).
//
// Try:
//
//	go run ./examples/goroutine-leak
//	go run ./cmd/goprof collect --addr localhost:6064 --type goroutine --output profiles/leak-goroutine.pb.gz
//	go tool pprof -top profiles/leak-goroutine.pb.gz
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"goprof/profiler"
)

func main() {
	rate := flag.Int("rate", 20, "leaked goroutines per second")
	duration := flag.Duration("duration", 25*time.Second, "how long to leak")
	pprofAddr := flag.String("pprof", "localhost:6064", "pprof endpoint (empty disables)")
	flag.Parse()
	if *rate <= 0 {
		fmt.Fprintln(os.Stderr, "rate must be positive")
		os.Exit(2)
	}
	if *duration <= 0 {
		fmt.Fprintln(os.Stderr, "duration must be positive")
		os.Exit(2)
	}

	fmt.Println("=== goprof example: goroutine-leak ===")
	fmt.Println("what: leaks blocked goroutines holding 4KB each (the per-request worker bug)")
	fmt.Println("look: goroutine profile count climbs; compare first vs last capture")
	if *pprofAddr != "" {
		fmt.Printf("pprof: http://%s/debug/pprof/goroutine\n", *pprofAddr)
		profiler.StartPprofServer(*pprofAddr)
	}

	deadline := time.Now().Add(*duration)
	tick := time.NewTicker(time.Second / time.Duration(*rate))
	defer tick.Stop()
	leaked := 0
	lastReport := time.Now()
	for time.Now().Before(deadline) {
		select {
		case <-tick.C:
			leak()
			leaked++
		case <-time.After(time.Until(deadline)):
		}
		if time.Since(lastReport) >= 5*time.Second {
			fmt.Printf("leaked=%d goroutines=%d\n", leaked, runtime.NumGoroutine())
			lastReport = time.Now()
		}
	}
	fmt.Printf("done: leaked %d goroutines, runtime reports %d\n", leaked, runtime.NumGoroutine())
}

// leak starts a goroutine that blocks forever holding some memory,
// like a worker waiting on a reply that never comes.
func leak() {
	data := make([]byte, 4096)
	go func() {
		_ = data
		select {} // park forever
	}()
}
