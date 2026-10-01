// Package hottytest is a HOTTY host that runs inside a test. The program
// under test reads from it and writes to it as it would a terminal; the
// test then looks at what the program showed, and plays the user.
//
//	h := hottytest.New(t)
//	run(h.Term("tool"))              // the program under test; or tea.WithInput(h), …
//	card := h.Surface("tool-card")
//	card.TextOf("status")            // what it shows
//	h.Click("tool-card", "retry")    // what the user does
//
// The host keeps every surface's document as the program's commands leave
// it, with the patch operations and the morph of SPEC §6, and answers each
// command as SPEC §3.6 has hosts do. It passes the HOTTY conformance
// vectors. It lays nothing out and draws no pixels: a placement with auto
// rows gets an estimate (AutoRows).
//
// A placement made on the alternate screen goes with it, and so does its
// surface (SPEC §5.4). A full reset (RIS) deletes every surface.
//
// It also answers the queries terminals answer: Primary Device Attributes,
// the cursor's position, the background colour, and, when asked to
// (KittyGraphics), the kitty graphics query. Text keeps the cells the
// program printed (Screen).
//
// A Host made with Text is a terminal that is not a HOTTY host: the
// program's commands get no answer, as in any other terminal.
//
// Unless Lenient, a program that breaks the protocol fails the test: a
// malformed command, one the host refuses as invalid (EINVAL), or a HOTTY
// command other than the query sent to a terminal that is not a host.
package hottytest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/term"
)

// Errors of the user's actions.
var (
	ErrNotHost    = errors.New("hottytest: the terminal is not a HOTTY host")
	ErrNoSurface  = errors.New("hottytest: no such surface")
	ErrNoElement  = errors.New("hottytest: no such element")
	ErrDetached   = errors.New("hottytest: the surface is detached, and reports nothing")
	ErrNotPlaced  = errors.New("hottytest: the surface is not placed")
	ErrNoPress    = errors.New("hottytest: the placement did not ask for presses (Placement.Press)")
	ErrNotControl = errors.New("hottytest: the element is not a control that takes this")
	ErrNoReport   = errors.New("hottytest: nothing with an id reports this click")
)

// Option configures a Host.
type Option func(*Host)

// Caps sets the capabilities the host reports. Fields left zero keep the
// defaults (DefaultCaps).
func Caps(c hotty.Caps) Option {
	return func(h *Host) {
		d := &h.caps
		if c.V != "" {
			d.V = c.V
		}
		if c.Ops != nil {
			d.Ops = c.Ops
		}
		if c.Events != nil {
			d.Events = c.Events
		}
		if c.Cell.W > 0 && c.Cell.H > 0 {
			d.Cell = c.Cell
		}
		if c.Scale > 0 {
			d.Scale = c.Scale
		}
		if c.Scheme != "" {
			d.Scheme = c.Scheme
		}
		if c.Limits != nil {
			d.Limits = c.Limits
		}
		if c.Net != nil {
			d.Net = c.Net
		}
		if c.Host != "" {
			d.Host = c.Host
		}
	}
}

// Text makes the host a terminal that is not a HOTTY host: it answers DA1
// and the cursor's position, and ignores HOTTY.
func Text() Option { return func(h *Host) { h.text = true } }

// Lenient keeps protocol errors from failing the test; Errors still lists
// them.
func Lenient() Option { return func(h *Host) { h.lenient = true } }

// KittyGraphics makes the terminal answer the kitty graphics query, as one
// that shows images does.
func KittyGraphics() Option { return func(h *Host) { h.kitty = true } }

// Size sets the terminal's size in cells; 80×24 by default.
func Size(cols, rows int) Option { return func(h *Host) { h.cols, h.rows = cols, rows } }

// AutoRows sets how many rows a placement with auto rows gets, for its
// surface laid out cols wide. The default counts the body's lines of text
// and its block elements.
func AutoRows(f func(s *Surface, cols int) int) Option { return func(h *Host) { h.autoRows = f } }

// DefaultCaps are the capabilities a Host reports unless Caps says
// otherwise.
func DefaultCaps() hotty.Caps {
	c := hotty.Caps{V: hotty.Version, Scale: 1, Scheme: "dark", Host: "hottytest",
		Limits: map[string]int{"surfaces": 64}}
	c.Cell.W, c.Cell.H = 9, 18
	for _, op := range []hotty.Op{hotty.OpMorph, hotty.OpInner, hotty.OpReplace, hotty.OpAppend, hotty.OpPrepend,
		hotty.OpBefore, hotty.OpAfter, hotty.OpRemove, hotty.OpAttr, hotty.OpUnattr, hotty.OpText, hotty.OpVar} {
		c.Ops = append(c.Ops, string(op))
	}
	c.Events = []string{hotty.EventClick, hotty.EventChange, hotty.EventInput, hotty.EventSubmit,
		hotty.EventPress, hotty.EventFocus, hotty.EventBlur, hotty.EventResize}
	return c
}

