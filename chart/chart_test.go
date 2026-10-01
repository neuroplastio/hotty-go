package chart

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/neuroplastio/hotty-go"
)

var nan = math.NaN()

func TestSVG(t *testing.T) {
	c := Line{ID: "cpu", W: 100, H: 40, Stroke: 2, Fill: 0.2, Lo: 0, Hi: 100, Color: "#7fd4a0", Grid: []float64{0.5}}
	got := c.SVG([]float64{0, 50, 100})
	want := `<div class="chart-box"><svg id="cpuV" viewBox="0 0 100 40" preserveAspectRatio="none">` +
		`<path id="cpuG" d="M0,20h100v1h-100Z" fill="#808080" fill-opacity="0.3"/>` +
		`<path id="cpuA" d="M0,40L0,40L50,20L100,0L100,40Z" fill="#7fd4a0" fill-opacity="0.20"/>` +
		`<path id="cpuL" d="M0,40L50,20L100,0" fill="none" stroke="#7fd4a0" stroke-width="2.0" stroke-linejoin="round" stroke-linecap="round"/>` +
		`</svg></div>`
	if got != want {
		t.Errorf("SVG =\n%s\nwant\n%s", got, want)
	}
}

func TestDefaults(t *testing.T) {
	c := Line{ID: "x", W: 10, H: 10, Hi: 1}
	got := c.Shapes([]float64{0, 1})
	want := `<path id="xL" d="M0,10L10,0" fill="none" stroke="` + DefaultColor + `" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>`
	if got != want {
		t.Errorf("Shapes = %s", got)
	}
	// Less than a pixel is a pixel: a viewBox of 0 draws nothing.
	if got := (Line{}).SVG(nil); !strings.Contains(got, `viewBox="0 0 1 1"`) {
		t.Errorf("an empty chart: %s", got)
	}
	// A grid of the program's own colour, at full opacity.
	c.Grid, c.GridColor = []float64{0, 1, nan, 2}, "#23232e"
	if got := c.SVG(nil); !strings.Contains(got, `<path id="xG" d="M0,9h10v1h-10ZM0,0h10v1h-10ZM0,0h10v1h-10Z" fill="#23232e"/>`) {
		t.Errorf("grid: %s", got)
	}
}

// A chart with no ID has no ids: it is drawn once and never patched.
func TestNoID(t *testing.T) {
	c := Line{W: 10, H: 10, Hi: 1, Fill: 0.5, Grid: []float64{0.5}}
	if got := c.SVG([]float64{0, 1}); strings.Contains(got, "id=") {
		t.Errorf("ids in %s", got)
	}
	if c.BoxID() != "" || c.LineID() != "" || c.AreaID() != "" || c.GridID() != "" {
		t.Error("ids without an ID")
	}
	c.ID = "k"
	if c.BoxID() != "kV" || c.LineID() != "kL" || c.AreaID() != "kA" || c.GridID() != "kG" {
		t.Errorf("ids: %s %s %s %s", c.BoxID(), c.LineID(), c.AreaID(), c.GridID())
	}
}

func TestEscaping(t *testing.T) {
	c := Line{ID: `a"b`, W: 10, H: 10, Hi: 1, Color: `red" onload="x`}
	got := c.SVG([]float64{0, 1})
	if strings.Contains(got, `onload="x"`) || !strings.Contains(got, `id="a&#34;bL"`) {
		t.Errorf("not escaped: %s", got)
	}
}

