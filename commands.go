package hotty

import (
	"strconv"
	"strings"
)

// Query asks whether the terminal is a HOTTY host that speaks Version,
// fenced by Primary Device Attributes (SPEC §4): a host replies to the
// query, numbered n, before the DA1 answer every terminal sends, in Version
// or with EVERSION. A DA1 answer with no reply before it means there is no
// host. Late asks for a late answer as well.
func Query(n int, opts ...QueryOption) string {
	ctl := Control{{"a", "q"}, {"n", strconv.Itoa(n)}, {"v", Version}}
	for _, o := range opts {
		o.query(&ctl)
	}
	return Encode(ctl, nil) + "\x1b[c"
}

// QueryOption is an option of Query: Late.
type QueryOption interface{ query(*Control) }

type late struct{}

func (late) query(c *Control) { set(c, "late", "1") }

// Late asks for a late answer (late=1, SPEC §4). What stands between the
// program and the terminal and has no host yet, such as a multiplexer
// running the program's pane with no terminal attached, may hold the
// query, and answer it once there is a host, whenever that is. A program
// that asks takes that answer as detection finding a host: it can start
// using HOTTY after it fell back to cells. A host answers at once, as it
// answers any query.
//
// A program that asked, and will no longer take the answer, such as one
// about to exit, sends WithdrawLate, or the answer would reach whatever
// reads the terminal after it.
func Late() QueryOption { return late{} }

// WithdrawLate withdraws a query that asked for a late answer (SPEC §4): a
// query that wants no answer (a=q:q=2), which takes the held one's place.
// It has no fence, and nothing answers it.
func WithdrawLate() string {
	return Encode(Control{{"a", "q"}, {"q", strconv.Itoa(int(NoReply))}}, nil)
}

// Doc creates a surface, or replaces its document (SPEC §5.1). The surface
// is the program's, even one it had detached: it reports what the user does
// in it, and takes the keyboard on the program's behalf (SPEC §5.5), unless
// the document is sent Detached. It is answered on error: EQUOTA when the
// host holds no more surfaces.
func Doc(surface, html string, opts ...DocOption) string {
	ctl := Control{{"a", "doc"}, {"s", surface}, {"q", ""}}
	var replies []ReplyOption
	for _, o := range opts {
		if r, ok := o.(ReplyOption); ok {
			replies = append(replies, r)
			continue
		}
		o.doc(&ctl)
	}
	return command(ctl, []byte(html), ReplyOnError, replies)
}

// DocOption is an option of Doc: a ReplyOption, Detached, or Scroll.
type DocOption interface{ doc(*Control) }

// doc does nothing: Doc takes its reply options apart from the others, so
// that their order does not matter.
func (ReplyOption) doc(*Control) {}

type detached struct{}

func (detached) doc(c *Control) { set(c, "d", "1") }

// Detached sends a document the program only shows (d=1, SPEC §5.5): the
// surface is created detached, or its document replaced and the surface
// detached, in the one command. It reports nothing, never takes the
// keyboard, and its controls act disabled; hover, selection, <details> and
// hyperlinks still work, and it is placed, changed by deltas, hidden and
// deleted as before.
//
// It is what a command prints among its output, which outlives it: whatever
// reads the terminal next, a shell, would read the surface's events as
// typing. A host older than §5.5 ignores d=1.
func Detached() DocOption { return detached{} }

// Axes are the directions a document scrolls in (SPEC §5.1): a bitmask of
// ScrollVertical and ScrollHorizontal.
type Axes int

// The axes of Scroll.
const (
	ScrollVertical   Axes = 1
	ScrollHorizontal Axes = 2
)

type scroll Axes

func (s scroll) doc(c *Control) {
	if s != 0 {
		set(c, "scroll", strconv.Itoa(int(s)))
	}
}

// Scroll lets the document scroll along the axes given (scroll=<axes>,
// SPEC §5.1, §5.3), as a page does in a browser: its overflow scrolls, with
// scrollbars in its pixels, never its cells, and a gesture it cannot take
// further goes on to the terminal unless the document's CSS
// overscroll-behavior stops it. Scrolling is local: the program hears
// nothing of it, and a delta keeps the offsets.
//
// Like Detached, it belongs to the document: one sent without it does not
// scroll. 0, the default, sends no key; any other value goes out as given,
// for the host to judge. A host that does not scroll (Caps.Scroll false)
// ignores it and clips, as it clips every other document.
func Scroll(axes Axes) DocOption { return scroll(axes) }

