package attach

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// versionedPerfGlobs locates real perf binaries that the distro
// wrapper (/usr/bin/perf) dispatches to. Overridable in tests.
var versionedPerfGlobs = []string{
	"/usr/lib/linux-tools-*/perf",
	"/usr/bin/perf_*",
}

// perfBinary resolves a working perf binary.
//
//  1. GOPROF_PERF_BIN, when set, wins (must exist and be executable).
//  2. Plain "perf" from PATH, when it answers --version cleanly.
//  3. The newest versioned binary (e.g. /usr/lib/linux-tools-*/perf).
//
// Step 3 is what saves WSL2 hosts, where the distro wrapper only says
// "WARNING: perf not found for kernel …-microsoft" because no
// microsoft-flavoured tools package exists.
func perfBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv("GOPROF_PERF_BIN")); override != "" {
		if st, err := os.Stat(override); err != nil || st.IsDir() {
			return "", fmt.Errorf("attach: GOPROF_PERF_BIN=%q is not usable: %v", override, err)
		}
		if err := probePerf(override); err != nil {
			return "", fmt.Errorf("attach: GOPROF_PERF_BIN=%q failed: %v", override, err)
		}
		return override, nil
	}

	if p, err := exec.LookPath("perf"); err == nil {
		if probeErr := probePerf(p); probeErr == nil {
			return "perf", nil
		} else if !isWrapperMiss(probeErr.output) {
			// "perf" exists but is broken for another reason (no
			// permission to run it at all, etc.): surface it.
			return "", fmt.Errorf("attach: perf breaks: %v", probeErr)
		}
		// Wrapper miss (e.g. WSL kernel): fall through to versioned.
	}

	best := ""
	for _, g := range versionedPerfGlobs {
		matches, _ := filepath.Glob(g)
		sort.Strings(matches)
		for i := len(matches) - 1; i >= 0; i-- { // newest first
			cand := matches[i]
			if cand == best {
				continue
			}
			if st, err := os.Stat(cand); err != nil || st.IsDir() || st.Mode().Perm()&0o111 == 0 {
				continue
			}
			if probePerf(cand) == nil {
				best = cand
				break
			}
		}
		if best != "" {
			break
		}
	}
	if best != "" {
		return best, nil
	}
	return "", fmt.Errorf("attach: no working perf found\n" +
		"  install it (e.g. `sudo apt install linux-tools-generic`), then retry.\n" +
		"  WSL2 note: the distro wrapper looks for microsoft-flavoured tools that\n" +
		"  don't exist; point goprof at the real binary instead:\n" +
		"    export GOPROF_PERF_BIN=/usr/lib/linux-tools-<ver>/perf\n" +
		"  or fix `perf` system-wide:\n" +
		"    sudo ln -sf /usr/lib/linux-tools-<ver>/perf /usr/local/bin/perf")
}

type probeError struct {
	output string
	err    error
}

func (e *probeError) Error() string { return e.output }

// probePerf reports whether bin answers --version successfully.
// A distro wrapper that only prints its "not found for kernel"
// warning counts as failure (see isWrapperMiss).
func probePerf(bin string) *probeError {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return &probeError{output: "perf --version timed out", err: ctx.Err()}
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return &probeError{output: text, err: err}
	}
	if isWrapperMiss(text) {
		return &probeError{output: text, err: fmt.Errorf("wrapper miss")}
	}
	return nil
}

// isWrapperMiss detects the distro wrapper's refusal to dispatch,
// e.g. "WARNING: perf not found for kernel 6.18.33.2-microsoft".
func isWrapperMiss(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "perf not found for kernel")
}

// wrapperHint explains the WSL wrapper failure with a fix.
func wrapperHint(kernelMsg string) string {
	return "the distro perf wrapper has no binary for this kernel (" + strings.TrimSpace(kernelMsg) + ").\n" +
		"  goprof usually routes around this automatically via GOPROF_PERF_BIN or\n" +
		"  versioned binaries; to fix `perf` system-wide (e.g. on WSL2):\n" +
		"    ls /usr/lib/linux-tools-*/perf\n" +
		"    sudo ln -sf /usr/lib/linux-tools-<ver>/perf /usr/local/bin/perf"
}

// pmuHint suggests a software event when hardware counters are
// unavailable (common in VMs without PMU passthrough, incl. WSL).
func pmuHint() string {
	return "hardware counters look unavailable (VM without PMU passthrough?); " +
		"retry with a software event, e.g. `--event cpu-clock`"
}

// isPMUFailure matches perf's no-counters signatures.
func isPMUFailure(output string) bool {
	lower := strings.ToLower(output)
	for _, sig := range []string{
		"not supported", "not counted", "no such device",
		"<not supported>", "pmu", "no hardware sampling",
	} {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}
