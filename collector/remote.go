package collector

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Supported remote profile types (map to /debug/pprof/<name>).
const (
	ProfileCPU       = "profile"
	ProfileHeap      = "heap"
	ProfileGoroutine = "goroutine"
	ProfileBlock     = "block"
	ProfileMutex     = "mutex"
	ProfileTrace     = "trace"
)

// FetchRemote downloads a pprof profile from a running app.
//
// baseAddr examples: "http://localhost:6060", "localhost:6060".
// profileType: one of Profile* constants.
// seconds only applies to cpu ("profile") and trace captures.
//
// Output examples:
//
//	FetchRemote("http://localhost:6060", "heap", 0, "profiles/remote-heap.pb.gz")
//	FetchRemote("http://localhost:6060", "profile", 30, "profiles/remote-cpu.pb.gz")
func FetchRemote(baseAddr, profileType string, seconds int, outputPath string) error {
	if baseAddr == "" {
		return fmt.Errorf("collector: base address required")
	}
	if _, err := url.Parse(baseAddr); err != nil {
		return fmt.Errorf("collector: bad address: %w", err)
	}
	// Allow bare "localhost:6060".
	endpoint := baseAddr + "/debug/pprof/" + profileType
	if profileType == ProfileCPU || profileType == ProfileTrace {
		if seconds <= 0 {
			seconds = 30
		}
		endpoint += fmt.Sprintf("?seconds=%d", seconds)
	}

	client := &http.Client{Timeout: time.Duration(seconds+60) * time.Second}
	resp, err := client.Get(endpoint) // #nosec G107 -- endpoint built from explicit args
	if err != nil {
		return fmt.Errorf("collector: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("collector: GET %s: status %s", endpoint, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("collector: mkdir: %w", err)
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("collector: create %s: %w", outputPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("collector: save %s: %w", outputPath, err)
	}
	return nil
}
