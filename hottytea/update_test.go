package hottytea

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// from is what the host sends, as Bubble Tea delivers it.
func from(ctl hotty.Control, payload string) tea.Msg {
	return uv.UnknownOscEvent(hotty.Encode(ctl, []byte(payload)))
}

func caps(n string) tea.Msg {
	return from(hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "q"}, {K: "n", V: n}},
		`{"v":"0.1","cell":{"w":9,"h":18},"limits":{"surfaces":64},"host":"test"}`)
}

var da1 = uv.PrimaryDeviceAttributesEvent{62, 22}

// after is the Detector's timer firing d from now.
func after(d time.Duration) tea.Msg { return detectTickMsg{time.Now().Add(d)} }

func detecting() *Session {
	s := New()
	s.Detect()
	return s
}

func TestDetectHost(t *testing.T) {
	s := detecting()
	msg, _ := s.Update(caps("1"))
	r, ok := msg.(ReadyMsg)
	if !ok || r.Mode != Native || r.Caps.Host != "test" || s.Mode != Native || s.Caps.Limits["surfaces"] != 64 {
		t.Fatalf("a host's reply gave %#v, mode %v", msg, s.Mode)
	}
	// The DA1 answer behind the reply is the Session's: consumed.
	if msg, _ := s.Update(da1); msg != nil {
		t.Errorf("the fence's DA1 came back: %#v", msg)
	}
	// The detection's timers, when they fire, change nothing.
	for _, late := range []tea.Msg{after(hotty.DetectAfterDA1), after(hotty.DetectTimeout)} {
		if msg, _ := s.Update(late); msg != nil || s.Mode != Native {
			t.Errorf("%T after detection: %#v, mode %v", late, msg, s.Mode)
		}
	}
}

func TestDetectText(t *testing.T) {
	s := detecting()
	// DA1 with no reply before it: not a host, after a moment in case a
	// reply is on its way.
	msg, cmd := s.Update(da1)
	if msg != nil || cmd == nil || s.Mode != Detecting {
		t.Fatalf("DA1: %#v, cmd %v, mode %v", msg, cmd != nil, s.Mode)
	}
	if msg, _ := s.Update(after(hotty.DetectAfterDA1)); !isReady(msg, Text) || s.Mode != Text {
		t.Fatalf("after the moment: %#v", msg)
	}
	// A reply that comes too late changes nothing.
	if msg, _ := s.Update(caps("1")); msg != nil || s.Mode != Text {
		t.Errorf("a late reply: %#v, mode %v", msg, s.Mode)
	}
}

func isReady(msg tea.Msg, mode Mode) bool {
	r, ok := msg.(ReadyMsg)
	return ok && r.Mode == mode
}

func TestDetectSilence(t *testing.T) {
	s := detecting()
	if msg, _ := s.Update(after(hotty.DetectTimeout)); !isReady(msg, Text) {
		t.Fatalf("a terminal that answers nothing: %#v", msg)
	}
}

func TestDetectStrayDA1(t *testing.T) {
	// A DA1 that answers an earlier request comes first; the host's reply
	// follows within the moment, and wins.
	s := detecting()
	s.Update(da1)
	if msg, _ := s.Update(caps("1")); msg == nil || s.Mode != Native {
		t.Fatalf("the reply after a stray DA1: %#v, mode %v", msg, s.Mode)
	}
	if msg, _ := s.Update(after(hotty.DetectAfterDA1)); msg != nil || s.Mode != Native {
		t.Errorf("the stray DA1's timer: %#v, mode %v", msg, s.Mode)
	}
}

func TestDetectIgnoresAnotherNumber(t *testing.T) {
	s := detecting()
	if msg, _ := s.Update(caps("2")); msg != nil || s.Mode != Detecting {
		t.Fatalf("a reply to another query: %#v, mode %v", msg, s.Mode)
	}
}

// The Session takes only the DA1 answers its own requests are owed: one
// the program asked for itself is the program's.
func TestTheProgramsOwnDA1(t *testing.T) {
	s := New()
	if msg, _ := s.Update(da1); msg == nil {
		t.Fatal("a DA1 before any detection was swallowed")
	}
	s = detecting()
	s.Update(caps("1"))
	s.Update(da1) // the fence's
	if msg, _ := s.Update(da1); msg == nil {
		t.Fatal("the program's DA1 after detection was swallowed")
	}
}

