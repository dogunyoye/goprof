package flamegraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderTestHTML(t *testing.T) string {
	t.Helper()
	data := &ProfileData{
		Stacks: []Stack{
			{Funcs: []string{"root", "left", "leaf"}, Value: 60},
			{Funcs: []string{"root", "right"}, Value: 40},
		},
		Total:      100,
		Unit:       "count",
		SampleType: "cpu",
	}
	var b strings.Builder
	if _, err := RenderHTML(data, &b, DefaultSVGOptions()); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestRenderHTMLToolbar checks the viewer shell: sticky toolbar with
// search + reset, inline SVG present, and no duplicated in-SVG controls.
func TestRenderHTMLToolbar(t *testing.T) {
	page := renderTestHTML(t)
	for _, want := range []string{
		`id="gprof-bar"`,
		`position:sticky`,
		`id="gprof-q"`,
		`id="gprof-reset"`,
		`<svg xmlns="http://www.w3.org/2000/svg" id="gprof-svg"`,
		`onclick="gprofZoom(`,
		`ondblclick="gprofReset()"`,
		`addEventListener('input'`,
		`addEventListener('click'`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("html page missing %q", want)
		}
	}
	if strings.Contains(page, "<foreignObject") {
		t.Error("inline SVG must not duplicate the toolbar search box")
	}
	// DOCTYPE first, single html root.
	if !strings.HasPrefix(strings.TrimSpace(page), "<!DOCTYPE html>") {
		t.Error("page must start with DOCTYPE")
	}
	if n := strings.Count(page, "<html"); n != 1 {
		t.Errorf("expected 1 <html, got %d", n)
	}
}

// TestRenderHTMLErrors checks empty input handling.
func TestRenderHTMLErrors(t *testing.T) {
	var b strings.Builder
	if _, err := RenderHTML(nil, &b, DefaultSVGOptions()); err == nil {
		t.Fatal("expected error for nil data")
	}
	if _, err := RenderHTML(&ProfileData{}, &b, DefaultSVGOptions()); err == nil {
		t.Fatal("expected error for empty data")
	}
}

// TestGenerateHTMLSidecar checks Generate writes SVG + HTML together
// from one parse, with the summary pointing at both.
func TestGenerateHTMLSidecar(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.folded")
	if err := os.WriteFile(in, []byte("a;b;c 7\na;b 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.FoldedOutput = filepath.Join(dir, "out.folded")
	opts.HTMLOutput = filepath.Join(dir, "out.html")
	s, err := GenerateFromFolded(in, filepath.Join(dir, "out.svg"), opts)
	if err != nil {
		t.Fatalf("GenerateFromFolded: %v", err)
	}
	if s.HTML != opts.HTMLOutput || s.SVG == "" || s.Folded == "" {
		t.Fatalf("unexpected summary: %+v", s)
	}
	html, err := os.ReadFile(s.HTML)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="gprof-bar"`, `<svg`, "a"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("html missing %q", want)
		}
	}
}
