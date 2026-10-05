package runner

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"goprof/collector"
	"goprof/flamegraph"
)

type pprofResult struct {
	cpu, heap, cpuSVG, heapSVG string
	warnings                   []string
	err                        error
}

// fetchEndpoint waits for the child's pprof endpoint, then fetches
// heap (fast) and CPU (cpuSeconds) profiles and renders both as
// flamegraphs. A child that exits mid-fetch keeps whatever completed.
func fetchEndpoint(addr string, wait time.Duration, cpuSeconds int, workDir string, fopts flamegraph.Options, title string) pprofResult {
	var r pprofResult
	base := normalizePprofAddr(addr)
	if err := waitPprofReady(base, wait); err != nil {
		r.err = err
		return r
	}

	heapPath := filepath.Join(workDir, "pprof-heap.pb.gz")
	if err := collector.FetchRemote(base, collector.ProfileHeap, 0, heapPath); err != nil {
		r.err = fmt.Errorf("fetch heap: %w", err)
		return r
	}
	r.heap = heapPath

	cpuPath := filepath.Join(workDir, "pprof-cpu.pb.gz")
	if err := collector.FetchRemote(base, collector.ProfileCPU, cpuSeconds, cpuPath); err != nil {
		r.warnings = append(r.warnings, "cpu fetch: "+err.Error()+" (heap kept; child may have exited)")
	} else {
		r.cpu = cpuPath
	}

	render := func(in, out, sample string) string {
		o := fopts
		if o.Width == 0 {
			o = flamegraph.DefaultOptions()
		}
		if o.Title == "" {
			o.Title = title
			if o.Title == "" {
				o.Title = "pprof " + sample + " — " + addr
			} else {
				o.Title += " — " + sample
			}
		}
		s, err := flamegraph.Generate(in, out, o)
		if err != nil {
			r.warnings = append(r.warnings, "render "+sample+": "+err.Error())
			return ""
		}
		return s.SVG
	}
	if r.heap != "" {
		r.heapSVG = render(r.heap, filepath.Join(workDir, "flame-pprof-heap.svg"), "heap")
	}
	if r.cpu != "" {
		r.cpuSVG = render(r.cpu, filepath.Join(workDir, "flame-pprof-cpu.svg"), "cpu")
	}
	return r
}

// waitPprofReady polls the pprof index until 200 OK or timeout.
func waitPprofReady(base string, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for {
		resp, err := client.Get(base + "/debug/pprof/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("pprof endpoint %s not ready after %s: %w", base, timeout, err)
			}
			return fmt.Errorf("pprof endpoint %s not ready after %s", base, timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func normalizePprofAddr(a string) string {
	a = strings.TrimSpace(a)
	if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
		a = "http://" + a
	}
	return strings.TrimSuffix(a, "/")
}