func TestMessages(t *testing.T) {
	s := native()
	ev := from(hotty.Control{{K: "a", V: "ev"}, {K: "s", V: "card"}, {K: "e", V: "click"}, {K: "t", V: "go"}}, `{"value":"1"}`)
	if msg, _ := s.Update(ev); msg == nil {
		t.Fatal("an event was swallowed")
	} else if e, ok := msg.(EventMsg); !ok || e.Surface != "card" || e.Kind != hotty.EventClick || e.Target != "go" || e.Value() != "1" {
		t.Errorf("event: %#v", msg)
	}
	refused := from(hotty.Control{{K: "a", V: "err"}, {K: "s", V: "card"}, {K: "re", V: "delta"}}, `{"code":"ENOTARGET","detail":"nope"}`)
	if msg, _ := s.Update(refused); msg == nil {
		t.Fatal("an error was swallowed")
	} else if e, ok := msg.(ErrorMsg); !ok || e.Code != hotty.ENOTARGET || e.Detail != "nope" || e.Err() == nil {
		t.Errorf("error: %#v", msg)
	}
	ok := from(hotty.Control{{K: "a", V: "ok"}, {K: "s", V: "card"}, {K: "re", V: "delta"}}, "")
	if msg, _ := s.Update(ok); msg != nil {
		t.Errorf("an ok reply: %#v", msg)
	}
	// Not HOTTY's: back as it was.
	title := uv.UnknownOscEvent("\x1b]2;title\x07")
	if msg, _ := s.Update(title); msg != tea.Msg(title) {
		t.Errorf("another OSC: %#v", msg)
	}
	key := tea.KeyPressMsg{Code: 'q', Text: "q"}
	if msg, _ := s.Update(key); msg != tea.Msg(key) {
		t.Errorf("a key: %#v", msg)
	}
	// A chunk that is not the last, and a malformed message: consumed.
	if msg, _ := s.Update(uv.UnknownOscEvent("\x1b]7279;a=ev:s=x:m=1;eA==\x1b\\")); msg != nil {
		t.Errorf("a first chunk: %#v", msg)
	}
	if msg, _ := s.Update(uv.UnknownOscEvent("\x1b]7279;a=ev:s\x1b\\")); msg != nil {
		t.Errorf("a malformed message: %#v", msg)
	}
}

// The ok of a command the program numbered is the program's: AckMsg, with
// its number. Its error is an ErrorMsg with the number too.
func TestAck(t *testing.T) {
	s := native()
	ok := from(hotty.Control{{K: "a", V: "ok"}, {K: "n", V: "7"}, {K: "s", V: "field"}, {K: "re", V: "focus"}}, "")
	msg, _ := s.Update(ok)
	if a, isAck := msg.(AckMsg); !isAck || a.N != 7 || a.Re != "focus" || a.Surface != "field" || a.Err() != nil {
		t.Fatalf("a numbered ok: %#v", msg)
	}
	refused := from(hotty.Control{{K: "a", V: "err"}, {K: "n", V: "8"}, {K: "s", V: "field"}, {K: "re", V: "focus"}},
		`{"code":"ENOTARGET","detail":"ed"}`)
	if e, isErr := mustMsg(s.Update(refused)).(ErrorMsg); !isErr || e.N != 8 || e.Code != hotty.ENOTARGET {
		t.Errorf("a numbered error: %#v", e)
	}
	// The detection's own query is numbered: its late answer is the
	// Session's, not an ack.
	if msg := mustMsg(s.Update(caps("1"))); msg != nil {
		t.Errorf("a late answer to the detection: %#v", msg)
	}
}

func card(name string, y int) Surface {
	return Surface{Name: name, Rect: Rect{0, y, 10, 2}, Doc: func() string { return "<p>" + name + "</p>" }}
}

