package hotty

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

// scanAll feeds stream to a Scanner cut at the offsets given, and returns
// its segments, adjacent passes joined, and its count of dropped sequences.
func scanAll(s *Scanner, stream string, cuts ...int) ([]segView, int) {
	var got []segView
	prev := 0
	for _, c := range append(cuts, len(stream)) {
		got = append(got, viewSegments(s.Feed([]byte(stream[prev:c])))...)
		prev = c
	}
	return mergePass(got), s.Invalid
}

// The limit is 65536 bytes, the terminator not counted (SDK.md §3.7).
func TestScannerLimit(t *testing.T) {
	body := strings.Repeat("A", scanMax-len(prefix))
	for _, term := range []string{st, "\x07"} {
		seq := prefix + body + term
		got, invalid := scanAll(&Scanner{}, seq+"k")
		if want := []segView{{"osc", seq}, {"pass", "k"}}; invalid != 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("65536 bytes and %q: %d invalid, %.40q", term, invalid, got)
		}
		got, invalid = scanAll(&Scanner{}, prefix+"A"+body+term+"k")
		if want := []segView{{"pass", "k"}}; invalid != 1 || !reflect.DeepEqual(got, want) {
			t.Errorf("65537 bytes and %q: %d invalid, %.40q", term, invalid, got)
		}
	}
	// One too long is in a sequence while it is dropped, holding nothing.
	var s Scanner
	if s.Feed([]byte(prefix + strings.Repeat("A", scanMax+1))); !s.InSequence() || len(s.Holding()) != 0 || s.Invalid != 1 {
		t.Errorf("dropping one too long: in a sequence %v, holding %d bytes", s.InSequence(), len(s.Holding()))
	}
	// One too long, ended by an ESC that is not ST: the ESC begins what
	// follows.
	long := prefix + strings.Repeat("A", scanMax)
	for _, cut := range []int{0, len(long), len(long) + 1} {
		got, invalid := scanAll(&Scanner{}, long+"\x1b[Ak", cut)
		if want := []segView{{"pass", "\x1b[Ak"}}; invalid != 1 || !reflect.DeepEqual(got, want) {
			t.Errorf("cut at %d: %d invalid, %q", cut, invalid, got)
		}
		got, _ = scanAll(&Scanner{}, long+"\x1b]7279;a=ok\x07", cut)
		if want := []segView{{"osc", "\x1b]7279;a=ok\x07"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("cut at %d: a sequence after one too long: %q", cut, got)
		}
	}
}

// However the stream is split, the segments are the same.
func TestScannerSplits(t *testing.T) {
	pieces := []string{
		"key", "\x1b]7279;a=ev:s=f:e=click:t=go\x1b\\", "\x1b[A", "\x1b]7279;a=ok:n=1:re=q\x07",
		"\x1b]11;rgb:0/0/0\x1b\\", "\x1b[?62;22c", "\x1b]7279;a=ok\x1b[B", "\x1b",
		"\x1b]7279;m=0;AAAA\x1b\\", "é", "\x1b]72790;x\x07", "\x1b[?1u",
	}
	seed := 11
	for round := range 200 {
		var b strings.Builder
		for range 6 {
			seed = (seed*1103515245 + 12345) % 2147483648
			b.WriteString(pieces[seed%len(pieces)])
		}
		data := b.String()
		every := make([]int, 0, len(data))
		for i := 1; i < len(data); i++ {
			every = append(every, i)
		}
		for _, da1 := range []bool{false, true} {
			whole, inv := scanAll(&Scanner{DA1: da1}, data)
			split, inv2 := scanAll(&Scanner{DA1: da1}, data, every...)
			if !reflect.DeepEqual(split, whole) || inv != inv2 {
				t.Fatalf("round %d, DA1 %v: %q\nwhole %q\nsplit %q", round, da1, data, whole, split)
			}
		}
	}
}

