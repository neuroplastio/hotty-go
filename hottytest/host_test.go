package hottytest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/neuroplastio/hotty-go"
	"github.com/tinylib/msgp/msgp"
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
		if ev, ok := hotty.EventOf(m); ok {
			line := "ev " + ev.Kind + " t=" + ev.Target
			if len(ev.Detail) > 0 {
				line += " " + detailJSON(ev.Detail)
			}
			out = append(out, line)
			continue
		}
		r, _ := hotty.ReplyOf(m)
		line := hotty.Get(m.Control, "a") + " re=" + r.Re
		if r.Surface != "" {
			line += " s=" + r.Surface
		}
		if !r.OK {
			line += " " + r.Code
		}
		if r.HasOp {
			line += fmt.Sprintf(" i=%d", r.Op)
		}
		out = append(out, line)
	}
	return out
}

// detailJSON is a msgpack detail as JSON, its maps' keys sorted.
func detailJSON(b []byte) string {
	v, err := msgp.NewReader(bytes.NewReader(b)).ReadIntf()
	if err != nil {
		return fmt.Sprintf("%x", b)
	}
	j, _ := json.Marshal(v)
	return string(j)
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
	r, _ := hotty.ReplyOf(m)
	caps := r.Caps
	if caps == nil || r.N != 5 || caps.Host != "mine" || caps.Limits["surfaces"] != 3 || caps.V != hotty.Version || caps.CellCSS.H != 18 {
		t.Errorf("caps %+v, reply %+v", caps, r)
	}
	// Hosts never compress (SPEC §3.3), however long the reply.
	h = New(t, Caps(hotty.Caps{Host: strings.Repeat("h", 400), Features: []string{"hover", "menu"}}))
	send(h, hotty.Query(1))
	if seqs := oscs(h.drain()); len(seqs) != 1 || strings.Contains(seqs[0], "o=z") {
		t.Errorf("the caps reply: %q", seqs)
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

// A terminal that is not a host holds the last query, and answers it when it
// becomes one if it asked for a late answer (SPEC §4); a withdrawal, a
// plain query or a full reset leaves nothing to answer.
func TestBecomeHostAnswersALateQuery(t *testing.T) {
	h := New(t, Text())
	send(h, hotty.Query(3, hotty.Late()))
	expect(t, sent(h), `"\x1b[?62;22c"`)
	h.BecomeHost()
	var d hotty.Decoder
	seqs := oscs(h.drain())
	if len(seqs) != 1 {
		t.Fatalf("after BecomeHost: %q", seqs)
	}
	m, _ := d.Feed(seqs[0])
	if r, _ := hotty.ReplyOf(m); !r.OK || r.Re != "q" || r.N != 3 {
		t.Errorf("the late answer: %+v", r)
	}
	send(h, hotty.Doc("x", "<p>hi</p>", hotty.N(4)))
	expect(t, sent(h), "ok re=doc s=x")

	for name, after := range map[string]string{
		"withdrawn":              hotty.WithdrawLate(),
		"a plain query after it": hotty.Query(2),
		"a full reset":           "\x1bc",
	} {
		rec := &recorder{TB: t}
		h := New(rec, Text())
		send(h, hotty.Query(1, hotty.Late()), after)
		h.drain()
		h.BecomeHost()
		if s := h.drain(); s != "" {
			t.Errorf("%s: sent %q", name, s)
		}
		if len(rec.errs) != 0 {
			t.Errorf("%s: %v", name, rec.errs)
		}
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
// A line longer than the terminal is wide goes on in the rows below:
// Screen has the rows, Lines the lines as the program wrote them.
func TestLines(t *testing.T) {
	h := New(t, Size(10, 6))
	if got := h.Lines(); len(got) != 0 {
		t.Errorf("Lines of a new host: %q", got)
	}
	send(h, "$ echo 0123456789abc\r\n", "0123456789abc \r\n", "short\r\n")
	if got, want := h.Screen(), "$ echo 012\n3456789abc\n0123456789\nabc\nshort"; got != want {
		t.Errorf("Screen %q, want %q", got, want)
	}
	lines := func(want ...string) {
		t.Helper()
		if got := h.Lines(); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("Lines %q, want %q", got, want)
		}
	}
	lines("$ echo 0123456789abc", "0123456789abc", "short")
	// A row erased whole goes on from nothing: what is written there is a
	// line of its own.
	send(h, "\x1b[2A\x1b[2Kx")
	lines("$ echo 0123456789abc", "0123456789", "x", "short")
	// Lines inserted and deleted take their rows' wraps with them.
	send(h, "\x1b[4A\r\x1b[L")
	lines("", "$ echo 0123456789abc", "0123456789", "x", "short")
	send(h, "\x1b[M")
	lines("$ echo 0123456789abc", "0123456789", "x", "short")
	// The alternate screen has rows of its own, and scrolls them with
	// their wraps.
	send(h, "\x1b[?1049h", "aaaaaaaaaabbbbbbbbbbcc\r\nd\r\ne\r\nf\r\ng")
	lines("bbbbbbbbbbcc", "d", "e", "f", "g")
	send(h, "\x1b[?1049l")
	lines("$ echo 0123456789abc", "0123456789", "x", "short")
}

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
	send(h, "> ", hotty.Doc("a", "<p>one</p><p>two</p>", hotty.Detached()), hotty.Place("a", hotty.Placement{Cols: 20}))
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
	send(h, hotty.Doc("b", "<p>x</p>"), hotty.PlaceAt("b", 5, 9, hotty.Placement{Cols: 4, Rows: 2, Z: -3, Press: hotty.On}, hotty.N(2)))
	expect(t, sent(h), "ok re=place s=b")
	b := h.Surface("b")
	if c, r := b.At(); c != 5 || r != 9 || b.Placement().Z != -3 || b.Placement().Press != hotty.On {
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

// The cursor: where a program leaves it, and whether it is on show
// (DECTCEM), for the shell that prints next.
func TestCursor(t *testing.T) {
	h := New(t, Size(10, 5))
	at := func(col, row int, shown bool) {
		t.Helper()
		if c, r, s := h.Cursor(); c != col || r != row || s != shown {
			t.Errorf("Cursor %d,%d shown %v; want %d,%d shown %v", c, r, s, col, row, shown)
		}
	}
	at(0, 0, true)
	send(h, "ab\r\ncd\x1b[?25l")
	at(2, 1, false)
	// At the end of a line, before the next rune wraps, it is on the last
	// column.
	send(h, "\r\n0123456789")
	at(9, 2, false)
	// The alternate screen has a cursor of its own; the main one's comes
	// back with it.
	send(h, "\x1b[?1049h\x1b[3;4H")
	at(3, 2, false)
	send(h, "\x1b[?1049l\x1b[?25h")
	at(9, 2, true)
	send(h, "\x1b[?25l\x1bc")
	at(0, 0, true)
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

// A double click is a click, then a click whose count is 2 (SPEC §9).
func TestDoubleClick(t *testing.T) {
	h := New(t)
	if !hotty.HasFeature(h.caps, hotty.FeatureCount) {
		t.Error("DefaultCaps: no count")
	}
	send(h, hotty.Doc("f", `<button id=open value=x>Open</button><a href="/doc" id=l>doc</a>`), hotty.Place("f", hotty.Placement{Cols: 20, Rows: 2}))
	_ = sent(h)
	for _, id := range []string{"open", "l"} {
		if err := h.DoubleClick("f", id); err != nil {
			t.Fatal(err)
		}
	}
	var counts []int
	for _, e := range h.Events() {
		if e.Kind == hotty.EventClick {
			counts = append(counts, e.Count)
		}
	}
	if !slices.Equal(counts, []int{1, 2, 1, 2}) {
		t.Errorf("counts %v, want [1 2 1 2]", counts)
	}
}

// The host reports the limit it decodes a message with (SPEC §13), and drops
// a message past it.
func TestMessageLimit(t *testing.T) {
	if c := DefaultCaps(); c.Limits["message"] != hotty.DefaultMax {
		t.Errorf("DefaultCaps: %v", c.Limits)
	}
	h := New(t, Caps(hotty.Caps{Limits: map[string]int{"surfaces": 2}}))
	if h.caps.Limits["message"] != hotty.DefaultMax {
		t.Errorf("a limit Caps leaves out: %v", h.caps.Limits)
	}
	h = New(t, Lenient(), Caps(hotty.Caps{Limits: map[string]int{"message": 8}}))
	send(h, hotty.Res("r", "text/css", []byte("012345678")), hotty.Res("s", "text/css", []byte("01234567")))
	if _, _, ok := h.Resource("r"); ok {
		t.Error("a resource past the limit was taken")
	}
	if _, _, ok := h.Resource("s"); !ok {
		t.Error("a resource at the limit was not taken")
	}
}

func TestDeltasAndInspection(t *testing.T) {
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
	send(h, hotty.SetText("x", "ta", "new"), hotty.SetAttr("x", "box", "checked", ""))
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
	expect(t, sent(h), "err re=delta s=x ENOTARGET i=0")
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
	expect(t, sent(h), "ev focus t=go", `ev click t=go {"value":"7"}`)
	// A click on what takes no focus gives the keyboard back (SPEC
	// §10.1): blur, and then the click it reports.
	_ = h.Click("c", "text")
	expect(t, sent(h), "ev blur t=", "ev click t=card")
	if err := h.Click("c", "plain"); !errors.Is(err, ErrNoReport) {
		t.Errorf("a click on plain text: %v", err)
	}
	// A link reports its href, and its id when it has one, as hosts do.
	_ = h.Click("c", "rel")
	expect(t, sent(h), "ev focus t=rel", `ev click t=rel {"href":"guide","url":"https://example.com/docs/guide"}`)
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
	expect(t, sent(local), "ev focus t=top", `ev click t=top {"href":"#top"}`)

	// So does a click on another surface, on what takes no focus: the
	// surface that had the keyboard hears blur.
	two := shown(t, "a", `<input id=i>`)
	send(two, hotty.Doc("b", `<div id=d data-on=click>d</div>`), hotty.Place("b", hotty.Placement{Cols: 10, Rows: 1}))
	_ = two.Click("a", "i")
	two.drain()
	_ = two.Click("b", "d")
	if evs := two.Events(); len(evs) < 2 || evs[len(evs)-2].Surface != "a" || evs[len(evs)-2].Kind != "blur" {
		t.Errorf("a click on another surface: %+v", evs)
	}
	expect(t, sent(two), "ev blur t=", "ev click t=d")
	if f := two.Surface("a").Focused(); f != "" {
		t.Errorf("the first surface still has the keyboard, at %q", f)
	}
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
	// Each control the user goes to is named as focus lands on it (SPEC
	// §10.1), after the change that focus leaving the last one commits.
	expect(t, sent(h),
		"ev focus t=name",
		`ev change t=name {"value":"Ada"}`,
		"ev focus t=notes",
		`ev input t=notes {"value":"hi"}`,
		`ev change t=notes {"value":"hi"}`,
		"ev focus t=ok",
		`ev change t=ok {"checked":true,"value":"yes"}`,
		"ev focus t=r2",
		`ev change t=r2 {"checked":true,"value":"prod"}`,
		"ev focus t=size",
		`ev change t=size {"value":"m"}`,
		`ev submit t=f {"fields":[["name","Ada"],["notes","hi"],["ok","yes"],["env","prod"],["size","m"],["hidden","h"]]}`)

	// A submit button reports its click, then submits with its own value.
	_ = h.Click("f", "save")
	expect(t, sent(h), "ev focus t=save", `ev click t=save {"value":"save"}`,
		`ev submit t=f {"fields":[["name","Ada"],["notes","hi"],["ok","yes"],["env","prod"],["size","m"],["hidden","h"],["action","save"]]}`)
	_ = h.Click("f", "reset")
	expect(t, sent(h), "ev focus t=reset", "ev click t=reset")

	// Typing commits when focus leaves: here, the user's Blur.
	_ = h.Fill("f", "name", "Grace")
	_ = h.Blur("f")
	expect(t, sent(h), "ev focus t=name", `ev change t=name {"value":"Grace"}`, "ev blur t=")
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
	expect(t, sent(h), "ev focus t=ok")

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
	send(h, hotty.Focus("f", "a"), hotty.Delta("f", []hotty.Op{hotty.OpRemove("a")}))
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
	expect(t, sent(h), "ev focus t=i")
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
	send(h, hotty.Doc("p", `<div id=card><p>text <b id=b>bold</b></p></div>`), hotty.Place("p", hotty.Placement{Cols: 10, Rows: 2, Press: hotty.On}))
	sent(h)
	_ = h.Press("p", "b")
	_ = h.Press("p", "")
	expect(t, sent(h), "ev press t=b", "ev press t=")
	if err := h.Press("p", "nope"); !errors.Is(err, ErrNoElement) {
		t.Errorf("Press nothing: %v", err)
	}
	// p belongs to the surface: placing again without it goes on, and p=0
	// stops the presses (SPEC §5.2).
	send(h, hotty.Place("p", hotty.Placement{Cols: 10, Rows: 2}))
	if err := h.Press("p", "b"); err != nil || !h.Surface("p").Press() {
		t.Errorf("a placement without p: %v", err)
	}
	send(h, hotty.Place("p", hotty.Placement{Cols: 10, Rows: 2, Press: hotty.Off}))
	if err := h.Press("p", "b"); !errors.Is(err, ErrNoPress) || h.Surface("p").Press() {
		t.Errorf("a placement with p=0: %v", err)
	}
	sent(h)
	if err := h.Press("nope", ""); !errors.Is(err, ErrNoSurface) {
		t.Errorf("Press on no surface: %v", err)
	}
	_ = h.Emit("p", "later", "", map[string]float64{"w": 90, "h": 36})
	expect(t, sent(h), `ev later t= {"h":36,"w":90}`)
	if err := h.Emit("nope", "x", "", nil); !errors.Is(err, ErrNoSurface) {
		t.Errorf("Emit on no surface: %v", err)
	}
	// A press comes first, before what the click does.
	send(h, hotty.Doc("q", `<button id=b>b</button>`), hotty.Place("q", hotty.Placement{Cols: 10, Rows: 1, Press: hotty.On}))
	sent(h)
	_ = h.Click("q", "b")
	expect(t, sent(h), "ev press t=b", "ev focus t=b", "ev click t=b")
}

// fit (SPEC §5.2): after whatever changed the rows the document needs, on
// placements that asked, once for each change, from the rows placed.
func TestFit(t *testing.T) {
	h := New(t)
	send(h, hotty.Doc("f", `<p id=a>one</p>`), hotty.Place("f", hotty.Placement{Cols: 10, Fit: hotty.On}))
	expect(t, sent(h))
	send(h, hotty.SetText("f", "a", "one\ntwo"))
	expect(t, sent(h), `ev fit t= {"r":2}`)
	send(h, hotty.SetText("f", "a", "one"))
	expect(t, sent(h), `ev fit t= {"r":1}`)
	send(h, hotty.SetText("f", "a", "uno"))
	expect(t, sent(h))

	// Placed at rows the document does not need: told at once.
	send(h, hotty.Place("f", hotty.Placement{Cols: 10, Rows: 4, Fit: hotty.On}))
	expect(t, sent(h), `ev fit t= {"r":1}`)

	// A resource can change any document: each one that asked is checked.
	send(h, hotty.Doc("g", `<p>one</p>`), hotty.Place("g", hotty.Placement{Cols: 10, Rows: 3, Fit: hotty.On}))
	expect(t, sent(h), `ev fit t= {"r":1}`)
	tall := 1
	h.autoRows = func(*Surface, int) int { return tall }
	tall = 5
	send(h, hotty.Res("img", "image/png", []byte("x")))
	expect(t, sent(h), `ev fit t= {"r":5}`, `ev fit t= {"r":5}`)

	// Placed again without f, it goes on, counted from its own rows; f=0
	// stops it; a hidden surface sends none.
	send(h, hotty.Place("f", hotty.Placement{Cols: 10, Rows: 2}), hotty.Hide("g"))
	expect(t, sent(h), `ev fit t= {"r":5}`)
	tall = 2
	send(h, hotty.Res("img", "image/png", []byte("y")))
	expect(t, sent(h), `ev fit t= {"r":2}`)
	tall = 3
	send(h, hotty.Place("f", hotty.Placement{Cols: 10, Rows: 3, Fit: hotty.Off}), hotty.SetText("f", "a", "x"))
	expect(t, sent(h))
}

// Hover (SPEC §9.4): each change of the element under the pointer, out
// when it leaves, only to a placement that asked.
func TestHover(t *testing.T) {
	h := New(t)
	list := `<div id=l><p id=a>a</p><p id=b>b</p></div>`
	send(h, hotty.Doc("l", list), hotty.Place("l", hotty.Placement{Cols: 10, Rows: 2, Hover: hotty.On}),
		hotty.Doc("m", list), hotty.Place("m", hotty.Placement{Cols: 10, Rows: 2}))
	sent(h)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(h.Hover("l", "a", 1, 0))
	expect(t, sent(h), `ev hover t=a {"c":1,"r":0}`)
	must(h.Hover("l", "a", 2, 0))
	expect(t, sent(h))
	must(h.Hover("l", "", 2, 1))
	expect(t, sent(h), `ev hover t= {"c":2,"r":1}`)
	// Onto a surface that did not ask: this one is left, that one is silent.
	must(h.Hover("m", "b", 1, 1))
	expect(t, sent(h), `ev hover t= {"out":true}`)
	must(h.Hover("l", "b", 1, 1))
	expect(t, sent(h), `ev hover t=b {"c":1,"r":1}`)
	h.Unhover()
	expect(t, sent(h), `ev hover t= {"out":true}`)
	h.Unhover()
	expect(t, sent(h))
	if err := h.Hover("l", "nope", 0, 0); !errors.Is(err, ErrNoElement) {
		t.Errorf("Hover on no element = %v", err)
	}

	// v belongs to the surface: placing again without it goes on, from what
	// the program heard; v=0 stops it, and v=1 again starts from out.
	must(h.Hover("l", "a", 1, 0))
	sent(h)
	send(h, hotty.Place("l", hotty.Placement{Cols: 10, Rows: 2}))
	must(h.Hover("l", "a", 1, 0))
	must(h.Hover("l", "b", 1, 1))
	expect(t, sent(h), `ev hover t=b {"c":1,"r":1}`)
	send(h, hotty.Place("l", hotty.Placement{Cols: 10, Rows: 2, Hover: hotty.Off}))
	must(h.Hover("l", "a", 1, 0))
	expect(t, sent(h))
	send(h, hotty.Place("l", hotty.Placement{Cols: 10, Rows: 2, Hover: hotty.On}))
	must(h.Hover("l", "a", 1, 0))
	expect(t, sent(h), `ev hover t=a {"c":1,"r":0}`)
	// Hidden, a surface sends none, and goes on from what the program
	// heard: the same element is not reported again.
	send(h, hotty.Hide("l"), hotty.Place("l", hotty.Placement{Cols: 10, Rows: 2}))
	must(h.Hover("l", "a", 1, 0))
	expect(t, sent(h))

	// Detached: nothing.
	send(h, hotty.Detach("l"))
	must(h.Hover("l", "a", 1, 0))
	h.Unhover()
	expect(t, sent(h))
}

// A drag (SPEC §9.1): dragstart, drag each time the element under the
// pointer changes (or, over none, the cell), dragend, and a click only
// where it began.
func TestDrag(t *testing.T) {
	h := New(t)
	grid := `<div id=g><i id=a data-on="click drag">a</i><i id=b data-on=drag>b</i><span id=gap>-</span>` +
		`<i data-on=drag>no id</i><input id=f></div>`
	send(h, hotty.Doc("g", grid), hotty.Place("g", hotty.Placement{Cols: 10, Rows: 2, Press: hotty.On}))
	sent(h)
	if err := h.DragStart("g", "a", 0, 0, "ctrl", "shift"); err != nil {
		t.Fatal(err)
	}
	_ = h.DragMove("a", 1, 0)   // the same element: nothing
	_ = h.DragMove("b", 2, 0)   // another: drag
	_ = h.DragMove("gap", 3, 0) // none: drag with no target
	_ = h.DragMove("", 3, 0)    // none, the same cell: nothing
	_ = h.DragMove("", 3, -1)   // none, another cell
	_ = h.DragEnd("b", 2, 1, "shift")
	expect(t, sent(h),
		`ev press t=a {"mods":5}`,
		`ev dragstart t=a {"c":0,"mods":5,"r":0}`,
		`ev drag t=b {"c":2,"r":0}`,
		`ev drag t= {"c":3,"r":0}`,
		`ev drag t= {"c":3,"r":-1}`,
		`ev dragend t=b {"c":2,"mods":1,"r":1}`)
	if h.Dragging() {
		t.Error("still dragging")
	}

	// Ending where it began is that element's click too.
	_ = h.DragStart("g", "a", 0, 0)
	_ = h.DragEnd("a", 0, 0)
	expect(t, sent(h), "ev press t=a", `ev dragstart t=a {"c":0,"r":0}`, `ev dragend t=a {"c":0,"r":0}`, "ev click t=a")

	// Nothing that opts in, or one with no id: a press, and no drag.
	if err := h.DragStart("g", "gap", 0, 0); !errors.Is(err, ErrNoReport) || h.Dragging() {
		t.Errorf("a drag on what does not opt in: %v", err)
	}
	expect(t, sent(h), "ev press t=gap")
	if err := h.DragMove("", 0, 0); !errors.Is(err, ErrNoDrag) {
		t.Errorf("a move with no drag: %v", err)
	}

	// The press takes the keyboard from the field, after dragstart.
	_ = h.Fill("g", "f", "x")
	sent(h)
	_ = h.DragStart("g", "b", 1, 0)
	if err := h.DragStart("g", "a", 0, 0); !errors.Is(err, ErrDragging) {
		t.Errorf("a second drag: %v", err)
	}
	expect(t, sent(h), "ev press t=b", `ev dragstart t=b {"c":1,"r":0}`, `ev change t=f {"value":"x"}`, "ev blur t=")

	// A new document or a hide cuts it short at the last cell; a detach or
	// a delete ends it with nothing.
	_ = h.DragMove("", 4, 1)
	sent(h)
	send(h, hotty.Doc("g", grid))
	expect(t, sent(h), `ev dragend t= {"c":4,"r":1}`)
	send(h, hotty.Place("g", hotty.Placement{Cols: 10, Rows: 2}))
	_ = h.DragStart("g", "b", 1, 0)
	send(h, hotty.Hide("g"))
	expect(t, sent(h), "ev press t=b", `ev dragstart t=b {"c":1,"r":0}`, `ev dragend t= {"c":1,"r":0}`)
	send(h, hotty.Place("g", hotty.Placement{Cols: 10, Rows: 2}))
	_ = h.DragStart("g", "b", 1, 0)
	send(h, hotty.Detach("g"))
	if h.Dragging() {
		t.Error("a detach leaves no drag")
	}
	expect(t, sent(h), "ev press t=b", `ev dragstart t=b {"c":1,"r":0}`)
}

// Steps (SPEC §9.1): a host reports where in
// an element with data-steps the pointer is, as the test gives it: clamped
// to the counts, measured against the element the drag began on wherever
// the pointer goes, with a drag each time it changes.
func TestDragSteps(t *testing.T) {
	h := New(t)
	doc := `<i id=track data-on=drag data-steps=20>t</i><i id=pad data-on=drag data-steps="0 4">p</i>` +
		`<i id=bad data-on=drag data-steps="2.5">b</i><i id=other data-on=drag>o</i>`
	send(h, hotty.Doc("s", doc), hotty.Place("s", hotty.Placement{Cols: 10, Rows: 4}))
	sent(h)
	_ = h.DragStartStep("s", "track", 0, 0, 3, 9)
	_ = h.DragMoveStep("track", 1, 0, 3, 0) // the same element and step: nothing
	_ = h.DragMoveStep("track", 2, 0, 5, 0) // another step: drag
	_ = h.DragMoveStep("", 2, 2, 5, 0)      // off the track, at the same step
	_ = h.DragMoveStep("", 9, 2, 99, 0)     // past its end: clamped
	_ = h.DragMove("other", 5, 3)           // a move with no step keeps the last
	_ = h.DragEndStep("", -4, 3, -1, 0)
	expect(t, sent(h),
		`ev dragstart t=track {"c":0,"r":0,"x":3}`,
		`ev drag t=track {"c":2,"r":0,"x":5}`,
		`ev drag t= {"c":2,"r":2,"x":5}`,
		`ev drag t= {"c":9,"r":2,"x":20}`,
		`ev drag t=other {"c":5,"r":3,"x":20}`,
		`ev dragend t= {"c":-4,"r":3,"x":0}`)

	// Only the axes an element counts; a value that is not one or two whole
	// numbers counts none.
	_ = h.DragStartStep("s", "pad", 0, 1, 7, 2)
	_ = h.DragEndStep("pad", 0, 1, 7, 3)
	_ = h.DragStartStep("s", "bad", 0, 2, 1, 1)
	_ = h.DragEnd("bad", 0, 2)
	expect(t, sent(h),
		`ev dragstart t=pad {"c":0,"r":1,"y":2}`,
		`ev dragend t=pad {"c":0,"r":1,"y":3}`,
		`ev dragstart t=bad {"c":0,"r":2}`,
		`ev dragend t=bad {"c":0,"r":2}`)

	// A hide cuts it short with the last step.
	_ = h.DragStartStep("s", "track", 0, 0, 12, 0)
	send(h, hotty.Hide("s"))
	expect(t, sent(h), `ev dragstart t=track {"c":0,"r":0,"x":12}`, `ev dragend t= {"c":0,"r":0,"x":12}`)
}

// Drop (SPEC §9.1): a host takes an element
// with drop in its data-on as a drag's t, never drags it, and reports
// where in a t with data-steps the pointer is, as the test gives it. A drag
// that ends on a drop-only element inside the one it began on is a click.
func TestDrop(t *testing.T) {
	h := New(t)
	doc := `<i id=card data-on=drag>c</i><i id=ada data-on=drop>Ada</i><i id=col data-on=drop data-steps="3 2">col</i>` +
		`<i id=row data-on="drag click"><b id=pin data-on=drop>p</b></i><i id=knob data-on=drag data-steps=4>k</i>`
	send(h, hotty.Doc("s", doc), hotty.Place("s", hotty.Placement{Cols: 10, Rows: 6}))
	sent(h)
	_ = h.DragStart("s", "card", 1, 0)
	_ = h.DragMove("ada", 1, 1)          // a drop-only t
	_ = h.DragMove("col", 1, 2)          // a t with steps, at step 0 of it
	_ = h.DragOver("col", 2, 2, 0, 0)    // the same step: nothing
	_ = h.DragOver("col", 2, 3, 1, 1)    // another step: drag
	_ = h.DragOver("col", 4, 4, 9, 2)    // past the count: clamped
	_ = h.DragEndOver("col", 4, 4, 3, 2) // the same step, in the dragend
	expect(t, sent(h),
		`ev dragstart t=card {"c":1,"r":0}`,
		`ev drag t=ada {"c":1,"r":1}`,
		`ev drag t=col {"c":1,"r":2,"tx":0,"ty":0}`,
		`ev drag t=col {"c":2,"r":3,"tx":1,"ty":1}`,
		`ev drag t=col {"c":4,"r":4,"tx":3,"ty":2}`,
		`ev dragend t=col {"c":4,"r":4,"tx":3,"ty":2}`)

	// A press on a drop-only element starts no drag.
	if err := h.DragStart("s", "ada", 1, 1); !errors.Is(err, ErrNoReport) {
		t.Errorf("DragStart on a drop-only element: %v", err)
	}
	// One inside a draggable element starts that one's, and a release on it
	// is still a click.
	_ = h.DragStart("s", "pin", 1, 5)
	_ = h.DragEnd("pin", 1, 5)
	expect(t, sent(h),
		`ev dragstart t=row {"c":1,"r":5}`,
		`ev dragend t=pin {"c":1,"r":5}`,
		`ev click t=row`)

	// A t that only opts in to drags has no steps in it.
	_ = h.DragStart("s", "card", 1, 0)
	_ = h.DragOver("knob", 1, 6, 2, 0)
	_ = h.DragEndOver("knob", 1, 6, 3, 0)
	expect(t, sent(h),
		`ev dragstart t=card {"c":1,"r":0}`,
		`ev drag t=knob {"c":1,"r":6}`,
		`ev dragend t=knob {"c":1,"r":6}`)
}

func TestReplies(t *testing.T) {
	h := New(t)
	send(h, hotty.Doc("x", "<p id=p>p</p>", hotty.N(1)), hotty.SetText("x", "p", "q", hotty.Q(hotty.ReplyAlways)))
	expect(t, sent(h), "ok re=doc s=x", "ok re=delta s=x")
	if n := len(h.Replies()); n != 2 {
		t.Errorf("%d replies", n)
	}
	send(h, hotty.Place("x", hotty.Placement{Cols: 5, Rows: 1}))
	_ = h.Emit("x", "custom", "p", nil)
	if evs := h.Events(); len(evs) != 1 || evs[0].Kind != "custom" || evs[0].Target != "p" {
		t.Errorf("Events %+v", evs)
	}
}

func TestBody(t *testing.T) {
	r, _ := hotty.ReplyOf(hotty.Message{Control: hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "q"}},
		Payload: Body(`{"v":"0.5","scale":2.0,"cellcss":{"w":9.0,"h":18.0},"limits":{"surfaces":3}}`)})
	if c := r.Caps; c == nil || c.Scale != 2 || c.CellCSS.W != 9 || c.Limits["surfaces"] != 3 {
		t.Errorf("caps %+v", c)
	}
	defer func() {
		if recover() == nil {
			t.Error("Body of what is not JSON")
		}
	}()
	Body(`{`)
}

// A delta's ops apply in order as one; the body is checked whole first, a
// missing target fails its op alone, and the reply names the first failure
// by its index (SPEC §6.1).
func TestDeltaOps(t *testing.T) {
	h := shown(t, "x", `<p id=a>1</p><p id=b>2</p>`, Lenient())
	ack := func(n int) hotty.ReplyOption { return hotty.N(n) }
	send(h, hotty.Delta("x", []hotty.Op{hotty.OpText("a", "x"), hotty.OpAttr("b", "class", "on"), hotty.OpAfter("b", "<p id=c>3</p>")}, ack(1)))
	expect(t, sent(h), "ok re=delta s=x")
	s := h.Surface("x")
	if s.TextOf("a") != "x" || s.TextOf("c") != "3" {
		t.Errorf("html %s", s.HTML())
	}
	send(h, hotty.Delta("x", []hotty.Op{hotty.OpText("a", "y"), hotty.OpText("zz", "n"), hotty.OpText("b", "z"), hotty.OpRemove("nope")}, hotty.Q(hotty.ReplyOnError)))
	expect(t, sent(h), "err re=delta s=x ENOTARGET i=1")
	if s.TextOf("a") != "y" || s.TextOf("b") != "z" {
		t.Errorf("the ops after a missing target apply: %s", s.HTML())
	}
	// Whole body checked first: nothing applies.
	send(h, hotty.Delta("x", []hotty.Op{hotty.OpText("a", "never"), {Op: "frob", Target: "a"}}, hotty.Q(hotty.ReplyOnError)))
	expect(t, sent(h), "err re=delta s=x EINVAL i=1")
	send(h, hotty.Delta("x", nil, hotty.Q(hotty.ReplyOnError)))
	expect(t, sent(h), "err re=delta s=x EINVAL")
	if s.TextOf("a") != "y" {
		t.Error("a delta refused applied an op")
	}
	if got := h.Errors(); len(got) != 2 {
		t.Errorf("errors %v", got)
	}
}

// Errors only, unless asked (SPEC §3.5).
func TestQuietDefaults(t *testing.T) {
	h := New(t)
	send(h, "\x1b]7279;a=doc:s=x;"+"PHAgaWQ9YT5hPC9wPg==\x1b\\", hotty.Place("x", hotty.Placement{Cols: 5, Rows: 1}))
	expect(t, sent(h))
	send(h, hotty.Place("x", hotty.Placement{Cols: 5, Rows: 1}, hotty.N(2)), hotty.Hide("x", hotty.N(3), hotty.Q(hotty.ReplyOnError)), "\x1b]7279;a=hide:s=nope\x1b\\")
	expect(t, sent(h), "ok re=place s=x", "err re=hide s=nope ENOENT")
	send(h, hotty.Query(7))
	if r := h.sentR(); len(r) != 1 || !r[0].OK || r[0].N != 7 {
		t.Errorf("a query is answered: %+v", r)
	}
}

func (h *Host) sentR() []hotty.Reply { r, _ := h.sentBoth(); return r }

// A key bound to prefix reaches the program, and so does every key after
// it, tab and escape too, until the program sends a=focus or a=blur, the
// surface loses the keyboard, or the user focuses an element with a press
// (SPEC §10.5).
func TestPrefix(t *testing.T) {
	doc := `<button id=b data-keys="g=prefix">B</button><button id=c>C</button><input id=f data-keys="ctrl+k=prefix">`
	h := shown(t, "x", doc)
	send(h, hotty.Focus("x", "b"))
	for _, k := range []string{"g", "space", "tab", "escape", "enter"} {
		if h.Key(k) {
			t.Errorf("%s was used after a prefix", k)
		}
	}
	if h.Prefix() != "x" {
		t.Error("no prefix")
	}
	h.drain()
	send(h, hotty.Focus("x", ""))
	if h.Prefix() != "" || !h.Key("space") {
		t.Error("a=focus ends the prefix")
	}
	expect(t, sent(h), "ev click t=b")
	h.Key("g")
	send(h, hotty.Blur("x"))
	send(h, hotty.Focus("x", "b"))
	if h.Prefix() != "" || !h.Key("tab") {
		t.Error("a=blur ends the prefix")
	}
	h.Key("shift+tab")
	// A press that focuses an element ends it; so does losing the keyboard.
	send(h, hotty.Focus("x", "b"))
	h.Key("g")
	_ = h.Click("x", "c")
	if h.Prefix() != "" || !h.Key("space") {
		t.Error("a press ends the prefix")
	}
	send(h, hotty.Focus("x", "b"))
	h.Key("g")
	_ = h.Blur("x")
	send(h, hotty.Focus("x", "c"))
	if !h.Key("space") {
		t.Error("losing the keyboard ends the prefix")
	}
	// In a text field: nothing is typed or edited after it.
	h.drain()
	send(h, hotty.Focus("x", "f"))
	h.Key("a")
	h.Key("ctrl+k")
	h.Key("b")
	h.Key("backspace")
	send(h, hotty.Focus("x", "f"))
	h.Key("c")
	if v, _ := h.Surface("x").Value("f"); v != "ac" {
		t.Errorf("the field holds %q", v)
	}
}

// The program's menu is kept, and the level it shows is hidden by show=0,
// Escape, a key the level does not list, a change of screen and a reset
// (SPEC §10.7).
func TestMenu(t *testing.T) {
	menu := &hotty.MenuTree{Label: "Files", Items: []hotty.MenuItem{
		{Key: "space", Label: "Leader", Enabled: true, Items: []hotty.MenuItem{
			{Key: "f", Label: "+file", Enabled: true, Items: []hotty.MenuItem{{Key: "n", Label: "New", Name: "new", Enabled: true}}},
			{Key: "mod+q", Label: "Quit", Name: "quit", Enabled: true},
			{Label: "Sort", Name: "sort", Enabled: true},
		}},
	}}
	h := New(t)
	show := func() {
		t.Helper()
		send(h, hotty.MenuHide())
		h.drain()
		send(h, hotty.Menu(menu, hotty.N(1)))
		expect(t, sent(h), "ok re=menu")
		if got := h.Menu(); got == nil || got.Items[0].Key != "space" {
			t.Fatalf("Menu() = %+v", got)
		}
		if h.Shown() != nil {
			t.Fatalf("a menu shows nothing: %+v", h.Shown())
		}
		send(h, hotty.MenuShow([]string{"space"}))
		if got := h.Shown(); len(got) != 2 || got[0].Key != "f" || got[1].Key != "mod+q" {
			t.Fatalf("Shown() = %+v", got)
		}
	}
	show()
	send(h, hotty.MenuHide())
	if h.Shown() != nil || h.Menu() == nil {
		t.Error("show=0 hides the level, not the menu")
	}
	send(h, hotty.MenuShow(nil))
	if got := h.Shown(); len(got) != 1 || got[0].Key != "space" {
		t.Errorf("the top level: %+v", got)
	}
	send(h, hotty.MenuShow([]string{"space", "q"}))
	if h.Shown() != nil {
		t.Error("keys that reach no group")
	}
	show()
	h.Key("f") // listed: the level stays until the program shows another
	h.Key("ctrl+q")
	if h.Shown() == nil {
		t.Error("a key it lists hides it")
	}
	h.Key("x")
	if h.Shown() != nil {
		t.Error("a key it does not list")
	}
	show()
	h.Key("escape")
	if h.Shown() != nil {
		t.Error("escape")
	}
	show()
	h.Type("z")
	if h.Shown() != nil {
		t.Error("typed text it does not list")
	}
	// A new menu shows the same keys' level of it, and one that no longer
	// has the group shows nothing; a hidden level stays hidden.
	show()
	next := &hotty.MenuTree{Items: []hotty.MenuItem{{Key: "space", Enabled: true, Items: []hotty.MenuItem{{Key: "w", Label: "Write"}}}}}
	send(h, hotty.Menu(next))
	if got := h.Shown(); len(got) != 1 || got[0].Key != "w" {
		t.Errorf("a new menu: %+v", got)
	}
	send(h, hotty.Menu(&hotty.MenuTree{Items: []hotty.MenuItem{{Key: "x"}}}))
	if h.Shown() != nil || h.Menu() == nil {
		t.Error("a new menu with no such group")
	}
	send(h, hotty.Menu(nil))
	if h.Menu() != nil || h.Shown() != nil {
		t.Error("an empty a=menu leaves none")
	}
	// Each screen keeps its own menu: the alternate starts with none, and
	// leaving it drops its own and hides the level; the main's stays.
	show()
	send(h, "\x1b[?1049h")
	if h.Menu() != nil || h.Shown() != nil {
		t.Error("the alternate screen has a menu of its own")
	}
	send(h, hotty.Menu(next))
	send(h, "\x1b[?1049l")
	if got := h.Menu(); got == nil || got.Label != "Files" || h.Shown() != nil {
		t.Errorf("back on the main screen: %+v", got)
	}
	send(h, "\x1b[?1049h")
	if h.Menu() != nil {
		t.Error("leaving the alternate screen drops its menu")
	}
	send(h, "\x1b[?1049l")
	show()
	send(h, "\x1bc")
	if h.Menu() != nil || h.Shown() != nil {
		t.Error("a reset")
	}
	// A body that is no menu or no keys is refused, and so is any a=menu on
	// a host without the feature.
	lenient := New(t, Lenient())
	for _, c := range []hotty.Control{{{K: "a", V: "menu"}, {K: "n", V: "2"}}, {{K: "a", V: "menu"}, {K: "n", V: "2"}, {K: "show", V: "1"}}} {
		send(lenient, hotty.Encode(c, Body(`["space"]`)))
		if r := lenient.sentR(); len(r) != 1 || r[0].Code != hotty.EINVAL {
			t.Errorf("a body that is none: %+v", r)
		}
	}
	send(lenient, hotty.Encode(hotty.Control{{K: "a", V: "menu"}, {K: "n", V: "2"}, {K: "show", V: "2"}}, nil))
	if r := lenient.sentR(); len(r) != 1 || r[0].Code != hotty.EINVAL {
		t.Errorf("show=2: %+v", r)
	}
	plain := New(t, Lenient(), Caps(hotty.Caps{Features: []string{hotty.FeatureHover}}))
	for _, cmd := range []string{hotty.Menu(menu, hotty.N(3)), hotty.MenuShow(nil, hotty.N(3)), hotty.MenuHide(hotty.N(3))} {
		send(plain, cmd)
		if r := plain.sentR(); len(r) != 1 || r[0].Code != hotty.EINVAL || plain.Menu() != nil {
			t.Errorf("a host without menu: %+v, keeps %+v", r, plain.Menu())
		}
	}
}

// The touch areas a program names are kept per screen: the alternate
// screen starts with none and leaving it drops its own, a reset drops both,
// and a body that is no map of areas is refused and changes nothing (SPEC
// §9.6).
func TestTouchAreas(t *testing.T) {
	h := New(t)
	if h.TouchAreas() != nil {
		t.Error("areas before any a=touch")
	}
	slider := []hotty.TouchArea{{C: 2, R: 3, W: 10, H: 1, Drag: "x"}, {C: 0, R: 5, W: 1, H: 4, Drag: "y"}}
	send(h, hotty.Touch(slider))
	if got := h.TouchAreas(); !slices.Equal(got, slider) {
		t.Errorf("areas %+v", got)
	}
	got := h.TouchAreas()
	got[0].C = 99
	if h.TouchAreas()[0].C != 2 {
		t.Error("TouchAreas shares the host's")
	}
	send(h, "\x1b[?1049h")
	if h.TouchAreas() != nil {
		t.Error("the alternate screen has areas of its own")
	}
	knob := []hotty.TouchArea{{C: 1, R: 1, W: 1, H: 1, Drag: "xy"}}
	send(h, hotty.Touch(knob))
	if got := h.TouchAreas(); !slices.Equal(got, knob) {
		t.Errorf("the alternate screen's %+v", got)
	}
	send(h, "\x1b[?1049l")
	if got := h.TouchAreas(); !slices.Equal(got, slider) {
		t.Errorf("back on the main screen: %+v", got)
	}
	send(h, "\x1b[?1049h")
	if h.TouchAreas() != nil {
		t.Error("leaving the alternate screen drops its areas")
	}
	send(h, hotty.Touch(knob))
	send(h, "\x1bc")
	if h.TouchAreas() != nil {
		t.Error("a reset, on the alternate screen")
	}
	send(h, "\x1b[?1049l")
	if h.TouchAreas() != nil {
		t.Error("a reset, on the main screen")
	}
	send(h, hotty.Touch(slider))
	send(h, hotty.Touch(nil))
	if h.TouchAreas() != nil {
		t.Error("an a=touch with no areas leaves none")
	}
	// What is no map of areas is EINVAL, and keeps the areas there were.
	lenient := New(t, Lenient())
	send(lenient, hotty.Touch(slider))
	for _, body := range []string{
		`["x"]`, `{}`, `{"areas":{"c":0}}`, `{"areas":[1]}`,
		`{"areas":[{"c":0,"r":0,"w":1,"drag":"x"}]}`,
		`{"areas":[{"c":0.5,"r":0,"w":1,"h":1,"drag":"x"}]}`,
		`{"areas":[{"c":0,"r":0,"w":1,"h":1,"drag":"yx"}]}`,
		`{"areas":[{"c":-1,"r":0,"w":1,"h":1,"drag":"x"}]}`,
		`{"areas":[{"c":0,"r":0,"w":1,"h":0,"drag":"x"}]}`,
	} {
		send(lenient, hotty.Encode(hotty.Control{{K: "a", V: "touch"}, {K: "n", V: "2"}}, Body(body)))
		if r := lenient.sentR(); len(r) != 1 || r[0].Code != hotty.EINVAL {
			t.Errorf("%s: %+v", body, r)
		}
		if got := lenient.TouchAreas(); !slices.Equal(got, slider) {
			t.Errorf("%s changed the areas: %+v", body, got)
		}
	}
	many := make([]hotty.TouchArea, hotty.TouchMax+1)
	for i := range many {
		many[i] = hotty.TouchArea{C: i % 80, R: i / 80, W: 1, H: 1, Drag: "x"}
	}
	send(lenient, hotty.Touch(many, hotty.N(3)))
	if r := lenient.sentR(); len(r) != 1 || r[0].Code != hotty.EINVAL {
		t.Errorf("%d areas: %+v", len(many), r)
	}
	send(lenient, hotty.Touch(many[:hotty.TouchMax], hotty.N(4)))
	if r := lenient.sentR(); len(r) != 1 || r[0].Code != "" || len(lenient.TouchAreas()) != hotty.TouchMax {
		t.Errorf("%d areas: %+v", hotty.TouchMax, r)
	}
	send(lenient, hotty.Encode(hotty.Control{{K: "a", V: "touch"}, {K: "n", V: "5"}}, Body(`{"areas":[],"more":1}`)))
	if r := lenient.sentR(); len(r) != 1 || r[0].Code != "" || lenient.TouchAreas() != nil {
		t.Errorf("no areas: %+v", r)
	}
}

// A pick sends command with the item's name and nothing else; a disabled
// item, an unknown name and no menu send nothing (SPEC §10.7).
func TestPick(t *testing.T) {
	h := New(t)
	if h.Pick("new") {
		t.Error("a pick with no menu")
	}
	send(h, hotty.Menu(&hotty.MenuTree{Items: []hotty.MenuItem{
		{Key: "g", Label: "Go", Enabled: true, Items: []hotty.MenuItem{
			{Label: "New", Name: "new", Enabled: true},
			{Label: "Off", Name: "off"},
		}},
		{Label: "Again", Name: "new", Enabled: true},
	}}))
	h.drain()
	if !h.Pick("new") {
		t.Fatal("no pick")
	}
	_, evs := h.sentBoth()
	if len(evs) != 1 || evs[0].Kind != hotty.EventCommand || evs[0].Surface != "" || evs[0].Target != "" {
		t.Fatalf("events %+v", evs)
	}
	if c := evs[0].Command; c == nil || c.Name != "new" || c.Key != "" || c.HasText {
		t.Errorf("command %+v", c)
	}
	if !bytes.Equal(evs[0].Detail, Body(`{"name":"new"}`)) {
		t.Errorf("detail %x", evs[0].Detail)
	}
	for _, name := range []string{"off", "nothing", ""} {
		if h.Pick(name) {
			t.Errorf("a pick of %q", name)
		}
	}
	if _, evs := h.sentBoth(); len(evs) != 0 {
		t.Errorf("events %+v", evs)
	}
}

// Changed capabilities are told with every field that differs from what the
// program last heard (SPEC §4, §9); a zoom says nothing.
func TestChangeCaps(t *testing.T) {
	h := New(t)
	h.Change(func(c *hotty.Caps) { c.Mod = "super" })
	expect(t, sent(h)) // the program has not asked
	send(h, hotty.Query(1))
	h.drain()
	h.Change(func(c *hotty.Caps) { c.Scale = 2 })
	expect(t, sent(h))
	h.Change(func(c *hotty.Caps) { c.CellCSS = hotty.Size{W: 10, H: 20}; c.Mod = "ctrl"; c.Features = nil })
	evs := h.sentEvents()
	if len(evs) != 1 || evs[0].Kind != hotty.EventCaps || evs[0].Surface != "" || evs[0].Changed == nil {
		t.Fatalf("events %+v", evs)
	}
	got := evs[0].Changed
	if got.CellCSS != (hotty.Size{W: 10, H: 20}) || got.Mod != "ctrl" || got.Scale != 2 || len(got.Features) != 0 {
		t.Errorf("changed %+v", got)
	}
	// The scale rode along; it is heard now, and the features are empty.
	merged := hotty.Merge(DefaultCaps(), *got)
	if merged.CellCSS.H != 20 || merged.Features != nil || merged.Scale != 2 {
		t.Errorf("merged %+v", merged)
	}
	h.Change(func(c *hotty.Caps) { c.Edit = map[string][]string{"copy": {"super+c"}} })
	if evs := h.sentEvents(); len(evs) != 1 || len(evs[0].Changed.Edit["copy"]) != 1 {
		t.Errorf("edit: %+v", evs)
	}
	// A new cell lays documents out again: fit.
	send(h, hotty.Doc("f", "<p>x</p>"), hotty.Place("f", hotty.Placement{Cols: 10, Rows: 1, Fit: hotty.On}))
	h.drain()
	h.autoRows = func(*Surface, int) int { return 3 }
	h.Change(func(c *hotty.Caps) { c.CellCSS.H = 30 })
	var kinds []string
	for _, e := range h.sentEvents() {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Equal(kinds, []string{"caps", "fit"}) {
		t.Errorf("events %v", kinds)
	}
}

// Modifiers ride on clicks, presses and drags as bits; a press with Alt is
// the program's gesture (SPEC §9, §9.2).
func TestMods(t *testing.T) {
	h := New(t)
	send(h, hotty.Doc("m", `<button id=b>b</button>`), hotty.Place("m", hotty.Placement{Cols: 10, Rows: 1, Press: hotty.On}))
	h.drain()
	_ = h.Click("m", "b", "shift", "super")
	_ = h.Press("m", "", "ctrl")
	_ = h.Click("m", "b", "alt")
	expect(t, sent(h), `ev press t=b {"mods":9}`, "ev focus t=b", `ev click t=b {"mods":9}`, `ev press t= {"mods":4}`)
	h.Key("shift+enter")
	expect(t, sent(h), `ev click t=b {"mods":1}`)
}

// Without the features hover and count, the host says neither.
func TestWithoutFeatures(t *testing.T) {
	h := New(t, Caps(hotty.Caps{Features: []string{}}))
	send(h, hotty.Doc("f", `<button id=b>b</button>`), hotty.Place("f", hotty.Placement{Cols: 10, Rows: 1, Hover: hotty.On}))
	h.drain()
	_ = h.Hover("f", "b", 0, 0)
	h.Unhover()
	_ = h.DoubleClick("f", "b")
	var counts []int
	for _, e := range h.Events() {
		if e.Kind == hotty.EventHover {
			t.Error("hover without the feature")
		}
		if e.Kind == hotty.EventClick {
			counts = append(counts, e.Count)
		}
	}
	if !slices.Equal(counts, []int{1, 1}) {
		t.Errorf("counts %v", counts)
	}
}
