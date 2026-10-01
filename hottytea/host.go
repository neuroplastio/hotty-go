// Package host is HOTTY for a Bubble Tea program: finding out whether the
// terminal is a host, keeping the program's surfaces on screen, and turning
// what comes back on the input into messages.
//
// A view declares the surfaces it wants each time the program's state
// changes (Layout); the host sends only what changed: a document once, a
// placement when a surface moves, a delete when it goes. Patches go out with
// Send. Everything leaves through Flush, as one tea.Raw, so HOTTY commands
// stay in order with Bubble Tea's frames.
//
// A host may lose placements to what the renderer writes (watch.go), and a
// document with them: then the host places every surface again, and sends a
// document again when a placement reports it gone.
package hottytea

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// Counts are what the host has sent: documents, placements, hides and
// deletes, and relayouts after the renderer erased or scrolled the screen.
type Counts struct{ Docs, Places, Hides, Deletes, Relayouts int }

// Mode is what the terminal turned out to be.
type Mode int

const (
	Detecting Mode = iota
	Native         // a HOTTY host: surfaces
	Text           // not a host: everything is drawn in cells
)

func (m Mode) String() string {
	return [...]string{"detecting", "native", "text"}[m]
}

// ReadyMsg reports the outcome of detection.
type ReadyMsg struct {
	Mode Mode
	Caps hotty.Caps
}

// EventMsg is something the user did in a surface.
type EventMsg struct{ hotty.Event }

// ErrorMsg is an error the host reported for a command.
type ErrorMsg struct{ hotty.ReplyMsg }

// RelayoutMsg asks the program to lay its surfaces out again (Layout): the
// screen was erased or scrolled under them, or a document went missing.
type RelayoutMsg struct{}

// PongMsg answers a Ping: the terminal has taken everything written before
// it. Took runs from writing the ping, right after the frame, to the answer.
type PongMsg struct{ Took time.Duration }

type (
	detectTimeoutMsg struct{}
	noHostMsg        struct{}
	erasedMsg        struct{}
	pingDueMsg       struct{ seq int }
)

// Rect is a rectangle of cells, from the top-left corner (0, 0).
type Rect struct{ X, Y, W, H int }

// Intersect is the part of r inside o; W or H is 0 or less if none is.
func (r Rect) Intersect(o Rect) Rect {
	x0, y0 := max(r.X, o.X), max(r.Y, o.Y)
	x1, y1 := min(r.X+r.W, o.X+o.W), min(r.Y+r.H, o.Y+o.H)
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// Surface is one surface a view wants on screen.
type Surface struct {
	Name string
	// Rect is the whole surface, in screen cells. With Clip it may reach
	// past the screen's edges.
	Rect Rect
	// Clip, if set, is the part of the screen the surface shows in, such as
	// a scrolling region: the host places the window of it that is inside
	// (SPEC §5.2). A surface with nothing inside is as good as not wanted.
	Clip *Rect
	// Keep: when the view stops wanting the surface, hide it instead of
	// deleting it, so it shows again without its document being sent
	// (SPEC §5.4). For surfaces that come back and are worth their memory.
	Keep bool
	// Z puts the surface above (greater) or below the surfaces it overlaps
	// (SPEC §5.2). Surfaces at the same Z stack in the order they were
	// first sent, the later above.
	Z int
	// Doc returns the document. It is called only when the document must be
	// sent: the first time, and again after the surface was deleted.
	Doc func() string
}

// placement is what the host last sent for a surface on screen.
type placement struct {
	at         Rect // the window's cells
	cols, rows int  // the whole surface
	win        hotty.Window
	z          int
}

// Host is the program's side of HOTTY.
type Host struct {
	Mode Mode
	Caps hotty.Caps

	dec    hotty.Decoder
	out    []string
	placed map[string]placement // on screen
	hasDoc map[string]bool      // sent: on screen or hidden
	keep   map[string]bool
	seen   map[string]int // the last layout that wanted a surface
	sawDA1 bool
	// layouts counts Layout calls; learned is a limit an EQUOTA taught.
	layouts, learned int
	watch            watcher
	pinging          bool
	pingSeq          int

	// Sent counts the bytes of HOTTY commands written, for the stats.
	Sent int
	// Count counts what Layout sent, for measuring.
	Count Counts
}

// New returns a host that has not been detected yet.
func New() *Host {
	return &Host{placed: map[string]placement{}, hasDoc: map[string]bool{}, keep: map[string]bool{}, seen: map[string]int{}}
}

// Detect asks the terminal whether it is a host (SPEC §4).
func (h *Host) Detect() tea.Cmd {
	return tea.Batch(
		tea.Raw(hotty.Query(1)),
		// A terminal that answers neither the query nor DA1 is not a host.
		tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return detectTimeoutMsg{} }),
	)
}

