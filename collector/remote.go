package collector

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"goprof/internal/atomicfile"
)

// Common remote profile types (map to /debug/pprof/<name>). Any other
// endpoint name served by net/http/pprof (allocs, threadcreate, …) is
// accepted by FetchRemote too.
const (
	ProfileCPU       = "profile"
	ProfileHeap      = "heap"
	ProfileGoroutine = "goroutine"
	ProfileBlock     = "block"
	ProfileMutex     = "mutex"
	ProfileTrace     = "trace"
)

// maxRemoteProfileBytes caps a single fetched profile (256MB).
const maxRemoteProfileBytes = 256 << 20

// defaultCaptureSeconds is used for cpu/trace when seconds <= 0.
const defaultCaptureSeconds = 30

// NormalizeAddr turns a user-supplied pprof server address into a base
// URL without a trailing slash. A missing scheme defaults to http (the
// scheme check is case-insensitive), and the result must have a host.
//
//	NormalizeAddr("localhost:6060")       -> "http://localhost:6060"
//	NormalizeAddr("HTTPS://host:6060/")   -> "https://host:6060"
//	NormalizeAddr("http://")              -> error
func NormalizeAddr(addr string) (string, error) {
	a := strings.TrimSpace(addr)
	if !strings.Contains(a, "://") {
		a = "http://" + a
	}
	u, err := url.Parse(a)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("collector: bad address %q", addr)
	}
	if u.Scheme != "http" && u.Scheme != "https" { // url.Parse lowercases the scheme
		return "", fmt.Errorf("collector: bad address %q (want http or https)", addr)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// validProfileName reports whether t is safe to use as a single
// /debug/pprof/<name> path segment.
func validProfileName(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return t != "." && t != ".."
}

// FetchRemote downloads a pprof profile from a running app.
//
// baseAddr examples: "http://localhost:6060", "localhost:6060".
// profileType: one of the Profile* constants, or any other
// /debug/pprof endpoint name (e.g. "allocs", "threadcreate").
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
	if outputPath == "" {
		return fmt.Errorf("collector: output path required")
	}
	if !validProfileName(profileType) {
		return fmt.Errorf("collector: invalid profile type %q", profileType)
	}
	base, err := NormalizeAddr(baseAddr)
	if err != nil {
		return err
	}
	endpoint := base + "/debug/pprof/" + profileType
	timeout := 60 * time.Second
	if profileType == ProfileCPU || profileType == ProfileTrace {
		if seconds <= 0 {
			seconds = defaultCaptureSeconds
		}
		endpoint += fmt.Sprintf("?seconds=%d", seconds)
		timeout += time.Duration(seconds) * time.Second
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(endpoint) // #nosec G107 -- endpoint built from a validated URL + path-safe profile name
	if err != nil {
		return fmt.Errorf("collector: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("collector: GET %s: status %s", endpoint, resp.Status)
	}

	// Download via temp file + rename so a failed fetch never leaves a
	// truncated profile at outputPath.
	var copyErr error
	err = atomicfile.Write(outputPath, func(f *os.File) error {
		limited := &io.LimitedReader{R: resp.Body, N: maxRemoteProfileBytes + 1}
		if _, err := io.Copy(f, limited); err != nil {
			copyErr = fmt.Errorf("collector: save %s: %w", outputPath, err)
			return copyErr
		}
		if limited.N <= 0 {
			copyErr = fmt.Errorf("collector: save %s: profile exceeds %d bytes", outputPath, maxRemoteProfileBytes)
			return copyErr
		}
		return nil
	})
	if err != nil && copyErr == nil {
		return fmt.Errorf("collector: save: %w", err)
	}
	return err
}