// Window is the part of a surface a placement shows, in cells from the
// surface's top-left corner (SPEC §5.2). The zero Window shows all of it.
type Window struct{ X, Y, W, H int }

// Placement is where and how a surface is shown (SPEC §5.2).
type Placement struct {
	// Cols and Rows are the surface's size in cells, 1 to 1000. Rows 0 is
	// auto: the host lays the document out Cols wide and takes the rows
	// its content needs, and says how many in the reply.
	Cols, Rows int
	// Window, if not zero, is the part of the surface shown: a full-screen
	// program scrolling a surface out of view shows what is still in it.
	Window Window
	// Z stacks overlapping placements, from -1000 to 1000: greater is
	// above, and 0 is the usual.
	Z int
	// Press asks for a press event wherever the user presses in the
	// placement, on text and empty space too (p=1).
	Press bool
	// Fit asks for a fit event whenever the rows the document needs at
	// this width change from the rows last heard, starting from this
	// placement's (f=1): an image or font arrived, a resource was
	// replaced, a delta landed. The placement keeps its size; placing the
	// surface again is the program's to do (SPEC §5.2).
	Fit bool
	// Hover asks for a hover event each time the element with an id under
	// the pointer changes, and when the pointer leaves the window (v=1):
	// for a hint of the program's own, or to clear what it lit outside
	// the surface when the pointer goes onto it (SPEC §9.4).
	Hover bool
	// KeepCursor leaves the cursor where it was (C=1). Otherwise the host
	// moves it to the start of the line below the placement, as text would.
	KeepCursor bool
}

func placementControl(p Placement, surface string) Control {
	ctl := Control{{"a", "place"}, {"s", surface}, {"c", strconv.Itoa(p.Cols)}}
	if p.Rows > 0 {
		ctl = With(ctl, "r", strconv.Itoa(p.Rows))
	} else {
		ctl = With(ctl, "r", "auto")
	}
	if w := p.Window; w != (Window{}) && (p.Rows <= 0 || w != (Window{0, 0, p.Cols, p.Rows})) {
		ctl = append(ctl, KV{"x", strconv.Itoa(w.X)}, KV{"y", strconv.Itoa(w.Y)},
			KV{"w", strconv.Itoa(w.W)}, KV{"h", strconv.Itoa(w.H)})
	}
	if p.Z != 0 {
		ctl = With(ctl, "z", strconv.Itoa(p.Z))
	}
	if p.Press {
		ctl = With(ctl, "p", "1")
	}
	if p.Fit {
		ctl = With(ctl, "f", "1")
	}
	if p.Hover {
		ctl = With(ctl, "v", "1")
	}
	if p.KeepCursor {
		ctl = With(ctl, "C", "1")
	}
	return ctl
}