// Host is a HOTTY host for a test: a terminal the program reads (Read) and
// writes (Write). Its methods are safe for concurrent use.
type Host struct {
	tb       testing.TB
	caps     hotty.Caps
	text     bool
	lenient  bool
	kitty    bool
	cols     int
	rows     int
	autoRows func(*Surface, int) int

	mu sync.Mutex
	// What the program writes, parsed as it comes.
	state   byte // 0 text, 'e' after ESC, '[' CSI, ']' OSC, 'P' another string
	seq     []byte
	partial []byte // the start of a UTF-8 rune split between writes
	dec     hotty.Decoder
	invalid int
	output  strings.Builder
	scr     screen
	main    screen // the main screen, while the alternate one is on

	surfaces map[string]*Surface
	created  int
	res      map[string]resource
	keyboard *Surface // the surface that has the keyboard
	alt      bool     // the alternate screen is on
	commands []hotty.Message
	replies  []hotty.Message
	events   []hotty.Event
	opened   []string // hyperlinks the user opened
	errs     []string

	in input
}

type resource struct {
	mime string
	data []byte
}

// New makes a host for a test, closed when the test ends. tb may be nil
// outside a test: then nothing fails, and Errors lists what would have.
func New(tb testing.TB, opts ...Option) *Host {
	h := &Host{tb: tb, caps: DefaultCaps(), cols: 80, rows: 24,
		surfaces: map[string]*Surface{}, res: map[string]resource{}}
	h.in.cond = sync.NewCond(&h.in.mu)
	for _, o := range opts {
		o(h)
	}
	h.scr = screen{cols: h.cols, rows: h.rows}
	if h.autoRows == nil {
		h.autoRows = estimateRows
	}
	if tb != nil {
		tb.Cleanup(func() { _ = h.Close() })
	}
	return h
}

// --- the terminal's two sides ---------------------------------------------

// input is what the program reads: an unbounded buffer, so the host never
// blocks on a program that is busy writing.
type input struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func (in *input) write(s string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return
	}
	in.buf = append(in.buf, s...)
	in.cond.Broadcast()
}

// Read is the program's input: answers, events and keys, as a terminal
// sends them. It blocks until there is some, or the host is closed.
func (h *Host) Read(p []byte) (int, error) {
	in := &h.in
	in.mu.Lock()
	defer in.mu.Unlock()
	for len(in.buf) == 0 && !in.closed {
		in.cond.Wait()
	}
	if len(in.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, in.buf)
	in.buf = in.buf[n:]
	return n, nil
}

// Close ends the program's input: a Read gets io.EOF once it has read what
// was sent.
func (h *Host) Close() error {
	h.in.mu.Lock()
	h.in.closed = true
	h.in.cond.Broadcast()
	h.in.mu.Unlock()
	return nil
}

// Type sends text to the program as keys typed: "q", "\r" for Enter,
// "\x03" for Ctrl-C, "\x1b" for Escape.
func (h *Host) Type(keys string) { h.in.write(keys) }

// TermSize is the terminal's size (Size).
func (h *Host) TermSize() (cols, rows int) { return h.cols, h.rows }

// Term is a term.Term on the host, for a program named name: its
// surfaces are name-…, without the process id term.Open adds.
func (h *Host) Term(name string) *term.Term {
	return term.New(h, h, name, func() term.Size { return term.Size{Cols: h.cols, Rows: h.rows} }, nil)
}

// Write is the program's output. It is parsed as it comes: HOTTY commands
// are carried out and answered, the terminal's queries answered, and the
// rest kept as cells (Screen). It never fails.
func (h *Host) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.output.Write(p)
	for _, c := range p {
		h.byte(c)
	}
	return len(p), nil
}

