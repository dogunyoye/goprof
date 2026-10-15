package collector

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
//
// maxRemoteProfileBytes caps a single fetched profile (256MB).
const maxRemoteProfileBytes = 256 << 20

func validProfileType(t string) bool {
	switch t {
	case ProfileCPU, ProfileHeap, ProfileGoroutine, ProfileBlock, ProfileMutex, ProfileTrace:
		return true
	}
	return false
}

func FetchRemote(baseAddr, profileType string, seconds int, outputPath string) error {
	if baseAddr == "" {
		return fmt.Errorf("collector: base address required")
	}
	if outputPath == "" {
		return fmt.Errorf("collector: output path required")
	}
	if !validProfileType(profileType) {
		return fmt.Errorf("collector: unknown profile type %q", profileType)
	}
	// Allow bare "localhost:6060": add a scheme before parsing so the
	// host check below actually validates something.
	addr := strings.TrimSpace(baseAddr)
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return fmt.Errorf("collector: bad address %q", baseAddr)
	}
	endpoint := strings.TrimRight(u.String(), "/") + "/debug/pprof/" + profileType
	if profileType == ProfileCPU || profileType == ProfileTrace {
		if seconds <= 0 {
			seconds = 30
		}
		endpoint += fmt.Sprintf("?seconds=%d", seconds)
	}
	if seconds <= 0 {
		seconds = 30
	}

	client := &http.Client{Timeout: time.Duration(seconds+60) * time.Second}
	resp, err := client.Get(endpoint) // #nosec G107 -- endpoint built from allowlisted type + validated URL
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
	// Download to a temp file and rename so a failed fetch never
	// leaves a truncated profile at outputPath.
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".tmp-*.part")
	if err != nil {
		return fmt.Errorf("collector: create %s: %w", outputPath, err)
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmpName)
		}
	}()
	limited := &io.LimitedReader{R: resp.Body, N: maxRemoteProfileBytes + 1}
	if _, err := io.Copy(tmp, limited); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("collector: save %s: %w", outputPath, err)
	}
	if limited.N <= 0 {
		_ = tmp.Close()
		return fmt.Errorf("collector: save %s: profile exceeds %d bytes", outputPath, maxRemoteProfileBytes)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("collector: save %s: %w", outputPath, err)
	}
	if err := os.Rename(tmpName, outputPath); err != nil {
		return fmt.Errorf("collector: save %s: %w", outputPath, err)
	}
	done = true
	return nil
}
