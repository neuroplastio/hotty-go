// Replay plays a terminal session back on a surface, at the size it was
// recorded and scaled to fit, and leaves its last frame in the scrollback.
//
// It shows a terminal inside a document: hottyvt keeps a terminal emulator
// of the session's size, apart from the terminal the program runs in, and
// sends its screen as HTML, then a delta per row that changes, in
// synchronized output so the host shows each frame whole (SPEC §6). The
// screen takes the terminal's font and colours from the host stylesheet
// (SPEC §8), and --vt-scale fits its 54 columns into the window, whose
// title bar shows the title the session gives its window (OSC 2).
// Where the terminal is not a host, the session plays in the terminal
// itself; into a pipe, the last frame as text.
//
// With -cast it plays a recording instead, asciicast version 2 or 3
// (hottyvt/asciicast), at the size it was recorded, and its markers name
// the window as they pass, a caption each.
//
//	replay -cols 48 -speed 2
//	replay -cast screencast.cast
package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyterm"
	"github.com/neuroplastio/hotty-go/hottyvt"
	"github.com/neuroplastio/hotty-go/hottyvt/asciicast"
)

// env is the process's streams and terminal: main fills it from the OS,
// a test from hottytest.
type env struct {
	stdout, stderr io.Writer
	tty            bool                            // stdout is the terminal
	open           func() (*hottyterm.Term, error) // the terminal, whatever the streams
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	e := env{stdout: os.Stdout, stderr: os.Stderr, tty: hottyterm.IsTerminal(os.Stdout),
		open: func() (*hottyterm.Term, error) { return hottyterm.Open("replay") }}
	os.Exit(run(ctx, os.Args[1:], e))
}

// The built-in session's size, as it was recorded.
const sessionCols, sessionRows = 54, 11

// event is what the session wrote, after a pause, or a caption its
// recording marked there.
type event struct {
	after   time.Duration
	out     string
	caption string
}

// recording is a session to play: its terminal's size, its title, and what
// happened in it.
type recording struct {
	cols, rows int
	title      string
	events     []event
}

// load reads a recording from an asciicast file. Its pauses are cut to its
// idle time limit, as a player shows them; what it typed, and its resizes,
// are not played: the screen keeps the size the recording began at.
func load(path string) (recording, error) {
	f, err := os.Open(path)
	if err != nil {
		return recording{}, err
	}
	defer f.Close()
	c, err := asciicast.Decode(f)
	if err != nil {
		return recording{}, err
	}
	rec := recording{cols: c.Cols, rows: c.Rows, title: c.Title}
	var at time.Duration
	for _, ev := range c.CapIdle(c.IdleTimeLimit).Events {
		switch ev.Code {
		case asciicast.Output:
			rec.events = append(rec.events, event{after: ev.Time - at, out: ev.Data})
		case asciicast.Marker:
			rec.events = append(rec.events, event{after: ev.Time - at, caption: ev.Data})
		default:
			continue
		}
		at = ev.Time
	}
	return rec, nil
}

// title is the sequence that names the terminal's window (OSC 2), as a
// shell does with the command it runs.
func title(t string) string { return "\x1b]2;" + t + "\x07" }