func (h *Host) byte(c byte) {
	switch h.state {
	case 0:
		switch {
		case c == 0x1b:
			h.state = 'e'
		case c < 0x20 || c == 0x7f:
			h.scr.control(c)
		default:
			h.partial = append(h.partial, c)
			if utf8.FullRune(h.partial) {
				r, _ := utf8.DecodeRune(h.partial)
				h.partial = h.partial[:0]
				h.scr.put(r)
			}
		}
	case 'e':
		h.state = 0
		switch c {
		case '[':
			h.state, h.seq = '[', h.seq[:0]
		case ']':
			h.state, h.seq = ']', append(h.seq[:0], "\x1b]"...)
		case 'P', '_', '^', 'X':
			h.state, h.seq = 'P', append(h.seq[:0], 0x1b, c)
		case '7':
			h.scr.save()
		case '8':
			h.scr.restore()
		case 'D':
			h.scr.index()
		case 'E':
			h.scr.index()
			h.scr.col = 0
		case 'M':
			h.scr.reverseIndex()
		case 'c':
			h.reset()
		}
	case '[':
		if c >= 0x40 && c <= 0x7e {
			h.state = 0
			h.csi(string(h.seq), c)
			return
		}
		h.seq = append(h.seq, c)
	case ']', 'P':
		h.seq = append(h.seq, c)
		n := len(h.seq)
		if h.state == ']' && c == 0x07 || n >= 2 && h.seq[n-2] == 0x1b && c == '\\' {
			kind := h.state
			h.state = 0
			if kind == ']' {
				h.osc(string(h.seq))
			} else {
				h.str(string(h.seq))
			}
		}
	}
}

// altScreen enters or leaves the alternate screen. A placement made there
// belongs to it: leaving deletes its surface (SPEC §5.4, SHOULD).
func (h *Host) altScreen(on bool) {
	if !on && h.alt {
		for name, s := range h.surfaces {
			if s.placed && s.alt {
				if h.keyboard == s {
					h.keyboard = nil
				}
				delete(h.surfaces, name)
			}
		}
	}
	switch {
	case on && !h.alt:
		h.main, h.scr = h.scr, screen{cols: h.cols, rows: h.rows, fixed: true}
	case !on && h.alt:
		h.scr = h.main
	}
	h.alt = on
}

// reset is RIS: a full reset deletes every surface (SPEC §5.4).
func (h *Host) reset() {
	h.surfaces = map[string]*Surface{}
	h.keyboard = nil
	h.scr, h.alt = screen{cols: h.cols, rows: h.rows}, false
}

func (h *Host) csi(params string, final byte) {
	switch {
	case final == 'c' && (params == "" || params == "0"):
		h.in.write("\x1b[?62;22c")
	case final == 'n' && params == "6":
		h.in.write(fmt.Sprintf("\x1b[%d;%dR", h.scr.row-h.scr.top+1, min(h.scr.col, h.cols-1)+1))
	case (final == 'h' || final == 'l') && (params == "?1049" || params == "?1047" || params == "?47"):
		h.altScreen(final == 'h')
	case final == 'n' && params == "?6":
		h.in.write(fmt.Sprintf("\x1b[?%d;%dR", h.scr.row-h.scr.top+1, min(h.scr.col, h.cols-1)+1))
	default:
		h.scr.csi(params, final)
	}
}

func (h *Host) str(seq string) {
	// The kitty graphics query (term.KittyGraphics): a=q on an image id.
	if !h.kitty || !strings.HasPrefix(seq, "\x1b_G") {
		return
	}
	ctl, _, _ := strings.Cut(strings.TrimSuffix(seq[3:], "\x1b\\"), ";")
	var id string
	query := false
	for kv := range strings.SplitSeq(ctl, ",") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "i":
			id = v
		case "a":
			query = v == "q"
		}
	}
	if query && id != "" {
		h.in.write("\x1b_Gi=" + id + ";OK\x1b\\")
	}
}

func (h *Host) osc(seq string) {
	if strings.HasPrefix(seq, "\x1b]11;?") {
		bg := "rgb:1212/1212/1a1a"
		if h.caps.Light() {
			bg = "rgb:ffff/ffff/ffff"
		}
		h.in.write("\x1b]11;" + bg + "\x1b\\")
		return
	}
	before := h.dec.Invalid
	m, r := h.dec.Feed(seq)
	if h.dec.Invalid > before {
		h.invalid += h.dec.Invalid - before
		h.protocol("a malformed HOTTY message (SPEC §3.7): %q", clip(seq))
	}
	if r != hotty.Complete {
		return
	}
	h.commands = append(h.commands, m)
	if h.text {
		if m.Get("a") != "q" {
			h.protocol("a HOTTY command to a terminal that is not a host (SPEC §14): %v", m.Control)
		}
		return
	}
	h.command(m)
}

