package profiler

import (
	"log"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on DefaultServeMux
)

// StartPprofServer serves net/http/pprof on addr in the background.
// Callers typically gate this behind a flag/env and firewall rules.
//
// Example: StartPprofServer("localhost:6060"), then:
//
//	go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
//	go tool pprof http://localhost:6060/debug/pprof/heap
func StartPprofServer(addr string) {
	go func() {
		// Deliberately DefaultServeMux so pprof handlers are registered.
		// #nosec G114 -- localhost-only by convention; caller chooses addr.
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("profiler: pprof server %s stopped: %v", addr, err)
		}
	}()
}
