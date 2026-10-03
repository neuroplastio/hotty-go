package main

import (
	"context"
	"strings"
	"testing"

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
		case cmd.Get("a") == "delta" && cmd.Get("t") == "vt-r7":
			lint = append(lint, cmd.Get("op"))
		case cmd.Get("a") == "delta" && cmd.Get("t") == "title":
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
	if code := run(context.Background(), []string{"-cols", "0"}, env{stdout: &out, stderr: &out}); code != 2 {
		t.Fatalf("exit %d", code)
	}
}
