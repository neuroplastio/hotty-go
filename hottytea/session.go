// Package hottytea is HOTTY for a full-screen Bubble Tea program: it finds
// out whether the terminal is a HOTTY host, keeps the program's surfaces
// on screen as its frame changes, and turns what the host sends into
// messages.
//
// A Session is the program's side. Its life in a program:
//
//   - Before the program runs, its output goes through the Session (Watch,
//     or WatchFile for a terminal), and Attach gives it the program.
//   - Init returns s.Detect().
//   - Update hands every message to s.Update first. What is HOTTY's comes
//     back as ReadyMsg once the Session knows the terminal (Mode Native or
//     Text), EventMsg for what the user did in a surface, ErrorMsg for a
//     command the host refused, AckMsg for the ok of a command the program
//     numbered, and RelayoutMsg when the surfaces must be placed again.
//     Anything else comes back as it was, and nil when it was the
//     Session's alone.
//   - When the program draws its frame (in Update, since View cannot
//     return commands), it says which surfaces it wants where (Layout),
//     and returns Flush with its commands. The Session sends only what
//     changed: a document once, a placement when a surface moves, a hide
//     or a delete when it goes. View then returns the cells, with room
//     left where the surfaces go.
//   - Deltas go out with Send, and leave with the next Flush, as one
//     tea.Raw, so that HOTTY commands stay in order with Bubble Tea's
//     frames.
//
// Bubble Tea's renderer erases the screen and scrolls regions on its own,
// and a host may drop placements with them. The Session reads the output
// on its way out, and asks for a new layout when that happens
// (RelayoutMsg); it sends a document again when a placement reports it
// gone, and keeps the number of surfaces within the host's limit.
//
// In a terminal that is not a host, Mode is Text and the Session sends
// nothing: the program draws everything in cells.
package hottytea

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// Counts are what Layout has sent so far: documents, placements, hides and
// deletes, and the relayouts after the renderer erased or scrolled the
// screen. A program that measures its own HOTTY traffic reads them.
type Counts struct{ Docs, Places, Hides, Deletes, Relayouts int }

// Mode is what the terminal turned out to be.
type Mode int

// The modes.
const (
	Detecting Mode = iota // not known yet: Layout and Send do nothing
	Native                // a HOTTY host: surfaces
	Text                  // not a host: everything is drawn in cells
)

// String is the mode's name: "detecting", "native" or "text".
func (m Mode) String() string {
	switch m {
	case Detecting:
		return "detecting"
	case Native:
		return "native"
	case Text:
		return "text"
	}
	return "Mode(" + strconv.Itoa(int(m)) + ")"
}

// ReadyMsg reports the outcome of detection, once.
type ReadyMsg struct {
	Mode Mode
	// Caps is what the host said about itself, when Mode is Native.
	Caps hotty.Caps
}

// EventMsg is something the user did in a surface (SPEC §9).
type EventMsg struct{ hotty.Event }

// ErrorMsg is an error the host replied to a command that the Session did
// not handle itself (it handles EQUOTA and ENOENT for its own documents
// and placements).
type ErrorMsg struct{ hotty.Reply }

// AckMsg is the host's ok for a command the program numbered (hotty.N):
// the host has carried it out. Its error comes as ErrorMsg, with the same
// N. A reply reaches the program the way the user's keys do, in order
// (SPEC §3.6), so every key typed before the host carried the command out
// has reached the program before its AckMsg: after a numbered hotty.Focus,
// the keys before the AckMsg were typed while the program had the
// keyboard, and the ones after it go to the surface.
type AckMsg struct{ hotty.Reply }

// RelayoutMsg asks the program to lay its surfaces out again: the screen
// was erased or scrolled under them, or a document went missing. Draw a
// frame; its Layout puts the surfaces back.
type RelayoutMsg struct{}

// PongMsg answers a Ping: the terminal has taken everything written before
// it. Took runs from writing the ping to the answer.
type PongMsg struct{ Took time.Duration }

type (
	detectTickMsg struct{ at time.Time }
	erasedMsg     struct{}
	pingDueMsg    struct{ seq int }
)

// Rect is a rectangle of cells, from the screen's top-left corner (0, 0).
type Rect struct{ X, Y, W, H int }

