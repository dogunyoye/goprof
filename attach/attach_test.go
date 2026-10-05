package attach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindSelf(t *testing.T) {
	me := os.Getpid()
	info, err := FindByPID(me)
	if err != nil {
		t.Fatalf("FindByPID(self): %v", err)
	}
	if info.PID != me || info.Name == "" {
		t.Fatalf("unexpected self info: %+v", info)
	}
	if info.Cmdline == "" {
		t.Fatal("expected non-empty cmdline for test binary")
	}
}

func TestFindByPIDMissing(t *testing.T) {
	if _, err := FindByPID(1 << 30); err == nil {
		t.Fatal("expected error for bogus pid")
	}
	if _, err := FindByPID(0); err == nil {
		t.Fatal("expected error for pid 0")
	}
}

func TestSelfIsGoBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	info := inspect(os.Getpid())
	if info.Exe == "" {
		t.Skip("cannot read /proc/self/exe")
	}
	if !info.IsGo {
		t.Fatalf("test binary %s should detect as Go", exe)
	}
	if !strings.HasPrefix(info.GoVersion, "go") {
		t.Fatalf("unexpected GoVersion %q", info.GoVersion)
	}
}

func TestMatchOne(t *testing.T) {
	list := []ProcInfo{
		{PID: 100, Name: "myservice", Exe: "/opt/myservice", Cmdline: "/opt/myservice --port 80"},
		{PID: 200, Name: "other", Exe: "/usr/bin/other", Cmdline: "other worker"},
		{PID: 300, Name: "myservice", Exe: "/opt/myservice", Cmdline: "/opt/myservice --port 81"},
	}
	// Unique match on exe substring.
	got, err := MatchOne(list, "usr/bin/other", 999)
	if err != nil || got.PID != 200 {
		t.Fatalf("unique match: %+v %v", got, err)
	}
	// Self is always excluded: with self=100, only pid 300 matches.
	got, err = MatchOne(list, "myservice", 100)
	if err != nil || got.PID != 300 {
		t.Fatalf("self-exclusion: %+v %v", got, err)
	}
	// No match at all.
	if _, err := MatchOne(list, "nosuchproc", 999); err == nil {
		t.Fatal("expected no-match error")
	}
	if _, err := MatchOne(list, "  ", 999); err == nil {
		t.Fatal("expected empty-pattern error")
	}
}

