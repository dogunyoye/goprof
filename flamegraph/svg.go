package flamegraph

import (
	"fmt"
	"hash/fnv"
	"io"
	"strings"
)

// SVGOptions tunes the flamegraph SVG renderer.
type SVGOptions struct {
	Title     string
	Width     int
	RowHeight int
	// MinWidth hides frames narrower than this (CSS px). Their width
	// still counts toward offsets, so the layout stays proportional.
	MinWidth float64
	// MaxDepth truncates stacks deeper than this (0 = unlimited).
	MaxDepth int
	// EmbedControls adds the in-SVG search box and "reset zoom" label.
	// Standalone .svg files want this (default behavior of Generate);
	// the HTML viewer (RenderHTML) sets it false because its sticky
	// toolbar provides those controls instead.
	EmbedControls bool
}

// DefaultSVGOptions returns v1 renderer defaults.
func DefaultSVGOptions() SVGOptions {
	return SVGOptions{
		Width:     1200,
		RowHeight: 22,
		MinWidth:  0.5,
		MaxDepth:  0,
	}
}

type fnode struct {
	name     string
	value    int64
	children map[string]*fnode
	sorted   []*fnode
}

func buildTree(stacks []Stack, maxDepth int) (*fnode, int) {
	root := &fnode{children: map[string]*fnode{}}
	maxD := 0
	for _, s := range stacks {
		funcs := s.Funcs
		if maxDepth > 0 && len(funcs) > maxDepth {
			funcs = funcs[:maxDepth]
		}
		if len(funcs) > maxD {
			maxD = len(funcs)
		}
		n := root
		n.value += s.Value
		for _, f := range funcs {
			c, ok := n.children[f]
			if !ok {
				c = &fnode{name: f, children: map[string]*fnode{}}
				n.children[f] = c
			}
			c.value += s.Value
			n = c
		}
	}
	sortTree(root)
	return root, maxD
}

func sortTree(n *fnode) {
	n.sorted = n.sorted[:0]
	for _, c := range n.children {
		n.sorted = append(n.sorted, c)
	}
	// Widest first for a stable, left-heavy layout; name breaks ties.
	for i := 0; i < len(n.sorted); i++ {
		for j := i + 1; j < len(n.sorted); j++ {
			a, b := n.sorted[i], n.sorted[j]
			if b.value > a.value || (b.value == a.value && b.name < a.name) {
				n.sorted[i], n.sorted[j] = n.sorted[j], n.sorted[i]
			}
		}
	}
	for _, c := range n.sorted {
		sortTree(c)
	}
}