func TestPath(t *testing.T) {
	c := Line{W: 100, H: 10, Lo: 0, Hi: 10}
	cases := []struct {
		name   string
		c      Line
		values []float64
		line   string
		area   string
	}{
		{"none", c, nil, "", ""},
		// One value is the whole width at that value.
		{"one", c, []float64{5}, "M0,5L100,5", "M0,10L0,5L100,5L100,10Z"},
		{"spread", c, []float64{0, 10, 5}, "M0,10L50,0L100,5", "M0,10L0,10L50,0L100,5L100,10Z"},
		// Out of range is drawn at the edge.
		{"clamped", c, []float64{-5, 20, math.Inf(1), math.Inf(-1)}, "M0,10L33.3,0L66.7,0L100,10", "M0,10L0,10L33.3,0L66.7,0L100,10L100,10Z"},
		// A NaN is a gap: the line breaks, it does not drop. A run of one
		// value between gaps is a dot.
		{"gaps", c, []float64{1, 2, nan, 3, nan, nan, 4, 5}, "M0,9L14.3,8M42.9,7L42.9,7M85.7,6L100,5",
			"M0,10L0,9L14.3,8L14.3,10ZM85.7,10L85.7,6L100,5L100,10Z"},
		{"all gaps", c, []float64{nan, nan}, "", ""},
		// Hi not above Lo: halfway up, never NaN.
		{"flat scale", Line{W: 100, H: 10, Lo: 3, Hi: 3}, []float64{1, 9}, "M0,5L100,5", "M0,10L0,5L100,5L100,10Z"},
		{"infinite scale", Line{W: 100, H: 10, Lo: math.Inf(-1), Hi: math.Inf(1)}, []float64{1, 9}, "M0,5L100,5", "M0,10L0,5L100,5L100,10Z"},
		// Xs place the values; values past the end of Xs are not drawn,
		// and an x that is not a number is a gap.
		{"xs", Line{W: 100, H: 10, Hi: 10, Xs: []float64{0.5, 0.75, nan, 1}}, []float64{0, 5, 7, 10, 99}, "M50,10L75,5M100,0L100,0", "M50,10L50,10L75,5L75,10Z"},
		{"xs longer", Line{W: 100, H: 10, Hi: 10, Xs: []float64{0, 0.25, 0.5}}, []float64{0, 10}, "M0,10L25,0", "M0,10L0,10L25,0L25,10Z"},
		{"one value at an x", Line{W: 100, H: 10, Hi: 10, Xs: []float64{0.5}}, []float64{5}, "M50,5L50,5", ""},
		{"infinite x", Line{W: 100, H: 10, Hi: 10, Xs: []float64{0, math.Inf(1), 1}}, []float64{0, 5, 10}, "M0,10L0,10M100,0L100,0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Path(tc.values); got != tc.line {
				t.Errorf("Path = %q, want %q", got, tc.line)
			}
			if got := tc.c.AreaPath(tc.values); got != tc.area {
				t.Errorf("AreaPath = %q, want %q", got, tc.area)
			}
		})
	}
}

func TestNum(t *testing.T) {
	for v, want := range map[float64]string{0: "0", -0.04: "0", 12: "12", 12.25: "12.3", 0.3: "0.3", -7.55: "-7.6", 1e6: "1000000"} {
		if got := num(v); got != want {
			t.Errorf("num(%v) = %q, want %q", v, got, want)
		}
	}
}

// patches decodes commands into "target key=value" lines.
func patches(t *testing.T, cmds []string) []string {
	t.Helper()
	var out []string
	var d hotty.Decoder
	for _, cmd := range cmds {
		m, r := d.Feed(cmd)
		if r != hotty.Complete || m.Get("a") != "patch" || m.Get("op") != "attr" || m.Get("s") != "dash" || m.Get("q") != "2" {
			t.Fatalf("not an attr patch to dash: %v", m.Control)
		}
		out = append(out, m.Get("t")+" "+m.Get("k")+"="+string(m.Payload))
	}
	return out
}

func TestPatch(t *testing.T) {
	c := Line{ID: "cpu", W: 100, H: 40, Fill: 0.2, Lo: 0, Hi: 100, Color: "#f07a7a", Grid: []float64{0.5}}
	got := patches(t, c.Patch("dash", []float64{0, 100}))
	want := []string{
		"cpuV viewBox=0 0 100 40",
		"cpuG d=M0,20h100v1h-100Z",
		"cpuL d=M0,40L100,0",
		"cpuL stroke=#f07a7a",
		"cpuA d=M0,40L0,40L100,0L100,40Z",
		"cpuA fill=#f07a7a",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Patch =\n%q\nwant\n%q", got, want)
	}

	got = patches(t, c.PatchShapes("dash", []float64{100, 0}))
	want = []string{"cpuL d=M0,0L100,40", "cpuA d=M0,40L0,0L100,40L100,40Z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PatchShapes = %q", got)
	}

	// No area, no grid: no patches for them.
	c.Fill, c.Grid = 0, nil
	if got := patches(t, c.Patch("dash", nil)); len(got) != 3 {
		t.Errorf("Patch without area and grid = %q", got)
	}
	if got := patches(t, c.PatchShapes("dash", nil)); !reflect.DeepEqual(got, []string{"cpuL d="}) {
		t.Errorf("PatchShapes without area = %q", got)
	}
}

