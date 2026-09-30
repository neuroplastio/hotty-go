package term

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-demo/sdk/hotty"
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
	return &fake{out: outR, answer: inW, t: New(inR, outW, "tool-7", func() Size { return Size{100, 30} }, known)}
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
		_, _ = io.WriteString(f.answer, "x"+capsReply()+"\x1b[?62;22c")
	}()
	if !f.t.Detect(context.Background()) {
		t.Fatal("a host that answers the query is native")
	}
	if got := f.t.Caps().Limits["surfaces"]; got != 64 {
		t.Errorf("caps: surfaces %d", got)
	}
	// The key typed before the answer is not lost.
	ev := <-f.t.Events(context.Background())
	if k, ok := ev.(uv.KeyPressEvent); !ok || k.String() != "x" {
		t.Errorf("first event %#v, want the key x", ev)
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