// session is a recorded session: a command typed, a spinner while it
// works, its results, and a progress bar that rewrites its row in place.
func session() []event {
	const prompt = "\x1b[36m~/acme\x1b[0m \x1b[35mmain\x1b[0m \x1b[1;32m❯\x1b[0m "
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	ev := []event{{out: title("~/acme")}, {out: prompt}}
	for _, r := range "make test" {
		ev = append(ev, event{after: ms(65), out: string(r)})
	}
	ev = append(ev, event{after: ms(350), out: title("make test — ~/acme")}, event{out: "\r\n"})
	spin := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	for i := range 12 {
		ev = append(ev, event{after: ms(80), out: fmt.Sprintf("\r\x1b[36m%c\x1b[0m testing 4 packages\x1b[2m…\x1b[0m", spin[i%len(spin)])})
	}
	ev = append(ev, event{out: "\r\x1b[K"})
	for _, p := range []struct{ pkg, took string }{
		{"acme/api", "0.41s"}, {"acme/store", "1.03s"}, {"acme/auth", "0.09s"},
	} {
		ev = append(ev, event{after: ms(160), out: fmt.Sprintf("\x1b[32m✓\x1b[0m %-12s \x1b[2m%s\x1b[0m\r\n", p.pkg, p.took)})
	}
	ev = append(ev,
		event{after: ms(160), out: "\x1b[31m✗\x1b[0m acme/web     \x1b[2m0.21s\x1b[0m\r\n"},
		event{after: ms(60), out: "  \x1b[2mweb_test.go:42\x1b[0m expected \x1b[1m200\x1b[0m, got \x1b[1;31m500\x1b[0m\r\n\r\n"})
	const width = 24
	for i := 0; i <= width; i += 2 {
		bar := "\x1b[35m" + strings.Repeat("━", i) + "\x1b[0m\x1b[2m" + strings.Repeat("━", width-i) + "\x1b[0m"
		ev = append(ev, event{after: ms(70), out: fmt.Sprintf("\r\x1b[36m%c\x1b[0m lint %s \x1b[2m%3d%%\x1b[0m", spin[i/2%len(spin)], bar, i*100/width)})
	}
	ev = append(ev,
		event{after: ms(120), out: "\r\x1b[32m✓\x1b[0m lint \x1b[32m" + strings.Repeat("━", width) + "\x1b[0m \x1b[2m100%\x1b[0m"},
		event{after: ms(250), out: "\r\n\r\n\x1b[1;32m3 passed\x1b[0m \x1b[2m·\x1b[0m \x1b[1;31m1 failed\x1b[0m \x1b[2m· 1.8s\x1b[0m\r\n"},
		event{after: ms(300), out: title("~/acme")}, event{out: prompt})
	return ev
}

// view is how a rendition shows the session: each frame, then the end.
type view interface {
	frame(ev event)
	end()
}

func run(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	cols := fs.Int("cols", 0, "the placement's width in columns; the window is scaled to it (default: the session's, with the window around it)")
	speed := fs.Float64("speed", 1, "how much faster than recorded to play; 0 for no pauses")
	cast := fs.String("cast", "", "a recording to play instead, asciicast version 2 or 3")
	if fs.Parse(args) != nil || *cols < 0 || *speed < 0 {
		return 2
	}
	rec := recording{cols: sessionCols, rows: sessionRows, events: session()}
	if *cast != "" {
		var err error
		if rec, err = load(*cast); err != nil {
			fmt.Fprintln(e.stderr, "replay:", err)
			return 1
		}
	}
	if *cols == 0 {
		*cols = rec.cols + paddingCols + marginCols
	}

	s := hottyvt.New(rec.cols, rec.rows)
	defer s.Close()
	var v view = text{e.stdout, s}
	if e.tty {
		if t, err := e.open(); err == nil {
			defer t.Close()
			if t.Detect(ctx) {
				p, err := newPlayer(ctx, t, s, rec, min(*cols, t.Size().Cols))
				if err != nil {
					fmt.Fprintln(e.stderr, "replay:", err)
					return 1
				}
				v = p
			} else {
				v = cells{e.stdout}
			}
		}
	}

	for _, ev := range rec.events {
		if *speed > 0 && ev.after > 0 {
			select {
			case <-ctx.Done():
				v.end()
				return 130
			case <-time.After(time.Duration(float64(ev.after) / *speed)):
			}
		}
		_, _ = s.WriteString(ev.out)
		v.frame(ev)
	}
	v.end()
	return 0
}

// player is the surface: a window with the screen in it, placed once,
// then its rows and its title changed by deltas. The title is the last
// caption, if the recording has captions; else the one the session gives
// its window, or the recording's own.
type player struct {
	ctx     context.Context
	t       *hottyterm.Term
	s       *hottyvt.Screen
	name    string
	fixed   string // the recording's title
	caption string // the last caption
	title   string // the title shown
}

func (p *player) titleNow() string {
	switch {
	case p.caption != "":
		return p.caption
	case p.s.Title() != "":
		return p.s.Title()
	}
	return p.fixed
}