// Update takes the program's messages first. What is HOTTY's comes back as
// ReadyMsg, EventMsg, ErrorMsg or RelayoutMsg; other messages come back as
// they are. A nil message was consumed.
func (h *Host) Update(msg tea.Msg) (tea.Msg, tea.Cmd) {
	switch m := msg.(type) {
	case uv.UnknownOscEvent:
		hm, complete, isHotty := h.dec.Feed(string(m))
		if !isHotty {
			return msg, nil
		}
		if !complete {
			return nil, nil
		}
		if r, ok := hm.Reply(); ok {
			if caps, ok := r.Caps(); ok && h.Mode == Detecting {
				h.Mode, h.Caps = Native, caps
				return ReadyMsg{Mode: Native, Caps: caps}, nil
			}
			if !r.OK && r.Re == "doc" && r.Code == "EQUOTA" && h.hasDoc[r.Surface] {
				// Over the terminal's limit: it holds fewer surfaces than it
				// has. Keep below that from now on, and lay out again.
				h.learned = max(1, len(h.hasDoc)-1)
				delete(h.hasDoc, r.Surface)
				delete(h.placed, r.Surface)
				return RelayoutMsg{}, nil
			}
			if !r.OK && r.Re == "place" && r.Code == "ENOENT" && h.hasDoc[r.Surface] {
				// The host dropped the document with its placement: send both.
				delete(h.hasDoc, r.Surface)
				delete(h.placed, r.Surface)
				return RelayoutMsg{}, nil
			}
			if !r.OK {
				return ErrorMsg{r}, nil
			}
			return nil, nil
		}
		if ev, ok := hm.Event(); ok {
			return EventMsg{ev}, nil
		}
		return nil, nil

	case uv.PrimaryDeviceAttributesEvent:
		// The fence: a host answers the query before DA1. Wait a moment
		// anyway, in case this DA1 answers someone else's request.
		if h.Mode == Detecting && !h.sawDA1 {
			h.sawDA1 = true
			return nil, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return noHostMsg{} })
		}
		if h.Mode != Detecting && h.pinging {
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
		if m.seq == h.pingSeq && h.watch.ping != nil {
			h.watch.sendPing()
		}
		h.watch.mu.Unlock()
		return nil, nil

	case noHostMsg, detectTimeoutMsg:
		if h.Mode == Detecting {
			h.Mode = Text
			return ReadyMsg{Mode: Text}, nil
		}
		return nil, nil

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

// Ping asks the terminal to answer (DA1) right after the next frame: its
// answer, a PongMsg, says it has taken that frame and everything before it.
// One at a time, for measuring. Without a frame within 100 ms the ping goes
// on its own.
func (h *Host) Ping() tea.Cmd {
	h.watch.mu.Lock()
	h.watch.ping = []byte("\x1b[c")
	h.watch.pingWant = time.Now()
	h.watch.mu.Unlock()
	h.pinging = true
	h.pingSeq++
	seq := h.pingSeq
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return pingDueMsg{seq} })
}

// Layout makes the surfaces on screen match want: a new surface gets its
// document and a placement, a moved or scrolled one is placed again (only
// the window inside its Clip), and one no longer wanted is hidden if it is
// kept, or else deleted.
func (h *Host) Layout(want []Surface) {
	if h.Mode != Native {
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
			h.makeRoom(wanted)
			h.Send(hotty.Doc(s.Name, s.Doc()))
			h.Count.Docs++
			h.hasDoc[s.Name] = true
			delete(h.placed, s.Name)
		}
		p := placement{at: at, cols: s.Rect.W, rows: s.Rect.H,
			win: hotty.Window{X: at.X - s.Rect.X, Y: at.Y - s.Rect.Y, W: at.W, H: at.H}, z: s.Z}
		if old, ok := h.placed[s.Name]; !ok || old != p {
			h.Send(hotty.PlaceAt(s.Name, at.X, at.Y, p.cols, p.rows, p.win, p.z))
			h.Count.Places++
			h.placed[s.Name] = p
		}
	}
	for name := range h.hasDoc {
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

// Surfaces is how many surfaces the host keeps at most, shown and hidden,
// when the terminal reports no lower limit: a hidden surface keeps its
// memory (a document, and pixels in a terminal that draws them itself).
const Surfaces = 48

// limit is the most surfaces to have at once: the terminal's limit
// (SPEC §13), a lower one learned from an EQUOTA, or Surfaces.
func (h *Host) limit() int {
	n := Surfaces
	if l := h.Caps.Limits["surfaces"]; l > 0 {
		n = min(n, l)
	}
	if h.learned > 0 {
		n = min(n, h.learned)
	}
	return n
}

// makeRoom deletes kept surfaces, the ones seen longest ago first, until one
// more fits the limit. Hidden surfaces count toward a terminal's limits; a
// surface on screen, or wanted now, is never taken.
func (h *Host) makeRoom(wanted map[string]bool) {
	for len(h.hasDoc) >= h.limit() {
		oldest, at := "", h.layouts+1
		for name := range h.hasDoc {
			if wanted[name] {
				continue
			}
			if _, on := h.placed[name]; on {
				continue
			}
			if s := h.seen[name]; s < at {
				oldest, at = name, s
			}
		}
		if oldest == "" {
			return // everything is on screen
		}
		h.Delete(oldest)
	}
}

// Delete deletes a surface, kept or not: one that will not come back.
func (h *Host) Delete(name string) {
	if h.hasDoc[name] {
		h.Send(hotty.Del(name))
		h.Count.Deletes++
	}
	delete(h.hasDoc, name)
	delete(h.placed, name)
	delete(h.keep, name)
	delete(h.seen, name)
}

// Shown reports whether a surface's document is with the host, on screen or
// hidden, so patches to it make sense (a hidden one stays current).
func (h *Host) Shown(name string) bool { return h.hasDoc[name] }

// Send queues commands (patches, focus) for the next Flush. It does nothing
// when the terminal is not a host.
func (h *Host) Send(cmds ...string) {
	if h.Mode == Native {
		h.out = append(h.out, cmds...)
	}
}

// Flush writes the queued commands, as one tea.Raw.
func (h *Host) Flush() tea.Cmd {
	if len(h.out) == 0 {
		return nil
	}
	s := strings.Join(h.out, "")
	h.out = h.out[:0]
	h.Sent += len(s)
	return tea.Raw(s)
}
