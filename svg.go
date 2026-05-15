package main

import (
	"fmt"
	"hash/fnv"
	"io"
	"strings"
)

// renderSVG writes an interactive flamegraph as an SVG document. The
// layout follows the conventional flame-graph shape: the root sits at
// the top, each node's width is proportional to its sampled value, and
// each level grows downward by one row.
//
// The output isn't trying to match Brendan Gregg's flamegraph.pl
// pixel-for-pixel. The goal is readable, scannable, single-file SVG
// that opens in any browser with no JS bundling.
type svgOpts struct {
	title    string
	subtitle string
	unit     string
	width    int
	rowH     int
	minPx    float64 // minimum pixels wide; below this we skip the node
}

func defaultOpts(title, unit string) svgOpts {
	return svgOpts{
		title: title,
		unit:  unit,
		width: 1400,
		rowH:  18,
		minPx: 1.5,
	}
}

func renderSVG(w io.Writer, root *node, opts svgOpts) error {
	if root.value == 0 {
		return fmt.Errorf("flamectl: profile has zero samples")
	}
	depth := treeDepth(root)
	height := (depth + 2) * opts.rowH + 60 // header + footer space

	fmt.Fprintf(w, `<?xml version="1.0" standalone="no"?>
<svg xmlns="http://www.w3.org/2000/svg" version="1.1" width="%d" height="%d" viewBox="0 0 %d %d">`,
		opts.width, height, opts.width, height)

	fmt.Fprint(w, `
<style>
  text { font-family: -apple-system, "SF Mono", Menlo, monospace; font-size: 11px; fill: #0f0e0d; pointer-events: none; }
  rect { stroke: #1a1917; stroke-width: 0.5; cursor: pointer; }
  rect:hover { stroke: #f0ede8; stroke-width: 1.5; }
  .title { font-size: 16px; font-weight: 700; fill: #d4a844; font-family: -apple-system, "SF Mono", Menlo, monospace; }
  .subtitle { font-size: 11px; fill: #a09890; }
  .total { font-size: 11px; fill: #6a6460; }
  .label { fill: #1a1917; }
  .label-dark { fill: #f0ede8; }
  #info { font-size: 12px; fill: #f0ede8; font-family: -apple-system, "SF Mono", Menlo, monospace; }
</style>
<rect x="0" y="0" width="100%" height="100%" fill="#0f0e0d"/>
`)

	fmt.Fprintf(w, `<text class="title" x="14" y="22">%s</text>`, escapeXML(opts.title))
	if opts.subtitle != "" {
		fmt.Fprintf(w, `<text class="subtitle" x="14" y="40">%s</text>`, escapeXML(opts.subtitle))
	}
	fmt.Fprintf(w, `<text class="total" x="%d" y="22" text-anchor="end">total: %s</text>`,
		opts.width-14, formatValue(root.value, opts.unit))

	fmt.Fprintf(w, `<text id="info" x="14" y="%d">hover a frame for details</text>`, height-12)

	// Recursively lay out the tree. Each node gets a rectangle whose width
	// is proportional to its share of its parent's value. Children stack
	// horizontally inside that span; subtrees grow downward.
	yTop := 52
	layoutAndRender(w, root, 14.0, float64(opts.width-28), yTop, opts, root.value)

	fmt.Fprint(w, `
<script><![CDATA[
  document.querySelectorAll('g.frame').forEach(function(g){
    g.addEventListener('mousemove', function(){
      var info = document.getElementById('info');
      info.textContent = g.getAttribute('data-label');
    });
  });
]]></script>
</svg>
`)
	return nil
}

func layoutAndRender(w io.Writer, n *node, x, width float64, y int, opts svgOpts, total int64) {
	if n.value == 0 || width < opts.minPx {
		return
	}
	if n.parent != nil { // skip the synthetic root rectangle
		drawFrame(w, n, x, width, y, opts, total)
	}
	childY := y + opts.rowH
	if n.parent == nil {
		childY = y
	}
	cx := x
	for _, c := range n.sortedChildren() {
		cw := float64(c.value) / float64(n.value) * width
		layoutAndRender(w, c, cx, cw, childY, opts, total)
		cx += cw
	}
}

