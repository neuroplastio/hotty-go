package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neuroplastio/hotty-go/form"
	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/term"
)

const surface = "ask-form"

// start runs the program in the background, as a script would, with h as
// its terminal; wait returns its exit status and stdout.
func start(t *testing.T, h *hottytest.Host) (wait func() (int, string)) {
	t.Helper()
	var out, errs strings.Builder
	done := make(chan int, 1)
	go func() {
		open := func() (*term.Term, error) { return h.Term("ask"), nil }
		done <- run(context.Background(), &out, &errs, open)
	}()
	return func() (int, string) {
		select {
		case code := <-done:
			if errs.Len() > 0 {
				t.Logf("stderr: %s", errs.String())
			}
			return code, out.String()
		case <-time.After(5 * time.Second):
			t.Fatal("the program did not finish")
			return 0, ""
		}
	}
}

// eventually waits for a condition on the host, for the program to catch
// up with the user.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// formShown waits until the form has the keyboard.
func formShown(t *testing.T, h *hottytest.Host) *hottytest.Surface {
	t.Helper()
	eventually(t, "the form has the keyboard", func() bool {
		s := h.Surface(surface)
		return s != nil && s.Focused() != ""
	})
	return h.Surface(surface)
}

func TestForm(t *testing.T) {
	h := hottytest.New(t)
	wait := start(t, h)
	s := formShown(t, h)
	if s.Detached() || s.Placement().Rows != deploy.Rows() || s.Focused() != deploy.First() {
		t.Errorf("the form: detached %v, %+v, focused %q", s.Detached(), s.Placement(), s.Focused())
	}
	for _, err := range []error{
		h.Fill(surface, form.ControlID(0), "api"),
		h.Check(surface, form.OptionID(1, 1), true),
		h.Fill(surface, form.ControlID(2), "3"),
		h.Check(surface, form.ControlID(3), false),
		h.Submit(surface, form.FormID),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	code, out := wait()
	if code != ok || out != `{"service":"api","env":"production","replicas":3,"notify":false}`+"\n" {
		t.Fatalf("exit %d: %q", code, out)
	}
	// What stays in the scrollback reports nothing, and says what was
	// decided.
	if !s.Detached() || !strings.Contains(s.Text(), "Environment production Replicas 3") {
		t.Errorf("the summary: detached %v, %q", s.Detached(), s.Text())
	}
}

func TestProblems(t *testing.T) {
	h := hottytest.New(t)
	wait := start(t, h)
	s := formShown(t, h)
	_ = h.Fill(surface, form.ControlID(2), "many")
	_ = h.Submit(surface, form.FormID)
	eventually(t, "the problems are shown", func() bool { return s.TextOf(form.ProblemID(0)) != "" })
	if got := s.TextOf(form.ProblemID(0)); got != "! required" {
		t.Errorf("service: %q", got)
	}
	if got := s.TextOf(form.ProblemID(2)); got != "! not a number" {
		t.Errorf("replicas: %q", got)
	}
	// The keyboard went to the first field that is wrong; what was typed
	// stays.
	if s.Focused() != form.ControlID(0) {
		t.Errorf("focused %q", s.Focused())
	}
	if v, _ := s.Value(form.ControlID(2)); v != "many" {
		t.Errorf("replicas lost what was typed: %q", v)
	}
	_ = h.Fill(surface, form.ControlID(0), "web")
	_ = h.Fill(surface, form.ControlID(2), "1")
	_ = h.Submit(surface, form.FormID)
	if code, out := wait(); code != ok || !strings.HasPrefix(out, `{"service":"web","env":"staging","replicas":1,"notify":true}`) {
		t.Errorf("exit %d: %q", code, out)
	}
}

func TestCancel(t *testing.T) {
	h := hottytest.New(t)
	wait := start(t, h)
	s := formShown(t, h)
	h.Type("\x1b")
	if code, out := wait(); code != cancelled || out != "" {
		t.Errorf("exit %d: %q", code, out)
	}
	if !s.Detached() || s.Text() != "Deploy cancelled." {
		t.Errorf("after Escape: %q", s.Text())
	}
}

func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	// Service left empty, then given; the second option by name; a
	// replicas count that is not a number, then one; a wrong y/n, then n.
	h.Type("\rdb\x7fb\rproduction\rx\r4\rmaybe\rn\r")
	code, out := start(t, h)()
	if code != ok || out != `{"service":"db","env":"production","replicas":4,"notify":false}`+"\n" {
		t.Fatalf("exit %d: %q", code, out)
	}
	want := strings.Join([]string{
		"Service: ",
		"  ! required",
		"Service: db",
		"Environment (staging, production) [staging]: production",
		"Replicas [2]: x",
		"  ! not a number",
		"Replicas [2]: 4",
		"Tell the team (y/n): maybe",
		"  ! y or n",
		"Tell the team (y/n): n",
	}, "\n")
	if got := h.Screen(); got != strings.ReplaceAll(want, ": \n", ":\n") {
		t.Errorf("the screen:\n%s\nwant:\n%s", got, want)
	}
}

func TestCellsDefaultsAndCancel(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	h.Type("api\r\r\r\r")
	if code, out := start(t, h)(); code != ok || out != `{"service":"api","env":"staging","replicas":2,"notify":true}`+"\n" {
		t.Errorf("defaults: %d %q", code, out)
	}
	c := hottytest.New(t, hottytest.Text())
	c.Type("ap\x03")
	if code, out := start(t, c)(); code != cancelled || out != "" {
		t.Errorf("Ctrl-C: %d %q", code, out)
	}
}

func TestNoTerminal(t *testing.T) {
	var out, errs strings.Builder
	open := func() (*term.Term, error) { return nil, term.ErrNoTerminal }
	if code := run(context.Background(), &out, &errs, open); code != noTTY || !strings.Contains(errs.String(), "no terminal") {
		t.Errorf("exit %d: %q", code, errs.String())
	}
}
