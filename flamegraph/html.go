package flamegraph

import (
	"fmt"
	"io"
	"strings"
)

// RenderHTML writes a standalone HTML viewer page: a sticky toolbar
// (title, stats, search box, reset button) that stays visible while
// scrolling and zooming, wrapping the same inline SVG flamegraph.
// Frame clicks zoom, background double-click resets, exactly as in the
// bare SVG — but the controls never scroll out of view. Returns the
// number of frames rendered.
func RenderHTML(data *ProfileData, w io.Writer, opts SVGOptions) (frames int, err error) {
	if data == nil || len(data.Stacks) == 0 || data.Total <= 0 {
		return 0, fmt.Errorf("flamegraph: nothing to render")
	}

	var svg strings.Builder
	opts.EmbedControls = false // toolbar owns search + reset
	frames, err = RenderSVG(data, &svg, opts)
	if err != nil {
		return 0, err
	}

	title := opts.Title
	if title == "" {
		title = "Flamegraph"
	}
	if data.SampleType != "" {
		title += " — " + data.SampleType
	}
	sub := fmt.Sprintf("total %s %s · %d stacks · %d frames",
		formatValue(data.Total, data.Unit), data.Unit, len(data.Stacks), frames)

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + esc(title) + ` — goprof flamegraph</title>
<style>
body{margin:0;font-family:monospace;background:#fff;color:#222}
#gprof-bar{position:sticky;top:0;z-index:10;background:#fff;border-bottom:1px solid #ddd;padding:8px 12px;display:flex;gap:10px;align-items:center;flex-wrap:wrap}
#gprof-bar .t{font-weight:bold;font-size:14px}
#gprof-bar .s{font-size:12px;color:#555}
#gprof-q{font:12px monospace;border:1px solid #ccc;border-radius:4px;padding:4px 8px;min-width:240px}
#gprof-reset{font:12px monospace;border:1px solid #ccc;border-radius:4px;background:#f5f5f5;padding:4px 10px;cursor:pointer}
#gprof-reset:hover{background:#e8e8e8}
#gprof-wrap{padding:4px 0 16px}
#gprof-wrap svg{max-width:100%;height:auto;display:block}
.hint{font-size:11px;color:#888;padding:0 12px}
</style>
</head>
<body>
<div id="gprof-bar" role="toolbar" aria-label="flamegraph controls">
<span class="t">` + esc(title) + `</span>
<span class="s">` + esc(sub) + `</span>
<input id="gprof-q" type="search" placeholder="search function…" autocomplete="off" aria-label="search functions">
<button id="gprof-reset" type="button">reset zoom</button>
</div>
<div id="gprof-wrap">
`)
	b.WriteString(svg.String())
	b.WriteString(`</div>
<p class="hint">click a frame to zoom · double-click background to reset · search dims non-matches</p>
<script>
(function(){
var q=document.getElementById('gprof-q'),r=document.getElementById('gprof-reset');
q.addEventListener('input',function(e){gprofSearch(e.target.value);});
r.addEventListener('click',function(){q.value='';gprofSearch('');gprofReset();});
})();
</script>
</body>
</html>
`)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return 0, fmt.Errorf("flamegraph: write html: %w", err)
	}
	return frames, nil
}
