// Example http-server: a tiny JSON API with fast/slow/alloc endpoints
// plus a built-in load generator, so it profiles itself.
//
// What to look for: CPU spread across handlers (slowHandler dominates),
// allocations under allocHandler. Compare with the pprof top view.
//
// Try:
//
//	go run ./examples/http-server
//	curl localhost:8080/fast localhost:8080/slow localhost:8080/alloc localhost:8080/stats
//	go run ./cmd/goprof collect --addr localhost:6063 --type cpu --seconds 10 --output profiles/http-cpu.pb.gz
//	go run ./cmd/goprof flame --input profiles/http-cpu.pb.gz --output profiles/flame-http.svg
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"goprof/profiler"
)

var (
	fastN  atomic.Uint64
	slowN  atomic.Uint64
	allocN atomic.Uint64
)

func main() {
	addr := flag.String("addr", "localhost:8080", "API listen address")
	pprofAddr := flag.String("pprof", "localhost:6063", "pprof endpoint (empty disables)")
	duration := flag.Duration("duration", 20*time.Second, "how long to serve")
	load := flag.Bool("load", true, "run built-in load generator")
	clients := flag.Int("clients", 8, "load generator workers")
	flag.Parse()

	fmt.Println("=== goprof example: http-server ===")
	fmt.Println("what: /fast + /slow (50ms) + /alloc (256KB) with self load")
	fmt.Println("look: CPU in slowHandler; allocations in allocHandler")
	fmt.Printf("api:   http://%s/fast /slow /alloc /stats\n", *addr)
	if *pprofAddr != "" {
		fmt.Printf("pprof: http://%s/debug/pprof/\n", *pprofAddr)
		profiler.StartPprofServer(*pprofAddr)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/fast", func(w http.ResponseWriter, r *http.Request) {
		fastN.Add(1)
		fmt.Fprintln(w, "fast")
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		slowN.Add(1)
		slowHandler()
		fmt.Fprintln(w, "slow")
	})
	mux.HandleFunc("/alloc", func(w http.ResponseWriter, r *http.Request) {
		allocN.Add(1)
		allocHandler()
		fmt.Fprintln(w, "allocated")
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]uint64{
			"fast": fastN.Load(), "slow": slowN.Load(), "alloc": allocN.Load(),
		})
	})

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("api: %v", err)
		}
	}()

	if *load {
		done := make(chan struct{})
		for i := 0; i < *clients; i++ {
			go loadLoop(*addr, i, done)
		}
		time.Sleep(*duration)
		close(done)
	} else {
		time.Sleep(*duration)
	}
	fmt.Printf("done: fast=%d slow=%d alloc=%d\n", fastN.Load(), slowN.Load(), allocN.Load())
	srv.Close()
}

func slowHandler() { time.Sleep(50 * time.Millisecond) }

func allocHandler() {
	buf := make([]byte, 256<<10)
	for i := range buf {
		buf[i] = byte(i)
	}
	_ = buf
}

func loadLoop(addr string, id int, done chan struct{}) {
	paths := []string{"/fast", "/fast", "/slow", "/alloc"}
	i := 0
	for {
		select {
		case <-done:
			return
		default:
		}
		resp, err := http.Get("http://" + addr + paths[(id+i)%len(paths)])
		if err != nil {
			time.Sleep(50 * time.Millisecond) // server starting up / closing
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		i++
	}
}
