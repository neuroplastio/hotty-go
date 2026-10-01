// Package chart draws line charts for HOTTY surfaces, as inline SVG that a
// program patches as values arrive, and in cells for a terminal that is not
// a host (Spark).
//
// A Line is one series: its box, its scale and its colour. SVG is the chart
// in a box of its own. For a box several series share (Box), each draws its
// Shapes, its line and area, and one of them its Rules, the grid. Patch and
// PatchShapes are the commands that bring a chart already sent to new
// values: a few hundred bytes a tick, where a new document would be
// thousands (SPEC §6).
//
// The SVG keeps to rules that make it draw the same in every host:
//
//   - Presentation attributes only (fill, stroke, stroke-width), never CSS.
//     Some hosts hand inline SVG to an SVG library: hotty-blitz gives it to
//     usvg, which the document's stylesheet never reaches.
//   - The viewBox is the box's size in CSS pixels (W, H), so the drawing is
//     stretched to its box by little, if at all. A stroke keeps its width at
//     any angle, and its joins stay round. usvg has no
//     vector-effect: non-scaling-stroke, and a viewBox far from the box's
//     shape draws steep segments thin. Take W and H from the box's cells
//     (hotty.Caps.CellCSS), and send new ones with Patch when it changes.
//   - The <svg> is absolutely positioned in its .chart-box (CSS), which has
//     the size: as a grid or flex item, an <svg> is sized by its aspect
//     ratio in some engines (Blitz) instead of stretched.
//
// Values that change over time go best in buckets fixed in time (Xs), so
// that what was drawn stays put and a tick's patch moves the line along
// rather than reshaping it.
package chart

import (
	"html"
	"math"
	"strconv"
	"strings"

	"github.com/neuroplastio/hotty-go"
)

// Defaults for the fields of a Line left zero.
const (
	// DefaultColor is the line's and the area's colour: a mid blue that
	// reads on dark and light backgrounds.
	DefaultColor = "#5b8def"
	// DefaultGridColor is the grid's colour, drawn at GridOpacity: a grey
	// that is faint on dark and light backgrounds alike.
	DefaultGridColor = "#808080"
	// GridOpacity is the default grid's opacity.
	GridOpacity = 0.3
	// DefaultStroke is the line's width in CSS pixels.
	DefaultStroke = 1.5
)

// CSS lays a chart's box out: the box takes the size its container gives
// it, and the <svg> fills the box, absolutely. Add it to the document's
// stylesheet.
const CSS = `
.chart-box { position: relative; min-width: 0; min-height: 0; height: 100%; }
.chart-box svg { position: absolute; top: 0; left: 0; width: 100%; height: 100%; display: block; }
`

// Line is a line chart of one series, with an optional area under it.
//
// Its elements have ids made from ID, for patches: the box's <svg>
// (BoxID), the line (LineID), the area (AreaID) and the grid (GridID). A
// chart with no ID has no ids, and cannot be patched.
type Line struct {
	ID string
	// W and H are the box's size in CSS pixels, and the viewBox's. Less
	// than 1 is 1.
	W, H float64
	// Stroke is the line's width in CSS pixels; 0 is DefaultStroke.
	Stroke float64
	// Fill is the area's opacity, from 0 to 1; 0 draws no area.
	Fill float64
	// Lo and Hi are the values at the bottom and the top of the box. A
	// value outside them is drawn at the edge. When Hi is not above Lo,
	// every value is drawn halfway up.
	Lo, Hi float64
	// Color is the line's and the area's colour, as CSS writes it; ""
	// is DefaultColor.
	Color string
	// Grid are horizontal rules, as fractions of the height from the
	// bottom: 0.5 is a rule halfway up.
	Grid []float64
	// GridColor is the rules' colour; "" is DefaultGridColor at
	// GridOpacity.
	GridColor string
	// Xs, if set, place the values along the width, as fractions of it: a
	// time axis. Values past the end of Xs are not drawn. Without Xs the
	// values are spread evenly, the first on the left edge and the last
	// on the right.
	Xs []float64
}

// BoxID is the id of the chart's <svg>, which SVG draws: its viewBox is
// the chart's size.
func (c Line) BoxID() string { return c.id("V") }

// LineID is the id of the line, a <path>.
func (c Line) LineID() string { return c.id("L") }

// AreaID is the id of the area under the line, a <path>, when Fill is set.
func (c Line) AreaID() string { return c.id("A") }

// GridID is the id of the grid's rules, one <path>, when Grid is set.
func (c Line) GridID() string { return c.id("G") }

