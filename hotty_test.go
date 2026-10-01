package hotty

import (
	"encoding/json"
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

func decodeAll(t *testing.T, stream string) []Message {
	t.Helper()
	var d Decoder
	var out []Message
	for _, seq := range split(stream) {
		if m, ok, _ := d.Feed(seq); ok {
			out = append(out, m)
		}
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		control map[string]string
		payload string
	}{
		{"doc", Doc("card", "<p>hello</p>"), map[string]string{"a": "doc", "s": "card", "q": "1"}, "<p>hello</p>"},
		{"doc detached", DocDetached("card", "<p>hello</p>"), map[string]string{"a": "doc", "s": "card", "d": "1", "q": "1"}, "<p>hello</p>"},
		{"detach", Detach("card"), map[string]string{"a": "detach", "s": "card", "q": "2"}, ""},
		{"text", SetText("card", "clock", "12:00"), map[string]string{"a": "patch", "s": "card", "op": "text", "t": "clock", "q": "2"}, "12:00"},
		{"var", SetVar("dash", "cpu", "p", "42"), map[string]string{"a": "patch", "s": "dash", "op": "var", "t": "cpu", "k": "p", "q": "2"}, "42"},
		{"place auto", Place("card", 40, 0, false, Reply), map[string]string{"a": "place", "s": "card", "c": "40", "r": "auto", "q": "0"}, ""},
		{"focus", Focus("form", "name"), map[string]string{"a": "focus", "s": "form", "t": "name", "q": "2"}, ""},
	}
	for _, c := range cases {
		ms := decodeAll(t, c.cmd)
		if len(ms) != 1 {
			t.Fatalf("%s: %d messages", c.name, len(ms))
		}
		if !reflect.DeepEqual(ms[0].Control, c.control) || string(ms[0].Payload) != c.payload {
			t.Errorf("%s: got %v %q", c.name, ms[0].Control, ms[0].Payload)
		}
	}
}

func TestLargePayloadsAreCompressedAndChunked(t *testing.T) {
	// Random-looking text compresses badly, so this one must be chunked.
	var b strings.Builder
	x := uint32(1)
	for b.Len() < 20000 {
		x = x*1664525 + 1013904223
		b.WriteByte(byte('a' + x>>24%26))
	}
	html := "<pre>" + b.String() + "</pre>"
	cmd := Doc("big", html)
	seqs := split(cmd)
	if len(seqs) < 2 {
		t.Fatalf("expected chunks, got %d sequence(s)", len(seqs))
	}
	for i, s := range seqs {
		payload := s[strings.LastIndexByte(s, ';')+1 : len(s)-2]
		if len(payload) > Chunk {
			t.Errorf("chunk %d carries %d bytes", i, len(payload))
		}
		if i > 0 && !strings.HasPrefix(s, "\x1b]7279;m=") {
			t.Errorf("continuation chunk %d carries more than m and q: %q", i, s[:30])
		}
	}
	ms := decodeAll(t, cmd)
	if len(ms) != 1 || string(ms[0].Payload) != html {
		t.Fatalf("chunks did not reassemble (%d messages)", len(ms))
	}

	// Repetitive markup compresses.
	rep := strings.Repeat("<li>row</li>", 500)
	if c := Doc("list", rep); !strings.Contains(c, ":o=z") || len(c) > len(rep)/4 {
		t.Errorf("repetitive markup was not compressed (%d bytes)", len(c))
	}
}

// A document the program only shows is detached in the command that sends
// it, and a surface is detached with no reply asked for (SPEC §5.5): a host
// older than §5.5 answers detach with EINVAL, which nobody would read.
func TestDetach(t *testing.T) {
	if got, want := Detach("showhot-7-doc1"), "\x1b]7279;a=detach:s=showhot-7-doc1:q=2\x1b\\"; got != want {
		t.Errorf("Detach = %q, want %q", got, want)
	}
	if got := DocDetached("card", "<p>hi</p>"); !strings.HasPrefix(got, "\x1b]7279;a=doc:s=card:d=1:q=1;") {
		t.Errorf("DocDetached = %q", got)
	}
	if got := Doc("card", "<p>hi</p>"); strings.Contains(got, ":d=") {
		t.Errorf("Doc is detached: %q", got)
	}
	// Chunked, d=1 is the first chunk's: the others carry m and q alone.
	var b strings.Builder
	x := uint32(7)
	for b.Len() < 20000 {
		x = x*1664525 + 1013904223
		b.WriteByte(byte('a' + x>>24%26))
	}
	cmd := DocDetached("big", b.String())
	seqs := split(cmd)
	if len(seqs) < 2 || !strings.Contains(seqs[0], ":d=1:") {
		t.Fatalf("%d chunks, the first %q", len(seqs), seqs[0][:min(40, len(seqs[0]))])
	}
	for _, s := range seqs[1:] {
		if strings.Contains(s, "d=1") {
			t.Errorf("a continuation chunk carries d: %q", s[:30])
		}
	}
	ms := decodeAll(t, cmd)
	if len(ms) != 1 || ms[0].Get("d") != "1" || string(ms[0].Payload) != b.String() {
		t.Errorf("the chunks did not reassemble a detached document (%d messages)", len(ms))
	}
}

func TestPlaceAtKeepsTheCursor(t *testing.T) {
	got := PlaceAt("chart", 4, 2, 30, 10, Window{}, 0)
	if !strings.HasPrefix(got, "\x1b7\x1b[3;5H\x1b]7279;a=place:s=chart:c=30:r=10:C=1:q=1") || !strings.HasSuffix(got, "\x1b8") {
		t.Errorf("PlaceAt = %q", got)
	}
	// A window: the surface is 30×10, rows 3-6 show.
	if got := PlaceAt("chart", 4, 2, 30, 10, Window{0, 3, 30, 4}, 0); !strings.Contains(got, "a=place:s=chart:c=30:r=10:x=0:y=3:w=30:h=4:C=1") {
		t.Errorf("PlaceAt with a window = %q", got)
	}
	// Above what it overlaps.
	if got := PlaceAt("frame", 0, 0, 30, 10, Window{}, 1); !strings.Contains(got, "a=place:s=frame:c=30:r=10:z=1:C=1") {
		t.Errorf("PlaceAt with z = %q", got)
	}
	if q := Query(7); !strings.HasSuffix(q, "\x1b[c") || !strings.HasPrefix(q, "\x1b]7279;a=q:n=7") {
		t.Errorf("Query = %q", q)
	}
}

func TestEventsRepliesAndCaps(t *testing.T) {
	var d Decoder
	m, ok, _ := d.Feed(Encode(Control{{"a", "ev"}, {"s", "form"}, {"e", "submit"}, {"t", "f"}}, []byte(`{"name":"Ada","n":3}`)))
	ev, isEv := m.Event()
	if !ok || !isEv || ev.Kind != "submit" || ev.Fields()["name"] != "Ada" || ev.Fields()["n"] != "3" {
		t.Fatalf("event: %+v", ev)
	}
	m, _, _ = d.Feed(Encode(Control{{"a", "err"}, {"s", "x"}, {"re", "patch"}}, []byte(`{"code":"ENOTARGET","detail":"gone"}`)))
	if r, _ := m.Reply(); r.OK || r.Code != "ENOTARGET" || r.Detail != "gone" || r.Re != "patch" {
		t.Errorf("error reply: %+v", r)
	}
	m, _, _ = d.Feed(Encode(Control{{"a", "ok"}, {"n", "1"}, {"re", "q"}},
		[]byte(`{"v":"0.1","cell":{"w":10,"h":21},"scale":1,"scheme":"dark","host":"xterm-addon-hotty"}`)))
	r, _ := m.Reply()
	caps, ok := r.Caps()
	if !ok || caps.V != "0.1" || caps.Cell.H != 21 || caps.Host != "xterm-addon-hotty" || r.N != 1 {
		t.Errorf("caps: %+v %+v", caps, r)
	}
	if _, _, isHotty := d.Feed("\x1b]52;c;aGk=\x07"); isHotty {
		t.Error("an OSC 52 is not HOTTY")
	}
}

// The shared conformance vectors' wire cases (neuroplastio/hotty), when a
// checkout is next to this repository or at HOTTY_DIR.
func TestWireVectors(t *testing.T) {
	dir := os.Getenv("HOTTY_DIR")
	if dir == "" {
		for _, d := range []string{"../../hotty", "../../../hotty/main"} {
			if _, err := os.Stat(filepath.Join(d, "SPEC.md")); err == nil {
				dir = d
				break
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "conformance", "vectors.json"))
	if dir == "" || err != nil {
		t.Skip("no hotty checkout (HOTTY_DIR)")
	}
	var v struct {
		Wire []struct {
			Name     string `json:"name"`
			Stream   string `json:"stream"`
			Commands []struct {
				Control map[string]string `json:"control"`
				Payload string            `json:"payload"`
			} `json:"commands"`
		} `json:"wire"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	for _, w := range v.Wire {
		got := decodeAll(t, w.Stream)
		if len(got) != len(w.Commands) {
			t.Errorf("%s: %d commands, want %d", w.Name, len(got), len(w.Commands))
			continue
		}
		for i, c := range w.Commands {
			for k, val := range c.Control {
				if got[i].Control[k] != val {
					t.Errorf("%s: %s=%q, want %q", w.Name, k, got[i].Control[k], val)
				}
			}
			if string(got[i].Payload) != c.Payload {
				t.Errorf("%s: payload %q, want %q", w.Name, got[i].Payload, c.Payload)
			}
		}
	}
}