// Placement reads a place command (SPEC §5.2), as a host or a relay
// does: Rows 0 for r=auto or none, and the window as the command gives it,
// its defaults filled in: w reaches the surface's right edge, and h its
// bottom, which with Rows 0 is the host's to find (H 0). A command out of
// range is an *Error with Code EINVAL, as a host answers it.
func PlacementOf(m Message) (Placement, error) {
	fail := func(detail string) (Placement, error) {
		return Placement{}, &Error{Code: EINVAL, Detail: detail, Re: Get(m.Control, "a"), Surface: Get(m.Control, "s")}
	}
	if Get(m.Control, "a") != "place" {
		return fail("not a place command")
	}
	num := func(k string, def, lo, hi int) (int, bool) {
		v, ok := Lookup(m.Control, k)
		if !ok {
			return def, true
		}
		n, err := strconv.Atoi(v)
		return n, err == nil && n >= lo && n <= hi
	}
	if !Has(m.Control, "c") {
		return fail("missing c")
	}
	var p Placement
	var ok bool
	if p.Cols, ok = num("c", 0, 1, MaxSize); !ok {
		return fail("c out of range")
	}
	if v := Get(m.Control, "r"); Has(m.Control, "r") && v != "auto" {
		if p.Rows, ok = num("r", 0, 1, MaxSize); !ok {
			return fail("r out of range")
		}
	}
	if Has(m.Control, "x") || Has(m.Control, "y") || Has(m.Control, "w") || Has(m.Control, "h") {
		x, okx := num("x", 0, 0, MaxSize-1)
		y, oky := num("y", 0, 0, MaxSize-1)
		w, okw := num("w", p.Cols-x, 1, MaxSize)
		h, okh := num("h", max(p.Rows-y, 0), 1, MaxSize)
		if !okx || !oky || !okw || !okh || w < 1 || x+w > p.Cols || p.Rows > 0 && (h < 1 || y+h > p.Rows) {
			return fail("window out of the surface")
		}
		p.Window = Window{X: x, Y: y, W: w, H: h}
	}
	if p.Z, ok = num("z", 0, -1000, 1000); !ok {
		return fail("z out of range")
	}
	p.Press, p.Fit, p.Hover, p.KeepCursor = Get(m.Control, "p") == "1", Get(m.Control, "f") == "1", Get(m.Control, "v") == "1", Get(m.Control, "C") == "1"
	return p, nil
}

// CursorBelow is the cursor's move a placement without KeepCursor makes
// (SPEC §5.2): rows index operations (IND) and a carriage return, to the
// start of the line below the placement. A relay that keeps a placement
// from the screen it emulates feeds it this instead.
func CursorBelow(rows int) string {
	return strings.Repeat("\x1bD", max(rows, 0)) + "\r"
}

// Place places a surface at the cursor (SPEC §5.2). Placing a surface that
// is placed moves it. It is answered on error: ENOENT when the host has no
// such surface (it may have dropped it), EINVAL for a size or window out of
// range. A reply to a placement with Rows 0 carries the rows chosen.
func Place(surface string, p Placement, opts ...ReplyOption) string {
	return command(placementControl(p, surface), nil, ReplyOnError, opts)
}

// PlaceAt places a surface with its window's top-left corner at cell (x,
// y), counted from 0 at the screen's top-left, and leaves the cursor where
// it was: the way a full-screen program lays surfaces out without
// disturbing its own drawing. It saves the cursor (DECSC), moves it, places
// with C=1, and restores it (DECRC).
func PlaceAt(surface string, x, y int, p Placement, opts ...ReplyOption) string {
	p.KeepCursor = true
	return "\x1b7\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(x+1) + "H" +
		command(placementControl(p, surface), nil, ReplyOnError, opts) + "\x1b8"
}

// Hide removes a surface's placement and keeps its document, to place it
// again without sending it (SPEC §5.4). Deltas still apply to it.
func Hide(surface string, opts ...ReplyOption) string {
	return command(Control{{"a", "hide"}, {"s", surface}}, nil, NoReply, opts)
}

// Op is a delta operation (SPEC §6.1).
type Op string

// The delta operations.
const (
	OpMorph   Op = "morph"   // morph the target into the payload; without a target, top-level elements by id
	OpInner   Op = "inner"   // morph the target's children into the payload's nodes
	OpReplace Op = "replace" // replace the target with the payload's nodes, without morphing
	OpAppend  Op = "append"  // add the payload's nodes as the target's last children
	OpPrepend Op = "prepend" // add the payload's nodes as the target's first children
	OpBefore  Op = "before"  // insert the payload's nodes before the target
	OpAfter   Op = "after"   // insert the payload's nodes after the target
	OpRemove  Op = "remove"  // remove the target
	OpAttr    Op = "attr"    // set the target's attribute named by the key
	OpUnattr  Op = "unattr"  // remove the target's attribute named by the key
	OpText    Op = "text"    // replace the target's children with one text node
	OpVar     Op = "var"     // set the custom property --key on the target
)