func clip(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

func (h *Host) protocol(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	h.errs = append(h.errs, msg)
	if h.tb != nil && !h.lenient {
		h.tb.Errorf("hottytest: %s", msg)
	}
}

// --- commands ---------------------------------------------------------------

// command carries out one command and answers it (SPEC §3.6).
func (h *Host) command(m hotty.Message) {
	a := m.Get("a")
	code, detail, extra := h.do(a, m)
	if code == hotty.EINVAL {
		h.protocol("%s refused: EINVAL (%s): %v", a, detail, m.Control)
	}
	q := m.Get("q")
	if q == "2" || q == "1" && code == "" {
		return
	}
	ctl := hotty.Control{{K: "a", V: "ok"}}
	if code != "" {
		ctl[0].V = "err"
	}
	if n, ok := m.Control["n"]; ok {
		ctl = ctl.With("n", n)
	}
	if s, ok := m.Control["s"]; ok {
		ctl = ctl.With("s", s)
	}
	ctl = ctl.With("re", a)
	ctl = append(ctl, extra...)
	var payload []byte
	if code != "" {
		payload, _ = json.Marshal(map[string]string{"code": code, "detail": detail})
	} else if a == "q" {
		payload, _ = json.Marshal(h.caps)
	}
	h.send(ctl, payload)
}

// send writes a message to the program's input.
func (h *Host) send(ctl hotty.Control, payload []byte) {
	enc := hotty.Encode(ctl, payload)
	var d hotty.Decoder
	if m, r := d.Feed(enc); r == hotty.Complete {
		if _, ok := m.Reply(); ok {
			h.replies = append(h.replies, m)
		}
		if ev, ok := m.Event(); ok {
			h.events = append(h.events, ev)
		}
	}
	h.in.write(enc)
}

func (h *Host) do(a string, m hotty.Message) (code, detail string, extra hotty.Control) {
	if a == "q" {
		return "", "", nil
	}
	if a == "res" {
		id := m.Get("id")
		if id == "" {
			return hotty.EINVAL, "missing id", nil
		}
		old := h.res[id]
		if limit := h.caps.Limits["resources"]; limit > 0 && h.resBytes()-len(old.data)+len(m.Payload) > limit {
			return hotty.EQUOTA, "resources", nil
		}
		h.res[id] = resource{mime: m.Get("type"), data: m.Payload}
		return "", "", nil
	}
	if a == "del" {
		if id, ok := m.Control["id"]; ok && m.Get("s") == "" {
			delete(h.res, id)
			return "", "", nil
		}
		if _, ok := m.Control["s"]; !ok {
			h.surfaces = map[string]*Surface{}
			h.keyboard = nil
			return "", "", nil
		}
	}
	switch a {
	case "doc", "place", "hide", "patch", "del", "detach", "focus", "blur":
	default:
		return hotty.EINVAL, "unknown action " + a, nil
	}
	name, ok := m.Control["s"]
	if !ok || !hotty.ValidName(name) {
		return hotty.EINVAL, "bad surface name", nil
	}
	s := h.surfaces[name]
	if a == "doc" {
		if s == nil {
			if limit := h.caps.Limits["surfaces"]; limit > 0 && len(h.surfaces) >= limit {
				return hotty.EQUOTA, "surfaces", nil
			}
			h.created++
			s = newSurface(&h.mu, name, string(m.Payload), h.created)
			h.surfaces[name] = s
		} else {
			if h.keyboard == s {
				h.keyboard = nil
			}
			s.setDoc(string(m.Payload))
		}
		s.detached = m.Get("d") == "1"
		return "", "", nil
	}
	if s == nil {
		return hotty.ENOENT, "no surface " + name, nil
	}
	switch a {
	case "place":
		return h.place(s, m)
	case "hide":
		if s.placed && h.keyboard == s {
			h.blur(s)
		}
		s.placed = false
	case "patch":
		op := hotty.Op(m.Get("op"))
		if op == "" {
			op = hotty.OpMorph
		}
		if !slices.Contains(DefaultCaps().Ops, string(op)) {
			return hotty.EINVAL, "unknown op " + string(op), nil
		}
		t, k := m.Get("t"), m.Get("k")
		if t == "" && op != hotty.OpMorph {
			return hotty.EINVAL, "missing t", nil
		}
		if k == "" && (op == hotty.OpAttr || op == hotty.OpUnattr || op == hotty.OpVar) {
			return hotty.EINVAL, "missing k", nil
		}
		code, detail = s.patch(op, t, k, string(m.Payload))
		if h.keyboard == s && !s.keyb {
			h.keyboard = nil // the focused element went
		}
		return code, detail, nil
	case "del":
		if h.keyboard == s {
			h.keyboard = nil
		}
		delete(h.surfaces, name)
	case "detach":
		if h.keyboard == s {
			h.keyboard = nil // back to the terminal, with no blur
		}
		s.detached, s.keyb = true, false
	case "focus":
		if s.detached {
			return hotty.EDETACHED, name, nil
		}
		var el *html.Node
		if t := m.Get("t"); t != "" {
			if el = s.byID(t); el == nil {
				return hotty.ENOTARGET, t, nil
			}
		} else if s.focused != nil {
			el = s.focused
		} else {
			el = find(s.doc, focusable)
		}
		// No focus event for focus the program gave (SPEC §10.1).
		h.takeKeyboard(s, el, false)
	case "blur":
		if !s.detached && h.keyboard == s {
			h.blur(s)
		}
	}
	return "", "", nil
}

func (h *Host) resBytes() int {
	n := 0
	for _, r := range h.res {
		n += len(r.data)
	}
	return n
}

func (h *Host) place(s *Surface, m hotty.Message) (code, detail string, extra hotty.Control) {
	num := func(k string, def, lo, hi int) (int, bool) {
		v, ok := m.Control[k]
		if !ok {
			return def, true
		}
		n, err := strconv.Atoi(v)
		return n, err == nil && n >= lo && n <= hi
	}
	if _, ok := m.Control["c"]; !ok {
		return hotty.EINVAL, "missing c", nil
	}
	c, ok := num("c", 0, 1, hotty.MaxSize)
	if !ok {
		return hotty.EINVAL, "c out of range", nil
	}
	r := 0
	if v := m.Get("r"); v != "" && v != "auto" {
		if r, ok = num("r", 0, 1, hotty.MaxSize); !ok {
			return hotty.EINVAL, "r out of range", nil
		}
	}
	if r == 0 {
		r = max(1, min(hotty.MaxSize, h.autoRows(s, c)))
	}
	x, okx := num("x", 0, 0, hotty.MaxSize)
	y, oky := num("y", 0, 0, hotty.MaxSize)
	w, okw := num("w", c-x, 1, hotty.MaxSize)
	hh, okh := num("h", r-y, 1, hotty.MaxSize)
	if !okx || !oky || !okw || !okh || x+w > c || y+hh > r || w < 1 || hh < 1 {
		return hotty.EINVAL, "window out of the surface", nil
	}
	z, ok := num("z", 0, -1000, 1000)
	if !ok {
		return hotty.EINVAL, "z out of range", nil
	}
	s.placed = true
	s.place = hotty.Placement{Cols: c, Rows: r, Z: z, Press: m.Get("p") == "1", KeepCursor: m.Get("C") == "1"}
	if _, ok := m.Control["x"]; ok || m.Control["y"] != "" || m.Control["w"] != "" || m.Control["h"] != "" {
		s.place.Window = hotty.Window{X: x, Y: y, W: w, H: hh}
	}
	s.col, s.row, s.alt = h.scr.col, h.scr.row, h.alt
	if !s.place.KeepCursor {
		// As if by h times IND, then CR.
		for range hh {
			h.scr.index()
		}
		h.scr.col = 0
	}
	return "", "", hotty.Control{{K: "c", V: strconv.Itoa(c)}, {K: "r", V: strconv.Itoa(r)}}
}

// estimateRows is AutoRows' default: a row for each line of the body's
// text, and one for each block element with nothing but other blocks in it.
func estimateRows(s *Surface, cols int) int {
	body := find(s.doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
	if body == nil {
		return 1
	}
	rows := 0
	for _, line := range strings.Split(strings.TrimSpace(textOf(body)), "\n") {
		n := utf8.RuneCountInString(strings.TrimSpace(line))
		rows += max(1, (n+cols-1)/max(1, cols))
	}
	return max(1, rows)
}

// --- events -------------------------------------------------------------------

func (h *Host) event(s *Surface, kind, target string, detail any) {
	var payload []byte
	if detail != nil {
		payload, _ = json.Marshal(detail)
	}
	h.send(hotty.Control{{K: "a", V: "ev"}, {K: "s", V: s.name}, {K: "e", V: kind}, {K: "t", V: target}}, payload)
}

// takeKeyboard gives a surface the keyboard at el, taking it from another
// (which sends blur), and committing the control that loses focus.
func (h *Host) takeKeyboard(s *Surface, el *html.Node, report bool) {
	if h.keyboard != nil && h.keyboard != s {
		h.blur(h.keyboard)
	}
	if s.keyb && s.focused != el {
		h.commit(s)
	}
	had := s.keyb
	s.focused, s.keyb = el, true
	h.keyboard = s
	if report && !had {
		h.event(s, hotty.EventFocus, "", nil)
	}
}

// commit sends change for the focused text control, if the user edited
// it since its last commit.
func (h *Host) commit(s *Surface) {
	el := s.focused
	if el == nil || !s.dirty()[el] {
		return
	}
	delete(s.dirty(), el)
	if id, ok := attr(el, "id"); ok && !s.detached {
		h.event(s, hotty.EventChange, id, map[string]string{"value": s.valueOf(el)})
	}
}

func (h *Host) blur(s *Surface) {
	if s.keyb {
		h.commit(s)
	}
	s.keyb = false
	if h.keyboard == s {
		h.keyboard = nil
	}
	h.event(s, hotty.EventBlur, "", nil)
}

func focusable(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if _, dis := attr(n, "disabled"); dis {
		return false
	}
	switch n.DataAtom {
	case atom.Input, atom.Select, atom.Textarea, atom.Button, atom.Summary:
		if t, _ := attr(n, "type"); n.DataAtom == atom.Input && strings.EqualFold(t, "hidden") {
			return false
		}
		return true
	case atom.A:
		_, href := attr(n, "href")
		return href && !hyperlink(n)
	}
	if t, ok := attr(n, "tabindex"); ok {
		v, err := strconv.Atoi(t)
		return err == nil && v >= 0
	}
	return false
}

func hyperlink(n *html.Node) bool {
	t, _ := attr(n, "target")
	return n.DataAtom == atom.A && t == "_blank"
}

// target finds a surface the user can act in.
func (h *Host) target(surface string) (*Surface, error) {
	if h.text {
		return nil, ErrNotHost
	}
	s := h.surfaces[surface]
	switch {
	case s == nil:
		return nil, fmt.Errorf("%w: %s", ErrNoSurface, surface)
	case !s.placed:
		return nil, fmt.Errorf("%w: %s", ErrNotPlaced, surface)
	}
	return s, nil
}

func (h *Host) element(s *Surface, id string) (*html.Node, error) {
	n := s.byID(id)
	if n == nil {
		return nil, fmt.Errorf("%w: #%s in %s", ErrNoElement, id, s.name)
	}
	return n, nil
}

// Click clicks the element with an id, as the user does (SPEC §9): a press
// first, if the placement asks for presses; the keyboard, if the element
// takes focus; then click, reported by the nearest element from it outward
// that reports clicks, if that has an id. A link reports its href, and a
// hyperlink (target=_blank) is opened by the terminal and reports nothing
// (Opened). A submit button then submits its form.
//
// On a detached surface the click does what is local and reports nothing:
// ErrDetached.
func (h *Host) Click(surface, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	el, err := h.element(s, id)
	if err != nil {
		return err
	}
	if a := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.A }); a != nil {
		if _, ok := attr(a, "href"); ok && hyperlink(a) {
			if u := s.resolve(a); u != "" {
				h.opened = append(h.opened, u)
			}
			return nil
		}
	}
	if s.detached {
		return ErrDetached
	}
	if s.place.Press {
		h.pressAt(s, el)
	}
	if f := closest(el, focusable); f != nil {
		h.takeKeyboard(s, f, true)
	}
	rep := closest(el, reportsClick)
	if rep == nil {
		return ErrNoReport
	}
	if rep.DataAtom == atom.A {
		if href, ok := attr(rep, "href"); ok {
			detail := map[string]string{"href": href}
			if u := s.resolve(rep); u != "" {
				detail["url"] = u
			}
			h.event(s, hotty.EventClick, "", detail)
			return nil
		}
	}
	rid, ok := attr(rep, "id")
	if !ok {
		return ErrNoReport
	}
	var detail any
	if v, ok := attr(rep, "value"); ok {
		detail = map[string]string{"value": v}
	}
	h.event(s, hotty.EventClick, rid, detail)
	if submits(rep) {
		if form := closest(rep, func(n *html.Node) bool { return n.DataAtom == atom.Form }); form != nil {
			h.submit(s, form, rep)
		}
	}
	return nil
}

