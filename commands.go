package hotty

import (
	"strconv"
	"strings"
)

// Query asks whether the terminal is a HOTTY host, fenced by Primary Device
// Attributes (SPEC §4): a host replies to the query, numbered n, before the
// DA1 answer every terminal sends. A DA1 answer with no reply before it
// means there is no host.
func Query(n int) string {
	return Encode(Control{{"a", "q"}, {"n", strconv.Itoa(n)}}, nil) + "\x1b[c"
}

// Doc creates a surface, or replaces its document (SPEC §5.1). The surface
// is the program's, even one it had detached: it reports what the user does
// in it, and takes the keyboard on the program's behalf (SPEC §5.5). It is
// answered on error: EQUOTA when the host holds no more surfaces.
func Doc(surface, html string, opts ...Option) string {
	return command(Control{{"a", "doc"}, {"s", surface}}, []byte(html), ReplyOnError, opts)
}

// DocDetached is Doc for a document the program only shows (d=1, SPEC
// §5.5): the surface is created detached, or its document replaced and the
// surface detached, in the one command. It reports nothing, never takes the
// keyboard, and its controls act disabled; hover, selection, <details> and
// hyperlinks still work, and it is placed, patched, hidden and deleted as
// before.
//
// It is what a command prints among its output, which outlives it: whatever
// reads the terminal next, a shell, would read the surface's events as
// typing. A host older than §5.5 ignores d=1.
func DocDetached(surface, html string, opts ...Option) string {
	return command(Control{{"a", "doc"}, {"s", surface}, {"d", "1"}}, []byte(html), ReplyOnError, opts)
}

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
	// KeepCursor leaves the cursor where it was (C=1). Otherwise the host
	// moves it to the start of the line below the placement, as text would.
	KeepCursor bool
}

func (p Placement) control(surface string) Control {
	ctl := Control{{"a", "place"}, {"s", surface}, {"c", strconv.Itoa(p.Cols)}}
	if p.Rows > 0 {
		ctl = ctl.With("r", strconv.Itoa(p.Rows))
	} else {
		ctl = ctl.With("r", "auto")
	}
	if w := p.Window; w != (Window{}) && (p.Rows <= 0 || w != (Window{0, 0, p.Cols, p.Rows})) {
		ctl = append(ctl, KV{"x", strconv.Itoa(w.X)}, KV{"y", strconv.Itoa(w.Y)},
			KV{"w", strconv.Itoa(w.W)}, KV{"h", strconv.Itoa(w.H)})
	}
	if p.Z != 0 {
		ctl = ctl.With("z", strconv.Itoa(p.Z))
	}
	if p.Press {
		ctl = ctl.With("p", "1")
	}
	if p.KeepCursor {
		ctl = ctl.With("C", "1")
	}
	return ctl
}

// Place places a surface at the cursor (SPEC §5.2). Placing a surface that
// is placed moves it. It is answered on error: ENOENT when the host has no
// such surface (it may have dropped it), EINVAL for a size or window out of
// range. A reply to a placement with Rows 0 carries the rows chosen.
func Place(surface string, p Placement, opts ...Option) string {
	return command(p.control(surface), nil, ReplyOnError, opts)
}

// PlaceAt places a surface with its window's top-left corner at cell (x,
// y), counted from 0 at the screen's top-left, and leaves the cursor where
// it was: the way a full-screen program lays surfaces out without
// disturbing its own drawing. It saves the cursor (DECSC), moves it, places
// with C=1, and restores it (DECRC).
func PlaceAt(surface string, x, y int, p Placement, opts ...Option) string {
	p.KeepCursor = true
	return "\x1b7\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(x+1) + "H" +
		command(p.control(surface), nil, ReplyOnError, opts) + "\x1b8"
}

// Hide removes a surface's placement and keeps its document, to place it
// again without sending it (SPEC §5.4). Patches still apply to it.
func Hide(surface string, opts ...Option) string {
	return command(Control{{"a", "hide"}, {"s", surface}}, nil, NoReply, opts)
}

// Op is a patch operation (SPEC §6.1).
type Op string

// The patch operations.
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