// A refused document's placement fails too: that ENOENT is expected, and
// is not the program's error.
func TestARefusedDocumentsPlacementIsNoError(t *testing.T) {
	s := native()
	s.Layout([]Surface{card("a", 0)})
	flushed(s)
	quota := from(hotty.Control{{K: "a", V: "err"}, {K: "s", V: "a"}, {K: "re", V: "doc"}}, `{"code":"EQUOTA"}`)
	gone := from(hotty.Control{{K: "a", V: "err"}, {K: "s", V: "a"}, {K: "re", V: "place"}}, `{"code":"ENOENT"}`)
	if msg, _ := s.Update(quota); msg != (RelayoutMsg{}) {
		t.Fatalf("EQUOTA: %#v", msg)
	}
	if msg, _ := s.Update(gone); msg != nil {
		t.Fatalf("the refused document's placement: %#v", msg)
	}
	// A later ENOENT for it is an error again.
	if msg, _ := s.Update(gone); msg == nil {
		t.Fatal("a second ENOENT was swallowed")
	}
}

// With the terminal's own limit, a surface beyond it is not sent: the
// terminal would refuse it, and the relayout send it again, for ever.
func TestTheTerminalsLimitIsHard(t *testing.T) {
	s := native()
	s.Caps.Limits = map[string]int{"surfaces": 2}
	want := []Surface{card("a", 0), card("b", 2), card("c", 4)}
	s.Layout(want)
	out := flushed(s)
	if strings.Contains(out, "a=doc:s=c") || strings.Contains(out, "a=place:s=c") || !s.Has("a") || !s.Has("b") || s.Has("c") {
		t.Fatalf("over the limit: %q", out)
	}
	// One goes: the third has room in the same layout, once it is gone.
	s.Layout(want[1:])
	out = flushed(s)
	del, doc := strings.Index(out, "a=del:s=a"), strings.Index(out, "a=doc:s=c")
	if del < 0 || doc < 0 || del > doc {
		t.Errorf("room again: %q", out)
	}
}

func TestALearnedLimitStopsTheLoop(t *testing.T) {
	s := native()
	want := []Surface{card("a", 0), card("b", 2), card("c", 4)}
	s.Layout(want)
	flushed(s)
	// The terminal holds two, says the third's EQUOTA.
	s.Update(from(hotty.Control{{K: "a", V: "err"}, {K: "s", V: "c"}, {K: "re", V: "doc"}}, `{"code":"EQUOTA"}`))
	for range 3 {
		s.Layout(want)
		if out := flushed(s); strings.Contains(out, "a=doc") {
			t.Fatalf("the refused document was sent again while nothing left: %q", out)
		}
	}
	// A later EQUOTA never raises what was learned.
	s.Update(from(hotty.Control{{K: "a", V: "err"}, {K: "s", V: "b"}, {K: "re", V: "doc"}}, `{"code":"EQUOTA"}`))
	if n, hard := s.limit(); n != 1 || !hard {
		t.Errorf("limit %d (hard %v), want 1", n, hard)
	}
}

// The Session's own limit is soft: it deletes hidden surfaces, and never
// keeps one the program wants from the screen.
func TestTheSessionsLimitIsSoft(t *testing.T) {
	s := native()
	s.Limit = 2
	region := Rect{0, 0, 80, 3}
	kept := func(name string, y int) Surface {
		c := card(name, y)
		c.Keep, c.Clip = true, &region
		return c
	}
	s.Layout([]Surface{kept("a", 0)})
	s.Layout([]Surface{kept("a", -5), kept("b", 0)}) // a scrolls out, hidden
	s.Layout([]Surface{kept("a", -5), kept("b", 0), kept("c", 1), kept("d", 2)})
	out := flushed(s)
	if !strings.Contains(out, "a=del:s=a") {
		t.Errorf("the hidden one was not deleted: %q", out)
	}
	for _, name := range []string{"b", "c", "d"} {
		if !s.Placed(name) {
			t.Errorf("%s is wanted on screen and not placed", name)
		}
	}
}

// What Layout sends is the same every run: hides and deletes in name
// order, and the oldest of equals deleted first by name.
func TestLayoutIsDeterministic(t *testing.T) {
	run := func() string {
		s := native()
		var want []Surface
		for i := range 12 {
			c := card(fmt.Sprint("s", i), 0)
			c.Keep = i%2 == 0
			want = append(want, c)
		}
		s.Layout(want)
		s.Layout(nil)
		s.Limit = 3
		s.Layout([]Surface{card("x", 0)})
		return flushed(s)
	}
	first := run()
	for range 20 {
		if got := run(); got != first {
			t.Fatal("two runs sent different commands")
		}
	}
}

