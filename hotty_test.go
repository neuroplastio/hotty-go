package hotty

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
		{"doc", Doc("card", "<p>hello</p>"), map[string]string{"a": "doc", "s": "card"}, "<p>hello</p>"},
		{"doc detached", Doc("card", "<p>hello</p>", Detached()), map[string]string{"a": "doc", "s": "card", "d": "1"}, "<p>hello</p>"},
		{"doc detached, numbered", Doc("card", "<p>hello</p>", Detached(), N(3)), map[string]string{"a": "doc", "s": "card", "d": "1", "n": "3"}, "<p>hello</p>"},
		{"doc scrolls both ways", Doc("log", "<pre></pre>", Scroll(ScrollVertical|ScrollHorizontal)), map[string]string{"a": "doc", "s": "log", "scroll": "3"}, "<pre></pre>"},
		{"doc scroll 0", Doc("log", "<pre></pre>", Scroll(0)), map[string]string{"a": "doc", "s": "log"}, "<pre></pre>"},
		{"doc scroll as given", Doc("log", "<pre></pre>", Scroll(4)), map[string]string{"a": "doc", "s": "log", "scroll": "4"}, "<pre></pre>"},
		{"place auto", Place("card", Placement{Cols: 40}), map[string]string{"a": "place", "s": "card", "c": "40", "r": "auto"}, ""},
		{"place", Place("card", Placement{Cols: 40, Rows: 3, Z: -2, Press: On, Fit: Off, KeepCursor: true}),
			map[string]string{"a": "place", "s": "card", "c": "40", "r": "3", "z": "-2", "p": "1", "f": "0", "C": "1"}, ""},
		{"hide", Hide("card"), map[string]string{"a": "hide", "s": "card", "q": "2"}, ""},
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

// A delta is one list of ops in a msgpack body (SPEC §6.1); an op has a
// value when it takes one, even empty, and none otherwise.
func TestDelta(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, c := range []struct {
		name string
		cmd  string
		ops  []Op
	}{
		{"text", SetText("card", "clock", "12:00"), []Op{{Op: "text", Target: "clock", Value: str("12:00")}}},
		{"text, empty", SetText("card", "clock", ""), []Op{{Op: "text", Target: "clock", Value: str("")}}},
		{"var", SetVar("dash", "cpu", "p", "42"), []Op{{Op: "var", Target: "cpu", Key: "p", Value: str("42")}}},
		{"attr", SetAttr("dash", "cpu", "class", "hot"), []Op{{Op: "attr", Target: "cpu", Key: "class", Value: str("hot")}}},
		{"unattr", RemoveAttr("dash", "cpu", "class"), []Op{{Op: "unattr", Target: "cpu", Key: "class"}}},
		{"reveal", Reveal("doc", "usage", RevealStart), []Op{{Op: "reveal", Target: "usage", Value: str("start")}}},
		{"reveal nearest", Reveal("doc", "usage", RevealNearest), []Op{{Op: "reveal", Target: "usage"}}},
		{"morph by ids", MorphTo("dash", "", "<b id=x>1</b>"), []Op{{Op: "morph", Value: str("<b id=x>1</b>")}}},
		{"ops in order", Delta("log", []Op{OpAppend("lines", "<li>x</li>"), OpRemove("old"), OpPrepend("lines", ""), OpInner("a", "<i></i>"),
			OpReplace("b", "<p></p>"), OpBefore("c", "1"), OpAfter("c", "2"), OpMorph("d", "<d></d>")}),
			[]Op{{Op: "append", Target: "lines", Value: str("<li>x</li>")}, {Op: "remove", Target: "old"}, {Op: "prepend", Target: "lines", Value: str("")},
				{Op: "inner", Target: "a", Value: str("<i></i>")}, {Op: "replace", Target: "b", Value: str("<p></p>")},
				{Op: "before", Target: "c", Value: str("1")}, {Op: "after", Target: "c", Value: str("2")}, {Op: "morph", Target: "d", Value: str("<d></d>")}}},
		{"no ops", Delta("log", nil), []Op{}},
		{"an id outside ASCII, as it is", SetText("card", "café:é", "x"), []Op{{Op: "text", Target: "café:é", Value: str("x")}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := decodeOne(t, c.cmd)
			var d deltaBody
			if !decodeBody(m.Payload, &d) || len(d.Ops) != len(c.ops) || len(c.ops) > 0 && !reflect.DeepEqual(d.Ops, c.ops) {
				t.Errorf("ops %+v, want %+v", d.Ops, c.ops)
			}
			if got := ctlMap(m.Control); got["a"] != "delta" || got["q"] != "2" || len(got) != 3 {
				t.Errorf("control %v", m.Control)
			}
		})
	}
	// The bytes: {"ops": [{"op": "remove", "t": "a"}]}.
	if got := decodeOne(t, Delta("x", []Op{OpRemove("a")})).Payload; fmt.Sprintf("%x", got) != "81a36f70739182a26f70a672656d6f7665a174a161" {
		t.Errorf("body % x", got)
	}
}

