package attach

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"goprof/flamegraph"
)

// Callgraph unwinding modes for perf record.
const (
	CallgraphDwarf = "dwarf"
	CallgraphFP    = "fp"
)

// PerfAvailable reports whether the perf binary exists and runs.
// A nil error does not guarantee recording permission (that depends
// on kernel.perf_event_paranoid and UID); record failures surface
// from RecordCPU with the kernel's message.
func PerfAvailable() error {
	if _, err := exec.LookPath("perf"); err != nil {
		return fmt.Errorf("attach: perf not found (install linux-tools / linux-perf, e.g. `apt install linux-tools-generic`)")
	}
	return nil
}

// RecordCPU samples a live PID with `perf record` for the given
// duration. freq is samples/sec (99 is a good default), callgraph is
// "dwarf" (reliable for Go, default) or "fp". Returns the perf.data
// path. Requires Linux, perf, and permission to trace the target
// (same UID or root; kernel.perf_event_paranoid <= 2 for user space).
func RecordCPU(pid, seconds, freq int, callgraph, workDir string) (string, error) {
	if err := PerfAvailable(); err != nil {
		return "", err
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
		return "", fmt.Errorf("attach: no such process (pid %d)", pid)
	}
	if seconds <= 0 {
		return "", fmt.Errorf("attach: seconds must be > 0")
	}
	if freq <= 0 {
		freq = 99
	}
	if callgraph != CallgraphFP {
		callgraph = CallgraphDwarf
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("attach: mkdir %s: %w", workDir, err)
	}
	dataPath := filepath.Join(workDir, "perf.data")

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(seconds+30)*time.Second)
	defer cancel()

	args := []string{
		"record", "-F", fmt.Sprint(freq),
		"-p", fmt.Sprint(pid),
		"-g", "--call-graph", callgraph,
		"-o", dataPath,
		"--", "sleep", fmt.Sprint(seconds),
	}
	cmd := exec.CommandContext(ctx, "perf", args...)
	out, err := cmd.CombinedOutput()
	tail := strings.TrimSpace(string(out))
	if len(tail) > 800 {
		tail = tail[len(tail)-800:]
	}
	if err != nil {
		hint := ""
		if strings.Contains(tail, "Permission") || strings.Contains(tail, "permission") ||
			strings.Contains(tail, "paranoid") || strings.Contains(tail, "Access") {
			hint = " (hint: run as the same user as the target or root; check /proc/sys/kernel/perf_event_paranoid, values 2 or lower allow user-space sampling)"
		}
		return "", fmt.Errorf("attach: perf record failed%s: %v\nperf output: %s", hint, err, tail)
	}
	if fi, err := os.Stat(dataPath); err != nil || fi.Size() == 0 {
		return "", fmt.Errorf("attach: perf produced no data (target may have exited): %s", tail)
	}
	return dataPath, nil
}

var (
	// e.g. "myapp 1234/1235 [003] 12345.678901: 10000 cycles:u:"
	perfEventRe = regexp.MustCompile(`^\S+\s+(\d+)(?:/\d+)?\s+\[\d+\]\s+[\d.]+:`)
	// e.g. "    7f9a2b3c main.burnCPU+0x2f (/tmp/myapp)"
	perfFrameRe = regexp.MustCompile(`^\s+[0-9a-fA-F]+\s+(.+?)\s+\((.+)\)\s*$`)
)

