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

// PerfAvailable reports whether a working perf binary can be
// resolved (see perfBinary). A nil error does not guarantee recording
// permission (that depends on kernel.perf_event_paranoid and UID);
// record failures surface from RecordCPU with the kernel's message.
func PerfAvailable() error {
	_, err := perfBinary()
	return err
}

// RecordCPU samples a live PID with `perf record` for the given
// duration. freq is samples/sec (99 is a good default), callgraph is
// "dwarf" (reliable for Go, default) or "fp", and event selects the
// sampling event ("" = perf default, usually cycles; "cpu-clock" on
// VMs/WSL without PMU passthrough). Returns the perf.data path.
// Requires Linux, perf, and permission to trace the target
// (same UID or root; kernel.perf_event_paranoid <= 2 for user space).
func RecordCPU(pid, seconds, freq int, callgraph, workDir, event string) (string, error) {
	if seconds <= 0 {
		return "", fmt.Errorf("attach: seconds must be > 0")
	}
	rec, err := StartRecording(pid, freq, callgraph, workDir, event)
	if err != nil {
		return "", err
	}
	time.Sleep(time.Duration(seconds) * time.Second)
	if err := rec.Stop(); err != nil {
		rec.Kill()
		return "", err
	}
	return rec.Persist(workDir)
}

// Recording is a running `perf record -p` session. It records until
// Stop (graceful, SIGINT) or Kill, which makes it suitable for
// profiling child processes whose lifetime is unknown upfront.
//
// perf.data is captured in a temp dir first: perf mmap-writes its
// output, which fails on some filesystems (notably 9P Windows mounts
// under WSL). Use Persist to copy the finished file next to the other
// artifacts.
type Recording struct {
	cmd      *exec.Cmd
	dataPath string // temp location; see Persist
	tmpDir   string
	stderr   strings.Builder
	waited   chan error // Wait result; closed exactly once
}

// StartRecording begins `perf record` on pid in the background.
// Call Stop to finish gracefully and finalize perf.data.
func StartRecording(pid, freq int, callgraph, workDir, event string) (*Recording, error) {
	bin, err := perfBinary()
	if err != nil {
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
	tmpDir, err := os.MkdirTemp("", "goprof-perf-*")
	if err != nil {
		return nil, fmt.Errorf("attach: temp dir for perf.data: %w", err)
	}
	dataPath := filepath.Join(tmpDir, "perf.data")

	cmd := exec.Command(bin, recordArgs(pid, freq, callgraph, dataPath, event)...)
	rec := &Recording{cmd: cmd, dataPath: dataPath, tmpDir: tmpDir, waited: make(chan error, 1)}
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
		os.RemoveAll(tmpDir)
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
	if err := r.checkData(); err != nil {
		os.RemoveAll(r.tmpDir)
		return err
	}
	return nil
}

// Kill aborts the recording immediately (no perf.data guarantee)
// and removes the temp dir.
func (r *Recording) Kill() {
	select {
	case <-r.waited:
	default:
		_ = r.cmd.Process.Kill()
		<-r.waited
	}
	os.RemoveAll(r.tmpDir)
}

// Persist copies the finished perf.data into workDir (created as
// needed) and returns the new path, cleaning up the temp dir.
// Call after a successful Stop; DataPath then reports the copy.
func (r *Recording) Persist(workDir string) (string, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("attach: mkdir %s: %w", workDir, err)
	}
	dst := filepath.Join(workDir, "perf.data")
	if err := copyFile(r.dataPath, dst); err != nil {
		return "", fmt.Errorf("attach: persist perf.data to %s: %w", dst, err)
	}
	os.RemoveAll(r.tmpDir)
	r.dataPath = dst
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
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

func recordArgs(pid, freq int, callgraph, dataPath, event string) []string {
	args := []string{"record"}
	if event != "" {
		args = append(args, "-e", event)
	}
	return append(args,
		"-F", fmt.Sprint(freq),
		"-p", fmt.Sprint(pid),
		"-g", "--call-graph", callgraph,
		"-o", dataPath,
	)
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
	if isWrapperMiss(t) {
		t += "\n" + wrapperHint(t)
	} else if isPMUFailure(t) {
		t += " (" + pmuHint() + ")"
	}
	return t
}

var (
	// e.g. "myapp 1234 [003] 12345.678901: 10000 cycles:u:" —
	// the [cpu] field is absent in some perf versions/configs.
	perfEventRe = regexp.MustCompile(`^\S+\s+(\d+)(?:/\d+)?\s+(?:\[\d+\]\s+)?[\d.]+:`)
	// e.g. "    7f9a2b3c main.burnCPU+0x2f (/tmp/myapp)"
	perfFrameRe = regexp.MustCompile(`^\s+[0-9a-fA-F]+\s+(.+?)\s+\((.+)\)\s*$`)
)

// ScriptToFolded runs `perf script` on perf.data and collapses the
// stacks to folded format at foldedPath. wantPID filters samples to
// one process (0 = accept all). Kernel frames are dropped unless
// includeKernel (they are then suffixed `_[k]`). Returns the sample
// count kept.
func ScriptToFolded(perfDataPath, foldedPath string, wantPID int, includeKernel bool) (samples int, err error) {
	bin, err := perfBinary()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(bin, "script", "-i", perfDataPath)
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
	keys := make([]string, 0, len(agg))
	for k, v := range agg {
		stacks = append(stacks, flamegraph.Stack{Funcs: funcsOf[k], Value: int64(v)})
		keys = append(keys, strings.Join(funcsOf[k], ";"))
	}
	order := make([]int, len(stacks))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return keys[order[a]] < keys[order[b]] })
	sorted := make([]flamegraph.Stack, len(stacks))
	for i, oi := range order {
		sorted[i] = stacks[oi]
	}
	copy(stacks, sorted)
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
	sc.Buffer(make([]byte, 64*1024), 10*1024*1024)
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
			if _, err := fmt.Sscan(m[1], &curPID); err != nil {
				inSample, haveEvent = false, false
				curLeaf = nil
				continue
			}
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
		isKernel := strings.HasPrefix(dso, "[kernel") || strings.Contains(dso, "vmlinux")
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
