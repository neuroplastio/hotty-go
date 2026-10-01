// Livechart charts the numbers a command prints, live, below the command
// line, and leaves the chart in the scrollback when the command ends.
//
//	ping -i 0.2 example.com | livechart -key time
//	(echo "rx tx"; while sleep 1; do rates; done) | livechart -rows 10
//
// series.Parser reads the numbers: a column a series, named by a first
// line with no numbers, or with -key the number after KEY=. On a HOTTY host
// the chart is a detached surface, sent once: each frame patches its lines
// (chart.Line.PatchShapes) and the values in its legend, and the grid only
// when the axis changes, which series.Scale keeps from happening on every
// sample. On a terminal that is not a host the chart is braille cells,
// redrawn in place. When stdout is not the terminal, livechart passes its
// input through, so it can sit in a pipeline; with no terminal at all, it
// only does that, and sums up on stderr.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/braille"
	"github.com/neuroplastio/hotty-go/chart"
	"github.com/neuroplastio/hotty-go/hottyterm"
	"github.com/neuroplastio/hotty-go/series"
)

// env is the process's streams and terminal: main fills it from the OS,
// a test from hottytest.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	tty            bool                            // stdout is the terminal
	open           func() (*hottyterm.Term, error) // the terminal, whatever the streams
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	e := env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, tty: hottyterm.IsTerminal(os.Stdout),
		open: func() (*hottyterm.Term, error) { return hottyterm.Open("livechart") }}
	os.Exit(run(ctx, os.Args[1:], e))
}

// Exit statuses.
const (
	ok          = 0
	noNumbers   = 1
	usage       = 2
	interrupted = 130
)

// maxSeries is how many series a chart draws: the palette's colours. The
// other series are summed up, not drawn.
const maxSeries = 8

// frameEvery is the most frames a second livechart draws: samples that
// come faster are drawn together.
const frameEvery = 50 * time.Millisecond

// view is how a rendition shows the chart: a frame, and the end.
type view interface {
	frame(c *data)
	end(c *data, summary []string)
}

// data is what has been read: the series' window and statistics.
type data struct {
	p      *series.Parser
	set    *series.Set
	window int
}

func run(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("livechart", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	keys := fs.String("key", "", "chart the numbers after KEY= instead of columns, comma-separated keys")
	rows := fs.Int("rows", 8, "the chart's height in rows, at least 3")
	window := fs.Int("window", 120, "how many samples the chart shows, at least 2")
	if err := fs.Parse(args); err != nil {
		return usage // the flag package said why
	}
	if *rows < 3 || *window < 2 || fs.NArg() > 0 {
		fs.Usage()
		return usage
	}
	d := &data{p: &series.Parser{}, set: series.NewSet(*window, false), window: *window}
	if *keys != "" {
		d.p.Keys = strings.Split(*keys, ",")
	}

	var v view = none{e.stderr}
	if t, err := e.open(); err == nil {
		defer t.Close()
		if t.Detect(ctx) {
			v = newSurface(ctx, t, *rows)
		} else {
			v = &cells{t: t, cols: t.Size().Cols, rows: *rows}
		}
	}

	lines := readLines(e.stdin)
	tick := time.NewTicker(frameEvery)
	defer tick.Stop()
	drawn := true
	for {
		select {
		case line, more := <-lines:
			if !more {
				return finish(v, d, ok)
			}
			if !e.tty {
				fmt.Fprintln(e.stdout, line) // the pipeline's, as it came
			}
			if sm, isSample := d.p.Line(line); isSample {
				d.set.Add(sm)
				d.set.SetNames(d.p.Names())
				drawn = false
			}
		case <-tick.C:
			if !drawn {
				v.frame(d)
				drawn = true
			}
		case <-ctx.Done():
			return finish(v, d, interrupted)
		}
	}
}

// finish draws the last frame and the summary.
func finish(v view, d *data, status int) int {
	if d.set.Len() == 0 {
		v.end(d, []string{"livechart: no numbers in the input"})
		if status == ok {
			status = noNumbers
		}
		return status
	}
	v.frame(d)
	v.end(d, summary(d))
	return status
}

// readLines sends stdin's lines, and closes the channel at its end.
func readLines(r io.Reader) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			out <- sc.Text()
		}
	}()
	return out
}

