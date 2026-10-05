package flamegraph

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	frameGeomRe = regexp.MustCompile(`<g class="f"[^>]*data-x="([\d.]+)"[^>]*data-w="([\d.]+)"[^>]*onclick="gprofZoom\(this\)"[^>]*>\s*<rect x="([\d.]+)"[^>]*width="([\d.]+)"`)
	viewBoxRe   = regexp.MustCompile(`<svg[^>]*id="gprof-svg"[^>]*data-w="(\d+)"[^>]*data-h="(\d+)"[^>]*width="(\d+)"[^>]*height="(\d+)"[^>]*viewBox="0 0 (\d+) (\d+)"`)
	calledFnRe  = regexp.MustCompile(`\son(?:click|dblclick|input)="([A-Za-z_][A-Za-z0-9_]*)\(`)
	definedFnRe = regexp.MustCompile(`function ([A-Za-z_][A-Za-z0-9_]*)\(`)
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

// TestZoomFramesCarryGeometry ensures every frame carries the original
// coordinates the transform zoom needs (data-x/data-w), consistent with
// its rect: rect.x == pad+data-x and rect.width == max(data-w-1, 0.5px
// gutter floor). Zoom math replays from these, so they must agree.
func TestZoomFramesCarryGeometry(t *testing.T) {
	svg := renderTestSVG(t)
	matches := frameGeomRe.FindAllStringSubmatch(svg, -1)
	if len(matches) == 0 {
		t.Fatal("no zoomable frames found")
	}
	const pad, plot = 10.0, 1180.0
	for _, m := range matches {
		ox, ow := mustFloat(t, m[1]), mustFloat(t, m[2])
		rx, rw := mustFloat(t, m[3]), mustFloat(t, m[4])
		if rx != pad+ox {
			t.Errorf("rect x %v != pad+data-x %v", rx, pad+ox)
		}
		wantW := ow - 1
		if wantW < 0.5 {
			wantW = 0.5
		}
		if rw != wantW {
			t.Errorf("rect width %v != data-w %v - gutter", rw, ow)
		}
		if ox < 0 || ox+ow > plot {
			t.Errorf("frame region out of plot: x=%v w=%v", ox, ow)
		}
		if ow <= 0 {
			t.Errorf("non-positive frame width %v", ow)
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
