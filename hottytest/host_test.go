package hottytest

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/neuroplastio/hotty-go"
)

// recorder is a test that records what would fail it.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func send(h *Host, cmds ...string) {
	for _, c := range cmds {
		_, _ = io.WriteString(h, c)
	}
}

// sent decodes what the host has sent the program since the last drain:
// replies and events as one line each, "ok re=doc s=x" or
// "ev click t=go {…}", and other bytes quoted.
func sent(h *Host) []string {
	var out []string
	var d hotty.Decoder
	stream := h.drain()
	for stream != "" {
		i := strings.Index(stream, "\x1b]7279;")
		if i != 0 {
			if i < 0 {
				i = len(stream)
			}
			out = append(out, fmt.Sprintf("%q", stream[:i]))
			stream = stream[i:]
			continue
		}
		end := strings.Index(stream, "\x1b\\") + 2
		m, _ := d.Feed(stream[:end])
		stream = stream[end:]
		if ev, ok := m.Event(); ok {
			line := "ev " + ev.Kind + " t=" + ev.Target
			if len(ev.Detail) > 0 {
				line += " " + string(ev.Detail)
			}
			out = append(out, line)
			continue
		}
		r, _ := m.Reply()
		line := m.Get("a") + " re=" + r.Re
		if r.Surface != "" {
			line += " s=" + r.Surface
		}
		if !r.OK {
			line += " " + r.Code
		}
		out = append(out, line)
	}
	return out
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("sent:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// shown is a host with a surface placed, its replies drained.
func shown(t *testing.T, name, markup string, opts ...Option) *Host {
	t.Helper()
	h := New(t, opts...)
	send(h, hotty.Doc(name, markup), hotty.Place(name, hotty.Placement{Cols: 40, Rows: 5}))
	h.drain()
	return h
}

func TestAnswersWhatTerminalsAnswer(t *testing.T) {
	h := New(t)
	send(h, "ab\x1b[c", "\x1b[6n", "\x1b[?6n", "\x1b]11;?\x07", "\x1b_Gi=31,a=q;AAAA\x1b\\")
	expect(t, sent(h), `"\x1b[?62;22c\x1b[1;3R\x1b[?1;3R\x1b]11;rgb:1212/1212/1a1a\x1b\\"`)

	k := New(t, KittyGraphics(), Caps(hotty.Caps{Scheme: "light"}))
	send(k, "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\", "\x1b]11;?\x1b\\")
	expect(t, sent(k), `"\x1b_Gi=31;OK\x1b\\\x1b]11;rgb:ffff/ffff/ffff\x1b\\"`)
}

func (h *Host) expectNothing(t *testing.T) {
	t.Helper()
	if s := h.drain(); s != "" {
		t.Errorf("sent %q", s)
	}
}

func TestQueryAndCaps(t *testing.T) {
	h := New(t, Caps(hotty.Caps{Host: "mine", Limits: map[string]int{"surfaces": 3}}))
	send(h, hotty.Query(5))
	var d hotty.Decoder
	seqs := oscs(h.drain())
	m, _ := d.Feed(seqs[0])
	r, _ := m.Reply()
	caps, ok := r.Caps()
	if !ok || r.N != 5 || caps.Host != "mine" || caps.Limits["surfaces"] != 3 || caps.V != hotty.Version || caps.Cell.H != 18 {
		t.Errorf("caps %+v, reply %+v", caps, r)
	}
}

func TestTextTerminal(t *testing.T) {
	rec := &recorder{TB: t}
	h := New(rec, Text())
	send(h, hotty.Query(1))
	expect(t, sent(h), `"\x1b[?62;22c"`)
	if len(rec.errs) != 0 {
		t.Errorf("the query is fine anywhere: %v", rec.errs)
	}
	send(h, hotty.Doc("x", "<p>hi</p>"))
	h.expectNothing(t)
	if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], "not a host") || h.Surface("x") != nil {
		t.Errorf("a document to a terminal that is not a host: %v", rec.errs)
	}
	if err := h.Click("x", "y"); !errors.Is(err, ErrNotHost) {
		t.Errorf("Click: %v", err)
	}
	if err := h.Emit("x", "click", "y", nil); !errors.Is(err, ErrNotHost) {
		t.Errorf("Emit: %v", err)
	}
}

