package attach

import (
	"bufio"
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

// SampleMemory polls RSS every interval for duration, streaming a CSV
// with header `elapsed_ms,rss_kb` to csvPath as it goes (so kills and
// Ctrl-C keep partial data). It stops early (returning partial stats
// with Completed=false) if the process exits. Stop early via stopCh
// (close it); a nil stopCh samples for the full duration.
func SampleMemory(pid int, interval, duration time.Duration, csvPath string) (MemStats, error) {
	return SampleMemoryStop(pid, interval, duration, csvPath, nil)
}

// SampleMemoryStop is SampleMemory with an early-stop channel.
func SampleMemoryStop(pid int, interval, duration time.Duration, csvPath string, stopCh <-chan struct{}) (MemStats, error) {
	var st MemStats
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	if duration <= 0 {
		return st, fmt.Errorf("attach: mem duration must be > 0")
	}
	first, err := ReadRSSKB(pid)
	if err != nil {
		return st, err
	}
	_ = first

	f, err := os.Create(csvPath)
	if err != nil {
		return st, fmt.Errorf("attach: create %s: %w", csvPath, err)
	}
	// Best-effort streaming: rows are flushed per sample; Close at end.
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "elapsed_ms,rss_kb")
	st.CSV = csvPath

	finish := func() {
		w.Flush()
		f.Close()
	}

	start := time.Now()
	deadline := start.Add(duration)
	var sum, firstRSS, lastRSS int64
	var lastElapsed time.Duration
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		rss, err := ReadRSSKB(pid)
		if err != nil {
			break // process gone; keep partial series
		}
		now := time.Since(start)
		fmt.Fprintf(w, "%d,%d\n", now.Milliseconds(), rss)
		w.Flush()
		sum += rss
		if st.Samples == 0 {
			st.MinKB, st.MaxKB, firstRSS = rss, rss, rss
		} else {
			if rss < st.MinKB {
				st.MinKB = rss
			}
			if rss > st.MaxKB {
				st.MaxKB = rss
			}
		}
		lastRSS, lastElapsed = rss, now
		st.Samples++
		if now >= duration {
			st.Completed = true
			break
		}
		select {
		case <-tick.C:
		case <-time.After(time.Until(deadline)):
		case <-stopCh:
			finish()
			goto done
		}
	}
	finish()
done:
	if st.Samples == 0 {
		return st, fmt.Errorf("attach: no memory samples (process exited?)")
	}
	st.AvgKB = sum / int64(st.Samples)
	st.GrowthKB = lastRSS - firstRSS
	st.ElapsedSecs = lastElapsed.Seconds()
	return st, nil
}