func drawFrame(w io.Writer, n *node, x, width float64, y int, opts svgOpts, total int64) {
	r, g, b := colorFor(n.name)
	dark := isDarkBg(r, g, b)
	labelClass := "label"
	if dark {
		labelClass = "label label-dark"
	}
	pct := float64(n.value) / float64(total) * 100
	pretty := fmt.Sprintf("%s    %s (%.1f%%)", n.name, formatValue(n.value, opts.unit), pct)

	fmt.Fprintf(w, `<g class="frame" data-label="%s">`, escapeXML(pretty))
	fmt.Fprintf(w,
		`<rect x="%.2f" y="%d" width="%.2f" height="%d" fill="rgb(%d,%d,%d)"/>`,
		x, y, width, opts.rowH-1, r, g, b,
	)
	// Truncate the label to roughly fit the box. ~7px per character for the chosen font.
	maxChars := int(width / 6.5)
	if maxChars > 3 {
		label := n.name
		if len(label) > maxChars {
			label = label[:maxChars-1] + "…"
		}
		fmt.Fprintf(w,
			`<text class="%s" x="%.2f" y="%d">%s</text>`,
			labelClass, x+3, y+opts.rowH-5, escapeXML(label),
		)
	}
	fmt.Fprint(w, `</g>`)
}

func treeDepth(n *node) int {
	max := 0
	for _, c := range n.children {
		d := treeDepth(c)
		if d > max {
			max = d
		}
	}
	return max + 1
}

// colorFor picks a deterministic warm color for a function name. Real
// flamegraph tooling traditionally uses red-orange-yellow for CPU. We
// stick with the same family so output looks familiar but with a more
// muted palette so labels stay readable.
func colorFor(name string) (int, int, int) {
	h := fnv.New32a()
	h.Write([]byte(name))
	v := h.Sum32()
	// Hue: 0-50 (red through orange to yellow). Vary saturation/lightness slightly.
	hue := float64(v%50) / 50.0
	sat := 0.55 + float64((v>>8)%30)/100.0
	light := 0.55 + float64((v>>16)%15)/100.0
	r, g, b := hslToRGB(hue*0.14, sat, light)
	return r, g, b
}

func hslToRGB(h, s, l float64) (int, int, int) {
	if s == 0 {
		c := int(l * 255)
		return c, c, c
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	r := hueToRGB(p, q, h+1.0/3.0)
	g := hueToRGB(p, q, h)
	b := hueToRGB(p, q, h-1.0/3.0)
	return int(r * 255), int(g * 255), int(b * 255)
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	switch {
	case t < 1.0/6.0:
		return p + (q-p)*6*t
	case t < 1.0/2.0:
		return q
	case t < 2.0/3.0:
		return p + (q-p)*(2.0/3.0-t)*6
	default:
		return p
	}
}

func isDarkBg(r, g, b int) bool {
	// luminance approx
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return lum < 110
}

func escapeXML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

func formatValue(v int64, unit string) string {
	switch unit {
	case "nanoseconds":
		return formatDuration(v)
	case "bytes":
		return formatBytes(v)
	case "count", "samples", "":
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%d %s", v, unit)
	}
}

func formatDuration(ns int64) string {
	switch {
	case ns >= int64(1e9):
		return fmt.Sprintf("%.2fs", float64(ns)/1e9)
	case ns >= int64(1e6):
		return fmt.Sprintf("%.1fms", float64(ns)/1e6)
	case ns >= int64(1e3):
		return fmt.Sprintf("%.1fµs", float64(ns)/1e3)
	default:
		return fmt.Sprintf("%dns", ns)
	}
}

func formatBytes(b int64) string {
	const k = 1024
	switch {
	case b >= k*k*k:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(k*k*k))
	case b >= k*k:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(k*k))
	case b >= k:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(k))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
