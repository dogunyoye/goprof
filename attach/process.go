// Package attach profiles a running process without prior
// instrumentation (no code changes, no pprof endpoint needed).
//
// On Linux it samples CPU with perf events (folded stacks rendered
// through the flamegraph package) and tracks resident memory by
// polling /proc. Targeting a PID requires the same UID or root and
// a kernel that permits perf (see README).
package attach

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"debug/buildinfo"
)

// ProcInfo describes a candidate target process.
type ProcInfo struct {
	PID       int
	Name      string // /proc/pid/comm
	Exe       string // /proc/pid/exe target ("" if unreadable)
	Cmdline   string // space-joined, truncated
	IsGo      bool   // exe carries Go build info
	GoVersion string // e.g. "go1.23.0" (when IsGo)
	Module    string // main module path (when IsGo)
}

// FindByPID returns info for a live PID or an error if it is gone.
func FindByPID(pid int) (ProcInfo, error) {
	if pid <= 0 {
		return ProcInfo{}, fmt.Errorf("attach: invalid pid %d", pid)
	}
	// Existence check first for a clear error.
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
		return ProcInfo{}, fmt.Errorf("attach: no such process (pid %d): %w", pid, err)
	}
	return inspect(pid), nil
}

// FindByName returns the single process whose comm, exe, or cmdline
// contains pattern (case-insensitive). The caller's own PID is always
// excluded. Errors on zero or ambiguous matches.
func FindByName(pattern string) (ProcInfo, error) {
	procs, err := ListProcesses(false)
	if err != nil {
		return ProcInfo{}, err
	}
	return MatchOne(procs, pattern, os.Getpid())
}

// MatchOne selects exactly one process from a list. Pure function,
// split out for testability.
func MatchOne(procs []ProcInfo, pattern string, selfPID int) (ProcInfo, error) {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return ProcInfo{}, fmt.Errorf("attach: empty name pattern")
	}
	var hits []ProcInfo
	for _, p := range procs {
		if p.PID == selfPID {
			continue
		}
		hay := strings.ToLower(p.Name + "\x00" + p.Exe + "\x00" + p.Cmdline)
		if strings.Contains(hay, pattern) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return ProcInfo{}, fmt.Errorf("attach: no process matches %q", pattern)
	case 1:
		return hits[0], nil
	default:
		var sb strings.Builder
		for _, h := range hits {
			fmt.Fprintf(&sb, "\n  pid %d: %s", h.PID, h.Display())
		}
		return ProcInfo{}, fmt.Errorf("attach: %q matches %d processes, use --pid:%s", pattern, len(hits), sb.String())
	}
}

// ListProcesses scans /proc best-effort. Unreadable entries are
// skipped; with goOnly, non-Go processes are filtered out.
func ListProcesses(goOnly bool) ([]ProcInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("attach: read /proc: %w", err)
	}
	var out []ProcInfo
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		info := inspect(pid)
		if info.PID == 0 {
			continue // vanished mid-scan
		}
		if goOnly && !info.IsGo {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// Display is a one-line human summary for ps output.
func (p ProcInfo) Display() string {
	goTag := ""
	if p.IsGo {
		goTag = " [go " + p.GoVersion + "]"
	}
	cmd := p.Cmdline
	if cmd == "" {
		cmd = "(" + p.Name + ")"
	}
	if len(cmd) > 120 {
		cmd = cmd[:117] + "..."
	}
	return fmt.Sprintf("%s%s", cmd, goTag)
}

func inspect(pid int) ProcInfo {
	base := fmt.Sprintf("/proc/%d", pid)
	info := ProcInfo{PID: pid}

	if b, err := os.ReadFile(base + "/comm"); err == nil {
		info.Name = strings.TrimSpace(string(b))
	} else {
		return ProcInfo{} // process gone
	}
	if exe, err := os.Readlink(base + "/exe"); err == nil {
		info.Exe = strings.TrimSuffix(exe, " (deleted)")
	}
	if b, err := os.ReadFile(base + "/cmdline"); err == nil {
		parts := strings.Split(string(b), "\x00")
		var keep []string
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				keep = append(keep, p)
			}
		}
		info.Cmdline = strings.Join(keep, " ")
	}

	if info.Exe != "" {
		if bi, err := buildinfo.ReadFile(info.Exe); err == nil && bi != nil {
			info.IsGo = true
			info.GoVersion = bi.GoVersion
			info.Module = bi.Path
		}
	}
	return info
}

// ExeBase returns the executable file name for prefix/default naming.
func (p ProcInfo) ExeBase() string {
	if p.Exe == "" {
		if p.Name != "" {
			return p.Name
		}
		return fmt.Sprintf("pid%d", p.PID)
	}
	return filepath.Base(p.Exe)
}
