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
const last = `~/acme $ make test
go test ./...
ok   acme/api       0.412s
ok   acme/store     1.031s
ok   acme/auth      0.088s
FAIL acme/web       0.207s
lint   ████████████████████ 100%
make: *** [test] Error 1
~/acme $`

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
	if got := s.TextOf("vt-r6"); got != "lint   ████████████████████ 100%" {
		t.Errorf("the progress row at the end: %q", got)
	}
	// Scaled to the placement: 32 of its columns hold the session's 64.
	if got := s.HTML(); !strings.Contains(got, "--vt-scale:0.5") {
		t.Errorf("not scaled: %s", got)
	}
	// Each frame went as deltas of the rows it changed: row 6 changed when
	// the cursor came to it, at each of the progress line's eleven
	// rewrites, and when the cursor left it, a command each.
	var rows6 int
	for _, cmd := range h.Commands() {
		if cmd.Get("a") == "delta" && cmd.Get("t") == "vt-r6" {
			rows6++
		}
	}
	if rows6 != 13 {
		t.Errorf("%d deltas to row 6, want 13", rows6)
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