func (c Line) id(suffix string) string {
	if c.ID == "" {
		return ""
	}
	return c.ID + suffix
}

func idAttr(id string) string {
	if id == "" {
		return ""
	}
	return ` id="` + html.EscapeString(id) + `"`
}

func (c Line) size() (w, h float64) { return math.Max(1, c.W), math.Max(1, c.H) }

func (c Line) color() string {
	if c.Color == "" {
		return DefaultColor
	}
	return c.Color
}

func (c Line) stroke() float64 {
	if c.Stroke <= 0 {
		return DefaultStroke
	}
	return c.Stroke
}

// SVG is the chart in a box of its own (Box), its grid below its area and
// line.
func (c Line) SVG(values []float64) string {
	w, h := c.size()
	return Box(c.BoxID(), w, h, c.Rules()+c.Shapes(values))
}

// Rules is the grid, one <path> of thin rectangles (a rectangle fills its
// pixel row where a 1px stroke would straddle two), for a box several
// series share: draw it first, under their Shapes. "" without Grid. When
// the grid changes, set its path data (GridID, "d") to GridPath.
func (c Line) Rules() string {
	if len(c.Grid) == 0 {
		return ""
	}
	fill, opacity := c.GridColor, ""
	if fill == "" {
		fill, opacity = DefaultGridColor, ` fill-opacity="`+num(GridOpacity)+`"`
	}
	return `<path` + idAttr(c.GridID()) + ` d="` + c.GridPath() + `" fill="` + html.EscapeString(fill) + `"` + opacity + `/>`
}

// GridPath is the grid's rules as path data: a rectangle 1 pixel tall and
// the box's width for each.
func (c Line) GridPath() string {
	w, h := c.size()
	var b strings.Builder
	for _, f := range c.Grid {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		y := math.Round(h - math.Max(0, math.Min(1, f))*h)
		y = math.Min(y, h-1)
		b.WriteString("M0," + num(y) + "h" + num(w) + "v1h-" + num(w) + "Z")
	}
	return b.String()
}

// Shapes are the chart's area, if it has one, and its line, for a box that
// several charts share (Box).
func (c Line) Shapes(values []float64) string {
	var b strings.Builder
	col := html.EscapeString(c.color())
	if c.Fill > 0 {
		b.WriteString(`<path` + idAttr(c.AreaID()) + ` d="` + c.AreaPath(values) + `" fill="` + col +
			`" fill-opacity="` + strconv.FormatFloat(math.Min(1, c.Fill), 'f', 2, 64) + `"/>`)
	}
	b.WriteString(`<path` + idAttr(c.LineID()) + ` d="` + c.Path(values) + `" fill="none" stroke="` + col +
		`" stroke-width="` + strconv.FormatFloat(c.stroke(), 'f', 1, 64) + `" stroke-linejoin="round" stroke-linecap="round"/>`)
	return b.String()
}

// Box is an SVG box w×h in its units, stretched over its .chart-box (CSS):
// shapes drawn in a viewBox of the box's size in CSS pixels. id, if not
// "", lets a patch change its viewBox when the box changes size.
func Box(id string, w, h float64, shapes string) string {
	return `<div class="chart-box"><svg` + idAttr(id) + ` viewBox="0 0 ` + num(w) + ` ` + num(h) +
		`" preserveAspectRatio="none">` + shapes + `</svg></div>`
}

// Patch is the commands that bring a chart drawn by SVG to values, and to
// its size, colour and grid now: for a chart whose box changed, or whose
// colour says something (a state). Send them as they are (hotty.Sync for
// several charts at once).
func (c Line) Patch(surface string, values []float64) []string {
	w, h := c.size()
	col := c.color()
	out := []string{hotty.SetAttr(surface, c.BoxID(), "viewBox", "0 0 "+num(w)+" "+num(h))}
	if len(c.Grid) > 0 {
		out = append(out, hotty.SetAttr(surface, c.GridID(), "d", c.GridPath()))
	}
	out = append(out,
		hotty.SetAttr(surface, c.LineID(), "d", c.Path(values)),
		hotty.SetAttr(surface, c.LineID(), "stroke", col))
	if c.Fill > 0 {
		out = append(out,
			hotty.SetAttr(surface, c.AreaID(), "d", c.AreaPath(values)),
			hotty.SetAttr(surface, c.AreaID(), "fill", col))
	}
	return out
}