// Intersect is the part of r inside o; W or H is 0 or less if none is.
func (r Rect) Intersect(o Rect) Rect {
	x0, y0 := max(r.X, o.X), max(r.Y, o.Y)
	x1, y1 := min(r.X+r.W, o.X+o.W), min(r.Y+r.H, o.Y+o.H)
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// Surface is one surface the program wants on screen.
type Surface struct {
	// Name is the surface's name: unique, and valid (hotty.ValidName).
	Name string
	// Rect is the whole surface, in screen cells. With Clip it may reach
	// past the screen's edges.
	Rect Rect
	// Clip, if set, is the part of the screen the surface shows in, such as
	// a scrolling region: the Session places the window of it that is
	// inside (SPEC §5.2). A surface with nothing inside is as good as not
	// wanted.
	Clip *Rect
	// Keep: when the program stops wanting the surface, hide it instead of
	// deleting it, so it shows again without its document being sent
	// (SPEC §5.4). For surfaces that come back and are worth their memory.
	Keep bool
	// Z puts the surface above (greater) or below the surfaces it overlaps
	// (SPEC §5.2). Surfaces at the same Z stack in the order they were
	// first sent, the later above.
	Z int
	// Press asks for an EventMsg of kind press wherever the user presses
	// in the surface, on text and empty space too: for a program that moves
	// its selection to the card pressed.
	Press bool
	// Fit asks for an EventMsg of kind fit whenever the rows the document
	// needs at Rect's width change: for a program that sizes the surface
	// by its content, and lays out again when a late image makes it
	// taller (SPEC §5.2).
	Fit bool
	// Hover asks for an EventMsg of kind hover each time the element with
	// an id under the pointer changes, and when the pointer leaves the
	// surface: for a hint of the program's own, or to clear what it lit
	// outside the surface (SPEC §9.4).
	Hover bool
	// Doc returns the document, and must not be nil. It is called only
	// when the document must be sent: the first time, and again after the
	// surface was deleted or the host lost it.
	Doc func() string
}

// placement is what the Session last sent for a surface on screen.
type placement struct {
	at         Rect // the window's cells
	cols, rows int  // the whole surface
	win        hotty.Window
	z          int
	press, fit bool
	hover      bool
}

// DefaultLimit is how many surfaces a Session keeps at most, shown and
// hidden, unless its Limit or the terminal says fewer: a hidden surface
// keeps its memory in the terminal (a document, and pixels in a terminal
// that draws them itself).
const DefaultLimit = 48

// Session is a Bubble Tea program's side of HOTTY (SPEC §2: a program's
// connection to a host). Its methods are for the program's Update, which
// Bubble Tea runs on one goroutine; the output it watches, and the
// commands Flush returns, run on others.
type Session struct {
	// Mode is what detection found.
	Mode Mode
	// Caps is what the host said about itself, when Mode is Native.
	Caps hotty.Caps
	// Limit is the most surfaces to keep, shown and hidden; 0 is
	// DefaultLimit. To stay within it, Layout deletes the hidden surfaces
	// seen longest ago. The terminal's own limit (Caps.Limits["surfaces"],
	// SPEC §13), and one an EQUOTA taught, lower it further, and those
	// are hard: a surface beyond them is not sent until there is room.
	Limit int

	// Sent counts the bytes of HOTTY commands Flush has written.
	Sent int
	// Count counts what Layout has sent.
	Count Counts

	dec     hotty.Decoder
	placed  map[string]placement // on screen
	hasDoc  map[string]bool      // sent: on screen or hidden
	keep    map[string]bool
	seen    map[string]int  // the last layout that wanted a surface
	refused map[string]bool // documents refused with EQUOTA, whose placement will fail too
	// det decides what the terminal is, from Detect on; da1 counts the
	// DA1 answers the Session's pings are owed.
	det       hotty.Detector
	detecting bool
	da1       int
	layouts   int // Layout calls
	learned   int // a limit an EQUOTA taught
	closed    bool
	watch     watcher
	pinging   bool
	pingSeq   int

	// The commands Send queued, which Flush's command takes when it runs,
	// on a goroutine of Bubble Tea's; queued counts the bytes since the
	// last Flush.
	outMu  sync.Mutex
	out    []string
	queued int
}

// New returns a Session that has not detected the terminal yet.
func New() *Session {
	return &Session{placed: map[string]placement{}, hasDoc: map[string]bool{}, keep: map[string]bool{},
		seen: map[string]int{}, refused: map[string]bool{}}
}

// Detect asks the terminal whether it is a host (SPEC §4): return it from
// Init. Update answers ReadyMsg once it knows: when the host replies, when
// the terminal answers the fence without a reply, or after 1.5 s of
// silence.
func (h *Session) Detect() tea.Cmd {
	h.detecting = true
	return tea.Batch(tea.Raw(h.det.Start(time.Now())), h.detectTick())
}

// detectTick calls the Detector's Tick at its deadline.
func (h *Session) detectTick() tea.Cmd {
	if h.det.Done {
		return nil
	}
	return tea.Tick(time.Until(h.det.Deadline), func(t time.Time) tea.Msg { return detectTickMsg{t} })
}

// detected reports what the Detector decided, once, as ReadyMsg, and keeps
// its Tick on time when its deadline moved from was.
func (h *Session) detected(was time.Time) (tea.Msg, tea.Cmd) {
	var msg tea.Msg
	if h.det.Decided && h.Mode == Detecting {
		h.Mode = Text
		if h.det.State == hotty.Native {
			h.Mode, h.Caps = Native, h.det.Caps
		}
		msg = ReadyMsg{Mode: h.Mode, Caps: h.Caps}
	}
	var cmd tea.Cmd
	if !h.det.Deadline.Equal(was) {
		cmd = h.detectTick()
	}
	return msg, cmd
}

// Update takes the program's messages first. What is HOTTY's comes back as
// ReadyMsg, EventMsg, ErrorMsg, AckMsg, RelayoutMsg or PongMsg; other
// messages come back as they are. A nil message was the Session's alone.
func (h *Session) Update(msg tea.Msg) (tea.Msg, tea.Cmd) {
	switch m := msg.(type) {
	case uv.UnknownOscEvent:
		hm, res := h.dec.Feed(string(m))
		switch res {
		case hotty.NotHotty:
			return msg, nil
		case hotty.Partial, hotty.Invalid:
			return nil, nil
		}
		if r, ok := hm.Reply(); ok {
			return h.reply(r)
		}
		if ev, ok := hm.Event(); ok {
			return EventMsg{ev}, nil
		}
		return nil, nil

	case uv.PrimaryDeviceAttributesEvent:
		// Until detection is done, every DA1 is its own: the query's fence,
		// or an earlier question's (SDK.md §3.8).
		if was := h.det.Deadline; h.detecting && h.det.DA1(time.Now()) {
			return h.detected(was)
		}
		if h.da1 == 0 {
			return msg, nil // the program's own request
		}
		h.da1--
		if h.pinging {
			h.pinging = false
			h.watch.mu.Lock()
			took := time.Since(h.watch.pingAt)
			h.watch.mu.Unlock()
			return PongMsg{Took: took}, nil
		}
		return nil, nil

	case pingDueMsg:
		// No frame came to carry the ping (the screen did not change):
		// write it on its own. A timer for an earlier ping does nothing.
		h.watch.mu.Lock()
		defer h.watch.mu.Unlock()
		if m.seq != h.pingSeq || h.watch.ping == nil {
			return nil, nil
		}
		if !h.watch.sendPing() {
			// Nothing has been written yet to write it with.
			ping := string(h.watch.ping)
			h.watch.ping = nil
			h.watch.pingAt = time.Now()
			return nil, tea.Raw(ping)
		}
		return nil, nil

	case detectTickMsg:
		was := h.det.Deadline
		h.det.Tick(m.at)
		return h.detected(was)

	case erasedMsg:
		h.watch.handled()
		if h.Mode != Native || len(h.placed) == 0 {
			return nil, nil
		}
		h.placed = map[string]placement{}
		h.Count.Relayouts++
		return RelayoutMsg{}, nil
	}
	return msg, nil
}

func (h *Session) reply(r hotty.Reply) (tea.Msg, tea.Cmd) {
	if was := h.det.Deadline; h.detecting && h.det.Reply(r, time.Now()) {
		return h.detected(was) // nothing, for a late answer
	}
	switch {
	case r.OK && r.N != 0 && r.Re != "q":
		return AckMsg{r}, nil // a command the program numbered; queries are the Session's
	case r.OK:
		return nil, nil
	case r.Re == "doc" && r.Code == hotty.EQUOTA && h.hasDoc[r.Surface]:
		// Over the terminal's limit: it holds fewer surfaces than the
		// Session has sent. Keep below that from now on, and lay out
		// again. The placement sent with the document fails too.
		if n := max(1, len(h.hasDoc)-1); h.learned == 0 || n < h.learned {
			h.learned = n
		}
		delete(h.hasDoc, r.Surface)
		delete(h.placed, r.Surface)
		h.refused[r.Surface] = true
		return RelayoutMsg{}, nil
	case r.Re == "place" && r.Code == hotty.ENOENT && h.refused[r.Surface]:
		delete(h.refused, r.Surface)
		return nil, nil
	case r.Re == "place" && r.Code == hotty.ENOENT && h.hasDoc[r.Surface]:
		// The host dropped the document with its placement: send both.
		delete(h.hasDoc, r.Surface)
		delete(h.placed, r.Surface)
		return RelayoutMsg{}, nil
	}
	return ErrorMsg{r}, nil
}

// Ping asks the terminal to answer (DA1) right after the next frame: its
// answer, a PongMsg, says it has taken that frame and everything before
// it, so a program can measure what a frame costs the terminal. One at a
// time. Without a frame within 100 ms the ping goes on its own.
func (h *Session) Ping() tea.Cmd {
	h.watch.mu.Lock()
	h.watch.ping = []byte("\x1b[c")
	h.watch.mu.Unlock()
	if !h.pinging {
		h.da1++
	}
	h.pinging = true
	h.pingSeq++
	seq := h.pingSeq
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return pingDueMsg{seq} })
}

