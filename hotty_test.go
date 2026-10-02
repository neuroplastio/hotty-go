package hotty

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// split cuts a stream into its OSC sequences (the way a terminal-input parser
// would deliver them), ignoring everything else.
func split(stream string) []string {
	var out []string
	for {
		i := strings.Index(stream, "\x1b]")
		if i < 0 {
			return out
		}
		stream = stream[i:]
		end, n := len(stream), 0
		if j := strings.Index(stream, "\x1b\\"); j >= 0 {
			end, n = j, 2
		}
		if j := strings.IndexByte(stream, '\x07'); j >= 0 && j < end {
			end, n = j, 1
		}
		out = append(out, stream[:end+n])
		stream = stream[end+n:]
	}
}

// decodeAll decodes every message in a stream, and counts the invalid ones.
func decodeAll(stream string) (msgs []Message, invalid int) {
	var d Decoder
	for _, seq := range split(stream) {
		if m, r := d.Feed(seq); r == Complete {
			msgs = append(msgs, m)
		}
	}
	return msgs, d.Invalid
}

func decodeOne(t *testing.T, cmd string) Message {
	t.Helper()
	ms, invalid := decodeAll(cmd)
	if len(ms) != 1 || invalid != 0 {
		t.Fatalf("%q: %d messages, %d invalid", cmd, len(ms), invalid)
	}
	return ms[0]
}

// noise is text that does not compress, so it must be chunked.
func noise(n int, seed uint32) string {
	var b strings.Builder
	x := seed
	for b.Len() < n {
		x = x*1664525 + 1013904223
		b.WriteByte(byte('a' + x>>24%26))
	}
	return b.String()
}

func TestCommandsRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		control map[string]string
		payload string
	}{
		{"doc", Doc("card", "<p>hello</p>"), map[string]string{"a": "doc", "s": "card", "q": "1"}, "<p>hello</p>"},
		{"doc detached", Doc("card", "<p>hello</p>", Detached()), map[string]string{"a": "doc", "s": "card", "d": "1", "q": "1"}, "<p>hello</p>"},
		{"doc detached, numbered", Doc("card", "<p>hello</p>", Detached(), N(3)), map[string]string{"a": "doc", "s": "card", "d": "1", "n": "3", "q": "0"}, "<p>hello</p>"},
		{"place auto", Place("card", Placement{Cols: 40}), map[string]string{"a": "place", "s": "card", "c": "40", "r": "auto", "q": "1"}, ""},
		{"place", Place("card", Placement{Cols: 40, Rows: 3, Z: -2, Press: true, KeepCursor: true}),
			map[string]string{"a": "place", "s": "card", "c": "40", "r": "3", "z": "-2", "p": "1", "C": "1", "q": "1"}, ""},
		{"hide", Hide("card"), map[string]string{"a": "hide", "s": "card", "q": "2"}, ""},
		{"text", SetText("card", "clock", "12:00"), map[string]string{"a": "delta", "s": "card", "op": "text", "t": "clock", "q": "2"}, "12:00"},
		{"var", SetVar("dash", "cpu", "p", "42"), map[string]string{"a": "delta", "s": "dash", "op": "var", "t": "cpu", "k": "p", "q": "2"}, "42"},
		{"attr", SetAttr("dash", "cpu", "class", "hot"), map[string]string{"a": "delta", "s": "dash", "op": "attr", "t": "cpu", "k": "class", "q": "2"}, "hot"},
		{"unattr", RemoveAttr("dash", "cpu", "class"), map[string]string{"a": "delta", "s": "dash", "op": "unattr", "t": "cpu", "k": "class", "q": "2"}, ""},
		{"morph by ids", MorphTo("dash", "", "<b id=x>1</b>"), map[string]string{"a": "delta", "s": "dash", "op": "morph", "q": "2"}, "<b id=x>1</b>"},
		{"append", Delta("log", OpAppend, "lines", "", []byte("<li>x</li>")), map[string]string{"a": "delta", "s": "log", "op": "append", "t": "lines", "q": "2"}, "<li>x</li>"},
		{"res", Res("logo", "image/svg+xml", []byte("<svg/>")), map[string]string{"a": "res", "id": "logo", "type": "image/svg+xml", "q": "2"}, "<svg/>"},
		{"del res", DelRes("logo"), map[string]string{"a": "del", "id": "logo", "q": "2"}, ""},
		{"del", Del("card"), map[string]string{"a": "del", "s": "card", "q": "2"}, ""},
		{"del all", DelAll(), map[string]string{"a": "del", "q": "2"}, ""},
		{"detach", Detach("card"), map[string]string{"a": "detach", "s": "card", "q": "2"}, ""},
		{"focus", Focus("form", "name"), map[string]string{"a": "focus", "s": "form", "t": "name", "q": "2"}, ""},
		{"focus surface", Focus("form", ""), map[string]string{"a": "focus", "s": "form", "q": "2"}, ""},
		{"blur", Blur("form"), map[string]string{"a": "blur", "s": "form", "q": "2"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := decodeOne(t, c.cmd)
			if !reflect.DeepEqual(m.Control, c.control) || string(m.Payload) != c.payload {
				t.Errorf("got %v %q", m.Control, m.Payload)
			}
		})
	}
}

