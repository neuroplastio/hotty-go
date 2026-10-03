// Replay plays a terminal session back on a surface, at the size it was
// recorded and scaled to fit, and leaves its last frame in the scrollback.
//
// It shows a terminal inside a document: hottyvt keeps a terminal emulator
// of the session's size, apart from the terminal the program runs in, and
// sends its screen as HTML, then a delta per row that changes, in
// synchronized output so the host shows each frame whole (SPEC §6). The
// screen takes the terminal's font and colours from the host stylesheet
// (SPEC §8), and --vt-scale fits its 64 columns into the placement's.
// Where the terminal is not a host, the session plays in the terminal
// itself; into a pipe, the last frame as text.
//
//	replay -cols 48 -speed 2
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyterm"
	"github.com/neuroplastio/hotty-go/hottyvt"
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

// The session's size, as it was recorded.
const sessionCols, sessionRows = 64, 12

// event is what the session wrote, after a pause.
type event struct {
	after time.Duration
	out   string
}

// session is a recorded session: a command typed, its output, and a
// progress line that rewrites itself in place.
func session() []event {
	var ev []event
	ev = append(ev, event{0, "\x1b[1;32m~/acme\x1b[0m $ "})
	for _, r := range "make test" {
		ev = append(ev, event{70 * time.Millisecond, string(r)})
	}
	ev = append(ev, event{300 * time.Millisecond, "\r\ngo test ./...\r\n"})
	for _, p := range []struct{ pkg, took string }{
		{"acme/api", "0.412s"}, {"acme/store", "1.031s"}, {"acme/auth", "0.088s"},
	} {
		ev = append(ev, event{250 * time.Millisecond, fmt.Sprintf("\x1b[32mok\x1b[0m   %-14s %s\r\n", p.pkg, p.took)})
	}
	ev = append(ev, event{250 * time.Millisecond, "\x1b[1;31mFAIL\x1b[0m acme/web       0.207s\r\n"})
	for i := 0; i <= 10; i++ {
		bar := strings.Repeat("█", i*2) + strings.Repeat("░", 20-i*2)
		ev = append(ev, event{90 * time.Millisecond, fmt.Sprintf("\rlint   \x1b[36m%s\x1b[0m %3d%%", bar, i*10)})
	}
	ev = append(ev,
		event{200 * time.Millisecond, "\r\n\x1b[2mmake: *** [test] Error 1\x1b[0m\r\n"},
		event{300 * time.Millisecond, "\x1b[1;32m~/acme\x1b[0m $ "})
	return ev
}

// view is how a rendition shows the session: each frame, then the end.
type view interface {
	frame(out string)
	end()
}

func run(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	cols := fs.Int("cols", sessionCols, "the placement's width in columns; the session is scaled to it")
	speed := fs.Float64("speed", 1, "how much faster than recorded to play; 0 for no pauses")
	if fs.Parse(args) != nil || *cols < 1 || *speed < 0 {
		return 2
	}

	s := hottyvt.New(sessionCols, sessionRows)
	defer s.Close()
	var v view = text{e.stdout, s}
	if e.tty {
		if t, err := e.open(); err == nil {
			defer t.Close()
			if t.Detect(ctx) {
				p, err := newPlayer(ctx, t, s, min(*cols, t.Size().Cols))
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

	for _, ev := range session() {
		if *speed > 0 && ev.after > 0 {
			select {
			case <-ctx.Done():
				v.end()
				return 130
			case <-time.After(time.Duration(float64(ev.after) / *speed)):
			}
		}
		_, _ = s.WriteString(ev.out)
		v.frame(ev.out)
	}
	v.end()
	return 0
}

// player is the surface: the screen's element, placed once, then its rows
// changed by deltas.
type player struct {
	ctx  context.Context
	t    *hottyterm.Term
	s    *hottyvt.Screen
	name string
}

// frame is the box the screen is shown in, as a screencast on a page
// would be: rounded, a shade off the terminal's background, with a cell of
// padding at each side.
const frame = `<style>` + hottyvt.CSS + `
  .cast { padding: 0.5rlh var(--hotty-cell-w); border-radius: 8px; width: max-content;
          background: color-mix(in srgb, var(--hotty-bg) 88%, var(--hotty-fg)); }
</style>`

func newPlayer(ctx context.Context, t *hottyterm.Term, s *hottyvt.Screen, cols int) (*player, error) {
	if err := t.LineStart(ctx); err != nil {
		return nil, err
	}
	// The screen's 64 columns, and the frame's two of padding, in cols.
	scale := float64(cols-2) / sessionCols
	if scale > 1 {
		scale = 1
	}
	_ = s.SetScale("", scale)
	name, err := t.Print("screen", frame+`<div class="cast">`+s.HTML()+`</div>`, hotty.Placement{Cols: cols})
	if err != nil {
		return nil, err
	}
	return &player{ctx: ctx, t: t, s: s, name: name}, nil
}

func (p *player) frame(string) {
	if d := p.s.Delta(p.name); len(d) > 0 {
		_ = p.t.Send(hotty.Sync(d...))
	}
}

func (p *player) end() {
	// Read the host's replies before the shell does.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), time.Second)
	defer cancel()
	_, _ = p.t.Fence(ctx)
}

// cells plays the session in the terminal itself: it is one.
type cells struct{ w io.Writer }

func (c cells) frame(out string) { _, _ = io.WriteString(c.w, out) }
func (c cells) end()             { _, _ = io.WriteString(c.w, "\r\n") }

// text is the last frame, for a pipe.
type text struct {
	w io.Writer
	s *hottyvt.Screen
}

func (text) frame(string) {}
func (t text) end()       { fmt.Fprintln(t.w, t.s.Text()) }