// Shapes of several charts share one box, as a multi-series plot does.
func TestBoxShared(t *testing.T) {
	a := Line{ID: "a", W: 10, H: 10, Hi: 1, Color: "red"}
	b := Line{ID: "b", W: 10, H: 10, Hi: 1, Color: "blue"}
	got := Box("", 10, 10, a.Shapes([]float64{0, 1})+b.Shapes([]float64{1, 0}))
	if !strings.HasPrefix(got, `<div class="chart-box"><svg viewBox="0 0 10 10" preserveAspectRatio="none"><path id="aL"`) ||
		!strings.Contains(got, `<path id="bL" d="M0,0L10,10"`) || !strings.HasSuffix(got, `</svg></div>`) {
		t.Errorf("Box = %s", got)
	}
	if !strings.Contains(CSS, ".chart-box svg { position: absolute;") {
		t.Error("CSS does not place the svg absolutely")
	}
}

func TestBounds(t *testing.T) {
	cases := []struct {
		values []float64
		lo, hi float64
	}{
		{nil, 0, 1},
		{[]float64{nan, math.Inf(1)}, 0, 1},
		{[]float64{4}, 4, 5},
		{[]float64{3, 3}, 3, 4},
		{[]float64{2, nan, -1, 7, math.Inf(-1)}, -1, 7},
	}
	for _, c := range cases {
		if lo, hi := Bounds(c.values); lo != c.lo || hi != c.hi {
			t.Errorf("Bounds(%v) = %v, %v; want %v, %v", c.values, lo, hi, c.lo, c.hi)
		}
	}
}

func TestSpark(t *testing.T) {
	cases := []struct {
		values []float64
		lo, hi float64
		w      int
		want   string
	}{
		{[]float64{0, 1, 2, 3, 4, 5, 6, 7}, 0, 7, 8, "▁▂▃▄▅▆▇█"},
		// The last w values.
		{[]float64{0, 1, 2, 3, 4, 5, 6, 7}, 0, 7, 3, "▆▇█"},
		// Out of range at the edges; a NaN is a gap.
		{[]float64{-10, 100, nan, math.Inf(1)}, 0, 10, 10, "▁█ █"},
		// Hi not above lo: the lowest bar.
		{[]float64{1, 5}, 5, 5, 10, "▁▁"},
		{[]float64{1, 2}, 0, 1, 0, ""},
		{nil, 0, 1, 5, ""},
	}
	for _, c := range cases {
		if got := Spark(c.values, c.lo, c.hi, c.w); got != c.want {
			t.Errorf("Spark(%v, %v, %v, %d) = %q, want %q", c.values, c.lo, c.hi, c.w, got, c.want)
		}
	}
}

func TestRules(t *testing.T) {
	a := Line{ID: "a", W: 10, H: 10, Grid: []float64{0.5}, GridColor: "#333"}
	if got, want := a.Rules(), `<path id="aG" d="M0,5h10v1h-10Z" fill="#333"/>`; got != want {
		t.Errorf("Rules:\n%s\nwant\n%s", got, want)
	}
	if (Line{}).Rules() != "" {
		t.Error("Rules without a grid")
	}
	// Two series in one box: the grid under both lines.
	b := Line{ID: "b", W: 10, H: 10, Color: "#f00"}
	box := Box(a.BoxID(), 10, 10, a.Rules()+a.Shapes([]float64{1, 2})+b.Shapes([]float64{2, 1}))
	if !strings.HasPrefix(box, `<div class="chart-box"><svg id="aV" viewBox="0 0 10 10" preserveAspectRatio="none"><path id="aG"`) ||
		strings.Index(box, `id="aL"`) > strings.Index(box, `id="bL"`) {
		t.Errorf("Box: %s", box)
	}
}
