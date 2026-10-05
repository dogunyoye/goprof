package flamegraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
)

func testProfile() *profile.Profile {
	fnMain := &profile.Function{ID: 1, Name: "main.main", Filename: "main.go"}
	fnWork := &profile.Function{ID: 2, Name: "main.work", Filename: "main.go"}
	fnBurn := &profile.Function{ID: 3, Name: "main.burnCPU", Filename: "main.go"}
	locMain := &profile.Location{ID: 1, Address: 0x1000, Line: []profile.Line{{Function: fnMain, Line: 10}}}
	locWork := &profile.Location{ID: 2, Address: 0x2000, Line: []profile.Line{{Function: fnWork, Line: 20}}}
	locBurn := &profile.Location{ID: 3, Address: 0x3000, Line: []profile.Line{{Function: fnBurn, Line: 30}}}
	return &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{Location: []*profile.Location{locBurn, locWork, locMain}, Value: []int64{10, 100}},
			{Location: []*profile.Location{locWork, locMain}, Value: []int64{5, 50}},
			{Location: []*profile.Location{locBurn, locWork, locMain}, Value: []int64{2, 20}}, // same stack as first
		},
		Function: []*profile.Function{fnMain, fnWork, fnBurn},
		Location: []*profile.Location{locMain, locWork, locBurn},
	}
}

