package main

import (
	"context"
	"strings"
	"testing"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/term"
)

var args = []string{"api", "v1.4.2", "ok", "42s"}

// on runs the program with stdout on a terminal played by h.
func on(t *testing.T, h *hottytest.Host, args []string) (code int, stdout string) {
	t.Helper()
	var out, errs strings.Builder
	e := env{stdout: &out, stderr: &errs, tty: true,
		open: func() (*term.Term, error) { return term.New(h, h, "card", h.TermSize, nil), nil }}
	code = run(context.Background(), args, e)
	// What the program prints to stdout reaches the same terminal.
	_, _ = h.Write([]byte(out.String()))
	if errs.Len() > 0 {
		t.Logf("stderr: %s", errs.String())
	}
	return code, out.String()
}

func TestSurfaces(t *testing.T) {
	h := hottytest.New(t)
	if code, out := on(t, h, args); code != 0 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	s := h.Surface("card-card")
	if s == nil || !s.Detached() || !s.Placed() {
		t.Fatalf("surfaces %v", h.Surfaces())
	}
	if got := s.Text(); got != "✓ api v1.4.2 logs deployed in 42s" {
		t.Errorf("the card reads %q", got)
	}
	// Nothing in it reports: the link is a hyperlink, which the terminal
	// opens itself.
	if err := h.Click("card-card", "logs"); err != nil {
		t.Fatal(err)
	}
	if o := h.Opened(); len(o) != 1 || o[0] != "https://ci.example.com/deploys/api/v1.4.2" {
		t.Errorf("opened %v", o)
	}
	if evs := h.Events(); len(evs) > 0 {
		t.Errorf("the program heard of the click: %+v", evs)
	}
	if p := s.Placement(); p.Cols != 60 || p.Rows != 4 {
		t.Errorf("placement %+v", p)
	}
}

func TestEscapes(t *testing.T) {
	h := hottytest.New(t)
	on(t, h, []string{`<script>x</script>`, `"><b>`, "failed", "1s"})
	s := h.Surface("card-card")
	if strings.Contains(s.HTML(), "<script>") || !strings.Contains(s.Text(), `<script>x</script> "><b>`) {
		t.Errorf("not escaped: %s", s.HTML())
	}
}

func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	if code, _ := on(t, h, []string{"api", "v1.4.3", "failed", "3s"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := strings.Join([]string{
		"╭───────────────────────────────────────────╮",
		"│ api v1.4.3                                │",
		"│ ✗ failed in 3s                            │",
		"│ https://ci.example.com/deploys/api/v1.4.3 │",
		"╰───────────────────────────────────────────╯",
	}, "\n")
	if got := h.Screen(); got != want {
		t.Errorf("the screen:\n%s\nwant:\n%s", got, want)
	}
}

// A host that refuses the card: the text instead.
func TestRefused(t *testing.T) {
	h := hottytest.New(t, hottytest.Caps(hotty.Caps{Limits: map[string]int{"surfaces": 1}}))
	_, _ = h.Write([]byte(hotty.Doc("another", "")))
	if _, out := on(t, h, args); !strings.Contains(out, "✓ deployed") {
		t.Errorf("stdout %q", out)
	}
}

func TestData(t *testing.T) {
	var out strings.Builder
	e := env{stdout: &out, stderr: &out, open: func() (*term.Term, error) { panic("a pipe needs no terminal") }}
	if code := run(context.Background(), args, e); code != 0 || out.String() != "service=api version=v1.4.2 status=ok took=42s\n" {
		t.Errorf("exit %d: %q", code, out.String())
	}
	// A terminal on stdin but none to open: the data.
	out.Reset()
	e = env{stdout: &out, stderr: &out, tty: true, open: func() (*term.Term, error) { return nil, term.ErrNoTerminal }}
	if code := run(context.Background(), args, e); code != 0 || !strings.HasPrefix(out.String(), "service=api") {
		t.Errorf("no terminal: %d %q", code, out.String())
	}
}

func TestUsage(t *testing.T) {
	for _, a := range [][]string{nil, {"a", "b", "maybe", "1s"}, {"a", "b", "ok", "soon"}} {
		var out strings.Builder
		if code := run(context.Background(), a, env{stdout: &out, stderr: &out}); code != 2 || !strings.Contains(out.String(), "usage") {
			t.Errorf("%v: exit %d %q", a, code, out.String())
		}
	}
}