func reportsClick(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.DataAtom {
	case atom.Button, atom.A, atom.Summary:
		return true
	case atom.Input:
		t, _ := attr(n, "type")
		switch strings.ToLower(t) {
		case "button", "submit", "reset":
			return true
		}
	}
	on, _ := attr(n, "data-on")
	return slices.Contains(strings.Fields(on), "click")
}

func submits(n *html.Node) bool {
	t, _ := attr(n, "type")
	t = strings.ToLower(t)
	return n.DataAtom == atom.Button && (t == "" || t == "submit") || n.DataAtom == atom.Input && t == "submit"
}

func closest(n *html.Node, f func(*html.Node) bool) *html.Node {
	for ; n != nil; n = n.Parent {
		if n.Type == html.ElementNode && f(n) {
			return n
		}
	}
	return nil
}

// resolve is a link's URL against the document's base (SPEC §7.3), "" when
// it is under https://hotty.invalid/.
func (s *Surface) resolve(a *html.Node) string {
	href, _ := attr(a, "href")
	base, _ := url.Parse("https://hotty.invalid/")
	if b := find(s.doc, func(n *html.Node) bool { return n.DataAtom == atom.Base }); b != nil {
		if v, ok := attr(b, "href"); ok {
			if u, err := url.Parse(v); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
				base = u
			}
		}
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	u := base.ResolveReference(ref)
	if u.Host == "hotty.invalid" {
		return ""
	}
	return u.String()
}