func TestProtocolErrorsFailTheTest(t *testing.T) {
	rec := &recorder{TB: t}
	h := New(rec)
	send(h, "\x1b]7279;a=doc:s;eA==\x1b\\")
	send(h, hotty.Place("x", hotty.Placement{Cols: 10}))           // ENOENT: a runtime condition, fine
	send(h, hotty.Encode(hotty.Control{{K: "a", V: "frob"}}, nil)) // EINVAL
	if len(rec.errs) != 2 || !strings.Contains(rec.errs[0], "malformed") || !strings.Contains(rec.errs[1], "EINVAL") {
		t.Errorf("errors: %q", rec.errs)
	}
	if h.Invalid() != 1 || len(h.Errors()) != 2 {
		t.Errorf("Invalid %d, Errors %v", h.Invalid(), h.Errors())
	}

	lenient := &recorder{TB: t}
	l := New(lenient, Lenient())
	send(l, hotty.Encode(hotty.Control{{K: "a", V: "frob"}}, nil))
	if len(lenient.errs) != 0 || len(l.Errors()) != 1 {
		t.Errorf("Lenient: %v %v", lenient.errs, l.Errors())
	}
}

func TestNilTB(t *testing.T) {
	h := New(nil)
	send(h, hotty.Encode(hotty.Control{{K: "a", V: "frob"}}, nil))
	if len(h.Errors()) != 1 {
		t.Errorf("Errors: %v", h.Errors())
	}
	_ = h.Close()
}

func TestReadAndClose(t *testing.T) {
	h := New(t)
	h.Type("ab")
	buf := make([]byte, 1)
	if n, err := h.Read(buf); n != 1 || err != nil || buf[0] != 'a' {
		t.Fatalf("Read: %d %v %q", n, err, buf)
	}
	_ = h.Close()
	h.Type("lost")
	if n, err := h.Read(buf); n != 1 || err != nil || buf[0] != 'b' {
		t.Errorf("Read after Close: what was sent is read first: %d %v", n, err)
	}
	if _, err := h.Read(buf); err != io.EOF {
		t.Errorf("then EOF: %v", err)
	}
	if c, r := New(t, Size(100, 30)).TermSize(); c != 100 || r != 30 {
		t.Errorf("TermSize %d×%d", c, r)
	}
}