func TestPressFitHoverAndZArePlaced(t *testing.T) {
	s := native()
	c := card("a", 1)
	c.Press, c.Fit, c.Hover, c.Z = true, true, true, -3
	s.Layout([]Surface{c})
	if out := flushed(s); !strings.Contains(out, "a=place:s=a:c=10:r=2:z=-3:p=1:f=1:v=1:C=1") {
		t.Fatalf("placement: %q", out)
	}
	// Asking for presses, fit and hover no more places it again.
	c.Press, c.Fit, c.Hover = false, false, false
	s.Layout([]Surface{c})
	if out := flushed(s); !strings.Contains(out, "a=place:s=a:c=10:r=2:z=-3:C=1") {
		t.Fatalf("without press: %q", out)
	}
}

func TestNothingIsSentToATerminalThatIsNotAHost(t *testing.T) {
	for _, mode := range []Mode{Detecting, Text} {
		s := New()
		s.Mode = mode
		s.Layout([]Surface{card("a", 0)})
		s.Send(hotty.SetText("a", "t", "x"))
		s.DetachAll()
		if cmd := s.Flush(); cmd != nil || s.Has("a") || s.Count != (Counts{}) || s.Close() != nil {
			t.Errorf("%v: sent something", mode)
		}
	}
}

func TestFlush(t *testing.T) {
	s := native()
	if s.Flush() != nil {
		t.Fatal("Flush with nothing queued")
	}
	s.Send(hotty.SetText("a", "t", "1"), hotty.SetText("a", "t", "2"))
	cmd := s.Flush()
	want := hotty.SetText("a", "t", "1") + hotty.SetText("a", "t", "2")
	if s.Sent != len(want) {
		t.Errorf("Sent %d, want %d", s.Sent, len(want))
	}
	if s.Flush() != nil {
		t.Error("Flush twice")
	}
	if raw, ok := cmd().(tea.RawMsg); !ok || raw.Msg != want {
		t.Fatalf("Flush: %#v", cmd())
	}
}

// Bubble Tea runs commands on goroutines, so a later update's Flush may
// run first: what it writes must still be in the order it was queued.
func TestFlushKeepsTheOrderWhicheverRunsFirst(t *testing.T) {
	s := native()
	s.Send(hotty.SetText("a", "t", "1"))
	first := s.Flush()
	s.Send(hotty.SetText("a", "t", "2"))
	second := s.Flush()
	raw, ok := second().(tea.RawMsg)
	if want := hotty.SetText("a", "t", "1") + hotty.SetText("a", "t", "2"); !ok || raw.Msg != want {
		t.Fatalf("the later command wrote %#v, want both deltas in order", second())
	}
	if msg := first(); msg != nil {
		t.Fatalf("the earlier command wrote again: %#v", msg)
	}
}

func TestDetachAllHasPlaced(t *testing.T) {
	s := native()
	kept := card("k", 0)
	kept.Keep = true
	s.Layout([]Surface{card("a", 2), kept})
	s.Layout([]Surface{card("a", 2)}) // k hidden
	flushed(s)
	if !s.Has("k") || s.Placed("k") || !s.Placed("a") || s.Has("nope") {
		t.Fatalf("Has/Placed: k %v %v, a %v", s.Has("k"), s.Placed("k"), s.Placed("a"))
	}
	s.DetachAll()
	if out := flushed(s); out != hotty.Detach("a")+hotty.Detach("k") {
		t.Errorf("DetachAll: %q", out)
	}
}

// Close deletes the Session's surfaces, and nothing is sent after it: a
// frame drawn before the program quits must not bring a surface back.
func TestClose(t *testing.T) {
	s := native()
	kept := card("k", 0)
	kept.Keep = true
	s.Layout([]Surface{card("a", 2), kept})
	flushed(s)
	cmd := s.Close()
	if raw, ok := cmd().(tea.RawMsg); !ok || raw.Msg != hotty.Del("a")+hotty.Del("k") || s.Has("a") || s.Has("k") {
		t.Fatalf("Close: %#v", cmd())
	}
	s.Layout([]Surface{card("a", 2)})
	s.Send(hotty.SetText("a", "t", "x"))
	if cmd := s.Flush(); cmd != nil {
		t.Fatalf("sent after Close: %#v", cmd())
	}
	if New().Close() != nil {
		t.Error("Close with nothing sent")
	}
}