// DeltaOf checks a delta whole, and names the first op at fault (SPEC
// §6.1).
func TestDeltaOf(t *testing.T) {
	delta := func(js string) Message {
		return host(Control{{"a", "delta"}, {"s", "x"}}, js)
	}
	ops, err := DeltaOf(delta(`{"ops":[{"op":"text","t":"a","v":"x"},{"op":"remove","t":"a","v":"ignored","x-vendor":1},{"op":"morph","v":"<p id=a></p>"},{"op":"reveal","t":"a"}]}`))
	if err != nil || len(ops) != 4 || ops[1].Op != "remove" || ops[3].Value != nil {
		t.Errorf("ops %+v, %v", ops, err)
	}
	for js, at := range map[string]int{
		`{"ops":[{"op":"text","t":"a","v":"x"},{"op":"frob","t":"a"}]}`:  1,
		`{"ops":[{"op":"text","t":"a","v":"x"},{"op":"attr","t":"a"}]}`:  1,
		`{"ops":[{"op":"var","t":"a","k":"w"},{"op":"unattr","k":"c"}]}`: 1,
		`{"ops":[{"op":"text","t":"a","v":"x"},{"op":"text","v":"y"}]}`:  1,
		`{"ops":[{"op":"reveal","t":"a","v":"middle"}]}`:                 0,
		`{"ops":[{"op":"text","t":"a","v":"x"},"text"]}`:                 1,
		`{"ops":[3]}`:                           0,
		`{"ops":[]}`:                            -1,
		`{"op":"text","t":"a","v":"x"}`:         -1,
		`["text","a","x"]`:                      -1,
		`{"ops":{"op":"text"}}`:                 -1,
		`{"ops":[{"op":"text","t":3,"v":"x"}]}`: 0,
		`{"ops":[{"op":"text","t":"a"},{"op":"remove","t":"a","v":7}]}`: 1,
		`{"ops":[{"op":"text","t":""}]}`:                                0,
		`{"ops":[{"op":"text","t":"a","v":"x"}],"ops":[]}`:              -1,
	} {
		_, err := DeltaOf(delta(js))
		var e *Error
		if !errors.As(err, &e) || e.Code != EINVAL || e.HasOp != (at >= 0) || at >= 0 && e.Op != at {
			t.Errorf("%s: %v (op %d, %v), want EINVAL at %d", js, err, e.Op, e.HasOp, at)
		}
	}
	if _, err := DeltaOf(host(Control{{"a", "hide"}}, "")); err == nil {
		t.Error("not a delta")
	}
}

func TestOptions(t *testing.T) {
	// N numbers the command and asks for its reply.
	// q goes out only where the wire does not say it: not 1 without n, not
	// 0 with it (SPEC §3.5).
	if m := decodeOne(t, Place("x", Placement{Cols: 10}, N(7))); Get(m.Control, "n") != "7" || Has(m.Control, "q") {
		t.Errorf("N: %v", m.Control)
	}
	// Q after N asks for less; the key is sent once, and either order.
	for _, cmd := range []string{SetText("x", "t", "v", N(3), Q(ReplyOnError)), SetText("x", "t", "v", Q(ReplyOnError), N(3))} {
		if m := decodeOne(t, cmd); Get(m.Control, "n") != "3" || Get(m.Control, "q") != "1" || strings.Count(cmd, "q=") != 1 {
			t.Errorf("N, Q: %q", cmd)
		}
	}
	if m := decodeOne(t, Doc("x", "<p>", Q(NoReply))); Get(m.Control, "q") != "2" {
		t.Errorf("Q: %v", m.Control)
	}
	if m := decodeOne(t, Doc("x", "<p>", Q(ReplyAlways))); Get(m.Control, "q") != "0" || Has(m.Control, "n") {
		t.Errorf("Q 0 without n: %v", m.Control)
	}
	if m := decodeOne(t, Hide("x", Q(ReplyOnError))); Has(m.Control, "q") {
		t.Errorf("Q 1 without n: %v", m.Control)
	}
}