// window is a terminal's window around the screen, as a screencast on a
// page shows one: rounded, lifted off the page by a shadow, a title bar
// with the program's title. It takes the terminal's colours, so it suits a
// dark theme and a light one, and it is sized in em of its font, which is
// the terminal's scaled as the screen is (--s), so a window scaled down to
// fit is the same window, smaller.
const window = `<style>` + hottyvt.CSS + `
  .win   { --s: 1; margin: 0.5rlh var(--hotty-cell-w) 1rlh; width: max-content;
           font-size: calc(var(--s) * 1rem); border-radius: 0.7em; overflow: hidden;
           background: var(--hotty-bg);
           box-shadow: 0 0 0 1px color-mix(in srgb, var(--hotty-fg) 16%, transparent),
                       0 0.7em 1.8em rgba(0, 0, 0, 0.45); }
  .bar   { position: relative; display: flex; align-items: center; height: 2em; padding: 0 0.8em;
           background: color-mix(in srgb, var(--hotty-bg) 84%, var(--hotty-fg));
           border-bottom: 1px solid color-mix(in srgb, var(--hotty-fg) 10%, transparent); }
  .dots  { display: flex; gap: 0.5em; }
  .dots i { display: block; width: 0.8em; height: 0.8em; border-radius: 50%; }
  .title { position: absolute; left: 0; right: 0; text-align: center;
           font: 500 0.8em system-ui, sans-serif; color: var(--hotty-fg); opacity: 0.7; }
  .body  { padding: calc(var(--s) * 0.75 * var(--hotty-cell-h)) calc(var(--s) * var(--hotty-cell-w)); }
</style>`

// The columns the window takes beside the screen's: a cell of margin at
// each side, and a cell of padding at each side, scaled with the screen.
const marginCols, paddingCols = 2, 2

func newPlayer(ctx context.Context, t *hottyterm.Term, s *hottyvt.Screen, rec recording, cols int) (*player, error) {
	if err := t.LineStart(ctx); err != nil {
		return nil, err
	}
	p := &player{ctx: ctx, t: t, s: s, fixed: rec.title}
	p.title = p.titleNow()
	scale := min(1, float64(cols-marginCols)/float64(rec.cols+paddingCols))
	_ = s.SetScale("", scale)
	doc := window + `<div class="win" style="--s:` + strconv.FormatFloat(scale, 'f', 3, 64) + `"><div class="bar"><div class="dots">` +
		`<i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i></div>` +
		`<div class="title" id="title">` + html.EscapeString(p.title) + `</div></div>` +
		`<div class="body">` + s.HTML() + `</div></div>`
	name, err := t.Print("screen", doc, hotty.Placement{Cols: cols})
	if err != nil {
		return nil, err
	}
	p.name = name
	return p, nil
}

// frame sends the rows that changed, and the title if it did, as one frame.
func (p *player) frame(ev event) {
	if ev.caption != "" {
		p.caption = ev.caption
	}
	d := p.s.Delta(p.name)
	if t := p.titleNow(); t != p.title {
		p.title = t
		d = append(d, hotty.SetText(p.name, "title", t))
	}
	if len(d) > 0 {
		_ = p.t.Send(hotty.Sync(d...))
	}
}

func (p *player) end() {
	// Read the host's replies before the shell does.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), time.Second)
	defer cancel()
	_, _ = p.t.Fence(ctx)
}

// cells plays the session in the terminal itself: it is one. The session's
// titles stay out of it, so the terminal keeps its own.
type cells struct{ w io.Writer }

func (c cells) frame(ev event) {
	if !strings.HasPrefix(ev.out, "\x1b]2;") {
		_, _ = io.WriteString(c.w, ev.out)
	}
}

func (c cells) end() { _, _ = io.WriteString(c.w, "\r\n") }

// text is the last frame, for a pipe.
type text struct {
	w io.Writer
	s *hottyvt.Screen
}

func (text) frame(event) {}
func (t text) end()      { fmt.Fprintln(t.w, t.s.Text()) }
