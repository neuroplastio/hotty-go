// Package hottytest is a HOTTY host that runs inside a test. The program
// under test reads from it and writes to it as it would a terminal; the
// test then looks at what the program showed, and plays the user.
//
//	h := hottytest.New(t)
//	run(hottyterm.New(h, h, "tool", h.TermSize, nil)) // the program under test; or tea.WithInput(h), …
//	card := h.Surface("tool-card")
//	card.TextOf("status")                         // what it shows
//	h.Click("tool-card", "retry")                 // what the user does
//	h.Key("Tab")                                  // keys, as SPEC §10.2 has a host take them
//
// The host keeps every surface's document as the program's commands leave
// it, with the delta operations and the morph of SPEC §6, and answers each
// command as SPEC §3.6 has hosts do. It passes the HOTTY conformance
// vectors that need no layout: it skips the ones marked scroll, steps, drop
// and view, and those for features it lacks (SDK.md §4.4). It lays nothing
// out and draws no pixels: a placement with auto rows gets an estimate
// (AutoRows), nothing in it scrolls, and a test says where in an element a
// pointer is (DragStartStep).
//
// A placement made on the alternate screen goes with it, and so does its
// surface (SPEC §5.4). A full reset (RIS) deletes every surface.
//
// It also answers the queries terminals answer: Primary Device Attributes,
// the cursor's position, the background colour, and, when asked to
// (KittyGraphics), the kitty graphics query. It keeps the cells the
// program printed, as rows (Screen) and as the lines it wrote (Lines), and
// where it left the cursor (Cursor).
//
// A Host made with Text is a terminal that is not a HOTTY host: the
// program's commands get no answer, as in any other terminal.
//
// Unless Lenient, a program that breaks the protocol fails the test: a
// malformed command, one the host refuses as invalid (EINVAL), or a HOTTY
// command other than the query sent to a terminal that is not a host.
package hottytest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"github.com/tinylib/msgp/msgp"
)

// Errors of the user's actions.
var (
	ErrNotHost    = errors.New("hottytest: the terminal is not a HOTTY host")
	ErrNoSurface  = errors.New("hottytest: no such surface")
	ErrNoElement  = errors.New("hottytest: no such element")
	ErrDetached   = errors.New("hottytest: the surface is detached, and reports nothing")
	ErrNotPlaced  = errors.New("hottytest: the surface is not placed")
	ErrNoPress    = errors.New("hottytest: the surface did not ask for presses (Placement.Press)")
	ErrNotControl = errors.New("hottytest: the element is not a control that takes this")
	ErrNoReport   = errors.New("hottytest: nothing with an id reports this click")
	ErrNoDrag     = errors.New("hottytest: no drag is in progress")
	ErrDragging   = errors.New("hottytest: a drag is already in progress")
)

// Option configures a Host.
type Option func(*Host)

