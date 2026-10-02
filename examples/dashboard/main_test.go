package main

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/neuroplastio/hotty-go/chart"
	"github.com/neuroplastio/hotty-go/hottytest"
)

// endpoints are three endpoints to watch: one up, one that answers 503,
// and one where nothing listens.
func endpoints(t *testing.T) []string {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Millisecond)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(up.Close)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
	}))
	t.Cleanup(failing.Close)
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	return []string{up.URL + "/healthz", failing.URL, gone.URL}
}

// start runs the dashboard on h, a screen of cols×rows, probing urls every
// 20 ms. p sends it messages; stop quits it as the user would, and waits.
func start(t *testing.T, h *hottytest.Host, cols, rows int, urls ...string) (p *tea.Program, stop func()) {
	t.Helper()
	cfg, err := parse(append([]string{"-every=20ms", "-timeout=1s"}, urls...), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfg, "dash-")
	p = tea.NewProgram(m, tea.WithInput(h), tea.WithOutput(m.s.Watch(h)), tea.WithWindowSize(cols, rows),
		tea.WithoutSignalHandler(), tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	m.s.Attach(p.Send)
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	return p, func() {
		t.Helper()
		h.Type("q")
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the dashboard did not quit")
		}
	}
}

// eventually waits for a condition on the host, for the program to catch
// up with the probes and the user.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// card is endpoint i's surface, nil if the host has none.
func card(h *hottytest.Host, i int) *hottytest.Surface { return h.Surface(fmt.Sprintf("dash-ep%d", i)) }

// state is what endpoint i's card says of it: "" with no card.
func state(h *hottytest.Host, i int) string {
	if s := card(h, i); s != nil {
		return s.TextOf("icon") + " " + s.TextOf("state")
	}
	return ""
}

// selected reports whether endpoint i's card is the one selected.
func selected(h *hottytest.Host, i int) bool {
	c, _ := card(h, i).Attr("card", "class")
	return c == "card sel"
}

// docs counts the documents sent for each surface.
func docs(h *hottytest.Host) map[string]int {
	n := map[string]int{}
	for _, c := range h.Commands() {
		if c.Get("a") == "doc" {
			n[c.Get("s")]++
		}
	}
	return n
}

var lat = chart.Line{ID: "lat"}

