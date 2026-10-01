package main

import (
	"context"
	"strings"
	"testing"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/term"
)

func opener(h *hottytest.Host) func() (*term.Term, error) {
	return func() (*term.Term, error) { return term.New(h, h, "hello", nil, nil), nil }
}

func TestHost(t *testing.T) {
	h := hottytest.New(t)
	var out, errs strings.Builder
	if code := run(context.Background(), &out, &errs, opener(h)); code != 0 || errs.Len() > 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	s := h.Surface("hello-hello")
	if s == nil || !s.Detached() || !strings.Contains(s.Text(), "Hello, HTML in the terminal.") {
		t.Fatalf("surfaces: %v", h.Surfaces())
	}
	if out.Len() > 0 {
		t.Errorf("stdout: %q", out.String())
	}
}

func TestText(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	var out, errs strings.Builder
	if code := run(context.Background(), &out, &errs, opener(h)); code != 0 || !strings.Contains(out.String(), "not a HOTTY host") {
		t.Errorf("exit %d: %q", code, out.String())
	}
}

func TestNoTerminal(t *testing.T) {
	var out, errs strings.Builder
	open := func() (*term.Term, error) { return nil, term.ErrNoTerminal }
	if code := run(context.Background(), &out, &errs, open); code != 0 || out.String() != "Hello.\n" {
		t.Errorf("exit %d: %q", code, out.String())
	}
}

// A host that holds no more surfaces refuses the document, and says so.
func TestHostRefuses(t *testing.T) {
	h := hottytest.New(t, hottytest.Caps(hotty.Caps{Limits: map[string]int{"surfaces": 1}}))
	_, _ = h.Write([]byte(hotty.Doc("another-program", "<p>full</p>")))
	var out, errs strings.Builder
	if code := run(context.Background(), &out, &errs, opener(h)); code != 1 || !strings.Contains(errs.String(), "EQUOTA") {
		t.Errorf("exit %d: %q", code, errs.String())
	}
}