// name is series i's name: its column's or key's, or its number.
func (d *data) name(i int) string {
	if names := d.set.Names(); i < len(names) && names[i] != "" {
		return names[i]
	}
	if d.set.Width() == 1 {
		return "value"
	}
	return "#" + strconv.Itoa(i+1)
}

// format writes series i's value with the decimals its input had.
func (d *data) format(i int, v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	dec := 0
	if ds := d.p.Decimals(); i < len(ds) {
		dec = ds[i]
	}
	return strconv.FormatFloat(v, 'f', dec, 64)
}

// series are the window's values of the series drawn, and where each
// sample goes across: the newest at the right edge, each in the place its
// turn in the window gives it, so that the lines move along rather than
// stretch while the window fills.
func (d *data) series() (values [][]float64, xs []float64) {
	_, win := d.set.Last()
	n := min(d.set.Width(), maxSeries)
	values = make([][]float64, n)
	for j, sm := range win {
		xs = append(xs, float64(d.window-len(win)+j)/float64(d.window-1))
		for i := range n {
			values[i] = append(values[i], sm.Values[i])
		}
	}
	return values, xs
}

// bounds are the smallest and largest values in the window.
func bounds(values [][]float64) (lo, hi float64) {
	var all []float64
	for _, v := range values {
		all = append(all, v...)
	}
	return chart.Bounds(all)
}

// summary is a line a series: the samples' statistics, in the input's
// precision.
func summary(d *data) []string {
	var out []string
	for i := range d.set.Width() {
		st := d.set.Stats(i)
		line := fmt.Sprintf("%s: n=%d min=%s mean=%s max=%s last=%s", d.name(i), st.N,
			d.format(i, st.Min), d.format(i, st.Mean()), d.format(i, st.Max), d.format(i, st.Last))
		if i >= maxSeries {
			line += " (not charted)"
		}
		out = append(out, line)
	}
	return out
}

// none is no terminal: nothing to draw on, and the summary goes to stderr.
type none struct{ stderr io.Writer }

func (none) frame(*data) {}

func (n none) end(_ *data, summary []string) {
	for _, l := range summary {
		fmt.Fprintln(n.stderr, l)
	}
}

// --- the surface ---------------------------------------------------------

// The series' colours, in their order, for dark and light terminals; and
// the grid's and the axis labels'. Text keeps the terminal's colours.
var (
	paletteDark  = []string{"#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"}
	paletteLight = []string{"#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"}
)

const (
	gridDark, gridLight = "#2c2c2a", "#e1e0d9"
	muted               = "#898781"
)

// surface is the chart on a HOTTY host.
type surface struct {
	ctx        context.Context
	t          *hottyterm.Term
	name       string
	cols, rows int
	w, h       float64 // the plot, in CSS pixels
	ch         float64 // a row, in CSS pixels
	light      bool
	scale      *series.Scale

	// What the host's document has, so that a frame patches only what
	// changed.
	n      int // the series drawn; -1 before the first sample
	axis   series.Axis
	values []string // the legend's
}

// newSurface places the chart, waiting for numbers, under the command line.
func newSurface(ctx context.Context, t *hottyterm.Term, rows int) *surface {
	cw, ch := t.Caps().CellCSS()
	s := &surface{ctx: ctx, t: t, name: t.Surface("chart"), cols: t.Size().Cols, rows: rows,
		ch: ch, light: t.Caps().Light(), scale: series.NewScale(), n: -1}
	s.w, s.h = float64(s.cols)*cw, float64(rows-1)*ch
	_ = t.LineStart(ctx)
	// Detached from the start: it reports nothing, so nothing is left to
	// report to the shell, whatever way livechart exits (SPEC §5.5).
	_ = t.Send(hotty.DocDetached(s.name, s.page(`<p class="wait">waiting for numbers…</p>`)),
		hotty.Place(s.name, hotty.Placement{Cols: s.cols, Rows: rows}))
	return s
}

func (s *surface) palette() []string {
	if s.light {
		return paletteLight
	}
	return paletteDark
}