// What is no segment of its own goes out in the Feed that brought it, as
// the bytes given: a relay's pass-through copies nothing.
func TestScannerPassesAtOnce(t *testing.T) {
	s := Scanner{DA1: true}
	p := []byte("abc\x1b[A")
	segs := s.Feed(p)
	if len(segs) != 1 || string(segs[0].Data) != "abc\x1b[A" || len(s.Holding()) != 0 {
		t.Fatalf("%q, holding %q", segs, s.Holding())
	}
	if &segs[0].Data[0] != &p[0] {
		t.Error("the pass segment is a copy")
	}
	// A caller's append to a segment does not write over the bytes after it.
	p = []byte("ab\x1b]7279;a=ok\x07cd")
	segs = s.Feed(p)
	_ = append(segs[0].Data, 'X')
	if !bytes.Equal(p, []byte("ab\x1b]7279;a=ok\x07cd")) {
		t.Errorf("append to a segment changed the input: %q", p)
	}
	if s.Feed([]byte("\x1b]72")); string(s.Holding()) != "\x1b]72" || s.InSequence() {
		t.Errorf("holding %q, in a sequence %v", s.Holding(), s.InSequence())
	}
	if f := s.Flush(); len(f) != 1 || f[0].Kind != SegmentPass || string(f[0].Data) != "\x1b]72" || s.Invalid != 0 {
		t.Errorf("Flush of a prefix: %q, %d invalid", f, s.Invalid)
	}
	// A lone ESC at the end of a read is held, for the program to flush as
	// the Escape key.
	if s.Feed([]byte("x\x1b")); string(s.Holding()) != "\x1b" {
		t.Errorf("holding %q", s.Holding())
	}
	if f := s.Flush(); len(f) != 1 || string(f[0].Data) != "\x1b" {
		t.Errorf("Flush of a lone ESC: %q", f)
	}
	if s.Feed([]byte("\x1b]7279;a=ok")); !s.InSequence() || string(s.Holding()) != "\x1b]7279;a=ok" {
		t.Errorf("a sequence in progress: holding %q, in a sequence %v", s.Holding(), s.InSequence())
	}
	if s.Flush() != nil || s.Invalid != 1 || s.InSequence() {
		t.Errorf("a sequence in progress, flushed: %d invalid", s.Invalid)
	}
	if SegmentDA1.String() != "da1" || SegmentKind(7).String() != "SegmentKind(7)" {
		t.Error("SegmentKind.String")
	}
}

func TestDetector(t *testing.T) {
	at := func(ms int64) time.Time { return time.UnixMilli(ms) }
	reply := func(payload string) Reply {
		r, _ := ReplyOf(host(Control{{"a", "ok"}, {"n", "1"}, {"re", "q"}}, payload))
		return r
	}
	var d Detector
	if q := d.Start(at(0)); q != Query(1) || d.Deadline != at(1500) {
		t.Fatalf("Start: %q, deadline %v", q, d.Deadline)
	}
	if !d.Reply(reply(`{"v":"0.1","host":"h","version":"0.0.10","passthrough":true}`), at(10)) ||
		d.State != Native || !d.Decided || d.Done || d.Caps.Version != "0.0.10" || !d.Caps.Passthrough {
		t.Fatalf("the host's reply: %+v", d)
	}
	if !d.DA1(at(20)) || !d.Done || !d.Deadline.IsZero() {
		t.Fatalf("the DA1 behind it: %+v", d)
	}
	if d.DA1(at(30)) {
		t.Error("a DA1 after done is detection's")
	}

	// A reply after text answers the query, and changes nothing.
	var late Detector
	late.Start(at(0))
	late.Tick(at(1500))
	if !late.Reply(reply(`{"v":"0.1"}`), at(1600)) || late.State != Text {
		t.Errorf("a late reply: %+v", late)
	}
	other, _ := ReplyOf(host(Control{{"a", "ok"}, {"n", "2"}, {"re", "q"}}, `{}`))
	if late.Reply(other, at(1700)) {
		t.Error("a reply to another query")
	}

	// A reply whose capabilities do not parse still says it is a host.
	var bare Detector
	bare.Start(at(0))
	if !bare.Reply(reply(`[`), at(5)) || bare.State != Native || bare.Caps.V != "" {
		t.Errorf("a reply without capabilities: %+v", bare)
	}
	bare.End(at(6))
	if !bare.Done || bare.State != Native {
		t.Errorf("End after native: %+v", bare)
	}
	if Detecting.String() != "detecting" || Text.String() != "text" || DetectState(9).String() != "DetectState(9)" {
		t.Error("DetectState.String")
	}
}

func TestSendsDrags(t *testing.T) {
	drags := Caps{Events: []string{EventClick, EventDrag}}
	if !Sends(drags, EventDragStart) || !Sends(drags, EventDragEnd) || Sends((Caps{Events: []string{EventClick}}), EventDragEnd) {
		t.Error("Sends for dragstart and dragend")
	}
}

// A Q given wins over the level N implies, in either order.
func TestReplyOptionsUnordered(t *testing.T) {
	for _, cmd := range []string{Hide("s", Q(1), N(4)), Hide("s", N(4), Q(1)), Doc("s", "", Q(1), Detached(), N(4))} {
		if m := decodeOne(t, cmd); Get(m.Control, "q") != "1" || Get(m.Control, "n") != "4" {
			t.Errorf("%q: %v", cmd, m.Control)
		}
	}
}

// A malformed message that interrupts a chunked one makes two (SPEC §3.7),
// one without any control too.
func TestDecoderAbortCount(t *testing.T) {
	var d Decoder
	d.Feed("\x1b]7279;a=ev:s=f:e=submit:t=form:m=1;eyJuYW\x1b\\")
	if _, r := d.Feed("\x1b]7279\x1b\\"); r != Invalid || d.Invalid != 2 {
		t.Errorf("%v, %d invalid", r, d.Invalid)
	}
	if _, r := d.Feed("\x1b]7279;m=0;AAAA\x1b\\"); r != Invalid || d.Invalid != 3 {
		t.Errorf("the chunked one is gone: %v, %d invalid", r, d.Invalid)
	}
}