// A test that hands a program its input itself, a Bubble Tea model's
// Update say, reads what the host sent without blocking: Buffered says how
// much there is.
func TestBuffered(t *testing.T) {
	h := New(t)
	if n := h.Buffered(); n != 0 {
		t.Fatalf("Buffered %d on a new host", n)
	}
	send(h, "\x1b[c")
	h.Type("q")
	const want = "\x1b[?62;22cq"
	n := h.Buffered()
	if n != len(want) {
		t.Fatalf("Buffered %d, want %d", n, len(want))
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(h, buf); err != nil || string(buf) != want {
		t.Fatalf("read %q, %v", buf, err)
	}
	if n := h.Buffered(); n != 0 {
		t.Errorf("Buffered %d once read", n)
	}
}

func TestScreen(t *testing.T) {
	h := New(t, Size(10, 5))
	send(h, "hello\r\n", "50%\r", "100%\n", "a\x1b[2Db", "\n", "x\tz\n", "ab\x08c\n")
	send(h, "\x1b[31mred\x1b[m\n", "gone\x1b[1K!\n", "keep\x1b[2D\x1b[K\n", "\xe2\x82", "\xac\n")
	send(h, "0123456789AB\n")
	want := "hello\n100%\nb\nx       z\nac\nred\n    !\nke\n€\n0123456789\nAB"
	if got := h.Screen(); got != want {
		t.Errorf("Screen:\n%s\nwant:\n%s", got, want)
	}
	// The screen is the last 5 lines; the rest went to the scrollback,
	// which erasing the screen keeps, and its own sequence erases.
	send(h, "\x1b[2J", "\x1b[3;2Hx", "\x1b[Hy", "\x1b[2B\x1b[2Cz\x1b[Az", "\x1b7\x1b[5;1Hs\x1b8!")
	want = "hello\n100%\nb\nx       z\nac\nred\n    !\n" + "y\n    z!\n x z\n\ns"
	if got := h.Screen(); got != want {
		t.Errorf("after moves:\n%q\nwant:\n%q", got, want)
	}
	send(h, "\x1b[3J")
	if got := h.Screen(); got != "y\n    z!\n x z\n\ns" {
		t.Errorf("after erasing the scrollback: %q", got)
	}
	send(h, "\x1b[2;1H\x1b[J", "\x1b[?25l\x1b[>1u\x1b[E\x1b[F\x1b[G", "\x1b[1E-\x1b[1F+")
	if got := h.Screen(); got != "y\n+\n-" {
		t.Errorf("after erasing below: %q", got)
	}
	if !strings.Contains(h.Output(), "\x1b[31mred") {
		t.Error("Output keeps every byte")
	}
}

// The sequences a full-screen renderer such as Bubble Tea's draws with.
func TestScreenEdits(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"VPA, HPA and relative moves", "\x1b[3dA\x1b[5`B\x1b[2aC\x1b[1eD", "\n\nA   B  C\n        D"},
		{"ECH", "abcdef\x1b[3G\x1b[2X", "ab  ef"},
		{"ICH and DCH", "abcdef\x1b[2G\x1b[2@xy\x1b[6G\x1b[P", "axybcef"},
		{"ICH past the edge", "0123456789\x1b[1G\x1b[3@", "   0123456"},
		{"REP", "a-\x1b[4b|", "a-----|"},
		{"CHT, and the right edge", "a\x1b[I|\x1b[2I-\x1b[20C!", "a       |!"},
		{"IL and DL", "1\r\n2\r\n3\r\n4\x1b[2;1H\x1b[L+\x1b[4;1H\x1b[2M", "1\n+\n2"},
		{"SU and SD", "1\r\n2\r\n3\x1b[S\x1b[3;1H\x1b[2T", "\n\n2\n3"},
		{"scrolling region", "1\r\n2\r\n3\r\n4\r\n5\x1b[2;4r\x1b[4;1H\n\n+", "1\n4\n\n+\n5"},
		{"RI at the top of the region", "1\r\n2\r\n3\x1b[2;3r\x1b[2;1H\x1bM+", "1\n+\n2"},
		{"IND and NEL", "a\x1bDb\x1bEc", "a\n b\nc"},
		{"SCOSC and SCORC", "ab\x1b[s\x1b[3;1Hc\x1b[ud", "abd\n\nc"},
		{"wrapping at the last row", "\x1b[5;1H0123456789xy", "\n\n\n0123456789\nxy"},
	} {
		h := New(t, Size(10, 5), Text())
		h.altScreen(true)
		send(h, tc.in)
		if got := h.Screen(); got != tc.want {
			t.Errorf("%s:\n%q\nwant:\n%q", tc.name, got, tc.want)
		}
	}
}

// The alternate screen is a screen of its own: the main one, scrollback
// and cursor, is as it was when the program leaves it.
func TestAlternateScreenBuffer(t *testing.T) {
	h := New(t, Size(10, 3), Text())
	send(h, "one\r\ntwo\r\nthree\r\nfour\r\n$ ")
	main := h.Screen()
	if main != "one\ntwo\nthree\nfour\n$" {
		t.Fatalf("main: %q", main)
	}
	send(h, "\x1b[?1049h", "\x1b[H\x1b[2Jtitle\x1b[3;1Hfooter\n")
	if got := h.Screen(); got != "\nfooter" {
		t.Errorf("full screen, after scrolling off its last row: %q", got)
	}
	send(h, "\x1b[?1049l", "x")
	if got := h.Screen(); got != main+" x" {
		t.Errorf("back: %q", got)
	}
	// The cursor's row on view, 3 of 3: the scrollback does not count.
	send(h, "\x1b[6n")
	if got := h.drain(); got != "\x1b[3;4R" {
		t.Errorf("cursor position: %q", got)
	}
	send(h, "\x1b[?1049h\x1bc")
	if got := h.Screen(); got != "" {
		t.Errorf("after a reset: %q", got)
	}
}

