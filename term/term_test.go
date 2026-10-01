package term

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// fake is a terminal on two pipes: what the program writes, and what the
// test answers.
type fake struct {
	out    *io.PipeReader // the program's output, read by the test
	answer *io.PipeWriter // the terminal's input, written by the test
	t      *Term
}

func newFake(known *Known) *fake {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	return &fake{out: outR, answer: inW, t: New(inR, outW, "tool-7", func() (int, int) { return 100, 30 }, known)}
}

// readQuery waits for the program's query, so the answer comes after it.
func (f *fake) readQuery(t *testing.T) {
	t.Helper()
	buf := make([]byte, 4096)
	n, err := f.out.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "a=q") {
		t.Errorf("query: %q, %v", buf[:n], err)
	}
}

func capsReply() string {
	return hotty.Encode(hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "q"}, {K: "n", V: "1"}},
		[]byte(`{"v":"0.1","cell":{"w":9,"h":18},"limits":{"surfaces":64}}`))
}

func TestDetectHost(t *testing.T) {
	f := newFake(nil)
	go func() {
		f.readQuery(t)
		_, _ = io.WriteString(f.answer, "x"+capsReply()+"\x1b[?62;22c"+"y")
	}()
	if !f.t.Detect(context.Background()) {
		t.Fatal("a host that answers the query is native")
	}
	if got := f.t.Caps().Limits["surfaces"]; got != 64 {
		t.Errorf("caps: surfaces %d", got)
	}
	// The key typed before the answer is not lost, and the DA1 answer
	// behind the host's is taken: the next event is the key after it.
	evs := f.t.Events(context.Background())
	for _, want := range []string{"x", "y"} {
		ev := <-evs
		if k, ok := ev.(uv.KeyPressEvent); !ok || k.String() != want {
			t.Errorf("event %#v, want the key %s", ev, want)
		}
	}
}

func TestDetectHostNoDA1(t *testing.T) {
	// A host whose DA1 answer never comes is still a host, after a moment.
	f := newFake(nil)
	go func() {
		f.readQuery(t)
		_, _ = io.WriteString(f.answer, capsReply())
	}()
	start := time.Now()
	if !f.t.Detect(context.Background()) {
		t.Fatal("a host that answers the query is native")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("waited %v for a DA1 that never came", d)
	}
}

func TestDetectText(t *testing.T) {
	f := newFake(nil)
	go func() {
		f.readQuery(t)
		_, _ = io.WriteString(f.answer, "\x1b[?62;22c") // DA1 only
	}()
	start := time.Now()
	if f.t.Detect(context.Background()) {
		t.Fatal("a terminal that answers only DA1 is not a host")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("text mode took %v; the DA1 fence should end it", took)
	}
}

func TestDetectKnown(t *testing.T) {
	f := newFake(&Known{Native: true})
	if !f.t.Detect(context.Background()) {
		t.Fatal("known native")
	}
}

func TestEventsMessage(t *testing.T) {
	f := newFake(&Known{Native: true})
	f.t.Detect(context.Background())
	evs := f.t.Events(context.Background())
	ev := hotty.Encode(hotty.Control{{K: "a", V: "ev"}, {K: "s", V: "tool-7-form"}, {K: "e", V: "submit"}, {K: "t", V: "f"}},
		[]byte(`{"name":"Ada"}`))
	go func() { _, _ = io.WriteString(f.answer, ev) }()
	got := <-evs
	m, ok := got.(Message)
	if !ok {
		t.Fatalf("got %#v, want a Message", got)
	}
	e, ok := m.Event()
	if !ok || e.Kind != "submit" || e.Fields()["name"] != "Ada" || e.Surface != f.t.Surface("form") {
		t.Errorf("event %+v", e)
	}
}

func TestSizesAndResized(t *testing.T) {
	size := Size{100, 30}
	tm := New(strings.NewReader(""), io.Discard, "x", func() (int, int) { return size.Cols, size.Rows }, nil)
	tm.Resized(Size{1, 1}) // nobody asked yet: dropped
	sizes := tm.Sizes()
	if tm.Sizes() != sizes {
		t.Error("Sizes returns its channel again")
	}
	tm.Resized(Size{90, 20})
	size = Size{120, 40}
	tm.Resized(size) // only the latest waits
	if got := <-sizes; got != size || tm.Size() != size {
		t.Errorf("Sizes gave %v, Size %v", got, tm.Size())
	}
	_ = tm.Close()
	tm.Resized(Size{5, 5})
	if _, open := <-sizes; open {
		t.Error("Sizes closes on Close")
	}
	closed := New(strings.NewReader(""), io.Discard, "x", nil, nil)
	_ = closed.Close()
	if _, open := <-closed.Sizes(); open {
		t.Error("Sizes after Close is closed")
	}
}

func TestOnCloseAndRaw(t *testing.T) {
	tm := New(strings.NewReader(""), io.Discard, "x", nil, nil)
	var order []string
	tm.OnClose(func() error { order = append(order, "first"); return io.ErrClosedPipe })
	tm.OnClose(func() error { order = append(order, "second"); return nil })
	if err := tm.Close(); err != io.ErrClosedPipe || strings.Join(order, ",") != "first,second" {
		t.Errorf("Close: %v %v", err, order)
	}
	if err := tm.Close(); err != nil || len(order) != 2 {
		t.Errorf("Close twice: %v %v", err, order)
	}
	restore, err := tm.Raw()
	if err != nil {
		t.Fatal(err)
	}
	restore()
	if tm.File() != nil {
		t.Error("a Term made with New has no file")
	}
	// Events after Close is closed at once.
	if _, open := <-tm.Events(context.Background()); open {
		t.Error("Events after Close")
	}
}

func TestDetectTimesOut(t *testing.T) {
	// A terminal that answers nothing: Detect gives up with the context.
	inR, _ := io.Pipe()
	tm := New(inR, io.Discard, "x", nil, nil)
	defer tm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if tm.Detect(ctx) || tm.Native() {
		t.Error("silence is not a host")
	}
	// The answer stands.
	if tm.Detect(context.Background()) {
		t.Error("Detect asks once")
	}
}

func TestDetectIgnoresAnotherNumber(t *testing.T) {
	f := newFake(nil)
	go func() {
		f.readQuery(t)
		other := hotty.Encode(hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "q"}, {K: "n", V: "9"}}, []byte(`{"v":"0.1"}`))
		_, _ = io.WriteString(f.answer, other+"\x1b[?62;22c")
	}()
	if f.t.Detect(context.Background()) {
		t.Error("a reply to someone else's query is not this one's answer")
	}
}
