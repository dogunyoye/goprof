package attach

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// MemSample is one RSS reading.
type MemSample struct {
	Elapsed time.Duration
	RSSKB   int64
}

// MemStats summarizes a memory sampling run.
type MemStats struct {
	Samples     int
	MinKB       int64
	MaxKB       int64
	AvgKB       int64
	GrowthKB    int64 // last - first
	Completed   bool  // false if the process vanished mid-run
	CSV         string
	ElapsedSecs float64
}

// ReadRSSKB returns the resident set size of pid in KiB via /proc.
func ReadRSSKB(pid int) (int64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("attach: read stat for pid %d: %w", pid, err)
	}
	// comm may contain spaces/parens: fields start after the last ')'.
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return 0, fmt.Errorf("attach: malformed stat for pid %d", pid)
	}
	fields := strings.Fields(string(b)[i+1:])
	if len(fields) < 22 { // rss is the 24th field overall, 22nd after comm
		return 0, fmt.Errorf("attach: short stat for pid %d", pid)
	}
	pages, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("attach: parse rss for pid %d: %w", pid, err)
	}
	return pages * int64(os.Getpagesize()) / 1024, nil
}

// SampleMemory polls RSS every interval for duration, writing a CSV
// with header `elapsed_ms,rss_kb` to csvPath. It stops early (returning
// partial stats with Completed=false) if the process exits.
func SampleMemory(pid int, interval, duration time.Duration, csvPath string) (MemStats, error) {
	var st MemStats
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	if duration <= 0 {
		return st, fmt.Errorf("attach: mem duration must be > 0")
	}
	if _, err := ReadRSSKB(pid); err != nil {
		return st, err
	}

	start := time.Now()
	deadline := start.Add(duration)
	var sum int64
	var series []MemSample
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		rss, err := ReadRSSKB(pid)
		if err != nil {
			break // process gone; keep partial series
		}
		now := time.Since(start)
		series = append(series, MemSample{Elapsed: now, RSSKB: rss})
		sum += rss
		if st.Samples == 0 {
			st.MinKB, st.MaxKB = rss, rss
		} else {
			if rss < st.MinKB {
				st.MinKB = rss
			}
			if rss > st.MaxKB {
				st.MaxKB = rss
			}
		}
		st.Samples++
		if now >= duration {
			st.Completed = true
			break
		}
		select {
		case <-tick.C:
		case <-time.After(time.Until(deadline)):
		}
	}
	if st.Samples == 0 {
		return st, fmt.Errorf("attach: no memory samples (process exited?)")
	}
	st.AvgKB = sum / int64(st.Samples)
	st.GrowthKB = series[len(series)-1].RSSKB - series[0].RSSKB
	st.ElapsedSecs = series[len(series)-1].Elapsed.Seconds()

	var sb strings.Builder
	sb.WriteString("elapsed_ms,rss_kb\n")
	for _, s := range series {
		fmt.Fprintf(&sb, "%d,%d\n", s.Elapsed.Milliseconds(), s.RSSKB)
	}
	if err := os.WriteFile(csvPath, []byte(sb.String()), 0o644); err != nil {
		return st, fmt.Errorf("attach: write %s: %w", csvPath, err)
	}
	st.CSV = csvPath
	return st, nil
}