func TestCards(t *testing.T) {
	h := hottytest.New(t, hottytest.Size(80, 24))
	_, stop := start(t, h, 80, 24, endpoints(t)...)
	eventually(t, "every endpoint probed", func() bool {
		return state(h, 0) == "● up" && state(h, 1) == "✕ down" && state(h, 2) == "✕ down"
	})
	// A card a third of the room each, under the title, the full width.
	for i, y := range []int{2, 9, 16} {
		s := card(h, i)
		if _, row := s.At(); !s.Placed() || row != y || s.Placement().Cols != 80 || s.Placement().Rows != 6 {
			t.Errorf("card %d at row %d: %+v", i, row, s.Placement())
		}
	}
	if s, _ := card(h, 1).Attr("status", "data-state"); s != "down" {
		t.Errorf("the failing endpoint's state: %q", s)
	}
	// The probes come as deltas: the line grows, and no document is sent
	// again.
	eventually(t, "five probes charted", func() bool {
		d, _ := card(h, 0).Attr(lat.LineID(), "d")
		return strings.Count(d, "L") >= 4
	})
	if n := docs(h); len(n) != 3 || n["dash-ep0"] != 1 || n["dash-ep1"] != 1 || n["dash-ep2"] != 1 {
		t.Errorf("documents sent: %v", n)
	}
	if last := card(h, 0).TextOf("last"); !strings.HasSuffix(last, " ms") {
		t.Errorf("the last latency: %q", last)
	}
	// Nothing listens at the third endpoint: no latency, no line.
	if last := card(h, 2).TextOf("last"); last != "—" {
		t.Errorf("the gone endpoint's latency: %q", last)
	}
	if d, _ := card(h, 2).Attr(lat.LineID(), "d"); d != "" || card(h, 2).TextOf("scale") != "" {
		t.Errorf("the gone endpoint's line: %q, scaled %q", d, card(h, 2).TextOf("scale"))
	}

	// The first is selected, and the footer says more about it.
	if !selected(h, 0) || selected(h, 1) {
		t.Error("the first card is not the one selected")
	}
	eventually(t, "the footer about the first", func() bool { return strings.Contains(h.Screen(), "200 OK · ") })

	// A press on a card selects it; so do the arrow keys.
	if err := h.Press("dash-ep1", "card"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second selected", func() bool { return selected(h, 1) && !selected(h, 0) })
	eventually(t, "the footer about the second", func() bool {
		return strings.Contains(h.Screen(), "503 Service Unavailable · down ") && !strings.Contains(h.Screen(), "down 0 of")
	})
	h.Type("\x1b[B")
	eventually(t, "the third selected", func() bool { return selected(h, 2) })
	eventually(t, "the footer about the third", func() bool { return strings.Contains(h.Screen(), "connection refused") })

	// On the way out, the cards go.
	stop()
	if s := h.Surfaces(); len(s) != 0 {
		t.Errorf("left behind: %v", s)
	}
}

// On a short screen, the cards that do not fit are scrolled off: hidden,
// and placed again without their documents when they come back.
func TestScrolling(t *testing.T) {
	h := hottytest.New(t, hottytest.Size(80, 12))
	_, stop := start(t, h, 80, 12, endpoints(t)...)
	defer stop()
	eventually(t, "two cards shown", func() bool { return state(h, 1) == "✕ down" })
	if card(h, 2) != nil {
		t.Fatal("the third card was sent, with no room for it")
	}
	h.Type("jj")
	eventually(t, "the third card shown", func() bool { s := card(h, 2); return s != nil && s.Placed() })
	if card(h, 0).Placed() {
		t.Error("the first card is still placed")
	}
	if _, row := card(h, 2).At(); row != 6 {
		t.Errorf("the third card at row %d", row)
	}
	h.Type("kk")
	eventually(t, "the first card back", func() bool { return card(h, 0).Placed() && !card(h, 2).Placed() })
	if n := docs(h); n["dash-ep0"] != 1 {
		t.Errorf("the first card's document was sent %d times", n["dash-ep0"])
	}
	// What it missed while hidden came as deltas.
	if !selected(h, 0) || selected(h, 2) {
		t.Error("the selection did not follow")
	}
}

// When the screen changes size, the cards are placed again, and their
// charts take the new size: the box's viewBox, its grid and line.
func TestResize(t *testing.T) {
	h := hottytest.New(t, hottytest.Size(80, 24))
	p, stop := start(t, h, 80, 24, endpoints(t)...)
	defer stop()
	eventually(t, "the first card", func() bool { return state(h, 0) == "● up" })
	p.Send(tea.WindowSizeMsg{Width: 60, Height: 15})
	// 15 rows leave 11 for cards: 3 rows each.
	eventually(t, "the cards placed again", func() bool {
		s := card(h, 0)
		return s.Placement().Cols == 60 && s.Placement().Rows == 3
	})
	want := fmt.Sprintf("0 0 %g %g", 60*9.0-2, 2*18.0-1)
	eventually(t, "the chart resized", func() bool {
		v, _ := card(h, 0).Attr(lat.BoxID(), "viewBox")
		return v == want
	})
	if n := docs(h); n["dash-ep0"] != 1 {
		t.Errorf("the first card's document was sent %d times", n["dash-ep0"])
	}
}

// A light terminal gets the light scheme's blue.
func TestLight(t *testing.T) {
	caps := hottytest.DefaultCaps()
	caps.Scheme = "light"
	h := hottytest.New(t, hottytest.Caps(caps))
	_, stop := start(t, h, 80, 24, endpoints(t)[0])
	defer stop()
	eventually(t, "the card", func() bool { return state(h, 0) == "● up" })
	if c, _ := card(h, 0).Attr(lat.LineID(), "stroke"); c != blueLight {
		t.Errorf("the line's colour: %q", c)
	}
}

// On a terminal that is not a host, an endpoint is a line of cells.
func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text(), hottytest.Size(80, 24))
	_, stop := start(t, h, 80, 24, endpoints(t)...)
	eventually(t, "every endpoint probed, twice", func() bool {
		lines := strings.Split(h.Screen(), "\n")
		return len(lines) > 4 && strings.Contains(lines[2], "● up") && strings.ContainsAny(lines[2], "▁▂▃▄▅▆▇█") &&
			strings.Contains(lines[3], "✕ down") && strings.Contains(lines[4], "✕ down")
	})
	lines := strings.Split(h.Screen(), "\n")
	if !strings.HasPrefix(lines[0], "Dashboard · 3 endpoints · every 20ms") || !strings.HasPrefix(lines[2], "› ") {
		t.Errorf("the screen:\n%s", h.Screen())
	}
	if !strings.Contains(lines[4], "—") {
		t.Errorf("the gone endpoint's latency: %q", lines[4])
	}
	h.Type("j")
	eventually(t, "the second selected", func() bool {
		return strings.HasPrefix(strings.Split(h.Screen(), "\n")[3], "› ")
	})
	stop()
	// The query was all: Text fails the test on any other command.
	if n := len(h.Commands()); n != 1 {
		t.Errorf("%d HOTTY commands sent", n)
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "usage: dashboard"},
		{[]string{"-window=1", "http://a"}, "usage: dashboard"},
		{[]string{"-nope", "http://a"}, "flag provided but not defined"},
		{[]string{"ftp://a"}, `"ftp://a" is not an http or https URL`},
		{[]string{"localhost:80"}, "is not an http or https URL"},
	} {
		var errs strings.Builder
		if _, err := parse(tc.args, &errs); err == nil || !strings.Contains(errs.String(), tc.want) {
			t.Errorf("%q: %v, %q", tc.args, err, errs.String())
		}
	}
	c, err := parse([]string{"-every=1s", "https://a/b", "http://c"}, io.Discard)
	if err != nil || c.every != time.Second || c.window != 60 || len(c.urls) != 2 {
		t.Errorf("%+v %v", c, err)
	}
}

func TestReason(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(time.Second) }))
	defer slow.Close()
	_, err := (&http.Client{Timeout: 10 * time.Millisecond}).Get(slow.URL)
	if r := reason(err); r != "timed out" {
		t.Errorf("a timeout: %q", r)
	}
}

func TestFormats(t *testing.T) {
	for v, want := range map[float64]string{0.25: "0.2 ms", 9.96: "10 ms", 999.7: "1.00 s", 12.4: "12 ms", 1500: "1.50 s"} {
		if got := ms(v); got != want {
			t.Errorf("ms(%v) = %q, want %q", v, got, want)
		}
	}
	e := &endpoint{lat: []float64{3, math.NaN(), 7}}
	if e.hi() != 10 || e.last() != "7.0 ms" {
		t.Errorf("hi %v, last %q", e.hi(), e.last())
	}
	if e.answer() != "no answer yet" || (&endpoint{code: 418}).answer() != "418 I'm a teapot" {
		t.Error("answer")
	}
	if cut("abcdef", 3) != "abc" || cut("ab", 3) != "ab" {
		t.Error("cut")
	}
}