// lines are the series' charts on the axis: the first draws the grid, at
// the axis' ticks.
func (s *surface) lines(n int, xs []float64, axis series.Axis) []chart.Line {
	grid := gridDark
	if s.light {
		grid = gridLight
	}
	out := make([]chart.Line, n)
	for i := range out {
		out[i] = chart.Line{ID: "s" + strconv.Itoa(i), W: s.w, H: s.h, Lo: axis.Lo, Hi: axis.Hi,
			Color: s.palette()[i], Xs: xs}
	}
	if n == 1 {
		out[0].Fill = 0.12 // one series has room for its area
	}
	if n > 0 {
		for _, v := range axis.Ticks() {
			out[0].Grid = append(out[0].Grid, axis.Frac(v))
		}
		out[0].GridColor = grid
	}
	return out
}

func (s *surface) frame(d *data) {
	values, xs := d.series()
	axis := s.scale.Fit(bounds(values))
	lines := s.lines(len(values), xs, axis)
	legend := make([]string, len(values))
	for i := range values {
		legend[i] = d.format(i, values[i][len(values[i])-1])
	}
	if len(values) != s.n {
		// The first sample, or a series more: the document again, which
		// keeps its placement (SPEC §5.1).
		_ = s.t.Send(hotty.DocDetached(s.name, s.page(s.body(d, lines, values, legend, axis))))
		s.n, s.axis, s.values = len(values), axis, legend
		return
	}
	var cmds []string
	if axis != s.axis {
		cmds = append(cmds, hotty.SetAttr(s.name, lines[0].GridID(), "d", lines[0].GridPath()),
			hotty.MorphTo(s.name, "ticks", s.ticks(d, axis)))
		s.axis = axis
	}
	for i, l := range lines {
		cmds = append(cmds, l.PatchShapes(s.name, values[i])...)
		if legend[i] != s.values[i] {
			cmds = append(cmds, hotty.SetText(s.name, "v"+strconv.Itoa(i), legend[i]))
		}
	}
	s.values = legend
	_ = s.t.Send(hotty.Sync(cmds...))
}

// page is the document around a body.
func (s *surface) page(body string) string {
	return fmt.Sprintf(`<style>%s
html, body { margin: 0; height: 100%%; }
.lc { height: 100%%; display: grid; grid-template-rows: %.2fpx 1fr; font: 12px/%.2fpx var(--hotty-font); color: var(--hotty-fg); }
.legend { display: flex; gap: 2.5ch; padding: 0 1ch; white-space: nowrap; overflow: hidden; }
.legend i { display: inline-block; width: 14px; height: 2px; border-radius: 1px; vertical-align: middle; margin-right: .8ch; }
.legend b { font-weight: 600; margin-left: .8ch; font-variant-numeric: tabular-nums; }
.plot { position: relative; }
.ticks span { position: absolute; left: .5ch; font-size: 10px; line-height: 1; color: %s; font-variant-numeric: tabular-nums; }
.wait { margin: 0 1ch; color: %s; }
</style><div class="lc">%s</div>`, chart.CSS, s.ch, s.ch, muted, muted, body)
}

// body is the legend over the plot: the series' lines in a box they share,
// and the axis' labels.
func (s *surface) body(d *data, lines []chart.Line, values [][]float64, legend []string, axis series.Axis) string {
	var b, shapes strings.Builder
	b.WriteString(`<div class="legend">`)
	for i, l := range lines {
		swatch := ""
		if len(lines) > 1 { // one series needs no key: its name says it
			swatch = `<i style="background:` + l.Color + `"></i>`
		}
		if i > 0 {
			b.WriteString(" ") // apart in the text too, as a reader reads it
		}
		fmt.Fprintf(&b, `<span>%s%s <b id="v%d">%s</b></span>`, swatch, html.EscapeString(d.name(i)), i, legend[i])
		shapes.WriteString(l.Shapes(values[i]))
	}
	if more := d.set.Width() - len(lines); more > 0 {
		fmt.Fprintf(&b, ` <span style="color:%s">%d more not charted</span>`, muted, more)
	}
	b.WriteString(`</div><div class="plot">`)
	b.WriteString(chart.Box(lines[0].BoxID(), s.w, s.h, lines[0].Rules()+shapes.String()))
	b.WriteString(s.ticks(d, axis))
	b.WriteString(`</div>`)
	return b.String()
}