func TestPlacementAndCursor(t *testing.T) {
	h := New(t)
	send(h, "> ", hotty.DocDetached("a", "<p>one</p><p>two</p>"), hotty.Place("a", hotty.Placement{Cols: 20}))
	expect(t, sent(h))
	a := h.Surface("a")
	if p := a.Placement(); !a.Placed() || p.Cols != 20 || p.Rows != 1 || !a.Detached() {
		t.Errorf("auto rows: %+v", p)
	}
	if c, r := a.At(); c != 2 || r != 0 {
		t.Errorf("placed at %d,%d, the cursor's", c, r)
	}
	// The cursor is below the placement, at the start of the line.
	send(h, "after")
	if h.Screen() != ">\nafter" {
		t.Errorf("Screen %q", h.Screen())
	}
	send(h, hotty.Doc("b", "<p>x</p>"), hotty.PlaceAt("b", 5, 9, hotty.Placement{Cols: 4, Rows: 2, Z: -3, Press: true}, hotty.N(2)))
	expect(t, sent(h), "ok re=place s=b")
	b := h.Surface("b")
	if c, r := b.At(); c != 5 || r != 9 || b.Placement().Z != -3 || !b.Placement().Press {
		t.Errorf("PlaceAt: %d,%d %+v", c, r, b.Placement())
	}
	send(h, "!")
	if !strings.HasSuffix(h.Screen(), "after!") {
		t.Errorf("C=1 left the cursor: %q", h.Screen())
	}
	send(h, hotty.Place("b", hotty.Placement{Cols: 4, Rows: 2, Window: hotty.Window{X: 1, Y: 1, W: 3, H: 1}, KeepCursor: true}))
	if w := b.Placement().Window; w != (hotty.Window{X: 1, Y: 1, W: 3, H: 1}) {
		t.Errorf("window %+v", w)
	}
	send(h, hotty.Hide("b", hotty.N(3)))
	expect(t, sent(h), "ok re=hide s=b")
	if b.Placed() {
		t.Error("hidden is not placed")
	}
	if err := h.Click("b", "x"); !errors.Is(err, ErrNotPlaced) {
		t.Errorf("Click on a hidden surface: %v", err)
	}

	tall := New(t, AutoRows(func(*Surface, int) int { return 2000 }))
	send(tall, hotty.Doc("t", "<p>x</p>"), hotty.Place("t", hotty.Placement{Cols: 10}, hotty.N(1)))
	if r := tall.Surface("t").Placement().Rows; r != hotty.MaxSize {
		t.Errorf("auto rows are at most 1000: %d", r)
	}
}

func TestSurfacesAndReset(t *testing.T) {
	h := New(t)
	send(h, hotty.Doc("b", "<p>b</p>"), hotty.Doc("a", "<p>a</p>"), hotty.Doc("b", "<p>B</p>"))
	var names []string
	for _, s := range h.Surfaces() {
		names = append(names, s.Name())
	}
	send(h, hotty.Doc("t", `<style>p{}</style><p>one<b>two</b></p><ul><li>three</li><li>four</li></ul>x<br>y`))
	if got := h.Surface("t").Text(); got != "onetwo three four x y" {
		t.Errorf("Text = %q", got)
	}
	if strings.Join(names, ",") != "b,a" || h.Surface("b").Text() != "B" {
		t.Errorf("Surfaces %v, b %q", names, h.Surface("b").Text())
	}
	send(h, "\x1bc")
	if len(h.Surfaces()) != 0 {
		t.Error("a full reset deletes every surface")
	}
	send(h, hotty.Doc("x", "<p>x</p>"), hotty.DelAll(hotty.N(1)))
	expect(t, sent(h), "ok re=del")
	if len(h.Surfaces()) != 0 {
		t.Error("DelAll")
	}
	if len(h.Commands()) != 6 {
		t.Errorf("%d commands", len(h.Commands()))
	}
}

