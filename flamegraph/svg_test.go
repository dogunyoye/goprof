package flamegraph

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	zoomHandlerRe = regexp.MustCompile(`<g class="f"[^>]*onclick="gprofZoom\(([\d.]+),([\d.]+)\)"[^>]*>\s*<rect x="([\d.]+)"[^>]*width="([\d.]+)"`)
	viewBoxRe     = regexp.MustCompile(`<svg[^>]*id="gprof-svg"[^>]*data-w="(\d+)"[^>]*data-h="(\d+)"[^>]*width="(\d+)"[^>]*height="(\d+)"[^>]*viewBox="0 0 (\d+) (\d+)"`)
	calledFnRe    = regexp.MustCompile(`\son(?:click|dblclick|input)="([A-Za-z_][A-Za-z0-9_]*)\(`)
	definedFnRe   = regexp.MustCompile(`function ([A-Za-z_][A-Za-z0-9_]*)\(`)
)

func renderTestSVG(t *testing.T) string {
	t.Helper()
	data := &ProfileData{
		Stacks: []Stack{
			{Funcs: []string{"root", "left", "leaf"}, Value: 60},
			{Funcs: []string{"root", "left"}, Value: 20},
			{Funcs: []string{"root", "right"}, Value: 20},
		},
		Total:      100,
		Unit:       "count",
		SampleType: "cpu",
	}
	var b strings.Builder
	if _, err := RenderSVG(data, &b, SVGOptions{Width: 1200, RowHeight: 22, MinWidth: 0.5, EmbedControls: true}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestZoomHandlersMatchFrames ensures every click-to-zoom handler points
// at exactly the frame it is attached to: same x, and width equal to the
// rect width plus the 1px gutter subtracted at render time.
func TestZoomHandlersMatchFrames(t *testing.T) {
	svg := renderTestSVG(t)
	matches := zoomHandlerRe.FindAllStringSubmatch(svg, -1)
	if len(matches) == 0 {
		t.Fatal("no zoom handlers found")
	}
	for _, m := range matches {
		hx, hw := mustFloat(t, m[1]), mustFloat(t, m[2])
		rx, rw := mustFloat(t, m[3]), mustFloat(t, m[4])
		if hx != rx {
			t.Errorf("handler x %v != rect x %v", hx, rx)
		}
		if hw-rw != 1 {
			t.Errorf("handler w %v != rect w %v + 1px gutter", hw, rw)
		}
		if hx < 0 || hx+hw > 1200 {
			t.Errorf("zoom region out of canvas: x=%v w=%v", hx, hw)
		}
		if hw <= 0 {
			t.Errorf("non-positive zoom width %v", hw)
		}
	}
}

// TestViewBoxConsistent checks the canvas geometry the zoom math relies
// on: viewBox, width/height and data-w/data-h must agree.
func TestViewBoxConsistent(t *testing.T) {
	svg := renderTestSVG(t)
	m := viewBoxRe.FindStringSubmatch(svg)
	if m == nil {
		t.Fatal("svg root geometry not found")
	}
	if m[1] != m[3] || m[1] != m[5] {
		t.Fatalf("width disagreement: data-w=%s width=%s viewBox-w=%s", m[1], m[3], m[5])
	}
	if m[2] != m[4] || m[2] != m[6] {
		t.Fatalf("height disagreement: data-h=%s height=%s viewBox-h=%s", m[2], m[4], m[6])
	}
}

// TestHandlersResolve ensures every JS function referenced from an event
// attribute is actually defined in the embedded script.
func TestHandlersResolve(t *testing.T) {
	svg := renderTestSVG(t)
	defined := map[string]bool{}
	for _, m := range definedFnRe.FindAllStringSubmatch(svg, -1) {
		defined[m[1]] = true
	}
	seen := map[string]bool{}
	for _, m := range calledFnRe.FindAllStringSubmatch(svg, -1) {
		seen[m[1]] = true
		if !defined[m[1]] {
			t.Errorf("handler calls undefined function %q", m[1])
		}
	}
	for _, want := range []string{"gprofZoom", "gprofReset", "gprofSearch"} {
		if !seen[want] {
			t.Errorf("expected %s to be wired to an event", want)
		}
	}
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
