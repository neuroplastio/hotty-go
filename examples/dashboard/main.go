// Dashboard watches HTTP endpoints, full screen: whether each is up, and
// its latency charted as the probes come back.
//
//	dashboard https://example.com https://api.example.com/healthz
//
// It is a Bubble Tea program, and each endpoint is a card on a surface that
// hottytea keeps in place as the frame changes. A probe changes its card
// with deltas: the chart's line (chart.Line.DeltaShapes) and whatever
// numbers changed, a few hundred bytes where a new document would be
// thousands. A card scrolled off the screen is hidden rather than deleted,
// so it comes back without its document being sent again. A press on a card
// selects it, and the footer, drawn in cells, says more about it.
//
// On a terminal that is not a HOTTY host, each endpoint is a line of cells
// with a sparkline (chart.Spark).
//
// Keys: ↑ and ↓ (or k and j) select, r probes now, q quits.
package main

import (
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/chart"
	"github.com/neuroplastio/hotty-go/hottytea"
	"github.com/neuroplastio/hotty-go/series"
)

func main() {
	cfg, err := parse(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	m := newModel(cfg, fmt.Sprintf("dashboard-%d-", os.Getpid()))
	p := tea.NewProgram(m, tea.WithOutput(m.s.WatchFile(os.Stdout)))
	m.s.Attach(p.Send)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
}

// config is what the command line asks for.
type config struct {
	urls    []string
	every   time.Duration // between probes of an endpoint
	timeout time.Duration // a probe's
	window  int           // the probes a chart shows
}

// parse reads the command line; it says what is wrong on stderr.
func parse(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: dashboard [flags] url...")
		fs.PrintDefaults()
	}
	var c config
	fs.DurationVar(&c.every, "every", 2*time.Second, "how often to probe each endpoint")
	fs.DurationVar(&c.timeout, "timeout", 5*time.Second, "how long a probe waits for an answer")
	fs.IntVar(&c.window, "window", 60, "how many probes a chart shows, at least 2")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() == 0 || c.every <= 0 || c.timeout <= 0 || c.window < 2 {
		fs.Usage()
		return c, errors.New("bad usage")
	}
	for _, a := range fs.Args() {
		u, err := url.Parse(a)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fmt.Fprintf(stderr, "dashboard: %q is not an http or https URL\n", a)
			return c, errors.New("bad url")
		}
		c.urls = append(c.urls, a)
	}
	return c, nil
}

// endpoint is a URL the dashboard watches, and what its probes found.
type endpoint struct {
	url, name string
	// lat are the latest probes' latencies in milliseconds, oldest first,
	// at most a window's: NaN for a probe that failed, a gap in the chart.
	lat           []float64
	code          int    // the last answer's HTTP status; 0 before one
	err           string // why the last probe got no answer; "" if it got one
	probes, downs int    // the probes, and those that found it down
	busy          bool   // a probe is on its way

	// What the host's card shows, so that a delta sends only what changed.
	shown struct {
		w, h  float64 // the chart's size
		state string
		last  string
		scale string
		sel   bool
	}
	dirty bool // probed since the card was sent or changed by deltas
}

// The states of an endpoint, and their icons: a status is never told by
// its colour alone.
var icons = map[string]string{"waiting": "…", "up": "●", "down": "✕"}

func (e *endpoint) state() string {
	switch {
	case e.probes == 0:
		return "waiting"
	case e.err != "" || e.code >= 400:
		return "down"
	}
	return "up"
}

// answer is what the last probe got: a status, or why it got none.
func (e *endpoint) answer() string {
	switch {
	case e.err != "":
		return e.err
	case e.code != 0:
		return fmt.Sprintf("%d %s", e.code, http.StatusText(e.code))
	}
	return "no answer yet"
}

// last is the last probe's latency, as the card shows it.
func (e *endpoint) last() string {
	if len(e.lat) == 0 || math.IsNaN(e.lat[len(e.lat)-1]) {
		return "—"
	}
	return ms(e.lat[len(e.lat)-1])
}

// finite are the latencies of the probes that were answered.
func (e *endpoint) finite() []float64 {
	var v []float64
	for _, x := range e.lat {
		if !math.IsNaN(x) {
			v = append(v, x)
		}
	}
	return v
}

// hi is the top of the endpoint's scale: a round number above its slowest
// probe, so that the scale does not move with every probe.
func (e *endpoint) hi() float64 {
	_, top := chart.Bounds(e.finite())
	return series.Nice(top * 1.2)
}