// A placement made on the alternate screen belongs to it: leaving the
// screen deletes the surface (SPEC §5.4).
func TestAlternateScreen(t *testing.T) {
	h := New(t)
	send(h, hotty.Doc("main", ""), hotty.Place("main", hotty.Placement{Cols: 5, Rows: 1}))
	send(h, "\x1b[?1049h", hotty.Doc("alt", ""), hotty.Place("alt", hotty.Placement{Cols: 5, Rows: 1}), hotty.Doc("unplaced", ""))
	send(h, "\x1b[?1049l")
	if h.Surface("alt") != nil || h.Surface("main") == nil || h.Surface("unplaced") == nil {
		t.Errorf("after leaving the alternate screen: %v", h.Surfaces())
	}
	// Placed on the main screen again, a surface stays.
	send(h, "\x1b[?1049h", hotty.Place("main", hotty.Placement{Cols: 5, Rows: 1}), hotty.Place("unplaced", hotty.Placement{Cols: 5, Rows: 1}), "\x1b[?1049l")
	send(h, hotty.Place("main", hotty.Placement{Cols: 5, Rows: 1}))
	if h.Surface("unplaced") != nil {
		t.Error("placed on the alternate screen")
	}
}

func TestLimits(t *testing.T) {
	h := New(t, Caps(hotty.Caps{Limits: map[string]int{"surfaces": 2, "resources": 10}}))
	send(h, hotty.Doc("a", ""), hotty.Doc("b", ""), hotty.Doc("c", ""), hotty.Doc("a", "<p>again</p>"))
	expect(t, sent(h), "err re=doc s=c EQUOTA")
	q := hotty.Q(hotty.ReplyOnError)
	send(h, hotty.Res("r", "text/css", []byte("0123456789"), q), hotty.Res("r", "text/css", []byte("abc"), q), hotty.Res("s", "text/css", []byte("12345678"), q))
	expect(t, sent(h), "err re=res EQUOTA")
	if mime, data, ok := h.Resource("r"); !ok || mime != "text/css" || string(data) != "abc" {
		t.Errorf("Resource: %q %q %v", mime, data, ok)
	}
	send(h, hotty.DelRes("r"))
	if _, _, ok := h.Resource("r"); ok {
		t.Error("DelRes")
	}
}

func TestPatchesAndInspection(t *testing.T) {
	h := shown(t, "x", `<div id=bar style="width: 1px"></div><p id=p class=a>old</p><textarea id=ta>t</textarea>`+
		`<select id=sel><option>a<option selected value=bee>b</select><input id=in value=v><input id=box type=checkbox>`)
	send(h, hotty.SetVar("x", "bar", "p", "42"), hotty.SetVar("x", "bar", "--q", "1"), hotty.SetVar("x", "bar", "p", "43"))
	s := h.Surface("x")
	if v, ok := s.Var("bar", "p"); !ok || v != "43" {
		t.Errorf("Var p = %q", v)
	}
	if v, _ := s.Attr("bar", "style"); v != "width: 1px; --p: 43; --q: 1" {
		t.Errorf("style %q", v)
	}
	if _, ok := s.Var("p", "p"); ok {
		t.Error("Var on an element without a style")
	}
	for id, want := range map[string]string{"ta": "t", "sel": "bee", "in": "v", "box": "false"} {
		if v, ok := s.Value(id); !ok || v != want {
			t.Errorf("Value(%s) = %q", id, v)
		}
	}
	if _, ok := s.Value("nope"); ok {
		t.Error("Value of nothing")
	}
	send(h, hotty.Patch("x", hotty.OpText, "ta", "", []byte("new")), hotty.SetAttr("x", "box", "checked", ""))
	if v, _ := s.Value("ta"); v != "new" {
		t.Errorf("a textarea's text is its value: %q", v)
	}
	if v, _ := s.Value("box"); v != "true" {
		t.Errorf("checked: %q", v)
	}
	if s.TextOf("p") != "old" || s.TextOf("nope") != "" || !strings.Contains(s.HTML(), `<p id="p" class="a">old</p>`) {
		t.Errorf("TextOf, HTML: %s", s.HTML())
	}
	send(h, hotty.MorphTo("x", "", `<p id=p>new</p><b id=gone>x</b>`, hotty.Q(hotty.ReplyOnError)))
	expect(t, sent(h), "err re=patch s=x ENOTARGET")
	if s.TextOf("p") != "new" {
		t.Error("morph by ids applies what it can")
	}
	if _, ok := s.Attr("p", "class"); ok {
		t.Error("morph removes attributes the payload lacks")
	}
}