// ScriptToFolded runs `perf script` on perf.data and collapses the
// stacks to folded format at foldedPath. wantPID filters samples to
// one process (0 = accept all). Kernel frames are dropped unless
// includeKernel (they are then suffixed `_[k]`). Returns the sample
// count kept.
func ScriptToFolded(perfDataPath, foldedPath string, wantPID int, includeKernel bool) (samples int, err error) {
	if err := PerfAvailable(); err != nil {
		return 0, err
	}
	cmd := exec.Command("perf", "script", "-i", perfDataPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("attach: perf script pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("attach: perf script: %w", err)
	}

	agg := map[string]int{}
	funcsOf := map[string][]string{}
	scanErr := func() error {
		var err error
		samples, err = parsePerfScript(stdout, wantPID, includeKernel, agg, funcsOf)
		return err
	}()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return 0, fmt.Errorf("attach: read perf script: %w", scanErr)
	}
	if waitErr != nil {
		return 0, fmt.Errorf("attach: perf script failed: %v\n%s", waitErr, stderr.String())
	}
	if samples == 0 {
		return 0, fmt.Errorf("attach: no usable samples in %s (all kernel/idle filtered? retry with --include-kernel)", perfDataPath)
	}

	stacks := make([]flamegraph.Stack, 0, len(agg))
	for k, v := range agg {
		stacks = append(stacks, flamegraph.Stack{Funcs: funcsOf[k], Value: int64(v)})
	}
	sort.Slice(stacks, func(i, j int) bool {
		return strings.Join(stacks[i].Funcs, ";") < strings.Join(stacks[j].Funcs, ";")
	})
	if err := os.MkdirAll(filepath.Dir(foldedPath), 0o755); err != nil {
		return 0, fmt.Errorf("attach: mkdir folded: %w", err)
	}
	f, err := os.Create(foldedPath)
	if err != nil {
		return 0, fmt.Errorf("attach: create %s: %w", foldedPath, err)
	}
	err = flamegraph.WriteFolded(stacks, f)
	closeErr := f.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, fmt.Errorf("attach: write %s: %w", foldedPath, closeErr)
	}
	return samples, nil
}

// parsePerfScript collapses `perf script` output into folded-stack
// counts. wantPID filters to one process (0 = all); kernel frames are
// dropped unless includeKernel (kept frames get an `_[k]` suffix).
func parsePerfScript(r io.Reader, wantPID int, includeKernel bool, agg map[string]int, funcsOf map[string][]string) (samples int, err error) {
	flush := func(pid int, frames []string) {
		if wantPID != 0 && pid != wantPID {
			return
		}
		// perf script prints leaf-first; folded wants root-first.
		var rootFirst []string
		for i := len(frames) - 1; i >= 0; i-- {
			rootFirst = append(rootFirst, frames[i])
		}
		if len(rootFirst) == 0 {
			return
		}
		key := strings.Join(rootFirst, "\x00")
		agg[key]++
		if _, ok := funcsOf[key]; !ok {
			funcsOf[key] = rootFirst
		}
		samples++
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var (
		curPID    int
		curLeaf   []string
		inSample  bool
		haveEvent bool
	)
	for sc.Scan() {
		line := sc.Text()
		if m := perfEventRe.FindStringSubmatch(line); m != nil {
			if inSample && haveEvent {
				flush(curPID, curLeaf)
			}
			fmt.Sscan(m[1], &curPID)
			curLeaf = nil
			inSample, haveEvent = true, true
			continue
		}
		if !inSample {
			continue // headers, comments
		}
		if strings.TrimSpace(line) == "" {
			if haveEvent {
				flush(curPID, curLeaf)
			}
			inSample, haveEvent = false, false
			curLeaf = nil
			continue
		}
		m := perfFrameRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		sym, dso := cleanSymbol(m[1]), m[2]
		isKernel := strings.Contains(dso, "kernel")
		switch {
		case isKernel && !includeKernel:
			continue
		case isKernel:
			sym += "_[k]"
		}
		if sym == "" {
			continue
		}
		curLeaf = append(curLeaf, sym)
	}
	if inSample && haveEvent {
		flush(curPID, curLeaf)
	}
	return samples, sc.Err()
}

// cleanSymbol strips `+0xOFFSET` and call args from a perf symbol.
func cleanSymbol(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "+0x"); i >= 0 {
		s = s[:i]
	} else if i := strings.Index(s, "+"); i >= 0 && strings.Trim(s[i+1:], "0123456789abcdefx") == "" {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}
