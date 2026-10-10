package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyterm"
	"github.com/neuroplastio/hotty-go/hottytest"
)

func on(h *hottytest.Host) (env, *strings.Builder) {
	var out strings.Builder
	return env{stdout: h, stderr: &out, tty: true,
		open: func() (*hottyterm.Term, error) { return hottyterm.New(h, h, "replay", h.TermSize, nil), nil }}, &out
}

// The last frame of the session, as text.
const last = `~/acme main ❯ make test
✓ acme/api     0.41s
✓ acme/store   1.03s
✓ acme/auth    0.09s
✗ acme/web     0.21s
  web_test.go:42 expected 200, got 500

✓ lint ━━━━━━━━━━━━━━━━━━━━━━━━ 100%

3 passed · 1 failed · 1.8s
~/acme main ❯`

func TestSurface(t *testing.T) {
	h := hottytest.New(t)
	e, errs := on(h)
	if code := run(context.Background(), []string{"-cols", "34", "-speed", "0"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	s := h.Surface("replay-screen")
	if s == nil || !s.Detached() || s.Placement().Cols != 34 {
		t.Fatalf("surfaces %v", h.Surfaces())
	}
	if got := s.TextOf("vt-r7"); got != "✓ lint ━━━━━━━━━━━━━━━━━━━━━━━━ 100%" {
		t.Errorf("the progress row at the end: %q", got)
	}
	// Scaled to the placement: 32 of its columns, past the margins, hold
	// the session's 54 and the padding's 2; the window with it.
	if got := s.HTML(); !strings.Contains(got, "--vt-scale:0.571") || !strings.Contains(got, `style="--s:0.571"`) {
		t.Errorf("not scaled: %s", got)
	}
	// Each frame went as deltas of what it changed: the progress row at
	// each of its thirteen steps, and the title each time the session
	// named its window.
	var lint []string
	var titles []string
	for _, cmd := range h.Commands() {
		switch {
		case hotty.Get(cmd.Control, "a") == "delta" && hotty.Get(cmd.Control, "t") == "vt-r7":
			lint = append(lint, hotty.Get(cmd.Control, "op"))
		case hotty.Get(cmd.Control, "a") == "delta" && hotty.Get(cmd.Control, "t") == "title":
			titles = append(titles, string(cmd.Payload))
		}
	}
	if len(lint) < 13 {
		t.Errorf("%d deltas to the progress row, want at least 13", len(lint))
	}
	if want := []string{"~/acme", "make test — ~/acme", "~/acme"}; strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Errorf("titles %q, want %q", titles, want)
	}
	if got := s.TextOf("title"); got != "~/acme" {
		t.Errorf("title at the end %q", got)
	}
}

func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	e, _ := on(h)
	if code := run(context.Background(), []string{"-speed", "0"}, e); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := h.Screen(); !strings.HasPrefix(got, last) {
		t.Errorf("screen\n%s", got)
	}
}

func TestPipe(t *testing.T) {
	var out, errs strings.Builder
	e := env{stdout: &out, stderr: &errs}
	if code := run(context.Background(), []string{"-speed", "0"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if got := out.String(); got != last+"\n" {
		t.Errorf("got\n%s", got)
	}
}

func TestStopped(t *testing.T) {
	var out strings.Builder
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, nil, env{stdout: &out, stderr: &out}); code != 130 {
		t.Fatalf("exit %d", code)
	}
}

func TestFlags(t *testing.T) {
	var out strings.Builder
	if code := run(context.Background(), []string{"-cols", "-1"}, env{stdout: &out, stderr: &out}); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

// A recording: its markers caption the window, and its long pause is cut
// to its idle time limit.
const cast = `{"version": 3, "term": {"cols": 20, "rows": 3, "type": "xterm-256color"}, "title": "demo", "idle_time_limit": 0.5}
[0.0, "o", "$ "]
[0.1, "m", "Type a command"]
[0.2, "o", "make\r\nok"]
[5.0, "m", "Done"]
[0.1, "i", "q"]
`

func castFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "demo.cast")
	if err := os.WriteFile(path, []byte(cast), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCast(t *testing.T) {
	h := hottytest.New(t)
	e, errs := on(h)
	if code := run(context.Background(), []string{"-cast", castFile(t), "-speed", "0"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	s := h.Surface("replay-screen")
	if s == nil || s.Placement().Cols != 24 {
		t.Fatalf("surfaces %v: want one placed 20 columns wide and the window's 4", h.Surfaces())
	}
	// The cursor's cell, after "ok", is a space.
	if got := s.TextOf("vt-r0") + "|" + s.TextOf("vt-r1"); got != "$ make|ok " {
		t.Errorf("rows %q", got)
	}
	var titles []string
	for _, cmd := range h.Commands() {
		if hotty.Get(cmd.Control, "a") == "delta" && hotty.Get(cmd.Control, "t") == "title" {
			titles = append(titles, string(cmd.Payload))
		}
	}
	if want := []string{"Type a command", "Done"}; strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Errorf("titles %q, want %q after the recording's own", titles, want)
	}
	if got := s.TextOf("title"); got != "Done" {
		t.Errorf("title at the end %q", got)
	}
}

func TestLoad(t *testing.T) {
	rec, err := load(castFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if rec.cols != 20 || rec.rows != 3 || rec.title != "demo" || len(rec.events) != 4 {
		t.Fatalf("recording %+v", rec)
	}
	// The 4.8s before "Done" is cut to the limit; what was typed is not played.
	if ev := rec.events[3]; ev.caption != "Done" || ev.after != 500*time.Millisecond {
		t.Errorf("last event %+v", ev)
	}
}

func TestCastNotThere(t *testing.T) {
	var out strings.Builder
	code := run(context.Background(), []string{"-cast", filepath.Join(t.TempDir(), "none.cast")}, env{stdout: &out, stderr: &out})
	if code != 1 || !strings.HasPrefix(out.String(), "replay: ") {
		t.Fatalf("exit %d: %q", code, out.String())
	}
}