// Layout makes the surfaces on screen match want: a new surface gets its
// document and a placement, a moved or scrolled one is placed again (only
// the window inside its Clip), and one no longer wanted is hidden if it is
// kept, or else deleted. What it sends leaves with the next Flush. It does
// nothing until Mode is Native, and after Close.
func (h *Session) Layout(want []Surface) {
	if h.Mode != Native || h.closed {
		return
	}
	h.layouts++
	type shown struct {
		s  Surface
		at Rect
	}
	var on []shown
	wanted := make(map[string]bool, len(want))
	for _, s := range want {
		at := s.Rect
		if s.Clip != nil {
			at = at.Intersect(*s.Clip)
		}
		if at.W <= 0 || at.H <= 0 {
			continue // nothing in view
		}
		wanted[s.Name] = true
		h.keep[s.Name] = s.Keep
		h.seen[s.Name] = h.layouts
		on = append(on, shown{s, at})
	}
	for _, o := range on {
		s, at := o.s, o.at
		if !h.hasDoc[s.Name] {
			if !h.makeRoom(wanted) {
				continue // the terminal holds no more: not until one goes
			}
			h.Send(hotty.Doc(s.Name, s.Doc()))
			h.Count.Docs++
			h.hasDoc[s.Name] = true
			delete(h.refused, s.Name)
			delete(h.placed, s.Name)
		}
		p := placement{at: at, cols: s.Rect.W, rows: s.Rect.H,
			win: hotty.Window{X: at.X - s.Rect.X, Y: at.Y - s.Rect.Y, W: at.W, H: at.H}, z: s.Z, press: s.Press, fit: s.Fit, hover: s.Hover}
		if old, ok := h.placed[s.Name]; !ok || old != p {
			h.Send(hotty.PlaceAt(s.Name, at.X, at.Y,
				hotty.Placement{Cols: p.cols, Rows: p.rows, Window: p.win, Z: p.z, Press: p.press, Fit: p.fit, Hover: p.hover}))
			h.Count.Places++
			h.placed[s.Name] = p
		}
	}
	for _, name := range h.names() {
		switch {
		case wanted[name]:
		case h.keep[name]:
			if _, on := h.placed[name]; on {
				h.Send(hotty.Hide(name))
				h.Count.Hides++
				delete(h.placed, name)
			}
		default:
			h.Delete(name)
		}
	}
}