// scale is the label of the top of the scale: none before there is a
// latency to scale.
func (e *endpoint) scale() string {
	if len(e.finite()) == 0 {
		return ""
	}
	return ms(e.hi())
}

// ms formats a latency.
func ms(v float64) string {
	switch {
	case v >= 999.5:
		return fmt.Sprintf("%.2f s", v/1000)
	case v >= 9.95:
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.1f ms", v)
}

// Messages.
type (
	tickMsg  struct{}
	probeMsg struct {
		i    int // the endpoint
		took time.Duration
		code int
		err  error
	}
)

// model is the dashboard.
type model struct {
	s      *hottytea.Session
	cfg    config
	prefix string // of the surfaces' names, unique on the terminal
	eps    []*endpoint
	client *http.Client
	light  bool // the terminal's colour scheme

	sel, top int // the endpoint selected, and the first on screen
	w, h     int // the screen, in cells
	frame    string
	problem  string // a command the host refused
}

func newModel(cfg config, prefix string) *model {
	m := &model{s: hottytea.New(), cfg: cfg, prefix: prefix, client: &http.Client{Timeout: cfg.timeout}}
	for _, a := range cfg.urls {
		u, _ := url.Parse(a)
		m.eps = append(m.eps, &endpoint{url: a, name: u.Host + strings.TrimSuffix(u.EscapedPath(), "/")})
	}
	return m
}

// name is endpoint i's surface.
func (m *model) name(i int) string { return fmt.Sprintf("%sep%d", m.prefix, i) }

// endpointOf is the endpoint whose surface is name.
func (m *model) endpointOf(name string) (int, bool) {
	for i := range m.eps {
		if m.name(i) == name {
			return i, true
		}
	}
	return 0, false
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.s.Detect(), m.probeAll(), m.tick())
}

func (m *model) tick() tea.Cmd {
	return tea.Tick(m.cfg.every, func(time.Time) tea.Msg { return tickMsg{} })
}

// probeAll probes every endpoint that is not being probed already.
func (m *model) probeAll() tea.Cmd {
	var cmds []tea.Cmd
	for i, e := range m.eps {
		if e.busy {
			continue
		}
		e.busy = true
		cmds = append(cmds, func() tea.Msg {
			start := time.Now()
			resp, err := m.client.Get(e.url)
			if err != nil {
				return probeMsg{i: i, took: time.Since(start), err: err}
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			return probeMsg{i: i, took: time.Since(start), code: resp.StatusCode}
		})
	}
	return tea.Batch(cmds...)
}

// record keeps what a probe found.
func (m *model) record(p probeMsg) {
	e := m.eps[p.i]
	e.busy = false
	e.probes++
	v := float64(p.took.Microseconds()) / 1000
	e.code, e.err = p.code, ""
	if p.err != nil {
		e.code, e.err, v = 0, reason(p.err), math.NaN()
	}
	if e.state() == "down" {
		e.downs++
	}
	e.lat = append(e.lat, v)
	if len(e.lat) > m.cfg.window {
		e.lat = slices.Delete(e.lat, 0, len(e.lat)-m.cfg.window)
	}
	e.dirty = true
}

// reason is why a probe failed, without the URL the card already shows.
func reason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return "timed out"
		}
		err = ue.Err
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		err = oe.Err
	}
	return err.Error()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg, cmd := m.s.Update(msg) // the Session's first
	cmds := []tea.Cmd{cmd}
	switch msg := msg.(type) {
	case hottytea.ReadyMsg:
		m.light = hotty.Light(msg.Caps)
	case hottytea.EventMsg:
		if i, ok := m.endpointOf(msg.Surface); ok && msg.Kind == hotty.EventPress {
			m.sel = i
		}
	case hottytea.ErrorMsg:
		m.problem = hotty.Err(msg.Reply).Error()
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		cmds = append(cmds, m.probeAll(), m.tick())
	case probeMsg:
		m.record(msg)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			m.sel = max(0, m.sel-1)
		case "down", "j":
			m.sel = min(len(m.eps)-1, m.sel+1)
		case "r":
			cmds = append(cmds, m.probeAll())
		case "q", "ctrl+c":
			return m, tea.Sequence(m.s.Close(), tea.Quit)
		}
	}
	return m, tea.Batch(append(cmds, m.draw())...)
}

func (m *model) View() tea.View {
	v := tea.NewView(m.frame)
	v.AltScreen = true
	return v
}

// The frame's rows that are not cards.
const (
	headRows = 2 // the title, and a blank row
	footRows = 2 // the selected endpoint, and the keys
)

