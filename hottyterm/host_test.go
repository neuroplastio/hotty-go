package hottyterm_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyterm"
	"github.com/neuroplastio/hotty-go/hottytest"
)

// open is a Term on a test host, closed when the test ends.
func open(t *testing.T, h *hottytest.Host) *hottyterm.Term {
	t.Helper()
	tm := hottyterm.New(h, h, "tool-7", h.TermSize, nil)
	t.Cleanup(func() { _ = tm.Close() })
	return tm
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestPrintAndFence(t *testing.T) {
	h := hottytest.New(t)
	tm := open(t, h)
	if !tm.Detect(ctx(t)) {
		t.Fatal("a host is native")
	}
	if c := tm.Caps(); c.Host != "hottytest" || !tm.Native() {
		t.Errorf("caps %+v", c)
	}
	_ = tm.Send("$ mytool")
	if err := tm.LineStart(ctx(t)); err != nil {
		t.Fatal(err)
	}
	name, err := tm.Print("card", `<p id=msg>Deployed</p>`, hotty.Placement{Cols: 30})
	if err != nil || name != "tool-7-card" {
		t.Fatalf("Print: %q %v", name, err)
	}
	replies, err := tm.Fence(ctx(t))
	if err != nil || len(replies) != 0 {
		t.Errorf("Fence: %v %v", replies, err)
	}
	s := h.Surface(name)
	if s == nil || !s.Detached() || !s.Placed() || s.TextOf("msg") != "Deployed" || s.Placement().Cols != 30 {
		t.Fatalf("the surface: %+v", s)
	}
	if col, row := s.At(); col != 0 || row != 1 {
		t.Errorf("placed at %d,%d: LineStart should have moved to a new line", col, row)
	}
	// At a line's start, LineStart writes nothing.
	before := h.Output()
	_ = tm.LineStart(ctx(t))
	if after := h.Output(); after != before+"\x1b[6n" {
		t.Errorf("LineStart at a line's start wrote %q", strings.TrimPrefix(after, before))
	}
}

func TestFenceReturnsErrors(t *testing.T) {
	h := hottytest.New(t, hottytest.Lenient())
	tm := open(t, h)
	tm.Detect(ctx(t))
	_ = tm.Send(hotty.Place(tm.Surface("never-sent"), hotty.Placement{Cols: 10}), hotty.Place(tm.Surface("x"), hotty.Placement{Cols: 0}))
	replies, err := tm.Fence(ctx(t))
	if err != nil || len(replies) != 2 {
		t.Fatalf("Fence: %v %v", replies, err)
	}
	if replies[0].Code != hotty.ENOENT || replies[1].Code != hotty.ENOENT {
		t.Errorf("codes %q %q", replies[0].Code, replies[1].Code)
	}
	if replies[0].Err() == nil {
		t.Error("an error reply is an error")
	}
}

func TestRequest(t *testing.T) {
	h := hottytest.New(t, hottytest.AutoRows(func(*hottytest.Surface, int) int { return 7 }))
	tm := open(t, h)
	tm.Detect(ctx(t))
	name := tm.Surface("doc")
	_ = tm.Send(hotty.Doc(name, "<p>long</p>"))
	r, err := tm.Request(ctx(t), func(o hotty.ReplyOption) string {
		return hotty.Place(name, hotty.Placement{Cols: 40}, o)
	})
	if err != nil || !r.OK || r.Rows != 7 || r.Cols != 40 || r.N == 0 {
		t.Fatalf("Request: %+v %v", r, err)
	}
	// An error reply is a reply.
	r, err = tm.Request(ctx(t), func(o hotty.ReplyOption) string { return hotty.Focus("nope", "", o) })
	if err != nil || r.OK || r.Code != hotty.ENOENT {
		t.Errorf("an error reply: %+v %v", r, err)
	}
	// Two requests never share a number.
	r2, _ := tm.Request(ctx(t), func(o hotty.ReplyOption) string { return hotty.Hide(name, o) })
	if r2.N == r.N || r2.Re != "hide" {
		t.Errorf("numbers %d and %d", r.N, r2.N)
	}
}

func TestRequestOnATerminalThatIsNotAHost(t *testing.T) {
	h := hottytest.New(t, hottytest.Text(), hottytest.Lenient())
	tm := open(t, h)
	start := time.Now()
	if tm.Detect(ctx(t)) {
		t.Fatal("not a host")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("detection took %v", d)
	}
	c, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := tm.Request(c, func(o hotty.ReplyOption) string { return hotty.Hide("x", o) })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Request: %v", err)
	}
	// Fence works on any terminal.
	if replies, err := tm.Fence(ctx(t)); err != nil || len(replies) != 0 {
		t.Errorf("Fence: %v %v", replies, err)
	}
}