// Opened are the hyperlinks the user opened (target=_blank), which the
// terminal opens itself and never reports (SPEC §9).
func (h *Host) Opened() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.opened...)
}

// Press presses the element with an id, or empty space ("") (SPEC §9):
// reported only on a placement made with Placement.Press, as the id of the
// nearest element with one.
func (h *Host) Press(surface, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	var el *html.Node
	if id != "" {
		if el, err = h.element(s, id); err != nil {
			return err
		}
	}
	if s.detached {
		return ErrDetached
	}
	if !s.place.Press {
		return ErrNoPress
	}
	h.pressAt(s, el)
	return nil
}

func (h *Host) pressAt(s *Surface, el *html.Node) {
	t := ""
	if n := closest(el, func(n *html.Node) bool { _, ok := attr(n, "id"); return ok }); n != nil {
		t, _ = attr(n, "id")
	}
	h.event(s, hotty.EventPress, t, nil)
}

// Fill types text into a text control, as the user does: the surface takes
// the keyboard there (a focus event, if it did not have it), the control's
// value becomes text (an input event, with data-on~=input), and the change
// is committed when focus leaves it (Blur, another control, Submit).
func (h *Host) Fill(surface, id, text string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	el, err := h.element(s, id)
	if err != nil {
		return err
	}
	if s.detached {
		return ErrDetached
	}
	if !control(el) || box(el) || !focusable(el) {
		return ErrNotControl
	}
	h.takeKeyboard(s, el, true)
	s.values[el] = text
	s.dirty()[el] = true
	if on, _ := attr(el, "data-on"); slices.Contains(strings.Fields(on), "input") {
		h.event(s, hotty.EventInput, id, map[string]string{"value": text})
	}
	return nil
}

