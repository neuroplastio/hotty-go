package hotty

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tinylib/msgp/msgp"
)

// split cuts a stream into its OSC sequences (the way a terminal-input parser
// would deliver them), ignoring everything else.
// ctlMap is a control as a map, for comparing with one.
func ctlMap(c Control) map[string]string {
	m := make(map[string]string, len(c))
	for _, kv := range c {
		m[kv.K] = kv.V
	}
	return m
}

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
		{"doc scrolls both ways", Doc("log", "<pre></pre>", Scroll(ScrollVertical|ScrollHorizontal)), map[string]string{"a": "doc", "s": "log", "scroll": "3", "q": "1"}, "<pre></pre>"},
		{"doc scroll 0", Doc("log", "<pre></pre>", Scroll(0)), map[string]string{"a": "doc", "s": "log", "q": "1"}, "<pre></pre>"},
		{"doc scroll as given", Doc("log", "<pre></pre>", Scroll(4)), map[string]string{"a": "doc", "s": "log", "scroll": "4", "q": "1"}, "<pre></pre>"},
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
			if !reflect.DeepEqual(ctlMap(m.Control), c.control) || string(m.Payload) != c.payload {
				t.Errorf("got %v %q", m.Control, m.Payload)
			}
		})
	}
}

func TestOptions(t *testing.T) {
	// N numbers the command and asks for its reply.
	if m := decodeOne(t, Place("x", Placement{Cols: 10}, N(7))); Get(m.Control, "n") != "7" || Get(m.Control, "q") != "0" {
		t.Errorf("N: %v", m.Control)
	}
	// Q after N asks for less; the key is sent once.
	cmd := SetText("x", "t", "v", N(3), Q(ReplyOnError))
	if m := decodeOne(t, cmd); Get(m.Control, "n") != "3" || Get(m.Control, "q") != "1" || strings.Count(cmd, "q=") != 1 {
		t.Errorf("N, Q: %q", cmd)
	}
	if m := decodeOne(t, Doc("x", "<p>", Q(NoReply))); Get(m.Control, "q") != "2" {
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
	if Get(m.Control, "t") != `x_q_0_AAAA_\` || Get(m.Control, "q") != "2" || string(m.Payload) != "payload" {
		t.Errorf("got %v %q", m.Control, m.Payload)
	}
	if m := decodeOne(t, SetAttr("s", "café", "data-x", "1")); Get(m.Control, "t") != "caf_" {
		t.Errorf("non-ASCII: %q", Get(m.Control, "t"))
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
	if got, want := Query(7), "\x1b]7279;a=q:n=7:v=0.2\x1b\\\x1b[c"; got != want {
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
	if Has(m.Control, "o") {
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
	if m := decodeOne(t, strings.Join(seqs, "")); Get(m.Control, "d") != "1" || string(m.Payload) != text {
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
	if r != Complete || Get(m.Control, "a") != "del" || len(m.Control) != 1 || d.Invalid != 1 {
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
	c := With(Control{{"a", "doc"}}, "s", "x")
	if v, ok := Lookup(c, "s"); !ok || v != "x" {
		t.Errorf("Get(s) = %q, %v", v, ok)
	}
	if _, ok := Lookup(c, "q"); ok {
		t.Error("Get(q) found a missing key")
	}
}

// host encodes what a host would send.
func host(ctl Control, payload string) Message {
	var d Decoder
	m, _ := d.Feed(Encode(ctl, body(payload)))
	return m
}

// body is a body written as JSON, as msgpack: a number with a fraction or
// an exponent a float, any other an int (conformance/README.md, Numbers).
// What is not JSON goes as it is.
func body(js string) []byte {
	if js == "" {
		return nil
	}
	d := json.NewDecoder(strings.NewReader(js))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return []byte(js)
	}
	var buf bytes.Buffer
	w := msgp.NewWriter(&buf)
	if err := w.WriteIntf(typed(v)); err != nil {
		panic(err)
	}
	_ = w.Flush()
	return buf.Bytes()
}

// typed is v with its JSON numbers as ints and floats.
func typed(v any) any {
	switch v := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			f, _ := v.Float64()
			return f
		}
		n, _ := v.Int64()
		return n
	case []any:
		for i := range v {
			v[i] = typed(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = typed(v[k])
		}
	}
	return v
}

func TestEvents(t *testing.T) {
	m := host(Control{{"a", "ev"}, {"s", "form"}, {"e", "submit"}, {"t", "f"}}, `{"name":"Ada","plan":"pro"}`)
	ev, ok := EventOf(m)
	if !ok || ev.Kind != EventSubmit || ev.Surface != "form" || ev.Target != "f" {
		t.Fatalf("event: %+v", ev)
	}
	if got, want := ev.Fields, map[string]string{"name": "Ada", "plan": "pro"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Fields = %v", got)
	}
	// A field that is not a string: the detail does not decode.
	if ev, _ := EventOf(host(Control{{"a", "ev"}, {"s", "form"}, {"e", "submit"}, {"t", "f"}}, `{"name":"Ada","n":3}`)); ev.Fields != nil {
		t.Errorf("Fields with a number = %v", ev.Fields)
	}

	change, _ := EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "change"}, {"t", "box"}}, `{"checked":true,"value":"on"}`))
	if change.Checked == nil || !*change.Checked || change.Value != "on" {
		t.Errorf("checkbox change: %v %q", change.Checked, change.Value)
	}
	if ev.Checked != nil {
		t.Error("a submit has no checked")
	}

	link, _ := EventOf(host(Control{{"a", "ev"}, {"s", "post"}, {"e", "click"}, {"t", ""}}, `{"href":"../about","url":"https://example.com/about"}`))
	if link.Link == nil || *link.Link != (Link{Href: "../about", URL: "https://example.com/about"}) {
		t.Errorf("Link = %+v", link.Link)
	}
	button, _ := EventOf(host(Control{{"a", "ev"}, {"s", "card"}, {"e", "click"}, {"t", "go"}}, ""))
	if button.Link != nil || button.Value != "" || button.Detail != nil {
		t.Error("a button's click is not a link's")
	}

	resize, _ := EventOf(host(Control{{"a", "ev"}, {"s", "card"}, {"e", "resize"}}, `{"w":360.0,"h":72.5}`))
	if resize.Size == nil || *resize.Size != (Size{W: 360, H: 72.5}) {
		t.Errorf("Size = %+v", resize.Size)
	}
	if ev, _ := EventOf(host(Control{{"a", "ev"}, {"s", "card"}, {"e", "resize"}}, `{"w":360,"h":72.5}`)); ev.Size != nil {
		t.Error("a size in an int is not a size")
	}

	fit, _ := EventOf(host(Control{{"a", "ev"}, {"s", "card"}, {"e", "fit"}, {"t", ""}}, `{"r":7}`))
	if fit.FitRows != 7 {
		t.Errorf("FitRows = %v", fit.FitRows)
	}
	for _, bad := range []string{`{"r":7.0}`, `{}`, `{"r":"7"}`} {
		if ev, _ := EventOf(host(Control{{"a", "ev"}, {"e", "fit"}}, bad)); ev.FitRows != 0 {
			t.Errorf("FitRows on %s", bad)
		}
	}
	if resize.FitRows != 0 {
		t.Error("a resize has no fit rows")
	}

	over, _ := EventOf(host(Control{{"a", "ev"}, {"s", "list"}, {"e", "hover"}, {"t", "row4"}}, `{"c":2,"r":4}`))
	if over.Hover == nil || *over.Hover != (Hover{Col: 2, Row: 4}) || over.Target != "row4" {
		t.Errorf("Hover = %+v", over.Hover)
	}
	out, _ := EventOf(host(Control{{"a", "ev"}, {"s", "list"}, {"e", "hover"}, {"t", ""}}, `{"out":true}`))
	if out.Hover == nil || *out.Hover != (Hover{Out: true}) {
		t.Errorf("Hover out = %+v", out.Hover)
	}
	for _, bad := range []string{`{}`, `{"c":1}`, `{"c":1.0,"r":2}`, `[`} {
		if ev, _ := EventOf(host(Control{{"a", "ev"}, {"e", "hover"}}, bad)); ev.Hover != nil {
			t.Errorf("Hover on %s", bad)
		}
	}
	if fit.Hover != nil {
		t.Error("a fit is no hover")
	}

	drag, _ := EventOf(host(Control{{"a", "ev"}, {"s", "grid"}, {"e", "drag"}, {"t", "c3_1"}}, `{"c":-2,"r":7,"keys":["shift","ctrl"]}`))
	if d := drag.Drag; d == nil || d.Col != -2 || d.Row != 7 || !slices.Equal(d.Keys, []string{"shift", "ctrl"}) || d.HasX || d.HasY {
		t.Errorf("Drag = %+v", d)
	}
	steps, _ := EventOf(host(Control{{"a", "ev"}, {"s", "grid"}, {"e", "dragend"}, {"t", ""}}, `{"c":0,"r":0,"keys":[],"x":0}`))
	if d := steps.Drag; d == nil || d.Col != 0 || len(d.Keys) != 0 || !d.HasX || d.X != 0 || d.HasY {
		t.Errorf("a dragend with a step: %+v", d)
	}
	if button.Drag != nil {
		t.Error("a click is no drag")
	}
	if ev, _ := EventOf(host(Control{{"a", "ev"}, {"e", "drag"}}, `{"c":1,"r":0,"keys":[],"x":2.5}`)); ev.Drag != nil {
		t.Error("a drag with a step that is not an int")
	}

	env, _ := EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "click"}, {"t", "env"}}, `{"value":"staging","area":{"c":2,"r":0,"w":12,"h":1}}`))
	if env.Area == nil || *env.Area != (Area{Col: 2, Row: 0, W: 12, H: 1}) || env.Value != "staging" {
		t.Errorf("Area = %+v", env.Area)
	}
	title, _ := EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "press"}, {"t", "title"}}, `{"area":{"c":-1,"r":3,"w":5,"h":2}}`))
	if a := title.Area; a == nil || a.Col != -1 || a.H != 2 {
		t.Errorf("a press's Area = %+v", a)
	}
	for _, bad := range []string{`{}`, `{"area":null}`, `{"area":[2,0,12,1]}`, `{"area":{"c":2,"r":0,"w":12,"h":1.5}}`, `{"area":{"c":2,"r":0,"w":"12","h":1}}`, `[`} {
		if ev, _ := EventOf(host(Control{{"a", "ev"}, {"e", "click"}}, bad)); ev.Area != nil {
			t.Errorf("Area on %s", bad)
		}
	}
	if ev, _ := EventOf(host(Control{{"a", "ev"}, {"e", "change"}}, `{"value":"x","area":{"c":2,"r":0,"w":12,"h":1}}`)); ev.Area != nil || ev.Value != "x" {
		t.Error("a change has no area")
	}

	if _, ok := EventOf(host(Control{{"a", "ok"}, {"re", "doc"}}, "")); ok {
		t.Error("a reply is not an event")
	}
}

// What a host writes is what a program reads.
func TestEncodeEvent(t *testing.T) {
	on, x := true, 3
	for _, e := range []Event{
		{Surface: "f", Kind: EventClick, Target: "go", Value: "yes", Area: &Area{Col: 1, Row: 2, W: 3, H: 1}},
		{Surface: "f", Kind: EventClick, Target: "", Link: &Link{Href: "/docs", URL: "https://example.com/docs"}},
		{Surface: "f", Kind: EventClick, Target: "plain"},
		{Surface: "f", Kind: EventPress, Target: "t", Area: &Area{Col: -1, Row: 0, W: 2, H: 2}},
		{Surface: "f", Kind: EventChange, Target: "box", Checked: &on, Value: "on"},
		{Surface: "f", Kind: EventChange, Target: "name", Value: ""},
		{Surface: "f", Kind: EventInput, Target: "q", Value: "ab"},
		{Surface: "f", Kind: EventSubmit, Target: "form", Fields: map[string]string{"name": "Ada"}},
		{Surface: "f", Kind: EventResize, Size: &Size{W: 320, H: 48.5}},
		{Surface: "f", Kind: EventFit, FitRows: 7},
		{Surface: "g", Kind: EventDrag, Target: "c1", Drag: &Drag{Col: -2, Row: 5, Keys: []string{"shift"}, X: x, HasX: true}},
		{Surface: "g", Kind: EventDragEnd, Drag: &Drag{Col: 1, Row: 0}},
		{Surface: "g", Kind: EventHover, Target: "c1", Hover: &Hover{Col: 0, Row: 4}},
		{Surface: "g", Kind: EventHover, Hover: &Hover{Out: true}},
		{Surface: "g", Kind: EventFocus, Target: "name"},
	} {
		var d Decoder
		m, _ := d.Feed(EncodeEvent(e))
		got, ok := EventOf(m)
		got.Detail = nil
		if !ok || !reflect.DeepEqual(got, e) {
			t.Errorf("%s: wrote %+v, read %+v", e.Kind, e, got)
		}
	}
	// A relay's detail goes on as it came.
	e := Event{Surface: "f", Kind: EventClick, Value: "not this", Detail: body(`{"value":"this","future":[1]}`)}
	var d Decoder
	m, _ := d.Feed(EncodeEvent(e))
	if got, _ := EventOf(m); got.Value != "this" || !bytes.Equal(got.Detail, e.Detail) {
		t.Errorf("a relay's detail: %+v", got)
	}
}

func TestReplies(t *testing.T) {
	m := host(Control{{"a", "err"}, {"s", "x"}, {"re", "delta"}}, `{"code":"ENOTARGET","detail":"gone"}`)
	r, ok := ReplyOf(m)
	if !ok || r.OK || r.Code != ENOTARGET || r.Detail != "gone" || r.Re != "delta" || r.Surface != "x" {
		t.Fatalf("error reply: %+v", r)
	}
	err := Err(r)
	var he *Error
	if !errors.As(err, &he) || he.Code != ENOTARGET || err.Error() != "hotty: delta x: ENOTARGET (gone)" {
		t.Errorf("Err = %v", err)
	}
	if err := Err((Reply{OK: false, Re: "q", Code: EINVAL})); err.Error() != "hotty: q: EINVAL" {
		t.Errorf("Err without surface or detail = %v", err)
	}
	var d Decoder
	written, _ := d.Feed(ReplyErr("3", "x", "delta", ENOTARGET, "gone"))
	if r, _ := ReplyOf(written); r.Code != ENOTARGET || r.Detail != "gone" || r.N != 3 {
		t.Errorf("ReplyErr read back: %+v", r)
	}
	if r, _ := ReplyOf(host(Control{{"a", "err"}, {"re", "doc"}}, `{"code":1}`)); r.OK || r.Code != "" {
		t.Errorf("an error whose body does not decode: %+v", r)
	}

	r, _ = ReplyOf(host(Control{{"a", "ok"}, {"n", "4"}, {"s", "card"}, {"re", "place"}, {"c", "40"}, {"r", "3"}}, ""))
	if !r.OK || r.N != 4 || r.Cols != 40 || r.Rows != 3 || Err(r) != nil {
		t.Errorf("place reply: %+v", r)
	}
	if r.Caps != nil {
		t.Error("a place reply has no caps")
	}
	if _, ok := ReplyOf(host(Control{{"a", "ev"}, {"e", "click"}}, "")); ok {
		t.Error("an event is not a reply")
	}
}

func TestCaps(t *testing.T) {
	r, _ := ReplyOf(host(Control{{"a", "ok"}, {"n", "1"}, {"re", "q"}},
		`{"v":"0.2","ops":["text","var"],"events":["click"],"cell":{"w":20,"h":42},"scale":2.0,"scheme":"light",`+
			`"limits":{"surfaces":64},"net":{"img-src":["https://example.com"]},"host":"xterm-addon-hotty","future":1}`))
	caps := r.Caps
	if caps == nil || caps.V != Version || caps.Host != "xterm-addon-hotty" || caps.Limits["surfaces"] != 64 || caps.Net["img-src"][0] != "https://example.com" {
		t.Fatalf("caps: %+v", caps)
	}
	if !bytes.Equal(caps.Raw, r.Message.Payload) {
		t.Error("Raw is not the body")
	}
	if w, h := CellCSS(*caps); w != 10 || h != 21 {
		t.Errorf("CellCSS = %v×%v", w, h)
	}
	if !Supports(*caps, OpText) || Supports(*caps, OpMorph) || !Sends(*caps, EventClick) || Sends(*caps, EventPress) || !Light(*caps) {
		t.Errorf("Supports, Sends, Light: %+v", caps)
	}
	var none Caps
	if w, h := CellCSS(none); w != 9 || h != 18 {
		t.Errorf("CellCSS before a host said = %v×%v", w, h)
	}
	if !Supports(none, OpMorph) || !Sends(none, EventPress) || Light(none) {
		t.Error("a host that lists nothing")
	}
	// Drags are only where a host lists them.
	if Drags(*caps) || Drags(none) || !Drags((Caps{Events: []string{EventClick, EventDrag}})) {
		t.Error("Drags")
	}
	// So is hover.
	if Hovers(*caps) || Hovers(none) || !Hovers((Caps{Events: []string{EventHover}})) {
		t.Error("Hovers")
	}
	// Scroll only where a host says so.
	scrolls, _ := ReplyOf(host(Control{{"a", "ok"}, {"re", "q"}}, `{"v":"0.2","scroll":true}`))
	if c := scrolls.Caps; c == nil || !c.Scroll || caps.Scroll {
		t.Error("Scroll")
	}
	for _, bad := range []string{`{"v":`, `{"v":"0.2","scale":2}`, `{"v":"0.2","events":["click",3]}`, `["v"]`} {
		if r, _ := ReplyOf(host(Control{{"a", "ok"}, {"re", "q"}}, bad)); r.Caps != nil {
			t.Errorf("caps from %s", bad)
		}
	}
	// What a host writes is what a program reads, and a relay passes on
	// what it does not know.
	var d Decoder
	m, _ := d.Feed(ReplyCaps("1", Caps{V: Version, Cell: &Cell{W: 16, H: 32}, Scale: 2, Events: []string{EventClick}}))
	if r, _ := ReplyOf(m); r.Caps == nil || r.Caps.Cell == nil || r.Caps.Cell.W != 16 || r.Caps.Scale != 2 || !Sends(*r.Caps, EventClick) {
		t.Errorf("ReplyCaps read back: %+v", r.Caps)
	}
	m, _ = d.Feed(ReplyCaps("2", *caps))
	if r, _ := ReplyOf(m); r.Caps == nil || !bytes.Equal(r.Caps.Raw, caps.Raw) {
		t.Error("a relay's caps changed")
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
					if Get(got[i].Control, k) != val {
						t.Errorf("%s=%q, want %q", k, Get(got[i].Control, k), val)
					}
				}
				for _, k := range []string{"m", "o"} {
					if Has(got[i].Control, k) {
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

// Program: a key any focused element's keymap gives the program (SPEC
// §10.2), read from data-keys with no default keymap.
func TestKeymapProgram(t *testing.T) {
	// The root gives the arrows and End to the program; the element takes
	// End back with an action, which counts for nothing outside a field.
	m := ParseKeymap(strings.Join([]string{"ArrowDown=program ArrowUp=program End=program j=program", "End=line-end"}, " "))
	for key, want := range map[string]bool{
		"ArrowDown": true, "Shift+ArrowDown": true, "ArrowUp": true, "j": true,
		"End": false, "Home": false, "Enter": false, "J": false, "Control+ArrowDown": false,
		"Tab": false, "Escape": false, "": false, "Hyper+a": false,
	} {
		if got := m.Program(key); got != want {
			t.Errorf("Program(%q) = %v, want %v", key, got, want)
		}
	}
	// A key with Shift bound to an action of its own is not looked up
	// without Shift.
	if ParseKeymap("ArrowDown=program Shift+ArrowDown=line-next").Program("Shift+ArrowDown") {
		t.Error("Shift+ArrowDown, bound to line-next, went to the program")
	}
	// In a field's keymap, Lookup says as much.
	f := Resolve(false, "ArrowLeft=program")
	if !f.Program("ArrowLeft") || f.Lookup("ArrowLeft") != "" || f.Program("ArrowRight") {
		t.Error("a field's keymap: ArrowLeft is not the program's, or ArrowRight is")
	}
}
