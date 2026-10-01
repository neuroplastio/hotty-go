// Progress shows a task's progress as a bar that moves in place, and leaves
// its last state in the scrollback.
//
// It shows the cheap way to change a surface many times a second: a custom
// property moves the bar (hotty.SetVar) and text patches change the labels
// (hotty.SetText), a few dozen bytes each, in synchronized output so the
// host shows them together (SPEC §6). Where the terminal is not a HOTTY
// host, the line is redrawn with a carriage return; into a pipe, a line per
// step.
//
//	progress -files 40 -delay 50ms
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/term"
)

// env is the process's streams and terminal: main fills it from the OS,
// a test from hottytest.
type env struct {
	stdout, stderr io.Writer
	tty            bool                       // stdout is the terminal
	open           func() (*term.Term, error) // the terminal, whatever the streams
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	e := env{stdout: os.Stdout, stderr: os.Stderr, tty: term.IsTerminal(os.Stdout),
		open: func() (*term.Term, error) { return term.Open("progress") }}
	os.Exit(run(ctx, os.Args[1:], e))
}

// view is how a rendition shows the work: each step, then the end.
type view interface {
	step(done, total int, name string)
	end(done, total int, took time.Duration, cancelled bool)
}

func run(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("progress", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	files := fs.Int("files", 20, "how many files to copy")
	delay := fs.Duration("delay", 80*time.Millisecond, "how long each takes")
	if fs.Parse(args) != nil || *files < 1 {
		return 2
	}

	var v view = lines{e.stdout}
	if e.tty {
		if t, err := e.open(); err == nil {
			defer t.Close()
			if t.Detect(ctx) {
				s, err := newBar(ctx, t)
				if err != nil {
					fmt.Fprintln(e.stderr, "progress:", err)
					return 1
				}
				v = s
			} else {
				v = cells{e.stdout}
			}
		}
	}

	start := time.Now()
	done := 0
	for i := range *files {
		select {
		case <-ctx.Done():
			v.end(done, *files, time.Since(start), true)
			return 130
		case <-time.After(*delay):
		}
		done++
		v.step(done, *files, fmt.Sprintf("file-%02d.dat", i+1))
	}
	v.end(done, *files, time.Since(start), false)
	return 0
}

// bar is the surface: placed once, then patched.
type bar struct {
	ctx  context.Context
	t    *term.Term
	name string
}

const page = `<style>
  .row   { display: flex; align-items: center; gap: 12px; height: 100vh; font: 13px system-ui, sans-serif; }
  .track { flex: 1; height: 8px; border-radius: 4px; background: var(--hotty-ansi-8); overflow: hidden; }
  .fill  { width: calc(var(--p, 0) * 1%); height: 100%; border-radius: 4px; background: var(--hotty-ansi-4); }
  .done .fill { background: var(--hotty-ansi-2); }
  .stopped .fill { background: var(--hotty-ansi-3); }
  #pct   { width: 4ch; text-align: right; font-variant-numeric: tabular-nums; }
  #label { width: 24ch; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
<div class="row" id="row"><span id="label">starting</span><div class="track"><div class="fill" id="fill"></div></div><span id="pct">0%</span></div>`

func newBar(ctx context.Context, t *term.Term) (*bar, error) {
	if err := t.LineStart(ctx); err != nil {
		return nil, err
	}
	// Detached from the start: the program reads nothing from it, and it
	// stays in the scrollback after the program exits (SPEC §5.5).
	name, err := t.Print("bar", page, hotty.Placement{Cols: min(72, t.Size().Cols), Rows: 1})
	if err != nil {
		return nil, err
	}
	return &bar{ctx: ctx, t: t, name: name}, nil
}

func (b *bar) step(done, total int, name string) {
	pct := strconv.Itoa(done * 100 / total)
	_ = b.t.Send(hotty.Sync(
		hotty.SetVar(b.name, "fill", "p", pct),
		hotty.SetText(b.name, "label", name),
		hotty.SetText(b.name, "pct", pct+"%"),
	))
}

func (b *bar) end(done, total int, took time.Duration, cancelled bool) {
	label, class := fmt.Sprintf("%d files in %s", total, took.Round(time.Millisecond)), "row done"
	if cancelled {
		label, class = fmt.Sprintf("stopped after %d of %d", done, total), "row stopped"
	}
	_ = b.t.Send(hotty.Sync(hotty.SetText(b.name, "label", label), hotty.SetAttr(b.name, "row", "class", class)))
	// Read the host's replies before the shell does. A cancelled run
	// still waits a moment for them.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(b.ctx), time.Second)
	defer cancel()
	_, _ = b.t.Fence(ctx)
}

// cells is the line redrawn in place.
type cells struct{ w io.Writer }

func (c cells) step(done, total int, name string) {
	const width = 30
	n := done * width / total
	fmt.Fprintf(c.w, "\r\x1b[K[%s%s] %3d%% %s", strings.Repeat("#", n), strings.Repeat(".", width-n), done*100/total, name)
}

func (c cells) end(done, total int, took time.Duration, cancelled bool) {
	if cancelled {
		fmt.Fprintf(c.w, "\r\x1b[K! stopped after %d of %d\n", done, total)
		return
	}
	fmt.Fprintf(c.w, "\r\x1b[K✓ %d files in %s\n", total, took.Round(time.Millisecond))
}

// lines is a line a step, for a pipe.
type lines struct{ w io.Writer }

func (l lines) step(_, _ int, name string) { fmt.Fprintln(l.w, name) }

func (l lines) end(done, total int, _ time.Duration, cancelled bool) {
	if cancelled {
		fmt.Fprintf(l.w, "stopped after %d of %d\n", done, total)
	}
}
