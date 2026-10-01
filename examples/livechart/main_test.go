package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/neuroplastio/hotty-go/chart"
	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/term"
)

const chartName = "livechart-chart"

// on is livechart's environment on h, reading in: stdout is the terminal.
func on(h *hottytest.Host, in io.Reader) (env, *strings.Builder) {
	var errs strings.Builder
	return env{stdin: in, stdout: h, stderr: &errs, tty: true,
		open: func() (*term.Term, error) { return term.New(h, h, "livechart", nil, nil), nil }}, &errs
}

// start runs livechart in the background; wait returns its exit status.
func start(t *testing.T, ctx context.Context, args []string, e env) (wait func() int) {
	t.Helper()
	done := make(chan int, 1)
	go func() { done <- run(ctx, args, e) }()
	return func() int {
		t.Helper()
		select {
		case code := <-done:
			return code
		case <-time.After(5 * time.Second):
			t.Fatal("livechart did not finish")
			return 0
		}
	}
}

// eventually waits for a condition on the host, for livechart to draw a
// frame.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// shows waits until the chart's element id says text.
func shows(t *testing.T, h *hottytest.Host, id, text string) {
	t.Helper()
	eventually(t, fmt.Sprintf("#%s says %q", id, text), func() bool {
		s := h.Surface(chartName)
		return s != nil && s.TextOf(id) == text
	})
}

// ping writes a line as ping prints it.
func ping(w io.Writer, seq int, ms float64) {
	fmt.Fprintf(w, "64 bytes from 192.0.2.1: icmp_seq=%d ttl=57 time=%.1f ms\n", seq, ms)
}

// docs counts the documents sent.
func docs(h *hottytest.Host) int {
	n := 0
	for _, c := range h.Commands() {
		if c.Get("a") == "doc" {
			n++
		}
	}
	return n
}

var first = chart.Line{ID: "s0"}

