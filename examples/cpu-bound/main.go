// Example cpu-bound: pure number crunching (prime sieve + sha256).
//
// What to look for: a CPU flamegraph dominated by sieve/sha256 frames,
// almost no heap growth.
//
// Try:
//
//	go run ./examples/cpu-bound
//	go run ./cmd/goprof collect --addr localhost:6061 --type cpu --seconds 10 --output profiles/cpu-bound.pb.gz
//	go run ./cmd/goprof flame --input profiles/cpu-bound.pb.gz --output profiles/flame-cpu-bound.svg
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"time"

	"goprof/flamegraph"
	"goprof/profiler"
)

func main() {
	duration := flag.Duration("duration", 15*time.Second, "how long to crunch")
	pprofAddr := flag.String("pprof", "localhost:6061", "pprof endpoint (empty disables)")
	outDir := flag.String("out", "profiles", "profile output dir")
	flag.Parse()

	fmt.Println("=== goprof example: cpu-bound ===")
	fmt.Println("what: prime sieve + sha256 stream on all idle time")
	fmt.Println("look: CPU flamegraph hot in sieve/sha256; flat heap")
	if *pprofAddr != "" {
		fmt.Printf("pprof: http://%s/debug/pprof/\n", *pprofAddr)
	}

	cfg := profiler.DefaultConfig()
	cfg.OutputDir = *outDir
	cfg.ServerAddr = *pprofAddr
	p := profiler.New(cfg)
	if err := p.Start(); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			log.Fatal(err)
		}
		for _, fc := range [][2]string{
			{*outDir + "/cpu.pb.gz", *outDir + "/flame-cpu-bound.svg"},
			{*outDir + "/mem.pb.gz", *outDir + "/flame-mem-cpu-bound.svg"},
		} {
			if s, err := flamegraph.Generate(fc[0], fc[1], flamegraph.DefaultOptions()); err != nil {
				log.Printf("flamegraph %s: %v", fc[0], err)
			} else {
				fmt.Printf("flamegraph: %s (%d stacks, %d frames)\n", s.SVG, s.Stacks, s.Frames)
			}
		}
	}()

	deadline := time.Now().Add(*duration)
	primes := 0
	var digest [32]byte
	for time.Now().Before(deadline) {
		primes += sieve(20000)
		h := sha256.New()
		fmt.Fprintf(h, "primes=%d digest=%x", primes, digest)
		copy(digest[:], h.Sum(nil))
	}
	fmt.Printf("done: found %d primes in %s\n", primes, *duration)
}

// sieve counts primes below n with trial division (slow on purpose).
func sieve(n int) int {
	count := 0
	for i := 2; i < n; i++ {
		prime := true
		for j := 2; j*j <= i; j++ {
			if i%j == 0 {
				prime = false
				break
			}
		}
		if prime {
			count++
		}
	}
	return count
}