func TestControlsFollowTheProgram(t *testing.T) {
	h := shown(t, "f", `<form id=f><input id=a name=a value=1><input id=b name=b value=2></form>`)
	_ = h.Fill("f", "a", "typed")
	_ = h.Fill("f", "b", "other")
	sent(h)
	// The program sets a's value: a is not focused, so it follows.
	send(h, hotty.SetAttr("f", "a", "value", "set"), hotty.SetAttr("f", "b", "value", "set"))
	s := h.Surface("f")
	if v, _ := s.Value("a"); v != "set" {
		t.Errorf("a: %q", v)
	}
	// b is focused: the user's value stays.
	if v, _ := s.Value("b"); v != "other" || s.Focused() != "b" {
		t.Errorf("b: %q, focused %q", v, s.Focused())
	}
}

func TestClick(t *testing.T) {
	h := shown(t, "c", `<base href="https://example.com/docs/">`+
		`<button id=go value=7><span id=label>Go</span></button>`+
		`<div data-on=click id=card><p id=text>text</p></div>`+
		`<p id=plain>plain</p><button>no id</button>`+
		`<a id=rel href="guide">Guide</a><a id=web href="https://neuroplast.io" target=_blank>web</a>`)
	if err := h.Click("c", "label"); err != nil {
		t.Fatal(err)
	}
	// The button takes the keyboard, then reports with its value.
	expect(t, sent(h), "ev focus t=", `ev click t=go {"value":"7"}`)
	_ = h.Click("c", "text")
	expect(t, sent(h), "ev click t=card")
	if err := h.Click("c", "plain"); !errors.Is(err, ErrNoReport) {
		t.Errorf("a click on plain text: %v", err)
	}
	_ = h.Click("c", "rel")
	expect(t, sent(h), `ev click t= {"href":"guide","url":"https://example.com/docs/guide"}`)
	_ = h.Click("c", "web")
	expect(t, sent(h))
	if o := h.Opened(); len(o) != 1 || o[0] != "https://neuroplast.io" {
		t.Errorf("Opened %v", o)
	}
	if err := h.Click("c", "nope"); !errors.Is(err, ErrNoElement) {
		t.Errorf("Click nothing: %v", err)
	}
	if err := h.Click("nope", "x"); !errors.Is(err, ErrNoSurface) {
		t.Errorf("Click on no surface: %v", err)
	}

	local := shown(t, "l", `<a id=top href="#top">top</a>`)
	_ = local.Click("l", "top")
	expect(t, sent(local), "ev focus t=", `ev click t= {"href":"#top"}`)
}

// Links have no id as often as not: a manual page's references, a
// document's. ClickLink clicks one by its href.
func TestClickLink(t *testing.T) {
	h := shown(t, "m", `<p>See <a href="man:ls(1)" class="xref"><b>ls</b>(1)</a> and `+
		`<a href="https://neuroplast.io" target=_blank>the site</a>.</p><p><a href="man:ls(1)">again</a></p>`)
	if err := h.ClickLink("m", "man:ls(1)"); err != nil {
		t.Fatal(err)
	}
	// The first of the two, which takes the keyboard (a link with an href)
	// and reports its href.
	expect(t, sent(h), "ev focus t=", `ev click t= {"href":"man:ls(1)","url":"man:ls(1)"}`)
	if f := h.Surface("m").Focused(); f != "" {
		t.Errorf("focused %q: the link has no id", f)
	}
	if err := h.ClickLink("m", "https://neuroplast.io"); err != nil {
		t.Fatal(err)
	}
	expect(t, sent(h))
	if o := h.Opened(); len(o) != 1 || o[0] != "https://neuroplast.io" {
		t.Errorf("Opened %v", o)
	}
	if err := h.ClickLink("m", "man:cat(1)"); !errors.Is(err, ErrNoElement) {
		t.Errorf("no such link: %v", err)
	}
	if err := h.ClickLink("nope", "man:ls(1)"); !errors.Is(err, ErrNoSurface) {
		t.Errorf("no such surface: %v", err)
	}
}