// What a call waits for goes to it; the rest goes to Events, in order,
// even while the call waits.
func TestEventsWhileRequesting(t *testing.T) {
	h := hottytest.New(t)
	tm := open(t, h)
	tm.Detect(ctx(t))
	name := tm.Surface("form")
	_ = tm.Send(hotty.Doc(name, `<button id=go>Go</button>`), hotty.Place(name, hotty.Placement{Cols: 10, Rows: 1}))
	evs := tm.Events(ctx(t))
	if tm.Events(ctx(t)) != evs {
		t.Error("Events returns its channel again")
	}
	h.Type("a")
	if err := h.Click(name, "go"); err != nil {
		t.Fatal(err)
	}
	r, err := tm.Request(ctx(t), func(o hotty.ReplyOption) string { return hotty.Blur(name, o) })
	if err != nil || r.Re != "blur" {
		t.Fatalf("Request: %+v %v", r, err)
	}
	var got []string
	for len(got) < 4 {
		select {
		case ev := <-evs:
			switch ev := ev.(type) {
			case uv.KeyPressEvent:
				got = append(got, "key "+ev.String())
			case hottyterm.Message:
				e, _ := ev.Event()
				got = append(got, e.Kind+" "+e.Target)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("events so far: %v", got)
		}
	}
	if strings.Join(got, ", ") != "key a, focus , click go, blur " {
		t.Errorf("events: %v", got)
	}
}

func TestEventsEndWithTheInput(t *testing.T) {
	h := hottytest.New(t)
	tm := open(t, h)
	evs := tm.Events(ctx(t))
	h.Type("x")
	_ = h.Close()
	var n int
	for range evs {
		n++
	}
	if n != 1 {
		t.Errorf("%d events before the end", n)
	}
	// Calls that wait return at once once the input has ended.
	if _, err := tm.Fence(ctx(t)); !errors.Is(err, hottyterm.ErrNoAnswer) {
		t.Errorf("Fence after the input ended: %v", err)
	}
}

func TestDetachAll(t *testing.T) {
	h := hottytest.New(t)
	tm := open(t, h)
	if err := tm.DetachAll(); err != nil {
		t.Fatal(err)
	}
	tm.Detect(ctx(t))
	a, b := tm.Surface("a"), tm.Surface("b")
	if again := tm.Surface("a"); again != a || len(tm.Surfaces()) != 2 {
		t.Errorf("Surface names a surface once: %v", tm.Surfaces())
	}
	_ = tm.Send(hotty.Doc(a, "<p>a</p>"), hotty.Doc(b, "<button id=x>x</button>"))
	_ = tm.DetachAll()
	_, _ = tm.Fence(ctx(t))
	for _, s := range h.Surfaces() {
		if !s.Detached() {
			t.Errorf("%s reports still", s.Name())
		}
	}
}

func TestKittyGraphics(t *testing.T) {
	for _, c := range []struct {
		opts []hottytest.Option
		want bool
	}{{[]hottytest.Option{hottytest.Text(), hottytest.KittyGraphics()}, true}, {[]hottytest.Option{hottytest.Text()}, false}} {
		h := hottytest.New(t, c.opts...)
		tm := open(t, h)
		start := time.Now()
		if got := tm.KittyGraphics(ctx(t)); got != c.want {
			t.Errorf("KittyGraphics = %v, want %v", got, c.want)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("took %v", d)
		}
		// Its DA1 is taken: nothing is left for Events.
		_ = h.Close()
		for ev := range tm.Events(ctx(t)) {
			t.Errorf("left behind: %#v", ev)
		}
	}
}

func TestNames(t *testing.T) {
	h := hottytest.New(t)
	tm := hottyterm.New(h, h, "my tool.v2", nil, nil)
	defer tm.Close()
	if got := tm.Surface("chart #1"); got != "my_tool_v2-chart__1" || !hotty.ValidName(got) {
		t.Errorf("Surface = %q", got)
	}
	if s := tm.Size(); s != (hottyterm.Size{Cols: 80, Rows: 24}) {
		t.Errorf("Size without a size function: %v", s)
	}
}