// A value can never end a command or add a key: what the grammar does not
// allow becomes '_', a character at a time (SPEC §3.2).
func TestValuesCannotInject(t *testing.T) {
	cmd := Focus("card", "x:q=0;AAAA\x1b\\")
	if got := split(cmd); len(got) != 1 {
		t.Fatalf("%d sequences: %q", len(got), cmd)
	}
	m := decodeOne(t, cmd)
	if Get(m.Control, "t") != `x_q_0_AAAA_\` || Get(m.Control, "q") != "2" {
		t.Errorf("got %v %q", m.Control, m.Payload)
	}
	if m := decodeOne(t, Focus("s", "café")); Get(m.Control, "t") != "caf_" {
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
		{"z, press, cursor", Placement{Cols: 5, Rows: 1, Z: 1, Press: On, KeepCursor: true}, "c=5:r=1:z=1:p=1:C=1"},
		{"fit", Placement{Cols: 5, Press: On, Fit: On}, "c=5:r=auto:p=1:f=1"},
		{"hover", Placement{Cols: 5, Fit: On, Hover: On}, "c=5:r=auto:f=1:v=1"},
		// Off turns an event off, and absent leaves it as it was.
		{"off", Placement{Cols: 5, Press: Off, Fit: On, Hover: Off}, "c=5:r=auto:p=0:f=1:v=0"},
	}
	for _, c := range cases {
		want := "\x1b]7279;a=place:s=x:" + c.want + "\x1b\\"
		if got := Place("x", c.p); got != want {
			t.Errorf("%s: %q, want %q", c.name, got, want)
		}
	}
}

func TestPlaceAtKeepsTheCursor(t *testing.T) {
	got := PlaceAt("chart", 4, 2, Placement{Cols: 30, Rows: 10})
	want := "\x1b7\x1b[3;5H\x1b]7279;a=place:s=chart:c=30:r=10:C=1\x1b\\\x1b8"
	if got != want {
		t.Errorf("PlaceAt = %q, want %q", got, want)
	}
	if got := PlaceAt("frame", 0, 0, Placement{Cols: 30, Rows: 10, Z: 1}, N(4)); !strings.Contains(got, "a=place:s=frame:c=30:r=10:z=1:C=1:n=4") {
		t.Errorf("PlaceAt with z and N = %q", got)
	}
}

func TestQuery(t *testing.T) {
	if got, want := Query(7), "\x1b]7279;a=q:n=7:v=0.5\x1b\\\x1b[c"; got != want {
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
		// Continuation chunks carry only m (SPEC §3.4).
		if i > 0 && !strings.HasPrefix(s, "\x1b]7279;m=1;") && !strings.HasPrefix(s, "\x1b]7279;m=0;") {
			t.Errorf("continuation chunk %d: %q", i, s[:min(30, len(s))])
		}
	}
	if last := seqs[len(seqs)-1]; !strings.HasPrefix(last, "\x1b]7279;m=0;") {
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
	if c := Doc("s", "short"); strings.Contains(c, "o=z") {
		t.Errorf("a short payload was compressed: %q", c)
	}
}

// Encode reuses its compressors: each message, one after another or at
// once, carries its own payload and nothing of another's, including one
// that did not compress.
func TestEncodeReusesCompressors(t *testing.T) {
	payloads := []string{
		strings.Repeat("<li>row</li>", 500),
		strings.Repeat("<p>x</p>", 40),
		noise(3000, 7),
		strings.Repeat("<td>cell</td>", 2000),
		strings.Repeat("<li>row</li>", 30),
	}
	check := func(p string) error {
		if m, invalid := decodeAll(Doc("s", p)); len(m) != 1 || invalid != 0 || string(m[0].Payload) != p {
			return fmt.Errorf("a %d-byte payload did not come back", len(p))
		}
		return nil
	}
	for range 2 {
		for _, p := range payloads {
			if err := check(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	errs := make(chan error, 8*len(payloads))
	for i := range 8 {
		go func() {
			for j := range payloads {
				errs <- check(payloads[(i+j)%len(payloads)])
			}
		}()
	}
	for range 8 * len(payloads) {
		if err := <-errs; err != nil {
			t.Error(err)
		}
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
		{"chunks", []string{"\x1b]7279;a=doc:s=x:m=1;PHA+\x1b\\", "\x1b]7279;m=1;aGk8\x1b\\", "\x1b]7279;m=0;L3A+\x1b\\"},
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

	// A further chunk that carries any key but m is another message, which
	// aborts the one in progress (SPEC §3.4).
	var e Decoder
	e.Feed("\x1b]7279;a=doc:s=x:m=1;PHA+\x1b\\")
	if _, r := e.Feed("\x1b]7279;m=0:q=2;L3A+\x1b\\"); r != Complete || e.Invalid != 1 {
		t.Errorf("a chunk with a q: %v, %d invalid", r, e.Invalid)
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
	m := host(Control{{"a", "ev"}, {"s", "form"}, {"e", "submit"}, {"t", "f"}}, `{"fields":[["name","Ada"],["plan","pro"],["plan","team"]]}`)
	ev, ok := EventOf(m)
	if !ok || ev.Kind != EventSubmit || ev.Surface != "form" || ev.Target != "f" {
		t.Fatalf("event: %+v", ev)
	}
	// In tree order, two fields of one name two pairs.
	if got, want := ev.Fields, []FormField{{"name", "Ada"}, {"plan", "pro"}, {"plan", "team"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Fields = %v", got)
	}
	// A pair that is not two strings, or fields that are not pairs: the
	// detail does not decode.
	for _, bad := range []string{`{"fields":[["n",3]]}`, `{"fields":[["n"]]}`, `{"fields":[["n","a","b"]]}`, `{"fields":{"n":"a"}}`, `{"name":"Ada"}`} {
		if ev, _ := EventOf(host(Control{{"a", "ev"}, {"s", "form"}, {"e", "submit"}, {"t", "f"}}, bad)); ev.Fields != nil {
			t.Errorf("Fields from %s = %v", bad, ev.Fields)
		}
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

	fit, _ := EventOf(host(Control{{"a", "ev"}, {"s", "card"}, {"e", "fit"}, {"t", ""}}, `{"r":7}`))
	if fit.FitRows != 7 {
		t.Errorf("FitRows = %v", fit.FitRows)
	}
	for _, bad := range []string{`{"r":7.0}`, `{}`, `{"r":"7"}`} {
		if ev, _ := EventOf(host(Control{{"a", "ev"}, {"e", "fit"}}, bad)); ev.FitRows != 0 {
			t.Errorf("FitRows on %s", bad)
		}
	}
	if fit.Mods != 0 || fit.Target != "" {
		t.Error("a fit has no mods and no target")
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

	drag, _ := EventOf(host(Control{{"a", "ev"}, {"s", "grid"}, {"e", "drag"}, {"t", "c3_1"}}, `{"c":-2,"r":7,"mods":5}`))
	if d := drag.Drag; d == nil || d.Col != -2 || d.Row != 7 || drag.Mods != ModShift|ModCtrl || d.HasX || d.HasY {
		t.Errorf("Drag = %+v, mods %d", d, drag.Mods)
	}
	steps, _ := EventOf(host(Control{{"a", "ev"}, {"s", "grid"}, {"e", "dragend"}, {"t", ""}}, `{"c":0,"r":0,"x":0}`))
	if d := steps.Drag; d == nil || d.Col != 0 || steps.Mods != 0 || !d.HasX || d.X != 0 || d.HasY {
		t.Errorf("a dragend with a step: %+v", d)
	}
	over2, _ := EventOf(host(Control{{"a", "ev"}, {"s", "list"}, {"e", "drag"}, {"t", "row"}}, `{"c":1,"r":2,"x":3,"ty":1}`))
	if d := over2.Drag; d == nil || !d.HasX || d.X != 3 || d.HasTX || !d.HasTY || d.TY != 1 {
		t.Errorf("a drag with a step in t: %+v", d)
	}
	if button.Drag != nil {
		t.Error("a click is no drag")
	}
	if ev, _ := EventOf(host(Control{{"a", "ev"}, {"e", "drag"}}, `{"c":1,"r":0,"x":2.5}`)); ev.Drag != nil {
		t.Error("a drag with a step that is not an int")
	}

	env, _ := EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "click"}, {"t", "env"}}, `{"value":"staging","area":{"c":2,"r":0,"w":12,"h":1}}`))
	if env.Area == nil || *env.Area != (Area{Col: 2, Row: 0, W: 12, H: 1}) || env.Value != "staging" {
		t.Errorf("Area = %+v", env.Area)
	}
	title, _ := EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "press"}, {"t", "title"}}, `{"area":{"c":-1,"r":3,"w":5,"h":2},"mods":2}`))
	if a := title.Area; a == nil || a.Col != -1 || a.H != 2 || title.Mods != ModAlt {
		t.Errorf("a press's Area = %+v, mods %d", a, title.Mods)
	}
	// A press with no element and a modifier has a detail of mods alone.
	if p, _ := EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "press"}}, `{"mods":8}`)); p.Mods != ModSuper || p.Area != nil || p.Target != "" {
		t.Errorf("a press on nothing: %+v", p)
	}
	if env.Mods != 0 {
		t.Error("a click with no mods has none")
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
		{Surface: "f", Kind: EventClick, Target: "go", Value: "yes", Area: &Area{Col: 1, Row: 2, W: 3, H: 1}, Mods: ModShift | ModSuper, Count: 1},
		{Surface: "f", Kind: EventClick, Target: "go", Area: &Area{Col: 1, Row: 2, W: 3, H: 1}, Count: 2},
		{Surface: "f", Kind: EventClick, Target: "", Link: &Link{Href: "/docs", URL: "https://example.com/docs"}, Count: 1},
		{Surface: "f", Kind: EventClick, Target: "plain", Count: 1},
		{Surface: "f", Kind: EventPress, Target: "t", Area: &Area{Col: -1, Row: 0, W: 2, H: 2}, Mods: ModAlt},
		{Surface: "f", Kind: EventPress, Mods: ModCtrl},
		{Surface: "f", Kind: EventChange, Target: "box", Checked: &on, Value: "on"},
		{Surface: "f", Kind: EventChange, Target: "name", Value: ""},
		{Surface: "f", Kind: EventInput, Target: "q", Value: "ab"},
		{Surface: "f", Kind: EventSubmit, Target: "form", Fields: []FormField{{"name", "Ada"}, {"name", "Bo"}}},
		{Surface: "f", Kind: EventFit, FitRows: 7},
		{Surface: "g", Kind: EventDrag, Target: "c1", Mods: ModShift, Drag: &Drag{Col: -2, Row: 5, X: x, HasX: true}},
		{Surface: "g", Kind: EventDragEnd, Drag: &Drag{Col: 1, Row: 0}},
		{Surface: "g", Kind: EventDrag, Target: "col", Drag: &Drag{Col: 1, Row: 2, TX: 0, HasTX: true, TY: 2, HasTY: true}},
		{Surface: "g", Kind: EventHover, Target: "c1", Hover: &Hover{Col: 0, Row: 4}},
		{Surface: "g", Kind: EventHover, Hover: &Hover{Out: true}},
		{Surface: "d", Kind: EventView, View: &View{Areas: map[string]Area{"intro": {Col: 0, Row: -3, W: 40, H: 5}, "usage": {Col: 0, Row: 2, W: 40, H: 9}}}},
		{Surface: "d", Kind: EventView, View: &View{Areas: map[string]Area{}, AtEnd: true}},
		{Surface: "f", Kind: EventSubmit, Target: "form"},
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
	if !ok || r.OK || r.Code != ENOTARGET || r.Detail != "gone" || r.Re != "delta" || r.Surface != "x" || r.HasOp {
		t.Fatalf("error reply: %+v", r)
	}
	// i names the op at fault; 0 is an index.
	for _, i := range []int{0, 3} {
		if r, _ := ReplyOf(decodeOne(t, ReplyDeltaErr("2", "x", i, ENOTARGET, "gone"))); !r.HasOp || r.Op != i || r.Code != ENOTARGET || r.N != 2 {
			t.Errorf("ReplyDeltaErr(%d) read back: %+v", i, r)
		}
	}
	if r, _ := ReplyOf(host(Control{{"a", "err"}, {"re", "delta"}, {"i", "x"}}, "")); r.HasOp {
		t.Error("an i that is no number")
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
		`{"v":"0.5","cellcss":{"w":10.0,"h":21.0},"scale":2.0,"scheme":"light","features":["hover","menu","future"],`+
			`"limits":{"surfaces":64},"net":{"img-src":["https://example.com"]},"host":"xterm-addon-hotty","future":1}`))
	caps := r.Caps
	if caps == nil || caps.V != Version || caps.Host != "xterm-addon-hotty" || caps.Limits["surfaces"] != 64 || caps.Net["img-src"][0] != "https://example.com" {
		t.Fatalf("caps: %+v", caps)
	}
	if !bytes.Equal(caps.Raw, r.Message.Payload) {
		t.Error("Raw is not the body")
	}
	if caps.CellCSS != (Size{W: 10, H: 21}) {
		t.Errorf("CellCSS = %+v", caps.CellCSS)
	}
	if !HasFeature(*caps, FeatureHover) || !HasFeature(*caps, FeatureMenu) || HasFeature(*caps, FeatureCount) || HasFeature(*caps, FeaturePassthrough) || !Light(*caps) {
		t.Errorf("HasFeature, Light: %+v", caps)
	}
	var none Caps
	if HasFeature(none, FeatureHover) || Light(none) {
		t.Error("a host that lists nothing")
	}
	for _, bad := range []string{`{"v":`, `{"v":"0.5","scale":2}`, `{"v":"0.5","features":["hover",3]}`, `{"v":"0.5","cellcss":{"w":10,"h":21.0}}`, `["v"]`} {
		if r, _ := ReplyOf(host(Control{{"a", "ok"}, {"re", "q"}}, bad)); r.Caps != nil {
			t.Errorf("caps from %s", bad)
		}
	}
	// What a host writes is what a program reads, and a relay passes on
	// what it does not know.
	var d Decoder
	m, _ := d.Feed(ReplyCaps("1", Caps{V: Version, CellCSS: Size{W: 8, H: 16}, Scale: 2, Features: []string{FeatureCount}}))
	if r, _ := ReplyOf(m); r.Caps == nil || r.Caps.CellCSS.W != 8 || r.Caps.Scale != 2 || !HasFeature(*r.Caps, FeatureCount) {
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
	m := ParseKeymap(Platform{}, strings.Join([]string{"arrow_down=program arrow_up=program end=program j=program", "end=line-end"}, " "))
	for key, want := range map[string]bool{
		"arrow_down": true, "shift+arrow_down": true, "arrow_up": true, "j": true,
		"end": false, "home": false, "enter": false, "shift+j": false, "ctrl+arrow_down": false,
		"tab": false, "escape": false, "": false, "hyper+a": false, "mod+j": false,
	} {
		if got := m.Program(key); got != want {
			t.Errorf("Program(%q) = %v, want %v", key, got, want)
		}
	}
	// A key with shift bound to an action of its own is not looked up
	// without shift.
	if ParseKeymap(Platform{}, "arrow_down=program shift+arrow_down=line-next").Program("shift+arrow_down") {
		t.Error("shift+arrow_down, bound to line-next, went to the program")
	}
	// A key bound to prefix is the program's, and Prefix says it of it
	// alone.
	p := ParseKeymap(Platform{}, "g=prefix j=program")
	if !p.Program("g") || !p.Prefix("g") || !p.Program("j") || p.Prefix("j") || p.Prefix("k") {
		t.Error("prefix and program")
	}
	// In a field's keymap, Lookup says as much.
	f := Resolve(Platform{}, false, "arrow_left=program")
	if !f.Program("arrow_left") || f.Lookup("arrow_left") != "" || f.Program("arrow_right") {
		t.Error("a field's keymap: arrow_left is not the program's, or arrow_right is")
	}
	if g := Resolve(Platform{}, false, "ctrl+k=prefix"); g.Lookup("ctrl+k") != "" || !g.Prefix("ctrl+k") {
		t.Error("a field does not use a key bound to prefix")
	}
	if !f.Binds("shift+arrow_left") || f.Binds("ctrl+key_w") || Resolve(Platform{}, false).Binds("shift+g") {
		t.Error("Binds")
	}
}

// A Platform reads mod as its own modifier (SPEC §10.4).
func TestPlatformReadKey(t *testing.T) {
	for _, c := range []struct {
		p          Platform
		name, want string
	}{
		{Platform{}, "mod+a", "ctrl+a"},
		{Platform{}, "Shift+Mod+z", "ctrl+shift+z"},
		{Platform{Mod: "super"}, "mod+a", "super+a"},
		{Platform{Mod: "super"}, "Ctrl+a", "ctrl+a"},
		{Platform{Mod: "super"}, "Shift+Mod+ArrowLeft", "super+shift+arrow_left"},
		{Platform{Mod: "other"}, "mod+x", "ctrl+x"},
		{Platform{}, "G", "g"},
	} {
		if got, ok := c.p.ReadKey(c.name); !ok || got != c.want {
			t.Errorf("%+v.ReadKey(%q) = %q, %v; want %q", c.p, c.name, got, ok, c.want)
		}
	}
	for _, name := range []string{"mod+nope+a", "mod+ctrl+x", "meta+a", ""} {
		if got, ok := (Platform{}).ReadKey(name); ok {
			t.Errorf("ReadKey(%q) = %q, want none", name, got)
		}
	}
	if _, ok := ParseKey("mod+a"); ok {
		t.Error("ParseKey read mod")
	}
}

// A caps event's field that became absent comes empty, and Merge drops it
// (SPEC §9).
func TestMergeDropsWhatAnEventEmpties(t *testing.T) {
	b := msgp.AppendMapHeader(nil, 4)
	b = msgp.AppendString(msgp.AppendString(b, "mod"), "Control")
	b = msgp.AppendMapHeader(msgp.AppendString(b, "edit"), 1)
	b = msgp.AppendString(msgp.AppendArrayHeader(msgp.AppendString(b, "copy"), 1), "Control+c")
	b = msgp.AppendString(msgp.AppendArrayHeader(msgp.AppendString(b, "features"), 1), "menu")
	b = msgp.AppendMapHeader(msgp.AppendString(b, "cellcss"), 2)
	b = msgp.AppendFloat64(msgp.AppendString(msgp.AppendFloat64(msgp.AppendString(b, "w"), 9), "h"), 18)
	c, ok := CapsOf(b)
	if !ok || len(c.Features) != 1 || len(c.Edit) != 1 {
		t.Fatalf("caps: %+v", c)
	}
	e := msgp.AppendMapHeader(nil, 3)
	e = msgp.AppendMapHeader(msgp.AppendString(e, "edit"), 0)
	e = msgp.AppendArrayHeader(msgp.AppendString(e, "features"), 0)
	e = msgp.AppendString(msgp.AppendString(e, "scheme"), "")
	changed, ok := CapsOf(e)
	if !ok {
		t.Fatal("changed")
	}
	m := Merge(c, changed)
	if m.Edit != nil || m.Features != nil || m.Scheme != "" || m.Mod != "Control" || m.CellCSS.H != 18 {
		t.Errorf("merged: %+v", m)
	}
	var keys []string
	for _, f := range rawFields(m.Raw) {
		keys = append(keys, f.key)
	}
	if !slices.Equal(keys, []string{"mod", "cellcss"}) {
		t.Errorf("merged Raw has %v, want [mod cellcss]", keys)
	}
	// What changed replaces, and the rest stays.
	f := msgp.AppendMapHeader(nil, 1)
	f = msgp.AppendFloat64(msgp.AppendString(msgp.AppendFloat64(msgp.AppendString(msgp.AppendMapHeader(msgp.AppendString(f, "cellcss"), 2), "w"), 10), "h"), 20)
	changed, _ = CapsOf(f)
	if m := Merge(m, changed); m.CellCSS != (Size{W: 10, H: 20}) || m.Mod != "Control" {
		t.Errorf("merged: %+v", m)
	}
}

// nested is a menu of n groups, one in the other, ending in a leaf.
func nested(n int) *MenuTree {
	leaf := MenuItem{Key: "a", Label: "Leaf", Name: "leaf", Enabled: true}
	for ; n > 1; n-- {
		leaf = MenuItem{Key: "a", Label: "Group", Enabled: true, Items: []MenuItem{leaf}}
	}
	return &MenuTree{Items: []MenuItem{leaf}}
}

// testMenu is a leader with a group in it, as a program names its bindings.
var testMenu = &MenuTree{Label: "Files", Items: []MenuItem{
	{Key: "space", Label: "Leader", Enabled: true, Items: []MenuItem{
		{Key: "f", Label: "File", Enabled: true, Items: []MenuItem{
			{Key: "n", Label: "New file", Name: "file-new", Enabled: true},
			{Key: "r", Label: "Rename", Name: "file-rename"},
		}},
		{Key: "q", Label: "Quit", Name: "quit", Enabled: true},
		{Label: "Sort by name", Name: "sort-name", Enabled: true},
	}},
	{Label: "Reload", Name: "reload", Enabled: true},
}}

func TestMenuOf(t *testing.T) {
	var d Decoder
	read := func(cmd string) Message {
		m, r := d.Feed(cmd)
		if r != Complete {
			t.Fatalf("%q: %v", cmd, r)
		}
		return m
	}
	l, err := MenuOf(read(Menu(testMenu)))
	if err != nil || !reflect.DeepEqual(l, testMenu) {
		t.Errorf("a menu: %+v, %v", l, err)
	}
	if l, err := MenuOf(read(Menu(nil))); l != nil || err != nil {
		t.Errorf("no body: %+v, %v", l, err)
	}
	if l, err := MenuOf(read(Menu(nested(MenuDepth)))); err != nil || l == nil {
		t.Errorf("a menu eight deep: %+v, %v", l, err)
	}
	for name, m := range map[string]Message{
		"not a menu":      read(Hide("s")),
		"not a map":       host(Control{{"a", "menu"}}, `[1]`),
		"does not parse":  host(Control{{"a", "menu"}}, `{`),
		"key of a number": host(Control{{"a", "menu"}}, `{"items":[{"key":5}]}`),
		"enabled a str":   host(Control{{"a", "menu"}}, `{"items":[{"enabled":"no"}]}`),
		"label a number":  host(Control{{"a", "menu"}}, `{"label":1}`),
		"items a map":     host(Control{{"a", "menu"}}, `{"items":{}}`),
		"nine deep":       read(Menu(nested(MenuDepth + 1))),
	} {
		var e *Error
		if _, err := MenuOf(m); !errors.As(err, &e) || e.Code != EINVAL {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMenuShowOf(t *testing.T) {
	var d Decoder
	read := func(cmd string) Message {
		m, r := d.Feed(cmd)
		if r != Complete {
			t.Fatalf("%q: %v", cmd, r)
		}
		return m
	}
	if seq, err := MenuShowOf(read(MenuShow([]string{"space", "f"}))); err != nil || !slices.Equal(seq, []string{"space", "f"}) {
		t.Errorf("keys: %v, %v", seq, err)
	}
	for name, m := range map[string]Message{
		"no keys":    read(MenuShow(nil)),
		"empty keys": host(Control{{"a", "menu"}, {"show", "1"}}, `{"seq":[]}`),
		"no seq":     host(Control{{"a", "menu"}, {"show", "1"}}, `{}`),
	} {
		if seq, err := MenuShowOf(m); err != nil || len(seq) != 0 {
			t.Errorf("%s: %v, %v", name, seq, err)
		}
	}
	for name, m := range map[string]Message{
		"not a menu":   read(Hide("s")),
		"not a map":    host(Control{{"a", "menu"}, {"show", "1"}}, `["space"]`),
		"seq a str":    host(Control{{"a", "menu"}, {"show", "1"}}, `{"seq":"space"}`),
		"seq a number": host(Control{{"a", "menu"}, {"show", "1"}}, `{"seq":[1]}`),
	} {
		var e *Error
		if _, err := MenuShowOf(m); !errors.As(err, &e) || e.Code != EINVAL {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !strings.Contains(MenuHide(), "show=0") {
		t.Errorf("MenuHide: %q", MenuHide())
	}
}

// TouchOf reads what Touch writes, and refuses what is no map of areas
// (SPEC §9.6).
func TestTouchOf(t *testing.T) {
	var d Decoder
	read := func(cmd string) Message {
		m, r := d.Feed(cmd)
		if r != Complete {
			t.Fatalf("%q: %v", cmd, r)
		}
		return m
	}
	areas := []TouchArea{{C: 2, R: 3, W: 10, H: 1, Drag: "x"}, {C: 0, R: 5, W: 1, H: 4, Drag: "y"}, {W: 1, H: 1, Drag: "xy"}}
	if got, err := TouchOf(read(Touch(areas))); err != nil || !slices.Equal(got, areas) {
		t.Errorf("areas: %+v, %v", got, err)
	}
	many := make([]TouchArea, TouchMax)
	for i := range many {
		many[i] = TouchArea{C: i, W: 1, H: 1, Drag: "x"}
	}
	if got, err := TouchOf(read(Touch(many))); err != nil || len(got) != TouchMax {
		t.Errorf("%d areas: %d, %v", TouchMax, len(got), err)
	}
	for name, m := range map[string]Message{
		"no body":         read(Touch(nil)),
		"no areas":        host(Control{{"a", "touch"}}, `{"areas":[]}`),
		"a key not known": host(Control{{"a", "touch"}}, `{"areas":[],"more":1}`),
	} {
		if got, err := TouchOf(m); err != nil || got != nil {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
	for name, m := range map[string]Message{
		"not a touch":       read(Hide("s")),
		"not a map":         host(Control{{"a", "touch"}}, `["x"]`),
		"does not parse":    host(Control{{"a", "touch"}}, `{`),
		"no areas key":      host(Control{{"a", "touch"}}, `{}`),
		"areas a map":       host(Control{{"a", "touch"}}, `{"areas":{"c":0}}`),
		"an area no map":    host(Control{{"a", "touch"}}, `{"areas":[1]}`),
		"without h":         host(Control{{"a", "touch"}}, `{"areas":[{"c":0,"r":0,"w":1,"drag":"x"}]}`),
		"without drag":      host(Control{{"a", "touch"}}, `{"areas":[{"c":0,"r":0,"w":1,"h":1,"x":1}]}`),
		"c a float":         host(Control{{"a", "touch"}}, `{"areas":[{"c":1.0,"r":0,"w":1,"h":1,"drag":"x"}]}`),
		"drag a number":     host(Control{{"a", "touch"}}, `{"areas":[{"c":0,"r":0,"w":1,"h":1,"drag":1}]}`),
		"r under 0":         host(Control{{"a", "touch"}}, `{"areas":[{"c":0,"r":-1,"w":1,"h":1,"drag":"x"}]}`),
		"w under 1":         host(Control{{"a", "touch"}}, `{"areas":[{"c":0,"r":0,"w":0,"h":1,"drag":"x"}]}`),
		"drag yx":           host(Control{{"a", "touch"}}, `{"areas":[{"c":0,"r":0,"w":1,"h":1,"drag":"yx"}]}`),
		"257 areas":         read(Touch(append(many, many[0]))),
		"a key given twice": host(Control{{"a", "touch"}}, "\x82\xa5areas\x90\xa5areas\x90"),
	} {
		var e *Error
		if _, err := TouchOf(m); !errors.As(err, &e) || e.Code != EINVAL {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FieldOf reads what SetField writes, and refuses a change no control
// could take (SPEC §6.4).
func TestFieldOf(t *testing.T) {
	var d Decoder
	read := func(cmd string) Message {
		m, r := d.Feed(cmd)
		if r != Complete {
			t.Fatalf("%q: %v", cmd, r)
		}
		return m
	}
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }
	no := false
	for _, c := range []FieldChange{
		{Rev: 2, From: num(0), To: num(2), Text: str("src/")},
		{Rev: 0, Value: str(""), Sel: &[2]int{3, 0}},
		{Rev: 7, Checked: &no},
		{Rev: 1, Sel: &[2]int{0, 0}},
		{Rev: 1, Text: str("x")},
	} {
		got, err := FieldOf(read(SetField("s", "f", c)))
		if err != nil || !reflect.DeepEqual(got, c) {
			t.Errorf("%+v: %+v, %v", c, got, err)
		}
	}
	field := Control{{"a", "field"}, {"s", "s"}, {"t", "f"}}
	if got, err := FieldOf(host(field, `{"rev":1,"value":"a","more":{"x":1}}`)); err != nil || *got.Value != "a" {
		t.Errorf("a key not known: %+v, %v", got, err)
	}
	// A negative position is the host's to check, after ESTALE (SPEC §6.4).
	if got, err := FieldOf(host(field, `{"rev":1,"from":-1,"to":0,"text":"a","sel":[0,-1]}`)); err != nil || *got.From != -1 || got.Sel[1] != -1 {
		t.Errorf("a negative position: %+v, %v", got, err)
	}
	for name, m := range map[string]Message{
		"not a field":       read(Hide("s")),
		"no t":              host(Control{{"a", "field"}, {"s", "s"}}, `{"rev":0,"value":"a"}`),
		"no body":           host(field, ``),
		"not a map":         host(field, `[0]`),
		"no rev":            host(field, `{"value":"a"}`),
		"rev a str":         host(field, `{"rev":"0","value":"a"}`),
		"rev alone":         host(field, `{"rev":0}`),
		"value and text":    host(field, `{"rev":0,"value":"a","text":"b"}`),
		"value and from":    host(field, `{"rev":0,"value":"a","from":0,"to":0}`),
		"from without to":   host(field, `{"rev":0,"from":0,"text":"b"}`),
		"from without text": host(field, `{"rev":0,"from":0,"to":0}`),
		"from past to":      host(field, `{"rev":0,"from":2,"to":1,"text":"b"}`),
		"sel of one":        host(field, `{"rev":0,"sel":[1]}`),
		"sel of a float":    host(field, `{"rev":0,"sel":[0,1.0]}`),
		"checked a str":     host(field, `{"rev":0,"checked":"yes"}`),
	} {
		var e *Error
		if _, err := FieldOf(m); !errors.As(err, &e) || e.Code != EINVAL {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMenuLevelOf(t *testing.T) {
	linux, mac := Platform{Mod: "ctrl"}, Platform{Mod: "super"}
	keys := func(items []MenuItem) (out []string) {
		for _, it := range items {
			out = append(out, it.Key)
		}
		return out
	}
	for _, c := range []struct {
		seq  []string
		want []string // nil: no group
	}{
		{nil, []string{"space", ""}},
		{[]string{"space"}, []string{"f", "q", ""}},
		{[]string{"space", "f"}, []string{"n", "r"}},
		{[]string{"space", "q"}, nil}, // a leaf is no group
		{[]string{"x"}, nil},
		{[]string{"space", "f", "n"}, nil},
		{[]string{""}, nil}, // an item with no key is reached by none
		{[]string{"ctrl+"}, nil},
	} {
		items, ok := MenuLevelOf(testMenu, linux, c.seq)
		if ok != (c.want != nil) || !slices.Equal(keys(items), c.want) {
			t.Errorf("%v: %v, %v; want %v", c.seq, keys(items), ok, c.want)
		}
	}
	if items, ok := MenuLevelOf(nil, linux, nil); ok || items != nil {
		t.Errorf("no menu: %v, %v", items, ok)
	}
	// Keys compare as the platform reads them: mod is ctrl on one and
	// super on the other.
	m := &MenuTree{Items: []MenuItem{{Key: "mod+k", Items: []MenuItem{{Key: "a"}}}}}
	for _, c := range []struct {
		p    Platform
		key  string
		want bool
	}{{linux, "ctrl+k", true}, {linux, "super+k", false}, {mac, "super+k", true}, {mac, "ctrl+k", false}, {mac, "mod+k", true}} {
		if _, ok := MenuLevelOf(m, c.p, []string{c.key}); ok != c.want {
			t.Errorf("%s on %s: %v", c.key, c.p.Mod, ok)
		}
	}
	// The first group of a key decides.
	dup := &MenuTree{Items: []MenuItem{{Key: "a", Label: "leaf"}, {Key: "a", Label: "one", Items: []MenuItem{{Key: "x"}}}, {Key: "a", Label: "two", Items: []MenuItem{{Key: "y"}}}}}
	if items, ok := MenuLevelOf(dup, linux, []string{"a"}); !ok || items[0].Key != "x" {
		t.Errorf("the first group: %v, %v", items, ok)
	}
}

func TestMenuFind(t *testing.T) {
	for name, want := range map[string]string{"file-new": "New file", "sort-name": "Sort by name", "reload": "Reload", "file-rename": "Rename"} {
		if it := MenuFind(testMenu, name); it == nil || it.Label != want {
			t.Errorf("%s: %+v", name, it)
		}
	}
	for _, name := range []string{"", "nothing"} {
		if it := MenuFind(testMenu, name); it != nil {
			t.Errorf("%q: %+v", name, it)
		}
	}
	if MenuFind(nil, "quit") != nil {
		t.Error("no menu")
	}
	// Depth first: a name in the first group beats one after it, and a
	// group's own name beats the items inside it.
	m := &MenuTree{Items: []MenuItem{{Label: "g1", Items: []MenuItem{{Label: "deep", Name: "x"}}}, {Label: "later", Name: "x"}, {Label: "named group", Name: "g", Items: []MenuItem{{Label: "inner", Name: "g"}}}}}
	if it := MenuFind(m, "x"); it == nil || it.Label != "deep" {
		t.Errorf("depth first: %+v", it)
	}
	if it := MenuFind(m, "g"); it == nil || it.Label != "named group" {
		t.Errorf("a group first: %+v", it)
	}
}