// Delta changes one surface's document (SPEC §6). target is an element id,
// "" for a morph by top-level ids; key names the attribute or the custom
// property. It is never answered, unless an option asks.
func Delta(surface string, op Op, target, key string, payload []byte, opts ...ReplyOption) string {
	ctl := Control{{"a", "delta"}, {"s", surface}, {"op", string(op)}}
	if target != "" {
		ctl = With(ctl, "t", target)
	}
	if key != "" {
		ctl = With(ctl, "k", key)
	}
	return command(ctl, payload, NoReply, opts)
}

// SetText replaces an element's children with one text node: a clock, a
// count. Hosts make it cheap (SPEC §6.1).
func SetText(surface, target, text string, opts ...ReplyOption) string {
	return Delta(surface, OpText, target, "", []byte(text), opts...)
}

// SetVar sets the custom property --name on an element: the cheap way to
// move a bar or a needle every frame, with CSS that reads it.
func SetVar(surface, target, name, value string, opts ...ReplyOption) string {
	return Delta(surface, OpVar, target, name, []byte(value), opts...)
}

// SetAttr sets an attribute on an element.
func SetAttr(surface, target, name, value string, opts ...ReplyOption) string {
	return Delta(surface, OpAttr, target, name, []byte(value), opts...)
}

// RemoveAttr removes an attribute from an element.
func RemoveAttr(surface, target, name string, opts ...ReplyOption) string {
	return Delta(surface, OpUnattr, target, name, nil, opts...)
}

// MorphTo morphs an element into html, or with no target, each top-level
// element of html into the document's element with its id. Morphing keeps
// what the user is doing in what stays: focus, the text being typed, an
// open <details> (SPEC §6.2).
func MorphTo(surface, target, html string, opts ...ReplyOption) string {
	return Delta(surface, OpMorph, target, "", []byte(html), opts...)
}

// Res stores a resource that documents refer to as cid:<id> (SPEC §7.1): a
// stylesheet shared by several surfaces, an image, a font. Sending it again
// replaces it, and redraws every surface that refers to it.
func Res(id, mime string, data []byte, opts ...ReplyOption) string {
	return command(Control{{"a", "res"}, {"id", id}, {"type", mime}}, data, NoReply, opts)
}

// DelRes deletes a resource.
func DelRes(id string, opts ...ReplyOption) string {
	return command(Control{{"a", "del"}, {"id", id}}, nil, NoReply, opts)
}

// Del deletes a surface and its placement (SPEC §5.4).
func Del(surface string, opts ...ReplyOption) string {
	return command(Control{{"a", "del"}, {"s", surface}}, nil, NoReply, opts)
}

// DelAll deletes every surface.
func DelAll(opts ...ReplyOption) string {
	return command(Control{{"a", "del"}}, nil, NoReply, opts)
}

// Detach gives a surface up (SPEC §5.5): it stays on the screen as text
// does, placed, changed by deltas and deleted as before, but sends no more
// events and never has the keyboard; if it has it, the keyboard goes back
// to the terminal with no blur. A program that leaves surfaces on the
// screen when it exits detaches them first, unless it sent them Detached.
// The next Doc sent without Detached makes the surface the program's again.
//
// It is never answered, unless an option asks: a host older than §5.5
// refuses it (EINVAL), and a reply nobody reads would reach the shell as
// typing.
func Detach(surface string, opts ...ReplyOption) string {
	return command(Control{{"a", "detach"}, {"s", surface}}, nil, NoReply, opts)
}

// Focus gives a surface the keyboard (SPEC §10.1), at an element if target
// is not empty; else its focused element keeps focus, or its first
// focusable element takes it.
func Focus(surface, target string, opts ...ReplyOption) string {
	ctl := Control{{"a", "focus"}, {"s", surface}}
	if target != "" {
		ctl = With(ctl, "t", target)
	}
	return command(ctl, nil, NoReply, opts)
}

// Blur takes the keyboard back from a surface. Its focused control commits
// its value first, so a change event may come before the blur event.
func Blur(surface string, opts ...ReplyOption) string {
	return command(Control{{"a", "blur"}, {"s", surface}}, nil, NoReply, opts)
}

// Sync wraps commands in synchronized output (DEC mode 2026, SPEC §6.3):
// the host shows the state after all of them, never one in between.
func Sync(cmds ...string) string {
	return "\x1b[?2026h" + strings.Join(cmds, "") + "\x1b[?2026l"
}