// draw lays the frame out, and returns the commands that bring the host's
// surfaces up to date.
func (m *model) draw() tea.Cmd {
	if m.w == 0 || m.h == 0 {
		return nil
	}
	if m.s.Mode != hottytea.Native {
		m.frame = m.cells()
		return nil
	}
	rows, shown := m.layout()
	w, h := m.chartSize(rows)
	var want []hottytea.Surface
	for k := range shown {
		i := m.top + k
		want = append(want, hottytea.Surface{
			Name: m.name(i),
			Rect: hottytea.Rect{X: 0, Y: headRows + k*(rows+1), W: m.w, H: rows},
			Keep: true, Press: true,
			Doc: func() string { return m.card(i, w, h) },
		})
	}
	m.s.Layout(want)
	// The cards the host has, shown or hidden, take deltas: a hidden one
	// shows them when it is placed again.
	for i := range m.eps {
		if m.s.Has(m.name(i)) {
			if d := m.delta(i, w, h); d != "" {
				m.s.Send(d)
			}
		}
	}
	lines := make([]string, m.h)
	lines[0] = m.title()
	m.footer(lines)
	m.frame = strings.Join(lines, "\n")
	return m.s.Flush()
}

// layout is how many rows a card takes, and how many cards fit: as many
// as there are endpoints, up to 6 rows each, at least 3. It scrolls the
// selected card into view.
func (m *model) layout() (rows, shown int) {
	room := max(1, m.h-headRows-footRows)
	rows = min(max((room+1)/len(m.eps)-1, 3), 6, room)
	shown = min(len(m.eps), max(1, (room+1)/(rows+1)))
	m.top = max(0, min(max(m.top, m.sel-shown+1), m.sel, len(m.eps)-shown))
	return rows, shown
}

// chartSize is a card's chart in CSS pixels: the card less its first row,
// the header, and its 1px ring.
func (m *model) chartSize(rows int) (w, h float64) {
	cw, ch := hotty.CellCSS(m.s.Caps)
	return float64(m.w)*cw - 2, max(float64(rows-1)*ch-1, 1)
}

// The colours: the series' blue and the hairlines for each scheme, and the
// status colours, the same in both. Text keeps the terminal's colours.
const (
	blueDark, blueLight = "#3987e5", "#2a78d6"
	gridDark, gridLight = "#2c2c2a", "#e1e0d9"
	ringDark, ringLight = "rgba(255,255,255,0.10)", "rgba(11,11,11,0.10)"
	good, critical      = "#0ca30c", "#d03b3b"
	muted               = "#898781"
)

// line is endpoint e's chart in a box w×h: the newest probe at the right
// edge, each probe in the place its turn in the window gives it, so that
// the line moves along as probes come rather than stretching.
func (m *model) line(e *endpoint, w, h float64) chart.Line {
	n := m.cfg.window
	xs := make([]float64, len(e.lat))
	for j := range xs {
		xs[j] = float64(n-len(e.lat)+j) / float64(n-1)
	}
	c := chart.Line{ID: "lat", W: w, H: h, Fill: 0.12, Hi: e.hi(), Xs: xs,
		Color: blueDark, Grid: []float64{0, 0.5, 1}, GridColor: gridDark}
	if m.light {
		c.Color, c.GridColor = blueLight, gridLight
	}
	return c
}