func TestErasedScreenWithNothingPlaced(t *testing.T) {
	s := native()
	if msg, _ := s.Update(erasedMsg{}); msg != nil {
		t.Errorf("an erase with nothing placed: %#v", msg)
	}
	s = New()
	if msg, _ := s.Update(erasedMsg{}); msg != nil {
		t.Errorf("an erase while detecting: %#v", msg)
	}
}

func TestModeString(t *testing.T) {
	for m, want := range map[Mode]string{Detecting: "detecting", Native: "native", Text: "text", 7: "Mode(7)"} {
		if m.String() != want {
			t.Errorf("%d: %q", int(m), m.String())
		}
	}
}

func TestPing(t *testing.T) {
	s := native()
	var out bytes.Buffer
	w := s.Watch(&out)
	// Nothing written yet: when the ping is due, it goes as a command.
	tick := s.Ping()
	if tick == nil {
		t.Fatal("Ping returned no timer")
	}
	msg, cmd := s.Update(pingDueMsg{s.pingSeq})
	if msg != nil || cmd == nil {
		t.Fatalf("a due ping with nothing written: %#v, %v", msg, cmd != nil)
	}
	if raw, ok := cmd().(tea.RawMsg); !ok || raw.Msg != "\x1b[c" {
		t.Fatalf("the ping: %#v", cmd())
	}
	if msg, _ := s.Update(da1); msg == nil {
		t.Fatal("no pong")
	} else if _, ok := msg.(PongMsg); !ok {
		t.Fatalf("pong: %#v", msg)
	}

	// Once something was written, the ping goes right after the next
	// frame, in the renderer's write.
	_, _ = w.Write([]byte("hello"))
	s.Ping()
	_, _ = w.Write([]byte("\x1b[?2026hframe\x1b[?2026l"))
	if !strings.HasSuffix(out.String(), "\x1b[?2026l\x1b[c") {
		t.Fatalf("the ping did not follow the frame: %q", out.String())
	}
	if msg, cmd := s.Update(pingDueMsg{s.pingSeq}); msg != nil || cmd != nil {
		t.Errorf("a ping already sent went again: %#v", msg)
	}
	if _, ok := mustMsg(s.Update(da1)).(PongMsg); !ok {
		t.Error("no pong after a frame")
	}

	// No frame: the timer writes it through the watched writer.
	s.Ping()
	before := out.Len()
	if msg, cmd := s.Update(pingDueMsg{s.pingSeq}); msg != nil || cmd != nil {
		t.Errorf("a due ping: %#v", msg)
	}
	if out.String()[before:] != "\x1b[c" {
		t.Errorf("written: %q", out.String()[before:])
	}
	// An earlier ping's timer does nothing.
	if msg, cmd := s.Update(pingDueMsg{s.pingSeq - 1}); msg != nil || cmd != nil {
		t.Error("an old timer acted")
	}
	if got := s.Written(); got != out.Len() {
		t.Errorf("Written %d, want %d", got, out.Len())
	}
}

func mustMsg(msg tea.Msg, _ tea.Cmd) tea.Msg { return msg }

func TestWatchFile(t *testing.T) {
	s := native()
	sent := make(chan tea.Msg, 1)
	s.Attach(func(m tea.Msg) { sent <- m })
	path := filepath.Join(t.TempDir(), "tty")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	wf := s.WatchFile(f)
	if wf.Fd() != f.Fd() {
		t.Error("Fd")
	}
	if _, err := wf.Write([]byte("\x1b[2Jhi")); err != nil {
		t.Fatal(err)
	}
	if m := <-sent; m != (erasedMsg{}) {
		t.Errorf("an erase through the file: %#v", m)
	}
	if err := wf.Close(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "\x1b[2Jhi" || s.Written() != len(got) {
		t.Errorf("written %q, counted %d", got, s.Written())
	}
	r, _ := os.Open(path)
	defer r.Close()
	buf := make([]byte, 16)
	n, _ := (&File{f: r, w: &s.watch}).Read(buf)
	if string(buf[:n]) != "\x1b[2Jhi" {
		t.Errorf("Read %q", buf[:n])
	}
}