func TestOptions(t *testing.T) {
	// N numbers the command and asks for its reply.
	if m := decodeOne(t, Place("x", Placement{Cols: 10}, N(7))); m.Get("n") != "7" || m.Get("q") != "0" {
		t.Errorf("N: %v", m.Control)
	}
	// Q after N asks for less; the key is sent once.
	cmd := SetText("x", "t", "v", N(3), Q(ReplyOnError))
	if m := decodeOne(t, cmd); m.Get("n") != "3" || m.Get("q") != "1" || strings.Count(cmd, "q=") != 1 {
		t.Errorf("N, Q: %q", cmd)
	}
	if m := decodeOne(t, Doc("x", "<p>", Q(NoReply))); m.Get("q") != "2" {
		t.Errorf("Q: %v", m.Control)
	}
}

// A value can never end a command or add a key: what the grammar does not
// allow becomes '_', a character at a time (SPEC §3.2).
func TestValuesCannotInject(t *testing.T) {
	cmd := SetText("card", "x:q=0;AAAA\x1b\\", "payload")
	if got := split(cmd); len(got) != 1 {
		t.Fatalf("%d sequences: %q", len(got), cmd)
	}
	m := decodeOne(t, cmd)
	if m.Get("t") != `x_q_0_AAAA_\` || m.Get("q") != "2" || string(m.Payload) != "payload" {
		t.Errorf("got %v %q", m.Control, m.Payload)
	}
	if m := decodeOne(t, SetAttr("s", "café", "data-x", "1")); m.Get("t") != "caf_" {
		t.Errorf("non-ASCII: %q", m.Get("t"))
	}
	if got := Encode(Control{{"a", "q"}}, nil); got != "\x1b]7279;a=q\x1b\\" {
		t.Errorf("clean values are sent as they are: %q", got)
	}
}

func TestNames(t *testing.T) {
	for name, valid := range map[string]bool{
		"card": true, "showhot-4121-doc1": true, "A_z-09": true, strings.Repeat("x", 64): true,
		"": false, "no/slash": false, "dot.ted": false, "café": false, strings.Repeat("x", 65): false,
	} {
		if ValidName(name) != valid {
			t.Errorf("ValidName(%q) = %v", name, !valid)
		}
	}
	for in, want := range map[string]string{
		"card": "card", "my tool.v2": "my_tool_v2", "café": "caf_", "": "_",
		strings.Repeat("ab", 40): strings.Repeat("ab", 32),
	} {
		got := SurfaceName(in)
		if got != want || !ValidName(got) {
			t.Errorf("SurfaceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlacement(t *testing.T) {
	cases := []struct {
		name string
		p    Placement
		want string // the control, after a=place:s=x:
	}{
		{"auto rows", Placement{Cols: 40}, "c=40:r=auto"},
		{"whole window", Placement{Cols: 30, Rows: 10, Window: Window{0, 0, 30, 10}}, "c=30:r=10"},
		{"window", Placement{Cols: 30, Rows: 10, Window: Window{0, 3, 30, 4}}, "c=30:r=10:x=0:y=3:w=30:h=4"},
		// With auto rows nothing is whole, so a window is always sent.
		{"window on auto", Placement{Cols: 30, Window: Window{0, 0, 1, 1}}, "c=30:r=auto:x=0:y=0:w=1:h=1"},
		{"z, press, cursor", Placement{Cols: 5, Rows: 1, Z: 1, Press: true, KeepCursor: true}, "c=5:r=1:z=1:p=1:C=1"},
		{"fit", Placement{Cols: 5, Press: true, Fit: true}, "c=5:r=auto:p=1:f=1"},
		{"hover", Placement{Cols: 5, Fit: true, Hover: true}, "c=5:r=auto:f=1:v=1"},
	}
	for _, c := range cases {
		want := "\x1b]7279;a=place:s=x:" + c.want + ":q=1\x1b\\"
		if got := Place("x", c.p); got != want {
			t.Errorf("%s: %q, want %q", c.name, got, want)
		}
	}
}

func TestPlaceAtKeepsTheCursor(t *testing.T) {
	got := PlaceAt("chart", 4, 2, Placement{Cols: 30, Rows: 10})
	want := "\x1b7\x1b[3;5H\x1b]7279;a=place:s=chart:c=30:r=10:C=1:q=1\x1b\\\x1b8"
	if got != want {
		t.Errorf("PlaceAt = %q, want %q", got, want)
	}
	if got := PlaceAt("frame", 0, 0, Placement{Cols: 30, Rows: 10, Z: 1}, N(4)); !strings.Contains(got, "a=place:s=frame:c=30:r=10:z=1:C=1:q=0:n=4") {
		t.Errorf("PlaceAt with z and N = %q", got)
	}
}

func TestQuery(t *testing.T) {
	if got, want := Query(7), "\x1b]7279;a=q:n=7\x1b\\\x1b[c"; got != want {
		t.Errorf("Query = %q, want %q", got, want)
	}
}

func TestSync(t *testing.T) {
	if got := Sync("a", "b"); got != "\x1b[?2026hab\x1b[?2026l" {
		t.Errorf("Sync = %q", got)
	}
}

func TestLargePayloadsAreCompressedAndChunked(t *testing.T) {
	html := "<pre>" + noise(20000, 1) + "</pre>"
	cmd := Doc("big", html, Q(NoReply))
	seqs := split(cmd)
	if len(seqs) < 2 {
		t.Fatalf("expected chunks, got %d sequence(s)", len(seqs))
	}
	for i, s := range seqs {
		payload := s[strings.LastIndexByte(s, ';')+1 : len(s)-2]
		if len(payload) > Chunk {
			t.Errorf("chunk %d carries %d bytes", i, len(payload))
		}
		// Continuation chunks carry m, and q since the message has one.
		if i > 0 && !strings.HasPrefix(s, "\x1b]7279;m=") || i > 0 && !strings.Contains(s, ":q=2;") {
			t.Errorf("continuation chunk %d: %q", i, s[:min(30, len(s))])
		}
	}
	if last := seqs[len(seqs)-1]; !strings.HasPrefix(last, "\x1b]7279;m=0:q=2;") {
		t.Errorf("the last chunk is not m=0: %q", last[:20])
	}
	m := decodeOne(t, cmd)
	if string(m.Payload) != html {
		t.Fatal("chunks did not reassemble")
	}
	if _, ok := m.Control["o"]; ok {
		t.Error("o is the envelope's, not the message's")
	}

	// Repetitive markup compresses into one sequence.
	rep := strings.Repeat("<li>row</li>", 500)
	c := Doc("list", rep)
	if !strings.Contains(c, ":o=z;") || len(c) > len(rep)/4 {
		t.Errorf("repetitive markup was not compressed (%d bytes)", len(c))
	}
	if m := decodeOne(t, c); string(m.Payload) != rep {
		t.Error("compressed payload did not decode")
	}
	// Short payloads are not worth it.
	if c := SetText("s", "t", "short"); strings.Contains(c, "o=z") {
		t.Errorf("a short payload was compressed: %q", c)
	}
}

// A document the program only shows is detached in the command that sends
// it; chunked, d=1 is the first chunk's alone.
func TestDocDetachedChunked(t *testing.T) {
	text := noise(20000, 7)
	seqs := split(Doc("big", text, Detached()))
	if len(seqs) < 2 || !strings.Contains(seqs[0], ":d=1:") {
		t.Fatalf("%d chunks", len(seqs))
	}
	for _, s := range seqs[1:] {
		if strings.Contains(s, "d=1") {
			t.Errorf("a continuation chunk carries d: %q", s[:30])
		}
	}
	if m := decodeOne(t, strings.Join(seqs, "")); m.Get("d") != "1" || string(m.Payload) != text {
		t.Error("the chunks did not reassemble a detached document")
	}
}

func TestDecoder(t *testing.T) {
	b64 := func(s string) string {
		return strings.TrimSuffix(Encode(Control{{"x", "1"}}, []byte(s)), "\x1b\\")[len("\x1b]7279;x=1;"):]
	}
	cases := []struct {
		name    string
		seqs    []string
		results []Result
	}{
		{"not hotty", []string{"\x1b]52;c;aGk=\x07"}, []Result{NotHotty}},
		{"another number that starts alike", []string{"\x1b]72790;a=q\x07"}, []Result{NotHotty}},
		{"BEL", []string{"\x1b]7279;a=q:n=1\x07"}, []Result{Complete}},
		{"no payload, no semicolon", []string{"\x1b]7279;a=del\x1b\\"}, []Result{Complete}},
		{"no control", []string{"\x1b]7279\x1b\\"}, []Result{Invalid}},
		{"empty control", []string{"\x1b]7279;;eA==\x1b\\"}, []Result{Invalid}},
		{"a pair without =", []string{"\x1b]7279;a=doc:s\x1b\\"}, []Result{Invalid}},
		{"a key that starts with a digit", []string{"\x1b]7279;1a=doc\x1b\\"}, []Result{Invalid}},
		{"a key twice", []string{"\x1b]7279;a=doc:a=del\x1b\\"}, []Result{Invalid}},
		{"not base64", []string{"\x1b]7279;a=doc:s=x;!!!!\x1b\\"}, []Result{Invalid}},
		{"not zlib", []string{"\x1b]7279;a=doc:s=x:o=z;" + b64("plain") + "\x1b\\"}, []Result{Invalid}},
		{"a continuation with nothing to continue", []string{"\x1b]7279;m=0;eA==\x1b\\"}, []Result{Invalid}},
		{"chunks", []string{"\x1b]7279;a=doc:s=x:m=1;PHA+\x1b\\", "\x1b]7279;m=1:q=2;aGk8\x1b\\", "\x1b]7279;m=0:q=2;L3A+\x1b\\"},
			[]Result{Partial, Partial, Complete}},
		// A bare a=del is a whole command, not a continuation: it aborts
		// the chunked message and is decoded itself.
		{"an interrupting bare command", []string{"\x1b]7279;a=doc:s=x:m=1;PHA+\x1b\\", "\x1b]7279;a=del\x1b\\"}, []Result{Partial, Complete}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var d Decoder
			for i, seq := range c.seqs {
				if _, r := d.Feed(seq); r != c.results[i] {
					t.Errorf("sequence %d: %v, want %v", i, r, c.results[i])
				}
			}
		})
	}

	// The interrupted message counts as invalid, and what interrupted it
	// is itself.
	var d Decoder
	d.Feed("\x1b]7279;a=doc:s=x:m=1;PHA+\x1b\\")
	m, r := d.Feed("\x1b]7279;a=del\x1b\\")
	if r != Complete || m.Get("a") != "del" || len(m.Control) != 1 || d.Invalid != 1 {
		t.Errorf("after the abort: %v %v, %d invalid", r, m.Control, d.Invalid)
	}

	// Base64 without padding and with whitespace.
	if m, r := d.Feed("\x1b]7279;a=ev;aG\r\nk\x1b\\"); r != Complete || string(m.Payload) != "hi" {
		t.Errorf("lenient base64: %v %q", r, m.Payload)
	}
}

func TestResultString(t *testing.T) {
	for r, want := range map[Result]string{NotHotty: "not hotty", Partial: "partial", Complete: "complete", Invalid: "invalid", 9: "Result(9)"} {
		if r.String() != want {
			t.Errorf("%d: %q", int(r), r.String())
		}
	}
}

func TestControlGet(t *testing.T) {
	c := Control{{"a", "doc"}}.With("s", "x")
	if v, ok := c.Get("s"); !ok || v != "x" {
		t.Errorf("Get(s) = %q, %v", v, ok)
	}
	if _, ok := c.Get("q"); ok {
		t.Error("Get(q) found a missing key")
	}
}

// host encodes what a host would send.
func host(ctl Control, payload string) Message {
	var d Decoder
	m, _ := d.Feed(Encode(ctl, []byte(payload)))
	return m
}

func TestEvents(t *testing.T) {
	m := host(Control{{"a", "ev"}, {"s", "form"}, {"e", "submit"}, {"t", "f"}}, `{"name":"Ada","n":3,"ok":true,"none":null}`)
	ev, ok := m.Event()
	if !ok || ev.Kind != EventSubmit || ev.Surface != "form" || ev.Target != "f" {
		t.Fatalf("event: %+v", ev)
	}
	if got, want := ev.Fields(), map[string]string{"name": "Ada", "n": "3", "ok": "true"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Fields = %v", got)
	}

	change, _ := host(Control{{"a", "ev"}, {"s", "f"}, {"e", "change"}, {"t", "box"}}, `{"checked":true,"value":"on"}`).Event()
	if c, ok := change.Checked(); !ok || !c || change.Value() != "on" {
		t.Errorf("checkbox change: %v %v %q", c, ok, change.Value())
	}
	if _, ok := ev.Checked(); ok {
		t.Error("a submit has no checked")
	}

	link, _ := host(Control{{"a", "ev"}, {"s", "post"}, {"e", "click"}, {"t", ""}}, `{"href":"../about","url":"https://example.com/about"}`).Event()
	if href, url, ok := link.Link(); !ok || href != "../about" || url != "https://example.com/about" {
		t.Errorf("Link = %q %q %v", href, url, ok)
	}
	button, _ := host(Control{{"a", "ev"}, {"s", "card"}, {"e", "click"}, {"t", "go"}}, "").Event()
	if _, _, ok := button.Link(); ok || button.Value() != "" {
		t.Error("a button's click is not a link's")
	}

	resize, _ := host(Control{{"a", "ev"}, {"s", "card"}, {"e", "resize"}}, `{"w":360,"h":72.5}`).Event()
	if w, h, ok := resize.Size(); !ok || w != 360 || h != 72.5 {
		t.Errorf("Size = %v %v %v", w, h, ok)
	}
	if _, _, ok := button.Size(); ok {
		t.Error("a click has no size")
	}

	fit, _ := host(Control{{"a", "ev"}, {"s", "card"}, {"e", "fit"}, {"t", ""}}, `{"r":7}`).Event()
	if r, ok := fit.FitRows(); !ok || r != 7 {
		t.Errorf("FitRows = %v %v", r, ok)
	}
	for _, bad := range []string{`{"r":0}`, `{}`, `{"r":"7"}`} {
		if _, ok := (Event{Kind: EventFit, Detail: []byte(bad)}).FitRows(); ok {
			t.Errorf("FitRows on %s", bad)
		}
	}
	if _, ok := resize.FitRows(); ok {
		t.Error("a resize has no fit rows")
	}

	over, _ := host(Control{{"a", "ev"}, {"s", "list"}, {"e", "hover"}, {"t", "row4"}}, `{"c":2,"r":4}`).Event()
	if h, ok := over.Hover(); !ok || h.Out || h.Col != 2 || h.Row != 4 || over.Target != "row4" {
		t.Errorf("Hover = %+v %v", h, ok)
	}
	out, _ := host(Control{{"a", "ev"}, {"s", "list"}, {"e", "hover"}, {"t", ""}}, `{"out":true}`).Event()
	if h, ok := out.Hover(); !ok || !h.Out || h.Col != 0 {
		t.Errorf("Hover out = %+v %v", h, ok)
	}
	for _, bad := range []string{`{}`, `{"c":1}`, `[`} {
		if _, ok := (Event{Kind: EventHover, Detail: []byte(bad)}).Hover(); ok {
			t.Errorf("Hover on %s", bad)
		}
	}
	if _, ok := fit.Hover(); ok {
		t.Error("a fit is no hover")
	}

	drag, _ := host(Control{{"a", "ev"}, {"s", "grid"}, {"e", "drag"}, {"t", "c3_1"}}, `{"c":-2,"r":7,"keys":["shift","ctrl"]}`).Event()
	if d, ok := drag.Drag(); !ok || d.Col != -2 || d.Row != 7 || !d.Has("shift") || !d.Has("ctrl") || d.Has("alt") {
		t.Errorf("Drag = %+v %v", d, ok)
	}
	end, _ := host(Control{{"a", "ev"}, {"s", "grid"}, {"e", "dragend"}, {"t", ""}}, `{"c":0,"r":0,"keys":[]}`).Event()
	if d, ok := end.Drag(); !ok || d.Col != 0 || d.Row != 0 || len(d.Keys) != 0 {
		t.Errorf("a dragend outside: %+v %v", d, ok)
	}
	if _, ok := button.Drag(); ok {
		t.Error("a click is no drag")
	}
	if _, ok := (Event{Kind: EventDragStart, Detail: []byte(`{"r":1}`)}).Drag(); ok {
		t.Error("a drag's detail without its column")
	}

	if _, ok := host(Control{{"a", "ok"}, {"re", "doc"}}, "").Event(); ok {
		t.Error("a reply is not an event")
	}
}

func TestReplies(t *testing.T) {
	m := host(Control{{"a", "err"}, {"s", "x"}, {"re", "delta"}}, `{"code":"ENOTARGET","detail":"gone"}`)
	r, ok := m.Reply()
	if !ok || r.OK || r.Code != ENOTARGET || r.Detail != "gone" || r.Re != "delta" || r.Surface != "x" {
		t.Fatalf("error reply: %+v", r)
	}
	err := r.Err()
	var he *Error
	if !errors.As(err, &he) || he.Code != ENOTARGET || err.Error() != "hotty: delta x: ENOTARGET (gone)" {
		t.Errorf("Err = %v", err)
	}
	if err := (Reply{OK: false, Re: "q", Code: EINVAL}).Err(); err.Error() != "hotty: q: EINVAL" {
		t.Errorf("Err without surface or detail = %v", err)
	}

	r, _ = host(Control{{"a", "ok"}, {"n", "4"}, {"s", "card"}, {"re", "place"}, {"c", "40"}, {"r", "3"}}, "").Reply()
	if !r.OK || r.N != 4 || r.Cols != 40 || r.Rows != 3 || r.Err() != nil {
		t.Errorf("place reply: %+v", r)
	}
	if _, ok := r.Caps(); ok {
		t.Error("a place reply has no caps")
	}
	if _, ok := host(Control{{"a", "ev"}, {"e", "click"}}, "").Reply(); ok {
		t.Error("an event is not a reply")
	}
}

func TestCaps(t *testing.T) {
	r, _ := host(Control{{"a", "ok"}, {"n", "1"}, {"re", "q"}},
		`{"v":"0.1","ops":["text","var"],"events":["click"],"cell":{"w":20,"h":42},"scale":2,"scheme":"light",`+
			`"limits":{"surfaces":64},"net":{"img-src":["https://example.com"]},"host":"xterm-addon-hotty","future":1}`).Reply()
	caps, ok := r.Caps()
	if !ok || caps.V != Version || caps.Host != "xterm-addon-hotty" || caps.Limits["surfaces"] != 64 || caps.Net["img-src"][0] != "https://example.com" {
		t.Fatalf("caps: %+v", caps)
	}
	if w, h := caps.CellCSS(); w != 10 || h != 21 {
		t.Errorf("CellCSS = %v×%v", w, h)
	}
	if !caps.Supports(OpText) || caps.Supports(OpMorph) || !caps.Sends(EventClick) || caps.Sends(EventPress) || !caps.Light() {
		t.Errorf("Supports, Sends, Light: %+v", caps)
	}
	var none Caps
	if w, h := none.CellCSS(); w != 9 || h != 18 {
		t.Errorf("CellCSS before a host said = %v×%v", w, h)
	}
	if !none.Supports(OpMorph) || !none.Sends(EventPress) || none.Light() {
		t.Error("a host that lists nothing")
	}
	// Drags are only where a host lists them.
	if caps.Drags() || none.Drags() || !(Caps{Events: []string{EventClick, EventDrag}}).Drags() {
		t.Error("Drags")
	}
	// So is hover.
	if caps.Hovers() || none.Hovers() || !(Caps{Events: []string{EventHover}}).Hovers() {
		t.Error("Hovers")
	}
	bad, _ := host(Control{{"a", "ok"}, {"re", "q"}}, `{"v":`).Reply()
	if _, ok := bad.Caps(); ok {
		t.Error("caps from bad JSON")
	}
}

// vectorsFile is the conformance vectors this package is tested with: a
// copy of neuroplastio/hotty's conformance/vectors.json (make vectors).
const vectorsFile = "testdata/conformance/vectors.json"

// The shared conformance vectors' wire cases: every stream decodes into
// its commands, and reports its malformed ones.
func TestWireVectors(t *testing.T) {
	data, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Version string `json:"version"`
		Wire    []struct {
			Name     string `json:"name"`
			Stream   string `json:"stream"`
			Commands []struct {
				Control map[string]string `json:"control"`
				Payload string            `json:"payload"`
			} `json:"commands"`
			Invalid int `json:"invalid"`
		} `json:"wire"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if v.Version != Version {
		t.Errorf("the vectors are for %s, this package for %s", v.Version, Version)
	}
	for _, w := range v.Wire {
		t.Run(w.Name, func(t *testing.T) {
			got, invalid := decodeAll(w.Stream)
			if invalid != w.Invalid {
				t.Errorf("%d invalid, want %d", invalid, w.Invalid)
			}
			if len(got) != len(w.Commands) {
				t.Fatalf("%d commands, want %d", len(got), len(w.Commands))
			}
			for i, c := range w.Commands {
				for k, val := range c.Control {
					if got[i].Control[k] != val {
						t.Errorf("%s=%q, want %q", k, got[i].Control[k], val)
					}
				}
				for _, k := range []string{"m", "o"} {
					if _, ok := got[i].Control[k]; ok {
						t.Errorf("%s is present", k)
					}
				}
				if string(got[i].Payload) != c.Payload {
					t.Errorf("payload %q, want %q", got[i].Payload, c.Payload)
				}
			}
		})
	}
}

// The copy is the spec's, when a checkout of neuroplastio/hotty is next to
// this repository or at HOTTY_DIR.
func TestVectorsAreCurrent(t *testing.T) {
	dir := os.Getenv("HOTTY_DIR")
	if dir == "" {
		for _, d := range []string{"../hotty", "../../hotty/main"} {
			if _, err := os.Stat(filepath.Join(d, "SPEC.md")); err == nil {
				dir = d
				break
			}
		}
	}
	if dir == "" {
		t.Skip("no hotty checkout (HOTTY_DIR)")
	}
	upstream, err := os.ReadFile(filepath.Join(dir, "conformance", "vectors.json"))
	if err != nil {
		t.Skip(err)
	}
	ours, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ours, upstream) {
		t.Errorf("%s differs from %s: run make vectors", vectorsFile, dir)
	}
}