// ping's times, charted as they come: one document, then patches.
func TestStream(t *testing.T) {
	h := hottytest.New(t)
	in, feed := io.Pipe()
	e, errs := on(h, in)
	wait := start(t, context.Background(), []string{"-key", "time", "-window", "10"}, e)
	eventually(t, "the chart waits", func() bool {
		s := h.Surface(chartName)
		return s != nil && s.Placed() && s.Text() == "waiting for numbers…"
	})
	s := h.Surface(chartName)
	if p := s.Placement(); !s.Detached() || p.Cols != 80 || p.Rows != 8 {
		t.Errorf("the chart: detached %v, %+v", s.Detached(), p)
	}
	fmt.Fprintln(feed, "PING example.com (192.0.2.1) 56(84) bytes of data.") // no time=: no sample
	ping(feed, 1, 12.3)
	shows(t, h, "v0", "12.3")
	if !strings.HasPrefix(s.Text(), "time 12.3") {
		t.Errorf("the legend: %q", s.Text())
	}
	for i, ms := range []float64{14.1, 11.8, 13.0} {
		ping(feed, i+2, ms)
		shows(t, h, "v0", fmt.Sprintf("%.1f", ms))
	}
	// The waiting document and the first sample's; the rest were patches.
	if n := docs(h); n != 2 {
		t.Errorf("%d documents", n)
	}
	// Four samples of a window of ten, the last at the right edge: the line
	// starts at the seventh place of ten, two thirds along.
	if d, _ := s.Attr(first.LineID(), "d"); strings.Count(d, "L") != 3 || !strings.HasPrefix(d, "M480,") {
		t.Errorf("the line: %q", d)
	}
	_ = feed.Close()
	if code := wait(); code != ok {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(h.Screen(), "time: n=4 min=11.8 mean=12.8 max=14.1 last=13.0") {
		t.Errorf("the summary:\n%s", h.Screen())
	}
}

// Columns named by a header: a line a series, in the palette's order, and
// a legend to tell them apart.
func TestColumns(t *testing.T) {
	h := hottytest.New(t)
	e, _ := on(h, strings.NewReader("rx tx\n1 10\n2 20\n3 15\n"))
	if code := start(t, context.Background(), nil, e)(); code != ok {
		t.Fatalf("exit %d", code)
	}
	s := h.Surface(chartName)
	if s.TextOf("v0") != "3" || s.TextOf("v1") != "15" || !strings.HasPrefix(s.Text(), "rx 3 tx 15 ") {
		t.Errorf("the legend: %q", s.Text())
	}
	for i, want := range paletteDark[:2] {
		l := chart.Line{ID: fmt.Sprint("s", i)}
		if c, _ := s.Attr(l.LineID(), "stroke"); c != want {
			t.Errorf("series %d's colour: %q", i, c)
		}
		if _, area := s.Attr(l.AreaID(), "d"); area {
			t.Errorf("series %d has an area: two would hide each other", i)
		}
	}
	// The grid is the axis' ticks: 0 to 25 by 5, some room above the top.
	if ticks := s.TextOf("ticks"); ticks != "0510152025" {
		t.Errorf("the ticks: %q", ticks)
	}
	if !strings.Contains(h.Screen(), "rx: n=3 min=1 mean=2 max=3 last=3\ntx: n=3 min=10 mean=15 max=20 last=15") {
		t.Errorf("the summary:\n%s", h.Screen())
	}
}

// The axis grows when a value falls outside it: the grid and its labels
// change, by patches still.
func TestAxis(t *testing.T) {
	h := hottytest.New(t)
	in, feed := io.Pipe()
	e, _ := on(h, in)
	wait := start(t, context.Background(), nil, e)
	fmt.Fprint(feed, "1\n2\n3\n")
	shows(t, h, "v0", "3")
	s := h.Surface(chartName)
	grid, _ := s.Attr(first.GridID(), "d")
	if ticks := s.TextOf("ticks"); ticks != "01234" {
		t.Errorf("the ticks: %q", ticks)
	}
	fmt.Fprint(feed, "100\n")
	shows(t, h, "v0", "100")
	if ticks := s.TextOf("ticks"); ticks != "050100" && ticks != "0255075100125" {
		t.Errorf("the ticks after 100: %q", ticks)
	}
	if g, _ := s.Attr(first.GridID(), "d"); g == grid {
		t.Error("the grid stayed")
	}
	_ = feed.Close()
	if code := wait(); code != ok || docs(h) != 2 {
		t.Errorf("exit %d, %d documents", code, docs(h))
	}
}

// A light terminal gets the palette's light steps; one series, an area.
func TestLight(t *testing.T) {
	caps := hottytest.DefaultCaps()
	caps.Scheme = "light"
	h := hottytest.New(t, hottytest.Caps(caps))
	e, _ := on(h, strings.NewReader("5\n7\n"))
	_ = start(t, context.Background(), nil, e)()
	s := h.Surface(chartName)
	if c, _ := s.Attr(first.AreaID(), "fill"); c != paletteLight[0] {
		t.Errorf("the area: %q", c)
	}
	if !strings.HasPrefix(s.Text(), "value 7 ") {
		t.Errorf("the legend: %q", s.Text())
	}
}

// More series than colours: those past the palette are summed up, not
// drawn.
func TestManySeries(t *testing.T) {
	h := hottytest.New(t)
	e, _ := on(h, strings.NewReader("1 2 3 4 5 6 7 8 9 10\n"))
	_ = start(t, context.Background(), nil, e)()
	s := h.Surface(chartName)
	if !strings.Contains(s.Text(), "2 more not charted") || s.TextOf("v8") != "" {
		t.Errorf("the legend: %q", s.Text())
	}
	if !strings.Contains(h.Screen(), "#10: n=1 min=10 mean=10 max=10 last=10 (not charted)") {
		t.Errorf("the summary:\n%s", h.Screen())
	}
}

// On a terminal that is not a host: braille, redrawn in place.
func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	in, feed := io.Pipe()
	e, _ := on(h, in)
	wait := start(t, context.Background(), []string{"-rows", "5"}, e)
	fmt.Fprint(feed, "1\n4\n2\n")
	eventually(t, "a frame", func() bool { return strings.HasPrefix(h.Screen(), "value 2") })
	fmt.Fprint(feed, "3\n6\n")
	eventually(t, "the next frame, over it", func() bool { return strings.HasPrefix(h.Screen(), "value 6") })
	_ = feed.Close()
	if code := wait(); code != ok {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(h.Screen(), "\n")
	if len(lines) != 6 || lines[5] != "value: n=5 min=1 mean=3 max=6 last=6" {
		t.Fatalf("the screen:\n%s", h.Screen())
	}
	// The axis' ends in the gutter, and dots beside them.
	braille := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return r > 0x2800 && r <= 0x28ff })
	}
	if !strings.HasPrefix(lines[1], "8") || !strings.HasPrefix(lines[4], "0") || !braille(lines[2]) || !braille(lines[3]) {
		t.Errorf("the chart:\n%s", h.Screen())
	}
}

