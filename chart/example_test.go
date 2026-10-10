package chart_test

import (
	"fmt"
	"math"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/chart"
)

// show prints what commands do: each delta's target, key and value.
func show(cmds []string) {
	var d hotty.Decoder
	for _, cmd := range cmds {
		m, _ := d.Feed(cmd)
		fmt.Printf("%s %s=%s\n", hotty.Get(m.Control, "t"), hotty.Get(m.Control, "k"), m.Payload)
	}
}

// A chart that streams: the document goes once, with the chart in it, and
// every tick after that sends a delta of a few dozen bytes that moves the
// line and the area.
func Example_liveChart() {
	// The box is 12×3 cells; at 9×18 CSS pixels a cell
	// (hotty.Caps.CellCSS), that is 108×54.
	c := chart.Line{ID: "rps", W: 108, H: 54, Stroke: 2, Fill: 0.2, Lo: 0, Hi: 100, Color: "#7fd4a0"}
	values := []float64{20, 35, 30, 60}

	doc := "<style>" + chart.CSS + "</style><div style=\"height:54px\">" + c.SVG(values) + "</div>"
	_ = hotty.Doc("dash", doc, hotty.Detached()) // and a placement, once

	// The next tick: a value in, the oldest out.
	values = append(values[1:], 80)
	show(c.DeltaShapes("dash", values))
	// Output:
	// rpsL d=M0,35.1L36,37.8L72,21.6L108,10.8
	// rpsA d=M0,54L0,35.1L36,37.8L72,21.6L108,10.8L108,54Z
}

// When the box changes size (the terminal was resized), or the colour says
// something new, Delta sends the size and the colour too.
func ExampleLine_Delta() {
	c := chart.Line{ID: "p99", W: 90, H: 36, Lo: 0, Hi: 200, Color: "#e8c872"}
	latency := []float64{120, 180, 240}
	if latency[len(latency)-1] > c.Hi {
		c.Color = "#f07a7a" // over budget
	}
	show(c.Delta("svc", latency))
	// Output:
	// p99V viewBox=0 0 90 36
	// p99L d=M0,14.4L45,3.6L90,0
	// p99L stroke=#f07a7a
}

// A sample that is missing (NaN) leaves a gap: the line breaks there
// rather than dropping to zero.
func ExampleLine_Path_gaps() {
	c := chart.Line{W: 60, H: 10, Lo: 0, Hi: 10}
	fmt.Println(c.Path([]float64{2, 4, math.NaN(), 6, 8}))
	// Output:
	// M0,8L15,6M45,4L60,2
}

// Samples placed in time (Xs): the last minute in a box, one sample every
// 15 seconds, and the newest at the right edge. The line slides as time
// passes, and what was drawn keeps its shape.
func ExampleLine_Path_timeAxis() {
	c := chart.Line{W: 120, H: 10, Lo: 0, Hi: 100, Xs: []float64{0.25, 0.5, 0.75, 1}}
	fmt.Println(c.Path([]float64{10, 40, 30, 90}))
	// Output:
	// M30,9L60,6L90,7L120,1
}

// Several series share one box, as a plot of several columns does: each
// draws its Shapes, and Box holds them.
func ExampleBox() {
	in := chart.Line{ID: "in", W: 40, H: 10, Hi: 10, Color: "#5b8def"}
	out := chart.Line{ID: "out", W: 40, H: 10, Hi: 10, Color: "#e8c872"}
	fmt.Println(chart.Box("net", 40, 10,
		in.Shapes([]float64{1, 3, 2})+
			out.Shapes([]float64{5, 4, 6})))
	// Output:
	// <div class="chart-box"><svg id="net" viewBox="0 0 40 10" preserveAspectRatio="none"><path id="inL" d="M0,9L20,7L40,8" fill="none" stroke="#5b8def" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/><path id="outL" d="M0,5L20,6L40,4" fill="none" stroke="#e8c872" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/></svg></div>
}

// In a terminal that is not a HOTTY host the same series is a row of
// cells, scaled by Bounds with some headroom.
func ExampleSpark() {
	load := []float64{0.4, 0.9, 1.6, 2.8, math.NaN(), 3.1, 2.2, 1.0}
	_, hi := chart.Bounds(load)
	fmt.Printf("load %s %.1f\n", chart.Spark(load, 0, hi*1.1, 20), load[len(load)-1])
	// Output:
	// load ▂▃▄▇ ▇▆▃ 1.0
}

// Bounds fit a scale to the values, leaving out what is not a number.
func ExampleBounds() {
	lo, hi := chart.Bounds([]float64{12, math.NaN(), 7, 30})
	fmt.Println(lo, hi)
	// Output:
	// 7 30
}