// Patch changes one surface's document (SPEC §6). target is an element id,
// "" for a morph by top-level ids; key names the attribute or the custom
// property. It is never answered, unless an option asks.
func Patch(surface string, op Op, target, key string, payload []byte, opts ...Option) string {
	ctl := Control{{"a", "patch"}, {"s", surface}, {"op", string(op)}}
	if target != "" {
		ctl = ctl.With("t", target)
	}
	if key != "" {
		ctl = ctl.With("k", key)
	}
	return command(ctl, payload, NoReply, opts)
}

// SetText replaces an element's children with one text node: a clock, a
// count. Hosts make it cheap (SPEC §6.1).
func SetText(surface, target, text string, opts ...Option) string {
	return Patch(surface, OpText, target, "", []byte(text), opts...)
}

// SetVar sets the custom property --name on an element: the cheap way to
// move a bar or a needle every frame, with CSS that reads it.
func SetVar(surface, target, name, value string, opts ...Option) string {
	return Patch(surface, OpVar, target, name, []byte(value), opts...)
}

// SetAttr sets an attribute on an element.
func SetAttr(surface, target, name, value string, opts ...Option) string {
	return Patch(surface, OpAttr, target, name, []byte(value), opts...)
}

// RemoveAttr removes an attribute from an element.
func RemoveAttr(surface, target, name string, opts ...Option) string {
	return Patch(surface, OpUnattr, target, name, nil, opts...)
}

// MorphTo morphs an element into html, or with no target, each top-level
// element of html into the document's element with its id. Morphing keeps
// what the user is doing in what stays: focus, the text being typed, an
// open <details> (SPEC §6.2).
func MorphTo(surface, target, html string, opts ...Option) string {
	return Patch(surface, OpMorph, target, "", []byte(html), opts...)
}

// Res stores a resource that documents refer to as cid:<id> (SPEC §7.1): a
// stylesheet shared by several surfaces, an image, a font. Sending it again
// replaces it, and redraws every surface that refers to it.
func Res(id, mime string, data []byte, opts ...Option) string {
	return command(Control{{"a", "res"}, {"id", id}, {"type", mime}}, data, NoReply, opts)
}

// DelRes deletes a resource.
func DelRes(id string, opts ...Option) string {
	return command(Control{{"a", "del"}, {"id", id}}, nil, NoReply, opts)
}

// Del deletes a surface and its placement (SPEC §5.4).
func Del(surface string, opts ...Option) string {
	return command(Control{{"a", "del"}, {"s", surface}}, nil, NoReply, opts)
}

// DelAll deletes every surface.
func DelAll(opts ...Option) string {
	return command(Control{{"a", "del"}}, nil, NoReply, opts)
}

// Detach gives a surface up (SPEC §5.5): it stays on the screen as text
// does, placed, patched and deleted as before, but sends no more events and
// never has the keyboard; if it has it, the keyboard goes back to the
// terminal with no blur. A program that leaves surfaces on the screen when
// it exits detaches them first, unless it sent them with DocDetached. The
// next Doc makes the surface the program's again.
//
// It is never answered, unless an option asks: a host older than §5.5
// refuses it (EINVAL), and a reply nobody reads would reach the shell as
// typing.
func Detach(surface string, opts ...Option) string {
	return command(Control{{"a", "detach"}, {"s", surface}}, nil, NoReply, opts)
}

// Focus gives a surface the keyboard (SPEC §10.1), at an element if target
// is not empty; else its focused element keeps focus, or its first
// focusable element takes it.
func Focus(surface, target string, opts ...Option) string {
	ctl := Control{{"a", "focus"}, {"s", surface}}
	if target != "" {
		ctl = ctl.With("t", target)
	}
	return command(ctl, nil, NoReply, opts)
}

// Blur takes the keyboard back from a surface. Its focused control commits
// its value first, so a change event may come before the blur event.
func Blur(surface string, opts ...Option) string {
	return command(Control{{"a", "blur"}, {"s", surface}}, nil, NoReply, opts)
}

// Sync wraps commands in synchronized output (DEC mode 2026, SPEC §6.3):
// the host shows the state after all of them, never one in between.
func Sync(cmds ...string) string {
	return "\x1b[?2026h" + strings.Join(cmds, "") + "\x1b[?2026l"
}