// Two series in cells: a legend of keys in their colours.
func TestCellsColumns(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	e, _ := on(h, strings.NewReader("a,b\n1,2\n2,1\n"))
	_ = start(t, context.Background(), nil, e)()
	if !strings.HasPrefix(h.Screen(), "━ a 2   ━ b 1") || !strings.Contains(h.Output(), "\x1b[38;5;202m━") {
		t.Errorf("the legend:\n%s", h.Screen())
	}
}

// In a pipeline: the input goes through to stdout as it came, the chart
// to the terminal.
func TestTee(t *testing.T) {
	h := hottytest.New(t)
	e, _ := on(h, strings.NewReader("x=1\nnoise\nx=3\n"))
	var out strings.Builder
	e.stdout, e.tty = &out, false
	if code := start(t, context.Background(), []string{"-key", "x"}, e)(); code != ok || out.String() != "x=1\nnoise\nx=3\n" {
		t.Errorf("exit %d, stdout %q", code, out.String())
	}
	if h.Surface(chartName).TextOf("v0") != "3" {
		t.Error("no chart")
	}
}

// With no terminal at all: the input through, and the summary on stderr.
func TestNoTerminal(t *testing.T) {
	var out, errs strings.Builder
	e := env{stdin: strings.NewReader("1\n3\n"), stdout: &out, stderr: &errs,
		open: func() (*term.Term, error) { return nil, term.ErrNoTerminal }}
	if code := run(context.Background(), nil, e); code != ok || out.String() != "1\n3\n" ||
		errs.String() != "value: n=2 min=1 mean=2 max=3 last=3\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errs.String())
	}
}

func TestNoNumbers(t *testing.T) {
	h := hottytest.New(t)
	e, _ := on(h, strings.NewReader("hello\nworld\n"))
	if code := start(t, context.Background(), nil, e)(); code != noNumbers {
		t.Errorf("exit %d", code)
	}
	if got := h.Surface(chartName).Text(); got != "livechart: no numbers in the input" {
		t.Errorf("the chart says %q", got)
	}
	var errs strings.Builder
	e = env{stdin: strings.NewReader(""), stdout: io.Discard, stderr: &errs,
		open: func() (*term.Term, error) { return nil, term.ErrNoTerminal }}
	if code := run(context.Background(), nil, e); code != noNumbers || !strings.Contains(errs.String(), "no numbers") {
		t.Errorf("exit %d: %q", code, errs.String())
	}
}

// Interrupted, livechart draws what it has and sums it up.
func TestInterrupted(t *testing.T) {
	h := hottytest.New(t)
	in, feed := io.Pipe()
	defer feed.Close()
	e, _ := on(h, in)
	ctx, cancel := context.WithCancel(context.Background())
	wait := start(t, ctx, nil, e)
	fmt.Fprint(feed, "4\n")
	shows(t, h, "v0", "4")
	cancel()
	if code := wait(); code != interrupted || !strings.Contains(h.Screen(), "value: n=1 ") {
		t.Errorf("exit %d:\n%s", code, h.Screen())
	}
	if !h.Surface(chartName).Detached() {
		t.Error("the chart reports to the shell")
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{{"-rows", "2"}, {"-window", "1"}, {"file.txt"}, {"-nope"}} {
		var errs strings.Builder
		e := env{stdin: strings.NewReader(""), stdout: io.Discard, stderr: &errs}
		if code := run(context.Background(), args, e); code != usage || !strings.Contains(errs.String(), "-rows") {
			t.Errorf("%q: exit %d, %q", args, code, errs.String())
		}
	}
}