// PatchShapes is the commands that move the line, and the area if there is
// one, to values, and nothing else: the cheap patch of every tick, for a
// chart whose box and colour stay as they were.
func (c Line) PatchShapes(surface string, values []float64) []string {
	out := []string{hotty.SetAttr(surface, c.LineID(), "d", c.Path(values))}
	if c.Fill > 0 {
		out = append(out, hotty.SetAttr(surface, c.AreaID(), "d", c.AreaPath(values)))
	}
	return out
}

// point is a value placed in the box.
type point struct{ x, y float64 }

// runs places the values in the box, as runs of points between gaps: a
// NaN value, or an x that is not a number, ends a run, so the line breaks
// there rather than dropping to the bottom.
func (c Line) runs(values []float64) [][]point {
	w, h := c.size()
	n := len(values)
	if c.Xs != nil {
		n = min(n, len(c.Xs))
	}
	var runs [][]point
	var cur []point
	for i := range n {
		v := values[i]
		var x float64
		switch {
		case c.Xs != nil:
			x = c.Xs[i] * w
		case n == 1:
			x = 0
		default:
			x = float64(i) * w / float64(n-1)
		}
		if math.IsNaN(v) || math.IsNaN(x) || math.IsInf(x, 0) {
			if len(cur) > 0 {
				runs = append(runs, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, point{x, c.y(v, h)})
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	// One value spread evenly is the whole width at that value.
	if c.Xs == nil && n == 1 && len(runs) == 1 {
		runs[0] = append(runs[0], point{w, runs[0][0].y})
	}
	return runs
}

// y is a value's height in the box, from the top, clamped to it.
func (c Line) y(v, h float64) float64 {
	if !(c.Hi > c.Lo) {
		return h / 2
	}
	y := h - (v-c.Lo)/(c.Hi-c.Lo)*h
	if math.IsNaN(y) { // an infinite scale
		return h / 2
	}
	return math.Max(0, math.Min(h, y))
}

// Path is the line through values, as path data: "" with no values to
// draw, and a separate stroke for each run of values between NaNs. A run
// of one value is a dot, drawn by the round cap.
func (c Line) Path(values []float64) string {
	var b strings.Builder
	for _, run := range c.runs(values) {
		for i, p := range run {
			if i == 0 {
				b.WriteString("M")
			} else {
				b.WriteString("L")
			}
			b.WriteString(num(p.x) + "," + num(p.y))
		}
		if len(run) == 1 {
			b.WriteString("L" + num(run[0].x) + "," + num(run[0].y))
		}
	}
	return b.String()
}

// AreaPath is the area under values, as path data: each run of values
// between NaNs closed along the bottom edge.
func (c Line) AreaPath(values []float64) string {
	_, h := c.size()
	var b strings.Builder
	for _, run := range c.runs(values) {
		if len(run) < 2 {
			continue
		}
		b.WriteString("M" + num(run[0].x) + "," + num(h))
		for _, p := range run {
			b.WriteString("L" + num(p.x) + "," + num(p.y))
		}
		b.WriteString("L" + num(run[len(run)-1].x) + "," + num(h) + "Z")
	}
	return b.String()
}

// num is a coordinate to a tenth of a pixel, in its shortest form: 12,
// 12.5, never -0.
func num(v float64) string {
	v = math.Round(v*10) / 10
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Bounds are the smallest and the largest of values, NaNs and infinities
// left out: a scale that fits them exactly (give it headroom yourself, as
// in hi*1.1). With no values it is 0 and 1; with one value v, or all
// equal, v and v+1.
func Bounds(values []float64) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if math.IsInf(lo, 1) {
		return 0, 1
	}
	if hi == lo {
		hi = lo + 1
	}
	return lo, hi
}

var bars = []rune("▁▂▃▄▅▆▇█")

// Spark is a chart in one row of cells, for a terminal that is not a host:
// the last w values at most, a block element a value, from ▁ at lo to █ at
// hi. A value outside them is drawn at the edge, and a NaN is a space, a
// gap. When hi is not above lo every value is ▁. It is as many cells wide
// as values, at most w.
func Spark(values []float64, lo, hi float64, w int) string {
	if w <= 0 {
		return ""
	}
	if len(values) > w {
		values = values[len(values)-w:]
	}
	var b strings.Builder
	for _, v := range values {
		if math.IsNaN(v) {
			b.WriteByte(' ')
			continue
		}
		i := 0
		if hi > lo {
			f := math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
			i = int(math.Round(f * float64(len(bars)-1)))
		}
		b.WriteRune(bars[i])
	}
	return b.String()
}