// RenderSVG writes a self-contained flamegraph SVG: native <title>
// tooltips, a search box that dims non-matches, and click-to-zoom
// (double-click background or "reset" to zoom out). Returns the number
// of frames rendered.
func RenderSVG(data *ProfileData, w io.Writer, opts SVGOptions) (frames int, err error) {
	if data == nil || len(data.Stacks) == 0 || data.Total <= 0 {
		return 0, fmt.Errorf("flamegraph: nothing to render")
	}
	if opts.Width <= 0 {
		opts.Width = 1200
	}
	if opts.RowHeight <= 0 {
		opts.RowHeight = 22
	}
	if opts.MinWidth < 0 {
		opts.MinWidth = 0
	}

	root, maxDepth := buildTree(data.Stacks, opts.MaxDepth)
	if maxDepth == 0 {
		return 0, fmt.Errorf("flamegraph: nothing to render")
	}

	const padL, padR = 10.0, 10.0
	const topH = 76
	const footerH = 26
	plotW := float64(opts.Width) - padL - padR
	rowH := opts.RowHeight
	height := topH + maxDepth*rowH + footerH

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" id="gprof-svg" data-w="%d" data-h="%d" width="%d" height="%d" viewBox="0 0 %d %d" font-family="monospace" role="img">`+"\n",
		opts.Width, height, opts.Width, height, opts.Width, height)
	b.WriteString(`<rect x="0" y="0" width="100%" height="100%" fill="#ffffff" ondblclick="gprofReset()"/>` + "\n")

	title := opts.Title
	if title == "" {
		title = "Flamegraph"
	}
	if data.SampleType != "" {
		title += " — " + data.SampleType
	}
	fmt.Fprintf(&b, `<text x="%d" y="24" font-size="16" font-weight="bold" fill="#222">%s</text>`+"\n",
		int(padL), esc(title))
	fmt.Fprintf(&b, `<text x="%d" y="42" font-size="12" fill="#555">%s · %d stacks · %d frames max depth</text>`+"\n",
		int(padL), esc(fmt.Sprintf("total %s %s", formatValue(data.Total, data.Unit), data.Unit)), len(data.Stacks), maxDepth)
	if opts.EmbedControls {
		fmt.Fprintf(&b, `<text x="%d" y="42" text-anchor="end" font-size="12" fill="#06c" style="cursor:pointer" onclick="gprofReset()">reset zoom</text>`+"\n",
			opts.Width-int(padR))
		fmt.Fprintf(&b, `<foreignObject x="%d" y="50" width="%d" height="22"><input xmlns="http://www.w3.org/1999/xhtml" placeholder="search function…" oninput="gprofSearch(this.value)" style="width:100%%;font:12px monospace;border:1px solid #ccc;border-radius:4px;padding:1px 6px;"/></foreignObject>`+"\n",
			int(padL), opts.Width-int(padL)-int(padR))
	}

	// y for depth d (0 = root frame, at the bottom).
	yOf := func(d int) int { return topH + (maxDepth-1-d)*rowH }

	var render func(n *fnode, x float64, depth int)
	render = func(n *fnode, x float64, depth int) {
		for _, c := range n.sorted {
			wpx := float64(c.value) / float64(data.Total) * plotW
			x0 := x
			x += wpx
			if wpx < opts.MinWidth {
				continue // keep offset, prune subtree
			}
			frames++
			y := yOf(depth)
			pct := 100 * float64(c.value) / float64(data.Total)
			tip := fmt.Sprintf("%s\n%s %s (%.2f%%)", c.name, formatValue(c.value, data.Unit), data.Unit, pct)
			fill := flameColor(c.name)
			fmt.Fprintf(&b, `<g class="f" data-f="%s" opacity="1" style="cursor:pointer" onclick="gprofZoom(%s,%s)">`+"\n",
				escAttr(c.name), f2s(padL+x0), f2s(wpx))
			fmt.Fprintf(&b, `<rect x="%s" y="%d" width="%s" height="%d" rx="2" fill="%s" stroke="#fff"/>`+"\n",
				f2s(padL+x0), y, f2s(wpx-1), rowH-2, fill)
			fmt.Fprintf(&b, `<title>%s</title>`+"\n", esc(tip))
			// Label only when the frame fits readable text.
			if wpx > 30 {
				maxChars := int((wpx - 8) / 7)
				label := c.name
				if len(label) > maxChars && maxChars > 4 {
					label = label[:maxChars-1] + "…"
				} else if len(label) > maxChars {
					label = ""
				}
				if label != "" {
					fmt.Fprintf(&b, `<text x="%s" y="%d" font-size="12" fill="#1a1a1a" pointer-events="none">%s</text>`+"\n",
						f2s(padL+x0+4), y+rowH-8, esc(label))
				}
			}
			b.WriteString("</g>\n")
			render(c, x0, depth+1)
		}
	}
	render(root, 0, 0)

	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="#888">%s</text>`+"\n",
		int(padL), height-8, esc("goprof flamegraph · click a frame to zoom, double-click background to reset"))
	b.WriteString(`<script><![CDATA[
function gprofZoom(x,w){var s=document.getElementById('gprof-svg');s.setAttribute('viewBox',x+' 0 '+w+' '+s.getAttribute('data-h'));}
function gprofReset(){var s=document.getElementById('gprof-svg');s.setAttribute('viewBox','0 0 '+s.getAttribute('data-w')+' '+s.getAttribute('data-h'));}
function gprofSearch(q){q=(q||'').toLowerCase();var els=document.getElementsByClassName('f');for(var i=0;i<els.length;i++){var t=els[i].getAttribute('data-f').toLowerCase();els[i].setAttribute('opacity',(!q||t.indexOf(q)>=0)?'1':'0.12');}}
]]></script>` + "\n")
	b.WriteString("</svg>\n")

	if _, err := io.WriteString(w, b.String()); err != nil {
		return 0, fmt.Errorf("flamegraph: write svg: %w", err)
	}
	return frames, nil
}

// flameColor returns a deterministic warm color per function name.
func flameColor(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	v := h.Sum32()
	hue := 5 + int(v%50)         // 5..54 red → orange → yellow
	light := 52 + int((v>>7)%14) // 52..65
	sat := 88
	r, g, b := hslToRGB(float64(hue)/360, float64(sat)/100, float64(light)/100)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func hslToRGB(h, s, l float64) (r, g, b int) {
	var f func(p, q, t float64) float64
	f = func(p, q, t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	var rr, gg, bb float64
	if s == 0 {
		rr, gg, bb = l, l, l
	} else {
		var q float64
		if l < 0.5 {
			q = l * (1 + s)
		} else {
			q = l + s - l*s
		}
		p := 2*l - q
		rr, gg, bb = f(p, q, h+1.0/3), f(p, q, h), f(p, q, h-1.0/3)
	}
	return int(rr*255 + 0.5), int(gg*255 + 0.5), int(bb*255 + 0.5)
}

func f2s(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;")
	return r.Replace(s)
}

// formatValue renders large counts/bytes compactly for labels.
func formatValue(v int64, unit string) string {
	u := strings.ToLower(unit)
	if strings.Contains(u, "byte") {
		switch {
		case v >= 1<<30:
			return fmt.Sprintf("%.2fG (%d)", float64(v)/(1<<30), v)
		case v >= 1<<20:
			return fmt.Sprintf("%.2fM (%d)", float64(v)/(1<<20), v)
		case v >= 1<<10:
			return fmt.Sprintf("%.1fK (%d)", float64(v)/(1<<10), v)
		}
	}
	if strings.Contains(u, "nano") && v >= 1e9 {
		return fmt.Sprintf("%.2fs (%d)", float64(v)/1e9, v)
	}
	if strings.Contains(u, "nano") && v >= 1e6 {
		return fmt.Sprintf("%.1fms (%d)", float64(v)/1e6, v)
	}
	return fmt.Sprintf("%d", v)
}