// card is endpoint i's document, as it is now.
func (m *model) card(i int, w, h float64) string {
	e := m.eps[i]
	_, ch := hotty.CellCSS(m.s.Caps)
	ring := ringDark
	if m.light {
		ring = ringLight
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<style>%s
html, body { margin: 0; height: 100%%; }
.card { height: 100%%; box-sizing: border-box; display: grid; grid-template-rows: %.2fpx 1fr;
  font: 12px/%.2fpx var(--hotty-font); color: var(--hotty-fg);
  border-radius: 6px; box-shadow: inset 0 0 0 1px %s; overflow: hidden; }
.card.sel { box-shadow: inset 0 0 0 1px var(--hotty-fg); }
.head { display: flex; gap: 2ch; padding: 0 1ch; white-space: nowrap; overflow: hidden; }
.name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; }
.status { min-width: 9ch; }
.status i { font-style: normal; color: %s; }
.status[data-state=up] i { color: %s; }
.status[data-state=down] i { color: %s; }
.last { margin-left: auto; font-variant-numeric: tabular-nums; }
.plot { position: relative; margin: 0 1px 1px; }
.scale { position: absolute; top: 2px; left: 1ch; font-size: 10px; line-height: 1; color: %s; }
</style>`, chart.CSS, ch, ch, ring, muted, good, critical, muted)
	st := e.state()
	c := m.line(e, w, h)
	fmt.Fprintf(&b, `<div id="card" class="%s"><div class="head">`+
		`<span id="status" class="status" data-state="%s"><i id="icon">%s</i> <span id="state">%s</span></span>`+
		`<span class="name">%s</span><span id="last" class="last">%s</span></div>`+
		`<div class="plot"><div class="chart-box">%s</div><span id="scale" class="scale">%s</span></div></div>`,
		cardClass(i == m.sel), st, icons[st], st, html.EscapeString(e.name), e.last(), c.SVG(e.lat), e.scale())
	e.shown.w, e.shown.h, e.shown.state, e.shown.last, e.shown.scale, e.shown.sel = w, h, st, e.last(), e.scale(), i == m.sel
	e.dirty = false
	return b.String()
}

func cardClass(sel bool) string {
	if sel {
		return "card sel"
	}
	return "card"
}

// delta is what brings endpoint i's card up to date, as one synchronized
// update; "" when it is.
func (m *model) delta(i int, w, h float64) string {
	e, name := m.eps[i], m.name(i)
	var cmds []string
	if e.dirty || e.shown.w != w || e.shown.h != h {
		c := m.line(e, w, h)
		if e.shown.w != w || e.shown.h != h {
			cmds = append(cmds, c.Delta(name, e.lat)...) // the box, its grid and line
		} else {
			cmds = append(cmds, c.DeltaShapes(name, e.lat)...) // the line alone
		}
		if s := e.scale(); s != e.shown.scale {
			cmds = append(cmds, hotty.SetText(name, "scale", s))
			e.shown.scale = s
		}
		e.shown.w, e.shown.h, e.dirty = w, h, false
	}
	if st := e.state(); st != e.shown.state {
		cmds = append(cmds, hotty.SetAttr(name, "status", "data-state", st),
			hotty.SetText(name, "icon", icons[st]), hotty.SetText(name, "state", st))
		e.shown.state = st
	}
	if l := e.last(); l != e.shown.last {
		cmds = append(cmds, hotty.SetText(name, "last", l))
		e.shown.last = l
	}
	if sel := i == m.sel; sel != e.shown.sel {
		cmds = append(cmds, hotty.SetAttr(name, "card", "class", cardClass(sel)))
		e.shown.sel = sel
	}
	if len(cmds) == 0 {
		return ""
	}
	return hotty.Sync(cmds...)
}

// title is the frame's first row.
func (m *model) title() string {
	return fmt.Sprintf("Dashboard · %d endpoints · every %s", len(m.eps), m.cfg.every)
}

// footer fills the frame's last rows: the selected endpoint, and the keys.
func (m *model) footer(lines []string) {
	e := m.eps[m.sel]
	detail := fmt.Sprintf("%s  %s · down %d of %d", e.url, e.answer(), e.downs, e.probes)
	if v := e.finite(); len(v) > 0 {
		slices.Sort(v)
		detail += " · median " + ms(v[len(v)/2])
	}
	if m.problem != "" {
		detail = m.problem
	}
	lines[len(lines)-2] = cut(detail, m.w)
	lines[len(lines)-1] = cut("↑/↓ select · r probe now · q quit", m.w)
}

// cells is the frame on a terminal that is not a host: a line an endpoint.
func (m *model) cells() string {
	if m.s.Mode == hottytea.Detecting {
		return "Dashboard: detecting the terminal…"
	}
	nameW := 0
	for _, e := range m.eps {
		nameW = max(nameW, len([]rune(e.name)))
	}
	nameW = min(nameW, 40)
	lines := make([]string, max(m.h, len(m.eps)+headRows+footRows))
	lines[0] = m.title()
	for i, e := range m.eps {
		mark := " "
		if i == m.sel {
			mark = "›"
		}
		st := e.state()
		row := fmt.Sprintf("%s %s %-7s  %-*s  %8s  ", mark, icons[st], st, nameW, cut(e.name, nameW), e.last())
		spark := chart.Spark(e.lat, 0, e.hi(), max(0, m.w-len([]rune(row))))
		lines[headRows+i] = cut(row+spark, m.w)
	}
	m.footer(lines)
	return strings.Join(lines, "\n")
}

// cut cuts s to n cells, as near as runes count them.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:max(n, 0)])
	}
	return s
}