// names are the surfaces sent, shown or hidden, in order, so that what
// Layout sends is the same every run.
func (h *Session) names() []string {
	out := make([]string, 0, len(h.hasDoc))
	for name := range h.hasDoc {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// limit is the most surfaces to have at once, and whether it is the
// terminal's: Limit or DefaultLimit, lowered by the terminal's limit
// (SPEC §13) and one learned from an EQUOTA.
func (h *Session) limit() (n int, hard bool) {
	n = DefaultLimit
	if h.Limit > 0 {
		n = h.Limit
	}
	if l := h.Caps.Limits["surfaces"]; l > 0 && l <= n {
		n, hard = l, true
	}
	if h.learned > 0 && h.learned <= n {
		n, hard = h.learned, true
	}
	return n, hard
}

// makeRoom deletes surfaces the program no longer wants, the ones seen
// longest ago first, until one more fits the limit: hidden ones, and ones
// leaving the screen in this layout. Hidden surfaces count toward a
// terminal's limits; a surface wanted now is never taken. It reports
// whether one more may be sent: only the terminal's own limit refuses one.
func (h *Session) makeRoom(wanted map[string]bool) bool {
	n, hard := h.limit()
	for len(h.hasDoc) >= n {
		oldest, at := "", h.layouts+1
		for _, name := range h.names() {
			if wanted[name] {
				continue
			}
			if s := h.seen[name]; s < at {
				oldest, at = name, s
			}
		}
		if oldest == "" {
			return !hard // everything is on screen
		}
		h.Delete(oldest)
	}
	return true
}

// Delete deletes a surface, kept or not: one that will not come back. It
// leaves with the next Flush.
func (h *Session) Delete(name string) {
	if h.hasDoc[name] {
		h.Send(hotty.Del(name))
		h.Count.Deletes++
	}
	delete(h.hasDoc, name)
	delete(h.placed, name)
	delete(h.keep, name)
	delete(h.seen, name)
}

// Close ends the Session, on the program's way out: it deletes every
// surface the Session sent, and none of anyone else's, and returns the
// command that writes the deletes. Return it with tea.Quit:
//
//	return m, tea.Sequence(m.s.Close(), tea.Quit)
//
// After it, Layout and Send do nothing, so that a frame drawn before the
// program quits does not send a document again. A host deletes the
// surfaces placed on the alternate screen when the program leaves it, but
// it may not (SPEC §5.4 says SHOULD).
func (h *Session) Close() tea.Cmd {
	for _, name := range h.names() {
		h.Delete(name)
	}
	h.closed = true
	return h.Flush()
}

// DetachAll detaches every surface the Session has sent (SPEC §5.5): for a
// program that leaves its surfaces on the screen when it quits, rather
// than Close. They stay as they are, and stop reporting to whatever reads
// the terminal after the program, a shell for instance. It leaves with the
// next Flush:
//
//	m.s.DetachAll()
//	return m, tea.Sequence(m.s.Flush(), tea.Quit)
func (h *Session) DetachAll() {
	for _, name := range h.names() {
		h.Send(hotty.Detach(name))
	}
}

// Has reports whether the host has a surface's document, on screen or
// hidden, so that deltas to it make sense: a hidden surface takes them,
// and shows them when it is placed again.
func (h *Session) Has(name string) bool { return h.hasDoc[name] }

// Placed reports whether a surface is on screen: placed by the last
// Layout, and not hidden since.
func (h *Session) Placed(name string) bool {
	_, ok := h.placed[name]
	return ok
}

// Send queues commands (deltas, focus) for the next Flush. It does
// nothing when the terminal is not a host, or after Close.
func (h *Session) Send(cmds ...string) {
	if h.Mode != Native || h.closed {
		return
	}
	h.outMu.Lock()
	defer h.outMu.Unlock()
	for _, c := range cmds {
		h.out = append(h.out, c)
		h.queued += len(c)
	}
}

// Flush returns the command that writes what is queued, as one tea.Raw, or
// nil when nothing was queued since the last Flush: return it from
// Update.
//
// Bubble Tea runs each command on a goroutine of its own, so two updates'
// commands may run in either order. The command therefore takes the queue
// when it runs, not when it is made: whichever runs first writes
// everything queued so far, in order, and a later one what is left, if
// anything.
func (h *Session) Flush() tea.Cmd {
	h.outMu.Lock()
	defer h.outMu.Unlock()
	if h.queued == 0 {
		return nil
	}
	h.Sent += h.queued
	h.queued = 0
	return h.take
}

// take is Flush's command.
func (h *Session) take() tea.Msg {
	h.outMu.Lock()
	defer h.outMu.Unlock()
	if len(h.out) == 0 {
		return nil
	}
	s := strings.Join(h.out, "")
	h.out = h.out[:0]
	return tea.RawMsg{Msg: s}
}