// Check sets a checkbox, or chooses a radio button (on must then be true),
// as the user does: change comes at once.
func (h *Host) Check(surface, id string, on bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	el, err := h.element(s, id)
	if err != nil {
		return err
	}
	if s.detached {
		return ErrDetached
	}
	if !box(el) || !focusable(el) {
		return ErrNotControl
	}
	t, _ := attr(el, "type")
	radio := strings.EqualFold(t, "radio")
	if radio && !on {
		return fmt.Errorf("%w: a radio button is unchosen by choosing another", ErrNotControl)
	}
	h.takeKeyboard(s, el, true)
	if radio {
		name, _ := attr(el, "name")
		scope := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form })
		if scope == nil {
			scope = s.doc
		}
		walk(scope, func(n *html.Node) {
			if n != el && box(n) {
				if nt, _ := attr(n, "type"); strings.EqualFold(nt, "radio") {
					if nn, _ := attr(n, "name"); nn == name && name != "" {
						s.checked[n] = false
					}
				}
			}
		})
	}
	if s.isChecked(el) == on {
		return nil
	}
	s.checked[el] = on
	h.event(s, hotty.EventChange, id, map[string]any{"checked": on, "value": s.valueOf(el)})
	return nil
}

// Choose picks an option of a select by its value: change comes at once.
func (h *Host) Choose(surface, id, value string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	el, err := h.element(s, id)
	if err != nil {
		return err
	}
	if s.detached {
		return ErrDetached
	}
	if el.DataAtom != atom.Select {
		return ErrNotControl
	}
	found := false
	walk(el, func(o *html.Node) {
		if o.DataAtom == atom.Option {
			v, ok := attr(o, "value")
			if !ok {
				v = textOf(o)
			}
			found = found || v == value
		}
	})
	if !found {
		return fmt.Errorf("%w: no option %q", ErrNotControl, value)
	}
	h.takeKeyboard(s, el, true)
	s.values[el] = value
	h.event(s, hotty.EventChange, id, map[string]string{"value": value})
	return nil
}