// ticks are the axis' labels, each just above its rule; the top one just
// below it.
func (s *surface) ticks(d *data, axis series.Axis) string {
	var b strings.Builder
	b.WriteString(`<div id="ticks" class="ticks">`)
	for _, v := range axis.Ticks() {
		label := strconv.FormatFloat(v, 'f', axis.Decimals(), 64)
		if f := axis.Frac(v); f > 0.99 {
			fmt.Fprintf(&b, `<span style="top:2px">%s</span>`, label)
		} else {
			fmt.Fprintf(&b, `<span style="bottom:calc(%.2f%% + 2px)">%s</span>`, 100*f, label)
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

// end writes the summary under the chart, and waits for the host to take
// everything, so that livechart exits with the chart drawn.
func (s *surface) end(d *data, summary []string) {
	if s.n < 0 {
		_ = s.t.Send(hotty.DocDetached(s.name, s.page(`<p class="wait">`+html.EscapeString(summary[0])+`</p>`)))
	}
	_ = s.t.Send(strings.Join(summary, "\r\n") + "\r\n")
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), hottyterm.FenceTimeout)
	defer cancel()
	_, _ = s.t.Fence(ctx)
}

// --- cells ---------------------------------------------------------------

// cells is the chart on a terminal that is not a host: braille, a legend
// row over it, and the axis' ends in a gutter on the left.
type cells struct {
	t          *hottyterm.Term
	cols, rows int
	drawn      int // the rows of the last frame, to go back up over
	scale      *series.Scale
}

// inks are the series' colours in cells: the 256 colours nearest the
// palette's.
var inks = []string{"38;5;32", "38;5;202", "38;5;36", "38;5;178", "38;5;168", "38;5;28", "38;5;98", "38;5;167"}

func (c *cells) frame(d *data) {
	if c.scale == nil {
		c.scale = series.NewScale()
	}
	values, xs := d.series()
	axis := c.scale.Fit(bounds(values))
	top := strconv.FormatFloat(axis.Hi, 'f', axis.Decimals(), 64)
	bottom := strconv.FormatFloat(axis.Lo, 'f', axis.Decimals(), 64)
	gutter := max(len(top), len(bottom)) + 1
	cv := braille.New(max(1, c.cols-gutter), c.rows-1)
	w, h := cv.Size()
	at := func(j, i int) (x, y int) {
		return int(math.Round(xs[j] * float64(w-1))), int(math.Round((1 - axis.Frac(values[i][j])) * float64(h-1)))
	}
	for i := range values {
		for j := range values[i] {
			if math.IsNaN(values[i][j]) {
				continue
			}
			x, y := at(j, i)
			if j > 0 && !math.IsNaN(values[i][j-1]) {
				x0, y0 := at(j-1, i)
				cv.Line(x0, y0, x, y, uint8(i+1))
			} else {
				cv.Set(x, y, uint8(i+1))
			}
		}
	}
	var b strings.Builder
	if c.drawn > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", c.drawn)
	}
	var legend []string
	for i := range values {
		key := ""
		if len(values) > 1 {
			key = "\x1b[" + inks[i] + "m━\x1b[m "
		}
		legend = append(legend, key+d.name(i)+" "+d.format(i, values[i][len(values[i])-1]))
	}
	b.WriteString("\r" + strings.Join(legend, "   ") + "\x1b[K\r\n")
	for r := range c.rows - 1 {
		label := ""
		switch r {
		case 0:
			label = top
		case c.rows - 2:
			label = bottom
		}
		fmt.Fprintf(&b, "\r%*s %s\x1b[K\r\n", gutter-1, label, cv.Row(r, func(ink uint8, s string) string {
			return "\x1b[" + inks[ink-1] + "m" + s + "\x1b[m"
		}))
	}
	c.drawn = c.rows
	_ = c.t.Send(b.String())
}

func (c *cells) end(_ *data, summary []string) {
	_ = c.t.Send(strings.Join(summary, "\r\n") + "\r\n")
}