func writeTestProfile(t *testing.T, p *profile.Profile) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.pb.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write(f); err != nil {
		f.Close()
		t.Fatalf("profile.Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseStacksAggregates(t *testing.T) {
	path := writeTestProfile(t, testProfile())
	data, err := ParseStacks(path, -1) // auto → cpu index 1
	if err != nil {
		t.Fatalf("ParseStacks: %v", err)
	}
	if data.SampleIndex != 1 {
		t.Fatalf("expected auto sample index 1, got %d", data.SampleIndex)
	}
	if len(data.Stacks) != 2 {
		t.Fatalf("expected 2 aggregated stacks, got %d", len(data.Stacks))
	}
	if data.Total != 170 { // 100+50+20
		t.Fatalf("expected total 170, got %d", data.Total)
	}
	// Stacks sort lexicographically, so locate the deep one by depth.
	var deep Stack
	found := false
	for _, s := range data.Stacks {
		if len(s.Funcs) == 3 {
			deep, found = s, true
		}
	}
	if !found {
		t.Fatalf("expected a 3-frame stack, got %v", data.Stacks)
	}
	if deep.Funcs[0] != "main.main" || deep.Funcs[2] != "main.burnCPU" {
		t.Fatalf("unexpected root-first order: %v", deep.Funcs)
	}
	if deep.Value != 120 {
		t.Fatalf("expected aggregated value 120, got %d", deep.Value)
	}
}

func TestGenerateEndToEnd(t *testing.T) {
	dir := t.TempDir()
	in := writeTestProfile(t, testProfile())
	svg := filepath.Join(dir, "flame.svg")
	folded := filepath.Join(dir, "flame.folded")

	opts := DefaultOptions()
	opts.FoldedOutput = folded
	s, err := Generate(in, svg, opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if s.Stacks != 2 || s.Frames == 0 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	svgBytes, err := os.ReadFile(svg)
	if err != nil || len(svgBytes) == 0 {
		t.Fatalf("expected non-empty svg: %v", err)
	}
	svgStr := string(svgBytes)
	for _, want := range []string{"<svg", "main.burnCPU", "gprofSearch", "gprofZoom"} {
		if !strings.Contains(svgStr, want) {
			t.Fatalf("svg missing %q", want)
		}
	}
	foldedBytes, err := os.ReadFile(folded)
	if err != nil {
		t.Fatalf("read folded: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(foldedBytes)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 folded lines, got %d: %q", len(lines), string(foldedBytes))
	}
	if !strings.Contains(string(foldedBytes), "main.main;main.work;main.burnCPU 120") {
		t.Fatalf("unexpected folded content:\n%s", foldedBytes)
	}
}

func TestDefaultSampleIndex(t *testing.T) {
	cpu := &profile.Profile{SampleType: []*profile.ValueType{
		{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"},
	}}
	if got := DefaultSampleIndex(cpu); got != 1 {
		t.Fatalf("cpu: got %d", got)
	}
	heap := &profile.Profile{SampleType: []*profile.ValueType{
		{Type: "alloc_objects", Unit: "count"}, {Type: "alloc_space", Unit: "bytes"},
		{Type: "inuse_objects", Unit: "count"}, {Type: "inuse_space", Unit: "bytes"},
	}}
	if got := DefaultSampleIndex(heap); got != 1 {
		t.Fatalf("heap: got %d", got)
	}
	single := &profile.Profile{SampleType: []*profile.ValueType{{Type: "goroutine", Unit: "count"}}}
	if got := DefaultSampleIndex(single); got != 0 {
		t.Fatalf("single: got %d", got)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := ParseStacks(filepath.Join(t.TempDir(), "missing.pb.gz"), -1); err == nil {
		t.Fatal("expected error for missing file")
	}
	empty := &profile.Profile{}
	if _, err := ParseStacks(writeTestProfile(t, empty), -1); err == nil {
		t.Fatal("expected error for empty profile")
	}
	// Garbage file.
	bad := filepath.Join(t.TempDir(), "bad.pb.gz")
	if err := os.WriteFile(bad, []byte("not a profile"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseStacks(bad, -1); err == nil {
		t.Fatal("expected error for garbage file")
	}
	if _, err := Generate("", filepath.Join(t.TempDir(), "x.svg"), DefaultOptions()); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestRenderSVGPrunesNarrowFrames(t *testing.T) {
	data := &ProfileData{
		Stacks: []Stack{
			{Funcs: []string{"a", "big"}, Value: 1000000},
			{Funcs: []string{"a", "tiny"}, Value: 1},
		},
		Total: 1000001,
		Unit:  "count",
	}
	var b strings.Builder
	frames, err := RenderSVG(data, &b, SVGOptions{Width: 200, RowHeight: 22, MinWidth: 100})
	if err != nil {
		t.Fatalf("RenderSVG: %v", err)
	}
	if !strings.Contains(b.String(), "big") {
		t.Fatal("expected wide frame to render")
	}
	if strings.Contains(b.String(), "tiny") {
		t.Fatal("expected narrow frame to be pruned")
	}
	if frames == 0 {
		t.Fatal("expected some frames")
	}
}

func TestParseFolded(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/in.folded"
	content := "# comment\n\nmain;work;burn 10\nmain;work 5\nmain;work;burn 3\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := ParseFolded(path)
	if err != nil {
		t.Fatalf("ParseFolded: %v", err)
	}
	if len(data.Stacks) != 2 || data.Total != 18 {
		t.Fatalf("got %d stacks total %d: %+v", len(data.Stacks), data.Total, data.Stacks)
	}
	if data.Unit != "samples" || data.SampleType != "collapsed" {
		t.Fatalf("unexpected meta: %+v", data)
	}
	// Duplicates aggregated.
	for _, s := range data.Stacks {
		if len(s.Funcs) == 3 && s.Value != 13 {
			t.Fatalf("expected aggregated 13, got %+v", s)
		}
	}
}

func TestParseFoldedErrors(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"empty.folded":    "",
		"comments.folded": "# only comments\n\n",
		"bad.folded":      "main;work\n",
		"novalue.folded":  "main;work abc\n",
		"zero.folded":     "main;work 0\n",
	} {
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseFolded(p); err == nil {
			t.Fatalf("expected error for %s", name)
		}
	}
	if _, err := ParseFolded(dir + "/missing.folded"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestGenerateFromFolded(t *testing.T) {
	dir := t.TempDir()
	in := dir + "/in.folded"
	if err := os.WriteFile(in, []byte("a;b;c 7\na;b 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := GenerateFromFolded(in, dir+"/out.svg", DefaultOptions())
	if err != nil {
		t.Fatalf("GenerateFromFolded: %v", err)
	}
	if s.Stacks != 2 || s.Frames == 0 || s.Total != 10 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	b, err := os.ReadFile(dir + "/out.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "<svg") {
		t.Fatal("expected svg output")
	}
}