// Submit submits a form, as Enter in one of its fields does: id is the
// form's, or a control's in it. The control being edited commits first. A
// form without an id reports nothing.
func (h *Host) Submit(surface, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	el, err := h.element(s, id)
	if err != nil {
		return err
	}
	if s.detached {
		return ErrDetached
	}
	form := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form })
	if form == nil {
		return fmt.Errorf("%w: #%s is in no form", ErrNotControl, id)
	}
	if s.keyb {
		h.commit(s)
	}
	h.submit(s, form, nil)
	return nil
}

// submit sends the form's fields as FormData has them: named controls that
// are not disabled, boxes only when checked, and the submitter's own.
func (h *Host) submit(s *Surface, form, submitter *html.Node) {
	fid, ok := attr(form, "id")
	if !ok {
		return
	}
	fields := map[string]string{}
	walk(form, func(n *html.Node) {
		name, named := attr(n, "name")
		if !named || name == "" {
			return
		}
		if _, dis := attr(n, "disabled"); dis {
			return
		}
		switch {
		case n.DataAtom == atom.Button || n.DataAtom == atom.Input && reportsClick(n):
			if n == submitter {
				v, _ := attr(n, "value")
				fields[name] = v
			}
		case box(n):
			if s.isChecked(n) {
				fields[name] = s.valueOf(n)
			}
		case control(n):
			fields[name] = s.valueOf(n)
		}
	})
	h.event(s, hotty.EventSubmit, fid, fields)
}

// Blur takes the keyboard from a surface, as a click elsewhere does: the
// control being edited commits (change), then blur.
func (h *Host) Blur(surface string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	if s.detached {
		return ErrDetached
	}
	if s.keyb {
		h.blur(s)
	}
	return nil
}

// Emit sends any event from a surface, for what the other actions do not
// cover (a resize, a kind from a later version). detail is marshalled as
// JSON; nil sends none.
func (h *Host) Emit(surface, kind, target string, detail any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.text {
		return ErrNotHost
	}
	s := h.surfaces[surface]
	if s == nil {
		return fmt.Errorf("%w: %s", ErrNoSurface, surface)
	}
	if s.detached {
		return ErrDetached
	}
	h.event(s, kind, target, detail)
	return nil
}

// --- inspection ---------------------------------------------------------------

// Surface is the surface of a name, nil when there is none.
func (h *Host) Surface(name string) *Surface {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.surfaces[name]
}

// Surfaces are the surfaces there are, in the order they were created.
func (h *Host) Surfaces() []*Surface {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Surface, 0, len(h.surfaces))
	for _, s := range h.surfaces {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b *Surface) int { return a.created - b.created })
	return out
}

// Screen is the cells the program printed, a line a row, without colours:
// what is left after carriage returns, cursor moves, erases and scrolls.
// It is the main screen, its scrollback first, or the alternate screen
// while the program has it on. Surfaces are not drawn in it; the rows a
// placement covered are blank.
func (h *Host) Screen() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scr.String()
}

// Output is every byte the program wrote.
func (h *Host) Output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.output.String()
}

// Commands are the HOTTY messages the program sent, in order.
func (h *Host) Commands() []hotty.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hotty.Message(nil), h.commands...)
}

// Replies are the replies the host sent, in order.
func (h *Host) Replies() []hotty.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hotty.Message(nil), h.replies...)
}

// Events are the events the host sent the program, in order: what the
// user's actions reported.
func (h *Host) Events() []hotty.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hotty.Event(nil), h.events...)
}

// Resource is a resource the program stored (SPEC §7.1).
func (h *Host) Resource(id string) (mime string, data []byte, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.res[id]
	return r.mime, r.data, ok
}

// Errors are the protocol errors the program made, as the test was told of
// them.
func (h *Host) Errors() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.errs...)
}

// Invalid counts the malformed messages the program sent.
func (h *Host) Invalid() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.invalid
}
