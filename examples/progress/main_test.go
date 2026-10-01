package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/term"
)

func on(h *hottytest.Host) (env, *strings.Builder) {
	var out strings.Builder
	return env{stdout: h, stderr: &out, tty: true,
		open: func() (*term.Term, error) { return term.New(h, h, "progress", nil, nil), nil }}, &out
}

func TestBar(t *testing.T) {
	h := hottytest.New(t)
	e, errs := on(h)
	if code := run(context.Background(), []string{"-files", "8", "-delay", "0"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	s := h.Surface("progress-bar")
	if s == nil || !s.Detached() {
		t.Fatalf("surfaces %v", h.Surfaces())
	}
	if p, _ := s.Var("fill", "p"); p != "100" || s.TextOf("pct") != "100%" || !strings.HasPrefix(s.TextOf("label"), "8 files in ") {
		t.Errorf("the bar at the end: --p %q, %q, %q", p, s.TextOf("pct"), s.TextOf("label"))
	}
	if class, _ := s.Attr("row", "class"); class != "row done" {
		t.Errorf("class %q", class)
	}
	// Each step is one synchronized batch of three small patches.
	var steps int
	for _, cmd := range h.Commands() {
		if cmd.Get("op") == "var" {
			steps++
		}
	}
	out := h.Output()
	if steps != 8 || strings.Count(out, "\x1b[?2026h") != 9 {
		t.Errorf("%d var patches, %d batches", steps, strings.Count(out, "\x1b[?2026h"))
	}
	first := out[strings.Index(out, "\x1b[?2026h"):]
	first = first[:strings.Index(first, "\x1b[?2026l")]
	if len(first) > 200 {
		t.Errorf("a step is %d bytes", len(first))
	}
}

func TestBarStopped(t *testing.T) {
	h := hottytest.New(t)
	e, _ := on(h)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for h.Surface("progress-bar") == nil || h.Surface("progress-bar").TextOf("pct") != "50%" {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if code := run(ctx, []string{"-files", "4", "-delay", "20ms"}, e); code != 130 {
		t.Fatalf("exit %d", code)
	}
	s := h.Surface("progress-bar")
	if got := s.TextOf("label"); !strings.HasPrefix(got, "stopped after ") {
		t.Errorf("label %q", got)
	}
}

func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	e, _ := on(h)
	if code := run(context.Background(), []string{"-files", "3", "-delay", "0"}, e); code != 0 {
		t.Fatalf("exit %d", code)
	}
	// The line was redrawn in place: what is left is the end.
	if got := h.Screen(); !strings.HasPrefix(got, "✓ 3 files in ") || strings.Contains(got, "\n") {
		t.Errorf("screen %q", got)
	}
	if !strings.Contains(h.Output(), "[####################..........]  66% file-02.dat") {
		t.Errorf("a step in cells: %q", h.Output())
	}
}

func TestPipe(t *testing.T) {
	var out strings.Builder
	e := env{stdout: &out, stderr: &out}
	if code := run(context.Background(), []string{"-files", "2", "-delay", "0"}, e); code != 0 || out.String() != "file-01.dat\nfile-02.dat\n" {
		t.Errorf("exit %d: %q", code, out.String())
	}
	out.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, []string{"-files", "2"}, e); code != 130 || out.String() != "stopped after 0 of 2\n" {
		t.Errorf("cancelled: %d %q", code, out.String())
	}
}

func TestUsage(t *testing.T) {
	var out strings.Builder
	if code := run(context.Background(), []string{"-files", "0"}, env{stdout: &out, stderr: &out}); code != 2 {
		t.Errorf("exit %d", code)
	}
}