// Caps sets the capabilities the host reports. Fields left zero keep the
// defaults (DefaultCaps). Features replace the default list: a test that
// wants a host without hover or menus names the ones it has.
func Caps(c hotty.Caps) Option {
	return func(h *Host) {
		d := &h.caps
		if c.V != "" {
			d.V = c.V
		}
		if c.CellCSS.W > 0 && c.CellCSS.H > 0 {
			d.CellCSS = c.CellCSS
		}
		if c.Scale > 0 {
			d.Scale = c.Scale
		}
		if c.Scheme != "" {
			d.Scheme = c.Scheme
		}
		// The platform its keymaps are read on (PlatformOf), and the keys.
		if c.Mod != "" {
			d.Mod = c.Mod
		}
		if c.Edit != nil {
			d.Edit = c.Edit
		}
		if c.Features != nil {
			d.Features = c.Features
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
		if c.Version != "" {
			d.Version = c.Version
		}
	}
}

// Text makes the host a terminal that is not a HOTTY host: it answers DA1
// and the cursor's position, and ignores HOTTY, until BecomeHost.
func Text() Option { return func(h *Host) { h.text = true } }

// BecomeHost makes a terminal made with Text a HOTTY host from now on, as a
// multiplexer becomes one when a terminal that is a host attaches to the
// program's pane: it answers the last query the program sent, if that one
// asked for a late answer (late=1, SPEC §4) and was not withdrawn or reset
// since, and every command after.
func (h *Host) BecomeHost() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.text = false
	if m := h.held; m != nil && hotty.Get(m.Control, "late") == "1" {
		h.command(*m)
	}
	h.held = nil
}

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
// otherwise. Its features are the ones it can honour: it lays nothing out,
// so it has no passthrough, but it hears where the pointer is (hover),
// counts clicks (count, DoubleClick) and records the menu a program shows
// (menu, Menu).
func DefaultCaps() hotty.Caps {
	return hotty.Caps{V: hotty.Version, CellCSS: hotty.Size{W: 9, H: 18}, Scale: 1, Scheme: "dark", Host: "hottytest",
		Features: []string{hotty.FeatureHover, hotty.FeatureCount, hotty.FeatureMenu},
		Limits:   map[string]int{"surfaces": 64, "message": hotty.DefaultMax}}
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
	hidden  bool   // the cursor is not on show (DECTCEM)

	surfaces map[string]*Surface
	created  int
	res      map[string]resource
	keyboard *Surface         // the surface that has the keyboard
	drag     *drag            // the drag in progress, if any (SPEC §9.1)
	hovered  *Surface         // the surface the pointer is on (Hover), if any
	menu     *hotty.MenuLevel // the menu the program shows (a=menu), if any
	heard    hotty.Caps       // the capabilities the program last heard (SPEC §4)
	queried  bool             // the program has asked, so it hears of changes
	faultOp  int              // the index of the op a delta failed at, or -1
	prefix   *Surface         // the surface whose keys all reach the program, while it has the keyboard (SPEC §10.5)
	alt      bool             // the alternate screen is on
	commands []hotty.Message
	replies  []hotty.Message
	events   []hotty.Event
	opened   []string // hyperlinks the user opened
	errs     []string
	// held is the last query a terminal that is not a host was sent, which
	// BecomeHost answers if it asked for a late answer (SPEC §4).
	held *hotty.Message

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
	// What the program sends is decoded with at most the limit the host
	// reports as limits.message (SPEC §13), DefaultMax when Caps names none.
	if h.dec.Max = h.caps.Limits["message"]; h.dec.Max <= 0 {
		h.dec.Max = hotty.DefaultMax
		h.caps.Limits = maps.Clone(h.caps.Limits)
		if h.caps.Limits == nil {
			h.caps.Limits = map[string]int{}
		}
		h.caps.Limits["message"] = hotty.DefaultMax
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

// Buffered is how many bytes the host has sent that the program has not
// read: a test that hands a program its input itself, a Bubble Tea
// model's Update say, reads that much without blocking.
func (h *Host) Buffered() int {
	h.in.mu.Lock()
	defer h.in.mu.Unlock()
	return len(h.in.buf)
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
func (h *Host) Type(keys string) {
	h.mu.Lock()
	for _, k := range hotty.DecodeKeys([]byte(keys)) {
		if k != "" {
			h.menuKey(k)
		}
	}
	h.mu.Unlock()
	h.in.write(keys)
}

// TermSize is the terminal's size (Size), as hottyterm.New takes it.
func (h *Host) TermSize() (cols, rows int) { return h.cols, h.rows }

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
	if on != h.alt {
		h.menu = nil // the screen changed (SPEC §10.5)
	}
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

// reset is RIS: a full reset deletes every surface (SPEC §5.4), and
// forgets a query held for a late answer (SPEC §4).
func (h *Host) reset() {
	h.surfaces = map[string]*Surface{}
	h.keyboard = nil
	h.held = nil
	h.menu = nil
	h.scr, h.alt, h.hidden = screen{cols: h.cols, rows: h.rows}, false, false
}

func (h *Host) csi(params string, final byte) {
	switch {
	case final == 'c' && (params == "" || params == "0"):
		h.in.write("\x1b[?62;22c")
	case final == 'n' && params == "6":
		h.in.write(fmt.Sprintf("\x1b[%d;%dR", h.scr.row-h.scr.top+1, min(h.scr.col, h.cols-1)+1))
	case (final == 'h' || final == 'l') && (params == "?1049" || params == "?1047" || params == "?47"):
		h.altScreen(final == 'h')
	case (final == 'h' || final == 'l') && params == "?25":
		h.hidden = final == 'l'
	case final == 'n' && params == "?6":
		h.in.write(fmt.Sprintf("\x1b[?%d;%dR", h.scr.row-h.scr.top+1, min(h.scr.col, h.cols-1)+1))
	default:
		h.scr.csi(params, final)
	}
}

func (h *Host) str(seq string) {
	// The kitty graphics query (hottyterm.KittyGraphics): a=q on an image id.
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
		if hotty.Light(h.caps) {
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
		if hotty.Get(m.Control, "a") != "q" {
			h.protocol("a HOTTY command to a terminal that is not a host (SPEC §14): %v", m.Control)
			return
		}
		// One query is held at a time: a later one, a withdrawal
		// (a=q:q=2) among them, takes its place.
		h.held = &m
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
	a := hotty.Get(m.Control, "a")
	h.faultOp = -1
	code, detail, extra := h.do(a, m)
	if code == "" {
		// After the reply: the host lays the document out on its next
		// frame, and fit comes from that layout.
		defer h.refit(a, m)
	}
	if code == hotty.EINVAL {
		h.protocol("%s refused: EINVAL (%s): %v", a, detail, m.Control)
	}
	// Only errors, unless asked: a command with n, and a query, are
	// answered whatever the outcome (SPEC §3.5).
	q, ok := hotty.Lookup(m.Control, "q")
	if !ok {
		q = "1"
		if hotty.Has(m.Control, "n") || a == "q" {
			q = "0"
		}
	}
	if q == "2" || q == "1" && code == "" {
		return
	}
	n, s := hotty.Get(m.Control, "n"), hotty.Get(m.Control, "s")
	switch {
	case code != "" && h.faultOp >= 0:
		h.send(hotty.ReplyDeltaErr(n, s, h.faultOp, code, detail))
	case code != "":
		h.send(hotty.ReplyErr(n, s, a, code, detail))
	case a == "q":
		h.heard, h.queried = h.caps, true
		h.send(hotty.ReplyCaps(n, h.caps))
	default:
		h.send(hotty.ReplyOK(n, s, a, extra, nil))
	}
}

// send writes a message to the program's input: never compressed, as no
// host compresses (SPEC §3.3).
func (h *Host) send(enc string) {
	var d hotty.Decoder
	if m, r := d.Feed(enc); r == hotty.Complete {
		if _, ok := hotty.ReplyOf(m); ok {
			h.replies = append(h.replies, m)
		}
		if ev, ok := hotty.EventOf(m); ok {
			h.events = append(h.events, ev)
		}
	}
	h.in.write(enc)
}

func (h *Host) do(a string, m hotty.Message) (code, detail string, extra hotty.Control) {
	if a == "q" {
		// This host speaks hotty.Version only (SPEC §4).
		if !hotty.Speaks(m.Control) {
			return hotty.EVERSION, hotty.Version, nil
		}
		return "", "", nil
	}
	if a == "res" {
		id := hotty.Get(m.Control, "id")
		if id == "" {
			return hotty.EINVAL, "missing id", nil
		}
		old := h.res[id]
		if limit := h.caps.Limits["resources"]; limit > 0 && h.resBytes()-len(old.data)+len(m.Payload) > limit {
			return hotty.EQUOTA, "resources", nil
		}
		h.res[id] = resource{mime: hotty.Get(m.Control, "type"), data: m.Payload}
		return "", "", nil
	}
	if a == "del" {
		if id, ok := hotty.Lookup(m.Control, "id"); ok && hotty.Get(m.Control, "s") == "" {
			delete(h.res, id)
			return "", "", nil
		}
		if !hotty.Has(m.Control, "s") {
			h.surfaces = map[string]*Surface{}
			h.keyboard = nil
			return "", "", nil
		}
	}
	if a == "menu" {
		// The menu a program shows, or none (SPEC §10.5). A host without
		// the feature shows nothing.
		l, err := hotty.MenuOf(m)
		if e := (*hotty.Error)(nil); errors.As(err, &e) {
			return e.Code, e.Detail, nil
		}
		if hotty.HasFeature(h.caps, hotty.FeatureMenu) {
			h.menu = l
		}
		return "", "", nil
	}
	switch a {
	case "doc", "place", "hide", "delta", "del", "detach", "focus", "blur":
	default:
		return hotty.EINVAL, "unknown action " + a, nil
	}
	name, ok := hotty.Lookup(m.Control, "s")
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
			if h.drag != nil && h.drag.s == s {
				if hotty.Get(m.Control, "d") == "1" {
					h.drag = nil // detached: it reports nothing more
				} else {
					h.cutDrag() // a new document ends it
				}
			}
			s.setDoc(string(m.Payload))
		}
		s.detached = hotty.Get(m.Control, "d") == "1"
		return "", "", nil
	}
	if s == nil {
		return hotty.ENOENT, "no surface " + name, nil
	}
	switch a {
	case "place":
		return h.place(s, m)
	case "hide":
		if h.drag != nil && h.drag.s == s {
			h.cutDrag()
		}
		if s.placed && h.keyboard == s {
			h.blur(s)
		}
		s.placed = false
	case "delta":
		// The whole body is checked before any op applies; a missing
		// target fails its op alone, and the ops after it still apply.
		ops, err := hotty.DeltaOf(m)
		if e := (*hotty.Error)(nil); errors.As(err, &e) {
			if e.HasOp {
				h.faultOp = e.Op
			}
			return e.Code, e.Detail, nil
		}
		for i, op := range ops {
			if c, d := s.delta(op); c != "" && code == "" {
				code, detail, h.faultOp = c, d, i
			}
		}
		if h.keyboard == s && !s.keyb {
			h.keyboard = nil // the focused element went
		}
		return code, detail, nil
	case "del":
		if h.keyboard == s {
			h.keyboard = nil
		}
		if h.drag != nil && h.drag.s == s {
			h.drag = nil // it reports nothing more
		}
		delete(h.surfaces, name)
	case "detach":
		if h.keyboard == s {
			h.keyboard = nil // back to the terminal, with no blur
		}
		if h.drag != nil && h.drag.s == s {
			h.drag = nil
		}
		s.detached, s.keyb = true, false
	case "focus":
		if s.detached {
			return hotty.EDETACHED, name, nil
		}
		var el *html.Node
		if t := hotty.Get(m.Control, "t"); t != "" {
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
	p, err := hotty.PlacementOf(m)
	if e := (*hotty.Error)(nil); errors.As(err, &e) {
		return e.Code, e.Detail, nil
	}
	r := p.Rows
	if r == 0 {
		r = max(1, min(hotty.MaxSize, h.autoRows(s, p.Cols)))
	}
	rows := r // the rows the placement covers
	if win := &p.Window; *win != (hotty.Window{}) {
		if win.H == 0 {
			win.H = r - win.Y // auto: to the bottom, now that the host knows it
		}
		if win.H < 1 || win.Y+win.H > r {
			return hotty.EINVAL, "window out of the surface", nil
		}
		rows = win.H
	}
	p.Rows = r
	s.placed = true
	s.place = p
	// p, f and v belong to the surface: a placement without one leaves it
	// as it was (SPEC §5.2).
	flag := func(f hotty.Flag, on *bool) {
		switch f {
		case hotty.On:
			*on = true
		case hotty.Off:
			*on = false
		}
	}
	wasHover := s.hover
	flag(p.Press, &s.press)
	flag(p.Fit, &s.fit)
	flag(p.Hover, &s.hover)
	if s.hover && !wasHover {
		s.heard = nil // v=1 starts from out (SPEC §9.4)
	}
	s.fitRows = r // each placement counts from its own rows (SPEC §5.2)
	s.col, s.row, s.alt = h.scr.col, h.scr.row, h.alt
	if !p.KeepCursor {
		for _, c := range []byte(hotty.CursorBelow(rows)) {
			h.byte(c)
		}
	}
	return "", "", hotty.Control{{K: "c", V: strconv.Itoa(p.Cols)}, {K: "r", V: strconv.Itoa(r)}}
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

// refit sends fit to each surface a command may have changed the height of
// whose placement asked for it (SPEC §5.2): the rows its document needs
// now, by AutoRows, when they differ from the rows it last heard. A
// resource can change any document that refers to it, so it checks all.
func (h *Host) refit(a string, m hotty.Message) {
	var check []*Surface
	switch a {
	case "res":
		for _, name := range slices.Sorted(maps.Keys(h.surfaces)) {
			check = append(check, h.surfaces[name])
		}
	case "doc", "delta", "place":
		if s := h.surfaces[hotty.Get(m.Control, "s")]; s != nil {
			check = append(check, s)
		}
	}
	for _, s := range check {
		h.refitSurface(s)
	}
}

// refitSurface sends fit if the rows the surface's document needs now
// differ from the rows the program last heard.
func (h *Host) refitSurface(s *Surface) {
	if !s.placed || !s.fit || s.detached {
		return
	}
	if n := max(1, min(hotty.MaxSize, h.autoRows(s, s.place.Cols))); n != s.fitRows {
		s.fitRows = n
		h.event(s, hotty.EventFit, "", map[string]int{"r": n})
	}
}

// event sends an event with its detail as msgpack: a Go int as an int, a
// float64 as a float (SPEC §3.3), nil for none.
func (h *Host) event(s *Surface, kind, target string, detail any) {
	var payload []byte
	if detail != nil {
		var buf bytes.Buffer
		w := msgp.NewWriter(&buf)
		if err := w.WriteIntf(detail); err != nil || w.Flush() != nil {
			h.tb.Errorf("hottytest: a %s detail: %v", kind, err)
			return
		}
		payload = buf.Bytes()
	}
	ctl := hotty.Control{{K: "a", V: "ev"}, {K: "s", V: s.name}, {K: "e", V: kind}}
	if target != "" {
		ctl = append(ctl, hotty.KV{K: "t", V: target})
	}
	h.send(hotty.EncodePlain(ctl, payload))
}

// takeKeyboard gives a surface the keyboard at el, taking it from another
// (which sends blur), and committing the control that loses focus. Focus
// the user moves (report) is reported each time it lands on another element,
// named by the nearest id from el outward (SPEC §10.1); the program's is not.
func (h *Host) takeKeyboard(s *Surface, el *html.Node, report bool) {
	if h.keyboard != nil && h.keyboard != s {
		h.blur(h.keyboard)
	}
	if s.keyb && s.focused != el {
		h.commit(s)
	}
	had, was := s.keyb, s.focused
	s.focused, s.keyb = el, true
	h.keyboard = s
	h.prefix = nil // a focus, the program's or the user's, ends a prefix (SPEC §10.5)
	if report && (!had || was != el) {
		target := ""
		if n := closest(el, func(n *html.Node) bool { id, _ := attr(n, "id"); return id != "" }); n != nil {
			target, _ = attr(n, "id")
		}
		h.event(s, hotty.EventFocus, target, nil)
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
	if h.prefix == s {
		h.prefix = nil
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
// takes focus, or else back to the terminal from the surface that had it
// (blur, SPEC §10.1); then click, reported by the nearest element from it
// outward that reports clicks, if that has an id. A link reports its href,
// and a hyperlink (target=_blank) is opened by the terminal and reports
// nothing (Opened). A submit button then submits its form.
//
// keys are the modifier keys held ("shift", "ctrl", "alt", "meta"); the
// click carries those held at its release (SPEC §9). A press with Alt is
// the program's own gesture (SPEC §9.2): the surface hears nothing of it,
// and the pointer is out of its window.
//
// On a detached surface the click does what is local and reports nothing:
// ErrDetached.
func (h *Host) Click(surface, id string, keys ...string) error {
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
	if h.alt1(s, keys) {
		return nil
	}
	return h.click(s, el, true, 1, modBits(keys))
}

// DoubleClick clicks an element twice, as a double click does: Click,
// then a click that says it is the second of its run (Event.Count 2,
// SPEC §9).
func (h *Host) DoubleClick(surface, id string, keys ...string) error {
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
	if h.alt1(s, keys) {
		return nil
	}
	if err := h.click(s, el, true, 1, modBits(keys)); err != nil {
		return err
	}
	return h.click(s, el, true, 2, modBits(keys))
}

// ClickLink clicks the first link in a surface whose href is href, as
// written in the document: for links with no id, such as a manual page's
// references. It is Click on that link.
func (h *Host) ClickLink(surface, href string, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	el := find(s.doc, func(n *html.Node) bool {
		v, ok := attr(n, "href")
		return n.DataAtom == atom.A && ok && v == href
	})
	if el == nil {
		return fmt.Errorf("%w: a link to %s in %s", ErrNoElement, href, s.name)
	}
	if h.alt1(s, keys) {
		return nil
	}
	return h.click(s, el, true, 1, modBits(keys))
}

// modBits are the modifier keys held, as the bits of a detail's mods
// (SPEC §9): "shift", "alt", "ctrl" and "meta".
func modBits(keys []string) int {
	bits := 0
	for _, k := range keys {
		switch k {
		case "shift":
			bits |= hotty.ModShift
		case "alt":
			bits |= hotty.ModAlt
		case "ctrl":
			bits |= hotty.ModControl
		case "meta":
			bits |= hotty.ModMeta
		}
	}
	return bits
}

// alt1 reports whether a press is made with Alt held, which is the
// program's gesture and not the surface's (SPEC §9.2): the pointer is then
// out of the window (SPEC §9.4), and the surface hears nothing more.
func (h *Host) alt1(s *Surface, keys []string) bool {
	if modBits(keys)&hotty.ModAlt == 0 {
		return false
	}
	h.leave(s)
	return true
}

// click is a click on el: by the pointer, which presses first, or by
// the keyboard (Key), which does not. count is its place in its run of
// clicks, which its detail carries from 2 on (SPEC §9), and mods the
// modifier keys held, as bits.
func (h *Host) click(s *Surface, el *html.Node, pointer bool, count, mods int) error {
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
	if !h.hears(hotty.FeatureCount) {
		count = 1
	}
	if pointer && s.press {
		h.pressAt(s, el, mods)
	}
	if f := closest(el, focusable); f != nil {
		h.takeKeyboard(s, f, true)
	} else if h.keyboard != nil {
		// A click on what takes no focus gives the keyboard back to the
		// terminal (SPEC §10.1).
		h.blur(h.keyboard)
	}
	rep := closest(el, reportsClick)
	if rep == nil {
		return ErrNoReport
	}
	if rep.DataAtom == atom.A {
		if href, ok := attr(rep, "href"); ok {
			// The href is the handle, and the id too when there is one.
			detail := map[string]any{"href": href}
			if mods != 0 {
				detail["mods"] = mods
			}
			if u := s.resolve(rep); u != "" {
				detail["url"] = u
			}
			if count > 1 {
				detail["count"] = count
			}
			id, _ := attr(rep, "id")
			h.event(s, hotty.EventClick, id, detail)
			return nil
		}
	}
	rid, ok := attr(rep, "id")
	if !ok {
		return ErrNoReport
	}
	detail := map[string]any{}
	if mods != 0 {
		detail["mods"] = mods
	}
	if v, ok := attr(rep, "value"); ok {
		detail["value"] = v
	}
	if count > 1 {
		detail["count"] = count
	}
	if len(detail) == 0 {
		h.event(s, hotty.EventClick, rid, nil)
	} else {
		h.event(s, hotty.EventClick, rid, detail)
	}
	if submits(rep) {
		if form := closest(rep, func(n *html.Node) bool { return n.DataAtom == atom.Form }); form != nil {
			h.submit(s, form, rep)
		}
	}
	return nil
}

// --- drags (SPEC §9.1) ----------------------------------------------------------

// drag is a drag in progress: the pointer is its surface's until the
// release.
type drag struct {
	s     *Surface
	start *html.Node // the element that opted in, where it began
	id    string     // its id
	t     string     // the element under the pointer, as last reported
	c, r  int        // the cell under the pointer, as last reported
	n     [2]int     // the element's data-steps when the drag began: none is 0
	x, y  int        // the step as last reported, along each counted axis
	tn    [2]int     // t's data-steps as last reported: none is 0
	tx    int        // the step in t as last reported, along each counted axis
	ty    int
}

// DragStart presses a mouse's primary button on the element with an id and
// starts a drag (SPEC §9.1), at the surface's cell c, r, with the modifier
// keys held ("shift", "ctrl", "alt", "meta"). The press is a press like
// any other: press first, if the placement asks for presses; then
// dragstart, from the nearest element from it outward with drag in its
// data-on; then the keyboard, as a click moves it. ErrNoReport, and no
// drag, if no element opts in or the one that does has no id.
//
// The host lays nothing out, so a test names what is under the pointer:
// DragMove for each element or cell crossed, then DragEnd.
func (h *Host) DragStart(surface, id string, c, r int, keys ...string) error {
	return h.dragStart(surface, id, c, r, 0, 0, keys)
}

// DragStartStep is DragStart on an element with data-steps (SPEC §9.1),
// with the pointer at step x, y of it. The host lays nothing out, so the
// test says where in the element the pointer is; a step past the
// element's count is clamped to it, and one along an axis it does not
// count is ignored.
func (h *Host) DragStartStep(surface, id string, c, r, x, y int, keys ...string) error {
	return h.dragStart(surface, id, c, r, x, y, keys)
}

func (h *Host) dragStart(surface, id string, c, r, x, y int, keys []string) error {
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
	if h.drag != nil {
		return ErrDragging
	}
	if h.alt1(s, keys) {
		return nil
	}
	if s.press {
		h.pressAt(s, el, modBits(keys))
	}
	if on := closest(el, listens("drag")); on != nil {
		if rid, ok := attr(on, "id"); ok {
			d := &drag{s: s, start: on, id: rid, t: rid, c: c, r: r}
			d.n = steps(on)
			d.x, d.y = d.clamp(x, y)
			h.drag = d
			h.event(s, hotty.EventDragStart, rid, d.detail(c, r, keys))
		}
	}
	if f := closest(el, focusable); f != nil {
		h.takeKeyboard(s, f, true)
	} else if h.keyboard != nil {
		h.blur(h.keyboard)
	}
	if h.drag == nil {
		return ErrNoReport
	}
	return nil
}

// DragMove moves the pointer of the drag in progress onto the element with
// an id in the drag's surface, or onto none ("": a gap, another surface, the
// cells, outside the window), at the surface's cell c, r (SPEC §9.1). It
// reports drag when the element under the pointer changes: t, the nearest
// one with an id and drag in its data-on (or drop), from it outward, or empty; while that is empty, when the cell
// changes instead. In a t with data-steps the pointer stays at the step it
// was at in t, or 0 in a new one (DragOver).
func (h *Host) DragMove(id string, c, r int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drag == nil {
		return ErrNoDrag
	}
	d := h.drag
	return h.dragMove(id, c, r, d.x, d.y, d.tx, d.ty, keys)
}

// DragMoveStep is DragMove with the pointer at step x, y of the element
// the drag began on, wherever the pointer is (DragStartStep). It also
// reports drag when the step changes.
func (h *Host) DragMoveStep(id string, c, r, x, y int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drag == nil {
		return ErrNoDrag
	}
	return h.dragMove(id, c, r, x, y, h.drag.tx, h.drag.ty, keys)
}

// DragOver is DragMove with the pointer at step tx, ty of t, for a t with
// drop in its data-on and data-steps (SPEC §9.1): a row with "0 1" is split
// in halves, a folder with "0 2" into before, into and after. The host
// lays nothing out, so the test says where in t the pointer is; a step past
// t's count is clamped to it, and one along an axis t does not count is
// ignored. It also reports drag when that step changes.
func (h *Host) DragOver(id string, c, r, tx, ty int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drag == nil {
		return ErrNoDrag
	}
	return h.dragMove(id, c, r, h.drag.x, h.drag.y, tx, ty, keys)
}

func (h *Host) dragMove(id string, c, r, x, y, tx, ty int, keys []string) error {
	d, t, _, err := h.dragOver(id)
	if err != nil {
		return err
	}
	x, y = d.clamp(x, y)
	tn := h.targetSteps(d, t)
	tx, ty = clampSteps(tn, tx, ty)
	if t == d.t && (t != "" || (c == d.c && r == d.r)) && x == d.x && y == d.y && tx == d.tx && ty == d.ty {
		return nil
	}
	d.t, d.c, d.r, d.x, d.y, d.tn, d.tx, d.ty = t, c, r, x, y, tn, tx, ty
	h.event(d.s, hotty.EventDrag, t, d.detail(c, r, keys))
	return nil
}

// DragEnd releases the button of the drag in progress over the element with
// an id, or none (""), at the surface's cell c, r (SPEC §9.1): dragend, its
// t as DragMove's. A drag that ends on the element it began on is also
// that element's click, after dragend, if it reports clicks: the nearest
// element with an id and drag in its data-on, from the one named outward,
// is the one it began on, whatever t is.
func (h *Host) DragEnd(id string, c, r int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drag == nil {
		return ErrNoDrag
	}
	d := h.drag
	return h.dragEnd(id, c, r, d.x, d.y, d.tx, d.ty, keys)
}

// DragEndStep is DragEnd with the pointer at step x, y of the element the
// drag began on (DragStartStep).
func (h *Host) DragEndStep(id string, c, r, x, y int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drag == nil {
		return ErrNoDrag
	}
	return h.dragEnd(id, c, r, x, y, h.drag.tx, h.drag.ty, keys)
}

// DragEndOver is DragEnd with the pointer at step tx, ty of t (DragOver).
func (h *Host) DragEndOver(id string, c, r, tx, ty int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drag == nil {
		return ErrNoDrag
	}
	return h.dragEnd(id, c, r, h.drag.x, h.drag.y, tx, ty, keys)
}

func (h *Host) dragEnd(id string, c, r, x, y, tx, ty int, keys []string) error {
	d, t, own, err := h.dragOver(id)
	if err != nil {
		return err
	}
	d.x, d.y = d.clamp(x, y)
	d.tn = h.targetSteps(d, t)
	d.tx, d.ty = clampSteps(d.tn, tx, ty)
	h.drag = nil
	h.event(d.s, hotty.EventDragEnd, t, d.detail(c, r, keys))
	if own == d.id && reportsClick(d.start) {
		detail := map[string]any{}
		if m := modBits(keys); m != 0 {
			detail["mods"] = m
		}
		if v, ok := attr(d.start, "value"); ok {
			detail["value"] = v
		}
		if len(detail) == 0 {
			h.event(d.s, hotty.EventClick, d.id, nil)
		} else {
			h.event(d.s, hotty.EventClick, d.id, detail)
		}
	}
	return nil
}

// Dragging reports whether a drag is in progress.
func (h *Host) Dragging() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.drag != nil
}

// dragOver finds what is under the pointer of the drag in progress, from
// the element named outward: t, the nearest element with an id and drag in
// its data-on, or drop; and own, the nearest
// with an id and drag, for whether the release is a click.
func (h *Host) dragOver(id string) (d *drag, t, own string, err error) {
	d = h.drag
	if d == nil {
		return nil, "", "", ErrNoDrag
	}
	if id == "" {
		return d, "", "", nil
	}
	el := d.s.byID(id)
	if el == nil {
		return nil, "", "", fmt.Errorf("%w: #%s in %s", ErrNoElement, id, d.s.name)
	}
	withID := func(on func(*html.Node) bool) string {
		n := closest(el, func(n *html.Node) bool { _, ok := attr(n, "id"); return ok && on(n) })
		if n == nil {
			return ""
		}
		v, _ := attr(n, "id")
		return v
	}
	own = withID(listens("drag"))
	t = withID(func(n *html.Node) bool { return listens("drag")(n) || listens("drop")(n) })
	return d, t, own, nil
}

// targetSteps is t's data-steps, when t has drop in its data-on.
func (h *Host) targetSteps(d *drag, t string) [2]int {
	if t == "" {
		return [2]int{}
	}
	el := d.s.byID(t)
	if !listens("drop")(el) {
		return [2]int{}
	}
	return steps(el)
}

// cutDrag ends the drag in progress early (SPEC §9.1): dragend, with no
// target, the last cell the pointer was on, and no step in t.
func (h *Host) cutDrag() {
	d := h.drag
	h.drag = nil
	d.tn = [2]int{}
	h.event(d.s, hotty.EventDragEnd, "", d.detail(d.c, d.r, nil))
}

// listens reports whether an element has kind in its data-on.
func listens(kind string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		on, _ := attr(n, "data-on")
		return slices.Contains(strings.Fields(on), kind)
	}
}

// detail is a drag event's detail: the cell, the modifier keys held as bits
// (left out when none is), and the step along each axis the element counts.
func (d *drag) detail(c, r int, keys []string) map[string]any {
	m := map[string]any{"c": c, "r": r}
	if mods := modBits(keys); mods != 0 {
		m["mods"] = mods
	}
	if d.n[0] > 0 {
		m["x"] = d.x
	}
	if d.n[1] > 0 {
		m["y"] = d.y
	}
	if d.tn[0] > 0 {
		m["tx"] = d.tx
	}
	if d.tn[1] > 0 {
		m["ty"] = d.ty
	}
	return m
}

// clamp keeps a step within the element's counts, 0 along an axis it does
// not count.
func (d *drag) clamp(x, y int) (int, int) {
	return clampSteps(d.n, x, y)
}

// clampSteps keeps a step within counts n, 0 along an axis n does not
// count.
func clampSteps(n [2]int, x, y int) (int, int) {
	return min(max(x, 0), n[0]), min(max(y, 0), n[1])
}

// steps reads an element's data-steps (SPEC §9.1): "<x>" or "<x> <y>",
// whole numbers from 0 up; anything else is none.
func steps(n *html.Node) [2]int {
	v, _ := attr(n, "data-steps")
	f := strings.Fields(v)
	if len(f) < 1 || len(f) > 2 {
		return [2]int{}
	}
	var out [2]int
	for i, s := range f {
		k, err := strconv.Atoi(s)
		if err != nil || k < 0 || strings.HasPrefix(s, "+") {
			return [2]int{}
		}
		out[i] = k
	}
	return out
}

func reportsClick(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if button(n) {
		return true
	}
	on, _ := attr(n, "data-on")
	return slices.Contains(strings.Fields(on), "click")
}

// button reports whether Space and Enter click n (SPEC §10.2): a button, a
// link, a summary. Any other element that reports clicks (data-on~=click)
// uses no keys.
func button(n *html.Node) bool {
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
	return false
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
func (h *Host) Press(surface, id string, keys ...string) error {
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
	if !s.press {
		return ErrNoPress
	}
	if h.alt1(s, keys) {
		return nil
	}
	h.pressAt(s, el, modBits(keys))
	return nil
}

func (h *Host) pressAt(s *Surface, el *html.Node, mods int) {
	t := ""
	if n := closest(el, func(n *html.Node) bool { _, ok := attr(n, "id"); return ok }); n != nil {
		t, _ = attr(n, "id")
	}
	// The detail is the element's cells, which this host cannot know, and
	// the modifiers held; with neither, there is none (SPEC §9).
	if mods == 0 {
		h.event(s, hotty.EventPress, t, nil)
		return
	}
	h.event(s, hotty.EventPress, t, map[string]int{"mods": mods})
}

// Fill types text into a text control, as the user does: the surface takes
// the keyboard there (a focus event, if it did not have it), the control's
// value becomes text (an input event, with data-on~=input), the caret goes
// to its end with nothing selected, and the change is committed when focus
// leaves it (Blur, another control, Submit).
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
	delete(s.fields, el)
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

// Choose picks an option of a select by its value, as from its list: input
// (with data-on~=input) and change come at once.
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
	h.pick(s, el, value)
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

// submit sends the form's fields as FormData has them, in tree order: named
// controls that are not disabled, boxes only when checked, and the
// submitter's own.
func (h *Host) submit(s *Surface, form, submitter *html.Node) {
	fid, ok := attr(form, "id")
	if !ok {
		return
	}
	fields := []any{}
	add := func(name, value string) { fields = append(fields, []any{name, value}) }
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
				add(name, v)
			}
		case box(n):
			if s.isChecked(n) {
				add(name, s.valueOf(n))
			}
		case control(n):
			add(name, s.valueOf(n))
		}
	})
	// The fields in tree order, two of one name two pairs; a form with
	// none still sends the list (SPEC §9).
	h.event(s, hotty.EventSubmit, fid, map[string]any{"fields": fields})
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

// --- hover (SPEC §9.4) ----------------------------------------------------------

// Hover moves the pointer onto a placed surface, over the element with an
// id (or "" for a point over nothing with an id), at the surface's cell c,
// r. The surface the pointer was on is left first. A placement made with
// Hover hears hover when the element differs from what it heard last;
// moves within an element send nothing.
//
// The host lays nothing out, so a test names what is under the pointer,
// as for drags.
func (h *Host) Hover(surface, id string, c, r int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, err := h.target(surface)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := h.element(s, id); err != nil {
			return err
		}
	}
	if h.hovered != nil && h.hovered != s {
		h.leave(h.hovered)
	}
	h.hovered = s
	if s.hover && s.placed && !s.detached && h.hears(hotty.FeatureHover) && (s.heard == nil || *s.heard != id) {
		s.heard = &id
		h.event(s, hotty.EventHover, id, map[string]int{"c": c, "r": r})
	}
	return nil
}

// Unhover takes the pointer off the surface it is on: onto the cells, or
// out of the terminal's window. A placement made with Hover hears it is
// out.
func (h *Host) Unhover() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hovered != nil {
		h.leave(h.hovered)
		h.hovered = nil
	}
}

// hears reports whether the host has a feature (SPEC §4): without hover it
// never says where the pointer is, and without count every click reads as
// the first of its run.
func (h *Host) hears(feature string) bool { return hotty.HasFeature(h.caps, feature) }

// leave tells a surface's program the pointer left it, if it asked and
// had not heard so.
func (h *Host) leave(s *Surface) {
	if s.placed && s.hover && !s.detached && h.hears(hotty.FeatureHover) && s.heard != nil {
		s.heard = nil
		h.event(s, hotty.EventHover, "", map[string]bool{"out": true})
	}
}

// Emit sends any event from a surface, for what the other actions do not
// cover (a resize, a kind from a later version). detail is written as
// msgpack, each Go int an int and each float64 a float (SPEC §3.3); nil
// sends none.
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

// Change changes the capabilities the host reports, as the terminal does
// when the user changes its font, theme or settings, or when a multiplexer's
// program moves to another viewer's host (SPEC §4): f is given a copy to
// change. The program, if it asked, then hears caps with every field that
// differs from what it last heard (SPEC §9), the empty value for one that
// became absent. A new scale alone says nothing: a zoom is not told (SPEC
// §4). A new cell size lays the documents out again, so a surface that asked
// for fit may hear it.
func (h *Host) Change(f func(c *hotty.Caps)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.caps
	f(&c)
	cellChanged := c.CellCSS != h.caps.CellCSS
	h.caps = c
	if h.queried && !h.text {
		h.tellCaps()
	}
	if cellChanged {
		for _, name := range slices.Sorted(maps.Keys(h.surfaces)) {
			h.refitSurface(h.surfaces[name])
		}
	}
}

// tellCaps sends caps with the fields that differ from what the program
// last heard, if any besides the scale.
func (h *Host) tellCaps() {
	was, now := h.heard, h.caps
	var buf bytes.Buffer
	w := msgp.NewWriter(&buf)
	var fields []func() error
	add := func(changed bool, key string, write func() error) {
		if changed {
			fields = append(fields, func() error {
				if err := w.WriteString(key); err != nil {
					return err
				}
				return write()
			})
		}
	}
	strs := func(v []string) func() error {
		return func() error {
			if err := w.WriteArrayHeader(uint32(len(v))); err != nil {
				return err
			}
			for _, x := range v {
				if err := w.WriteString(x); err != nil {
					return err
				}
			}
			return nil
		}
	}
	lists := func(v map[string][]string) func() error {
		return func() error {
			if err := w.WriteMapHeader(uint32(len(v))); err != nil {
				return err
			}
			for _, k := range slices.Sorted(maps.Keys(v)) {
				if err := w.WriteString(k); err != nil {
					return err
				}
				if err := strs(v[k])(); err != nil {
					return err
				}
			}
			return nil
		}
	}
	add(now.V != was.V, "v", func() error { return w.WriteString(now.V) })
	add(now.CellCSS != was.CellCSS, "cellcss", func() error {
		if err := w.WriteMapHeader(2); err != nil {
			return err
		}
		for _, kv := range []struct {
			k string
			v float64
		}{{"w", now.CellCSS.W}, {"h", now.CellCSS.H}} {
			if err := w.WriteString(kv.k); err != nil {
				return err
			}
			if err := w.WriteFloat64(kv.v); err != nil {
				return err
			}
		}
		return nil
	})
	add(now.Scale != was.Scale, "scale", func() error { return w.WriteFloat64(now.Scale) })
	add(now.Scheme != was.Scheme, "scheme", func() error { return w.WriteString(now.Scheme) })
	add(now.Mod != was.Mod, "mod", func() error { return w.WriteString(now.Mod) })
	add(!slices.Equal(now.Features, was.Features), "features", strs(now.Features))
	add(!maps.Equal(now.Limits, was.Limits), "limits", func() error {
		if err := w.WriteMapHeader(uint32(len(now.Limits))); err != nil {
			return err
		}
		for _, k := range slices.Sorted(maps.Keys(now.Limits)) {
			if err := w.WriteString(k); err != nil {
				return err
			}
			if err := w.WriteInt(now.Limits[k]); err != nil {
				return err
			}
		}
		return nil
	})
	add(!maps.EqualFunc(now.Net, was.Net, slices.Equal), "net", lists(now.Net))
	add(!maps.EqualFunc(now.Edit, was.Edit, slices.Equal), "edit", lists(now.Edit))
	add(now.Host != was.Host, "host", func() error { return w.WriteString(now.Host) })
	add(now.Version != was.Version, "version", func() error { return w.WriteString(now.Version) })
	// A zoom changes the scale alone, and says nothing (SPEC §4).
	if len(fields) == 0 || len(fields) == 1 && now.Scale != was.Scale {
		return
	}
	err := w.WriteMapHeader(uint32(len(fields)))
	for _, f := range fields {
		if err == nil {
			err = f()
		}
	}
	if err == nil {
		err = w.Flush()
	}
	if err != nil {
		h.protocol("a caps detail: %v", err)
		return
	}
	h.heard = now
	h.send(hotty.EncodePlain(hotty.Control{{K: "a", V: "ev"}, {K: "e", V: hotty.EventCaps}}, buf.Bytes()))
}

// Menu is the menu the program shows (a=menu, SPEC §10.5), nil when it
// shows none: the program sent none or an empty a=menu, or the menu went
// with Escape, a key it does not list, a change of screen or a reset. A
// host without the feature menu shows none.
func (h *Host) Menu() *hotty.MenuLevel {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.menu
}

// Prefix is the surface whose keys all reach the program since a key its
// keymap binds to prefix (SPEC §10.5), until the program sends a=focus or
// a=blur, the surface loses the keyboard, or the user focuses an element of
// it with a press; "" when none.
func (h *Host) Prefix() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.prefix != nil && h.prefix == h.keyboard {
		return h.prefix.name
	}
	return ""
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

// Lines is the screen's text as the program wrote its lines: Screen, with
// each row that a line went on to when it was wider than the terminal
// (autowrap) joined to the row before it. A row erased whole starts a line
// of its own.
func (h *Host) Lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scr.logical()
}

// Cursor is where the cursor is, and whether it is on show (DECTCEM:
// ESC [ ?25 l hides it): where the program leaves it for what prints next.
// row is a line of Screen, as a placement's (Surface.At).
func (h *Host) Cursor() (col, row int, shown bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return min(h.scr.col, h.cols-1), h.scr.row, !h.hidden
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