func TestFormAndKeyboard(t *testing.T) {
	h := shown(t, "f", `<form id=f>`+
		`<input id=name name=name><textarea id=notes name=notes data-on=input></textarea>`+
		`<input id=ok name=ok type=checkbox value=yes><input id=nope name=nope type=checkbox>`+
		`<input id=r1 name=env type=radio value=staging checked><input id=r2 name=env type=radio value=prod>`+
		`<select id=size name=size><option>s<option>m</select>`+
		`<input id=off name=off value=x disabled><input name=hidden type=hidden value=h>`+
		`<button id=save name=action value=save>Save</button><button id=reset type=button>Reset</button>`+
		`</form>`)
	_ = h.Fill("f", "name", "Ada")
	_ = h.Fill("f", "notes", "hi")
	_ = h.Check("f", "ok", true)
	_ = h.Check("f", "r2", true)
	_ = h.Choose("f", "size", "m")
	_ = h.Submit("f", "f")
	expect(t, sent(h),
		"ev focus t=",
		`ev change t=name {"value":"Ada"}`,
		`ev input t=notes {"value":"hi"}`,
		`ev change t=notes {"value":"hi"}`,
		`ev change t=ok {"checked":true,"value":"yes"}`,
		`ev change t=r2 {"checked":true,"value":"prod"}`,
		`ev change t=size {"value":"m"}`,
		`ev submit t=f {"env":"prod","hidden":"h","name":"Ada","notes":"hi","ok":"yes","size":"m"}`)

	// A submit button reports its click, then submits with its own value.
	_ = h.Click("f", "save")
	expect(t, sent(h), `ev click t=save {"value":"save"}`,
		`ev submit t=f {"action":"save","env":"prod","hidden":"h","name":"Ada","notes":"hi","ok":"yes","size":"m"}`)
	_ = h.Click("f", "reset")
	expect(t, sent(h), "ev click t=reset")

	// Typing commits when focus leaves: here, the user's Blur.
	_ = h.Fill("f", "name", "Grace")
	_ = h.Blur("f")
	expect(t, sent(h), `ev change t=name {"value":"Grace"}`, "ev blur t=")
	_ = h.Blur("f")
	expect(t, sent(h))

	for _, err := range []error{
		h.Fill("f", "ok", "x"), h.Check("f", "name", true), h.Check("f", "r1", false),
		h.Choose("f", "name", "m"), h.Choose("f", "size", "xl"), h.Fill("f", "off", "x"), h.Submit("f", "missing"),
	} {
		if !errors.Is(err, ErrNotControl) && !errors.Is(err, ErrNoElement) {
			t.Errorf("a wrong action: %v", err)
		}
	}
	if err := h.Check("f", "ok", true); err != nil {
		t.Errorf("checking a checked box does nothing: %v", err)
	}
	expect(t, sent(h), "ev focus t=")

	outside := shown(t, "o", `<input id=i><form><input id=j></form>`)
	if err := outside.Submit("o", "i"); !errors.Is(err, ErrNotControl) {
		t.Errorf("Submit outside a form: %v", err)
	}
	// A form without an id reports nothing.
	if err := outside.Submit("o", "j"); err != nil {
		t.Error(err)
	}
	expect(t, sent(outside))
}

func TestProgramFocusAndBlur(t *testing.T) {
	h := shown(t, "f", `<input id=a><input id=b>`)
	send(h, hotty.Doc("g", `<input id=c>`), hotty.Place("g", hotty.Placement{Cols: 10, Rows: 1}))
	send(h, hotty.Focus("f", "b", hotty.N(1)))
	// No focus event for focus the program gave.
	expect(t, sent(h), "ok re=focus s=f")
	if h.Surface("f").Focused() != "b" {
		t.Errorf("focused %q", h.Surface("f").Focused())
	}
	_ = h.Fill("f", "b", "x")
	send(h, hotty.Blur("f"))
	expect(t, sent(h), `ev change t=b {"value":"x"}`, "ev blur t=")
	// Focus without a target: the focused element keeps it, or the first.
	send(h, hotty.Focus("f", ""), hotty.Focus("g", ""))
	expect(t, sent(h), "ev blur t=")
	if h.Surface("g").Focused() != "c" || h.Surface("f").Focused() != "" {
		t.Error("the keyboard moved to g")
	}
	send(h, hotty.Focus("f", "nope", hotty.N(2)))
	expect(t, sent(h), "err re=focus s=f ENOTARGET")
	// Hiding the surface that has the keyboard gives it back.
	send(h, hotty.Hide("g"))
	expect(t, sent(h), "ev blur t=")
	// So does deleting it, or replacing its document, silently.
	send(h, hotty.Focus("f", "a"), hotty.Doc("f", "<input id=a>"))
	if h.Surface("f").Focused() != "" {
		t.Error("a new document has no focus")
	}
	send(h, hotty.Focus("f", "a"), hotty.Patch("f", hotty.OpRemove, "a", "", nil))
	if h.Surface("f").Focused() != "" {
		t.Error("the focused element went")
	}
	expect(t, sent(h))
}

