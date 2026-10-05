package attach

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
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
	if seconds <= 0 {
		return "", fmt.Errorf("attach: seconds must be > 0")
	}
	rec, err := StartRecording(pid, freq, callgraph, workDir)
	if err != nil {
		return "", err
	}
	time.Sleep(time.Duration(seconds) * time.Second)
	if err := rec.Stop(); err != nil {
		return "", err
	}
	return rec.DataPath(), nil
}

// Recording is a running `perf record -p` session. It records until
// Stop (graceful, SIGINT) or Kill, which makes it suitable for
// profiling child processes whose lifetime is unknown upfront.
type Recording struct {
	cmd      *exec.Cmd
	dataPath string
	stderr   strings.Builder
	waited   chan error // Wait result; closed exactly once
}

// StartRecording begins `perf record` on pid in the background.
// Call Stop to finish gracefully and finalize perf.data.
func StartRecording(pid, freq int, callgraph, workDir string) (*Recording, error) {
	if err := PerfAvailable(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
		return nil, fmt.Errorf("attach: no such process (pid %d)", pid)
	}
	if freq <= 0 {
		freq = 99
	}
	if callgraph != CallgraphFP {
		callgraph = CallgraphDwarf
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("attach: mkdir %s: %w", workDir, err)
	}
	dataPath := filepath.Join(workDir, "perf.data")

	cmd := exec.Command("perf", recordArgs(pid, freq, callgraph, dataPath)...)
	rec := &Recording{cmd: cmd, dataPath: dataPath, waited: make(chan error, 1)}
	cmd.Stderr = &rec.stderr
	// Detach perf from our process group so a terminal Ctrl-C aimed at
	// the profiled app does not kill the recorder mid-write; Stop
	// signals it explicitly.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("attach: start perf: %w", err)
	}
	go func() { rec.waited <- cmd.Wait() }()
	// Fail fast if perf exits immediately (bad pid, no permission).
	select {
	case err := <-rec.waited:
		return nil, fmt.Errorf("attach: perf exited immediately: %v%s", err, withTail(rec.stderr.String()))
	case <-time.After(200 * time.Millisecond):
	}
	return rec, nil
}

// Stop asks perf to finalize (SIGINT) and waits up to 10s for
// perf.data. Falls back to SIGKILL on timeout.
func (r *Recording) Stop() error {
	select {
	case <-r.waited: // already exited (e.g. immediate failure missed above)
		return r.checkData()
	default:
	}
	_ = r.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-r.waited:
	case <-time.After(10 * time.Second):
		_ = r.cmd.Process.Kill()
		<-r.waited
	}
	return r.checkData()
}

// Kill aborts the recording immediately (no perf.data guarantee).
func (r *Recording) Kill() {
	select {
	case <-r.waited:
	default:
		_ = r.cmd.Process.Kill()
		<-r.waited
	}
}

// DataPath is the destination perf.data file.
func (r *Recording) DataPath() string { return r.dataPath }

func (r *Recording) checkData() error {
	if fi, err := os.Stat(r.dataPath); err != nil || fi.Size() == 0 {
		return fmt.Errorf("attach: perf produced no data (target may have exited)%s",
			withTail(r.stderr.String()))
	}
	return nil
}

func recordArgs(pid, freq int, callgraph, dataPath string) []string {
	return []string{
		"record", "-F", fmt.Sprint(freq),
		"-p", fmt.Sprint(pid),
		"-g", "--call-graph", callgraph,
		"-o", dataPath,
	}
}

func withTail(s string) string {
	if t := perfTail(s); t != "" {
		return "\nperf output: " + t
	}
	return ""
}

func perfTail(s string) string {
	t := strings.TrimSpace(s)
	if len(t) > 800 {
		t = t[len(t)-800:]
	}
	// Reuse the permission hint for the common failure mode.
	if strings.Contains(t, "Permission") || strings.Contains(t, "permission") ||
		strings.Contains(t, "paranoid") || strings.Contains(t, "Access") {
		t += " (hint: run as the same user as the target or root; check /proc/sys/kernel/perf_event_paranoid, values 2 or lower allow user-space sampling)"
	}
	return t
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
