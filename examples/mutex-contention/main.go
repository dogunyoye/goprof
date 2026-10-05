// Example mutex-contention: workers hammering shared state under
// locks, plus slow readers — the classic contended-counter service.
//
// What to look for: mutex profile hot in lockedIncrement; block profile
// shows waiters. Needs the SetMutexProfileFraction / SetBlockProfileRate
// calls below, otherwise those profiles stay empty.
//
// Try:
//
//	go run ./examples/mutex-contention
//	go run ./cmd/goprof collect --addr localhost:6065 --type mutex --output profiles/mutex.pb.gz
//	go run ./cmd/goprof collect --addr localhost:6065 --type block --output profiles/block.pb.gz
//	go tool pprof -top profiles/mutex.pb.gz
package main

import (
	"flag"
	"fmt"
	"runtime"
	"sync"
	"time"

	"goprof/profiler"
)

var (
	mu      sync.Mutex // guards stats: hammered by every writer
	rw      sync.RWMutex
	counter int64
	cache   = map[string]int64{}
	ops     int64
)

func main() {
	workers := flag.Int("workers", 16, "writer goroutines")
	duration := flag.Duration("duration", 20*time.Second, "how long to contend")
	pprofAddr := flag.String("pprof", "localhost:6065", "pprof endpoint (empty disables)")
	flag.Parse()

	// Without these the mutex/block profiles record nothing.
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(100000) // sample waits >= 100us

	fmt.Println("=== goprof example: mutex-contention ===")
	fmt.Println("what: writers contend on one mutex, readers hold RLock while writers wait")
	fmt.Println("look: mutex profile in lockedIncrement; block profile in lock waiters")
	if *pprofAddr != "" {
		fmt.Printf("pprof: http://%s/debug/pprof/\n", *pprofAddr)
		profiler.StartPprofServer(*pprofAddr)
	}

	deadline := time.Now().Add(*duration)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				lockedIncrement()
			}
		}()
	}
	// Slow readers hold the read lock while writers queue behind them.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			slowRead()
		}
	}()
	wg.Wait()
	fmt.Printf("done: counter=%d cacheKeys=%d ops=%d\n", counter, len(cache), ops)
}

func lockedIncrement() {
	// Exclusive state update: every writer serializes here, and slow
	// readers below make them queue.
	rw.Lock()
	counter++
	cache["n"] = counter
	rw.Unlock()

	// Second contended lock so the mutex profile has its own hotspot.
	mu.Lock()
	ops++
	mu.Unlock()
}

func slowRead() {
	// Readers hold RLock long enough that writers pile up behind them;
	// that queueing is what the block profile records.
	rw.RLock()
	time.Sleep(time.Millisecond)
	_ = counter
	rw.RUnlock()
}