func TestDetached(t *testing.T) {
	h := shown(t, "d", `<form id=f><input id=i><button id=b>go</button><a id=l href="https://x.io" target=_blank>x</a></form>`)
	_ = h.Fill("d", "i", "typed")
	send(h, hotty.Detach("d"))
	// The keyboard goes back with no change and no blur.
	expect(t, sent(h), "ev focus t=")
	for _, err := range []error{h.Click("d", "b"), h.Fill("d", "i", "x"), h.Check("d", "i", true), h.Choose("d", "i", "x"),
		h.Submit("d", "i"), h.Press("d", ""), h.Blur("d"), h.Emit("d", "resize", "", nil)} {
		if !errors.Is(err, ErrDetached) {
			t.Errorf("an action on a detached surface: %v", err)
		}
	}
	// A hyperlink still opens.
	if err := h.Click("d", "l"); err != nil || len(h.Opened()) != 1 {
		t.Errorf("hyperlink: %v %v", err, h.Opened())
	}
	send(h, hotty.Blur("d", hotty.N(1)), hotty.Focus("d", "", hotty.N(2)))
	expect(t, sent(h), "ok re=blur s=d", "err re=focus s=d EDETACHED")
	expect(t, sent(h))
}

func TestPressAndEmit(t *testing.T) {
	h := New(t)
	send(h, hotty.Doc("p", `<div id=card><p>text <b id=b>bold</b></p></div>`), hotty.Place("p", hotty.Placement{Cols: 10, Rows: 2, Press: true}))
	sent(h)
	_ = h.Press("p", "b")
	_ = h.Press("p", "")
	expect(t, sent(h), "ev press t=b", "ev press t=")
	if err := h.Press("p", "nope"); !errors.Is(err, ErrNoElement) {
		t.Errorf("Press nothing: %v", err)
	}
	send(h, hotty.Place("p", hotty.Placement{Cols: 10, Rows: 2}))
	if err := h.Press("p", "b"); !errors.Is(err, ErrNoPress) {
		t.Errorf("a placement without p=1: %v", err)
	}
	if err := h.Press("nope", ""); !errors.Is(err, ErrNoSurface) {
		t.Errorf("Press on no surface: %v", err)
	}
	_ = h.Emit("p", hotty.EventResize, "", map[string]int{"w": 90, "h": 36})
	expect(t, sent(h), `ev resize t= {"h":36,"w":90}`)
	if err := h.Emit("nope", "x", "", nil); !errors.Is(err, ErrNoSurface) {
		t.Errorf("Emit on no surface: %v", err)
	}
	// A press comes first, before what the click does.
	send(h, hotty.Doc("q", `<button id=b>b</button>`), hotty.Place("q", hotty.Placement{Cols: 10, Rows: 1, Press: true}))
	sent(h)
	_ = h.Click("q", "b")
	expect(t, sent(h), "ev press t=b", "ev focus t=", "ev click t=b")
}

func TestReplies(t *testing.T) {
	h := New(t)
	send(h, hotty.Doc("x", "<p id=p>p</p>", hotty.N(1)), hotty.SetText("x", "p", "q", hotty.Q(hotty.ReplyAlways)))
	expect(t, sent(h), "ok re=doc s=x", "ok re=patch s=x")
	if n := len(h.Replies()); n != 2 {
		t.Errorf("%d replies", n)
	}
	send(h, hotty.Place("x", hotty.Placement{Cols: 5, Rows: 1}))
	_ = h.Emit("x", "custom", "p", nil)
	if evs := h.Events(); len(evs) != 1 || evs[0].Kind != "custom" || evs[0].Target != "p" {
		t.Errorf("Events %+v", evs)
	}
}