func TestMatchOneAmbiguous(t *testing.T) {
	list := []ProcInfo{
		{PID: 100, Name: "svc", Cmdline: "svc a"},
		{PID: 200, Name: "svc", Cmdline: "svc b"},
	}
	if _, err := MatchOne(list, "svc", 999); err == nil {
		t.Fatal("expected ambiguity error")
	} else if !strings.Contains(err.Error(), "2 processes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMatchOneCaseInsensitive(t *testing.T) {
	list := []ProcInfo{{PID: 7, Name: "MyService", Cmdline: ""}}
	got, err := MatchOne(list, "myservice", 999)
	if err != nil || got.PID != 7 {
		t.Fatalf("case-insensitive match: %+v %v", got, err)
	}
}

func TestReadRSSSelf(t *testing.T) {
	rss, err := ReadRSSKB(os.Getpid())
	if err != nil {
		t.Fatalf("ReadRSSKB(self): %v", err)
	}
	if rss <= 0 {
		t.Fatalf("expected positive RSS, got %d", rss)
	}
}

func TestSampleMemoryShort(t *testing.T) {
	csv := filepath.Join(t.TempDir(), "rss.csv")
	st, err := SampleMemory(os.Getpid(), 20*time.Millisecond, 120*time.Millisecond, csv)
	if err != nil {
		t.Fatalf("SampleMemory: %v", err)
	}
	if st.Samples < 2 {
		t.Fatalf("expected >=2 samples, got %d", st.Samples)
	}
	if st.MinKB <= 0 || st.MaxKB < st.MinKB || st.AvgKB <= 0 {
		t.Fatalf("bad stats: %+v", st)
	}
	b, err := os.ReadFile(csv)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if lines[0] != "elapsed_ms,rss_kb" {
		t.Fatalf("bad CSV header: %q", lines[0])
	}
	if len(lines) != st.Samples+1 {
		t.Fatalf("CSV rows %d != samples %d + header", len(lines), st.Samples)
	}
}

func TestAttachMemOnlySelf(t *testing.T) {
	opts := DefaultOptions()
	opts.PID = os.Getpid()
	opts.Seconds = 1
	opts.CPU = false
	opts.Mem = true
	opts.MemInterval = 100 * time.Millisecond
	opts.OutputDir = t.TempDir()
	opts.Prefix = "memonly"
	s, err := Attach(opts)
	if err != nil {
		t.Fatalf("Attach mem-only: %v", err)
	}
	if !s.HaveMem || s.Mem.Samples < 2 {
		t.Fatalf("expected memory stats, got %+v", s.Mem)
	}
	if s.SVG != "" {
		t.Fatal("expected no SVG in mem-only mode")
	}
}

func TestAttachBadPID(t *testing.T) {
	opts := DefaultOptions()
	opts.PID = 1 << 30
	opts.CPU = false
	opts.Seconds = 1
	if _, err := Attach(opts); err == nil {
		t.Fatal("expected error for bogus pid")
	}
}

func TestAttachNeedsTarget(t *testing.T) {
	if _, err := Attach(DefaultOptions()); err == nil {
		t.Fatal("expected error with no target")
	}
}

// TestParsePerfScript exercises the `perf script` parser directly
// (no perf binary needed) with pid filtering and kernel handling.
func TestParsePerfScript(t *testing.T) {
	script := `myapp 1234 [001] 100.000000: 10000 cycles:u:
    7f0001 main.burnCPU+0x2f (/tmp/myapp)
    7f0002 main.work+0x5a (/tmp/myapp)
    7f0003 main.main+0x1c (/tmp/myapp)

myapp 1234 [001] 100.010000: 10000 cycles:u:
    ffffffff810001 native_safe_halt ([kernel.kallsyms])
    7f0002 main.work+0x5a (/tmp/myapp)

other 9999 [000] 100.030000: 10000 cycles:u:
    7f0009 evil (/bin/other)
`
	agg := map[string]int{}
	funcsOf := map[string][]string{}
	samples, err := parsePerfScript(strings.NewReader(script), 1234, false, agg, funcsOf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if samples != 2 {
		t.Fatalf("expected 2 kept samples (pid filter), got %d", samples)
	}
	got := map[string]int{}
	for k, v := range agg {
		got[strings.ReplaceAll(k, "\x00", ";")] = v
	}
	if got["main.main;main.work;main.burnCPU"] != 1 {
		t.Fatalf("missing deep stack: %v", got)
	}
	if got["main.work"] != 1 {
		t.Fatalf("expected kernel frame dropped, got: %v", got)
	}

	// With kernel frames kept, the halt frame is marked _[k].
	agg2 := map[string]int{}
	if _, err := parsePerfScript(strings.NewReader(script), 1234, true, agg2, map[string][]string{}); err != nil {
		t.Fatal(err)
	}
	found := false
	for k := range agg2 {
		if strings.ReplaceAll(k, "\x00", ";") == "main.work;native_safe_halt_[k]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected _[k] kernel frame, got %v", agg2)
	}

	// No filter keeps everything, including other pids.
	agg3 := map[string]int{}
	if n, err := parsePerfScript(strings.NewReader(script), 0, true, agg3, map[string][]string{}); err != nil || n != 3 {
		t.Fatalf("unfiltered: n=%d err=%v", n, err)
	}
}

func TestCleanSymbol(t *testing.T) {
	cases := map[string]string{
		"main.burnCPU+0x2f":   "main.burnCPU",
		"main.work+0x1a":      "main.work",
		"runtime.mallocgc":    "runtime.mallocgc",
		"[unknown]":           "[unknown]",
		"native_safe_halt":    "native_safe_halt",
		"foo(int, char**)":    "foo",
		"  padded+0x10  ":     "padded",
		"std::_Rb_tree+0x5f0": "std::_Rb_tree",
	}
	for in, want := range cases {
		if got := cleanSymbol(in); got != want {
			t.Fatalf("cleanSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}
