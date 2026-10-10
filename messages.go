package hotty

import (
	"slices"
	"strconv"
	"strings"

	"github.com/tinylib/msgp/msgp"
)

// Event kinds (SPEC §9). A program ignores kinds it does not know: later
// versions add some.
const (
	EventClick  = "click"  // a button, link, summary or data-on~=click element was activated
	EventChange = "change" // a control's value was committed, or a box toggled
	EventInput  = "input"  // an edit of a control with data-on~=input
	EventSubmit = "submit" // a form was submitted; the detail is its fields
	EventPress  = "press"  // a press anywhere in a placement made with Press
	EventFocus  = "focus"  // the user focused an element; Target names it (SPEC §10.1)
	EventBlur   = "blur"   // the surface gave the keyboard back
	EventResize = "resize" // the surface's pixel size changed, its cells did not
	EventFit    = "fit"    // the rows the document needs changed, on a placement made with Fit
	EventHover  = "hover"  // the element under the pointer changed, or the pointer left, on a placement made with Hover

	// A drag (SPEC §9.1), on an element with data-on~=drag. In the host's
	// events (Drags), EventDrag stands for all three.
	EventDragStart = "dragstart" // the primary button pressed on the element
	EventDrag      = "drag"      // the element under the pointer changed
	EventDragEnd   = "dragend"   // the button released, or the drag cut short
)

// Event is what the user did in a surface (SPEC §9).
type Event struct {
	Surface string
	// Kind is one of the Event kinds, or one this package does not know.
	Kind string
	// Target is the id of the element that reported; empty for focus,
	// blur and resize, for a link without one (its href is in the
	// detail), and for a press on nothing with an id.
	Target string

	// What the detail carries, read by the event's kind (SDK.md §3.9):
	// each is zero, or nil, when the event does not carry it, or its
	// detail does not decode.

	// Value is a control's value (change, input), or the value attribute
	// of what was clicked (click).
	Value string
	// Checked is a checkbox's or a radio button's state, in a change.
	Checked *bool
	// Fields are a submitted form's fields by name.
	Fields map[string]string
	// Link is a clicked link's.
	Link *Link
	// Size is a resize's new size, in CSS pixels.
	Size *Size
	// FitRows is the rows a fit says the document needs now.
	FitRows int
	// Drag is a dragstart's, a drag's or a dragend's.
	Drag *Drag
	// Hover is a hover's (SPEC §9.4).
	Hover *Hover
	// Area is the cells of the element a click or a press reports, a
	// keyboard click's too: what a program places something next to the
	// element by, as a browser places a select's list or a menu by its
	// control.
	Area *Area

	// Detail is the detail as it came, msgpack: nil when the event has
	// none. It is what a relay passes on (EncodeEvent).
	Detail []byte
}

// Link is a clicked link: its href, as the document has it, and its URL
// resolved against the document's base (SPEC §7.3), "" when the href
// resolves to nothing outside the document.
type Link struct{ Href, URL string }

// Hover is where a hover event says the pointer is (SPEC §9.4); the
// element is the event's Target, empty over nothing with an id and when
// Out.
type Hover struct {
	// Out is true when the pointer left the window: onto the cells or
	// another surface, through a part that lets it through, out of the
	// terminal, or pressed with Alt. Col and Row are then 0.
	Out bool
	// Col and Row are the surface's cell under the pointer when the
	// element changed, from 0 at its top left (not its window's).
	Col, Row int
}

// Drag is where a drag's pointer is, and the modifier keys held
// (SPEC §9.1).
type Drag struct {
	// Col and Row are the surface's cell under the pointer, from 0 at its
	// top left (not its window's). They go on past its edges: negative
	// above it and to its left, its size or more below and to its right.
	Col, Row int
	// Keys are the modifier keys held: "shift", "ctrl", "alt" and "meta",
	// in that order.
	Keys []string
	// X and Y are where in the dragged element the pointer is, for an
	// element with data-steps (SPEC §9.1): a step from 0 to its count,
	// along its width and down its height, measured against the element
	// the drag started on wherever the pointer is. HasX and HasY say
	// whether the detail has them: an element with no steps along an axis
	// has none, and 0 is a step like any other.
	X, Y       int
	HasX, HasY bool
}

// EventOf returns the message as an event, if it is one, its detail read
// by its kind.
func EventOf(m Message) (Event, bool) {
	if Get(m.Control, "a") != "ev" {
		return Event{}, false
	}
	e := Event{Surface: Get(m.Control, "s"), Kind: Get(m.Control, "e"), Target: Get(m.Control, "t")}
	if len(m.Payload) > 0 {
		e.Detail = m.Payload
		readDetail(&e)
	}
	return e, true
}

// readDetail fills in what e's detail carries for its kind.
func readDetail(e *Event) {
	b := e.Detail
	switch e.Kind {
	case EventClick:
		var d clickDetail
		if decodeBody(b, &d) {
			e.Value, e.Area = d.Value, d.Area
			if d.Href != nil {
				e.Link = &Link{Href: *d.Href, URL: d.URL}
			}
		}
	case EventPress:
		var d pressDetail
		if decodeBody(b, &d) {
			e.Area = d.Area
		}
	case EventChange:
		var d changeDetail
		if decodeBody(b, &d) {
			e.Value, e.Checked = d.Value, d.Checked
		}
	case EventInput:
		var d inputDetail
		if decodeBody(b, &d) {
			e.Value = d.Value
		}
	case EventSubmit:
		d := submitDetail{}
		if decodeBody(b, &d) {
			e.Fields = d
		}
	case EventResize:
		var d Size
		if decodeBody(b, &d) {
			e.Size = &d
		}
	case EventFit:
		var d fitDetail
		if decodeBody(b, &d) {
			e.FitRows = d.R
		}
	case EventDragStart, EventDrag, EventDragEnd:
		var d dragDetail
		if decodeBody(b, &d) {
			e.Drag = &Drag{Col: d.C, Row: d.R, Keys: d.Keys}
			if d.X != nil {
				e.Drag.X, e.Drag.HasX = *d.X, true
			}
			if d.Y != nil {
				e.Drag.Y, e.Drag.HasY = *d.Y, true
			}
		}
	case EventHover:
		var d hoverDetail
		switch {
		case !decodeBody(b, &d):
		case d.Out:
			e.Hover = &Hover{Out: true}
		case d.C != nil && d.R != nil:
			e.Hover = &Hover{Col: *d.C, Row: *d.R}
		}
	}
}

// EncodeEvent is the event as a host sends it (SPEC §9): what a relay
// writes to a program, the surface named as the program knows it, or a
// host's own. The detail is e.Detail when it has one, as it came, and
// otherwise what e carries for its kind, each field as its type. Never
// compressed (SPEC §3.3).
func EncodeEvent(e Event) string {
	body := e.Detail
	if body == nil {
		body = detailOf(e)
	}
	return EncodePlain(Control{{"a", "ev"}, {"s", e.Surface}, {"e", e.Kind}, {"t", e.Target}}, body)
}

// detailOf is the detail of what e carries for its kind; nil for none.
func detailOf(e Event) []byte {
	var d msgp.Encodable
	switch e.Kind {
	case EventClick:
		c := clickDetail{Value: e.Value, Area: e.Area}
		if e.Link != nil {
			c.Href, c.URL = &e.Link.Href, e.Link.URL
		}
		if c == (clickDetail{}) {
			return nil
		}
		d = &c
	case EventPress:
		if e.Area == nil {
			return nil
		}
		d = &pressDetail{Area: e.Area}
	case EventChange:
		d = &changeDetail{Checked: e.Checked, Value: e.Value}
	case EventInput:
		d = &inputDetail{Value: e.Value}
	case EventSubmit:
		d = submitDetail(e.Fields)
	case EventResize:
		if e.Size == nil {
			return nil
		}
		d = e.Size
	case EventFit:
		d = &fitDetail{R: e.FitRows}
	case EventDragStart, EventDrag, EventDragEnd:
		if e.Drag == nil {
			return nil
		}
		g := e.Drag
		dd := dragDetail{C: g.Col, R: g.Row, Keys: g.Keys}
		if dd.Keys == nil {
			dd.Keys = []string{}
		}
		if g.HasX {
			dd.X = &g.X
		}
		if g.HasY {
			dd.Y = &g.Y
		}
		d = &dd
	case EventHover:
		switch h := e.Hover; {
		case h == nil:
			return nil
		case h.Out:
			d = &hoverDetail{Out: true}
		default:
			d = &hoverDetail{C: &h.Col, R: &h.Row}
		}
	default:
		return nil
	}
	return encodeBody(d)
}

// Error codes, in a reply's Code (SPEC §3.6).
const (
	EINVAL    = "EINVAL"    // an unknown action or op, a bad name, a missing key, a value out of range
	ENOENT    = "ENOENT"    // no surface of that name
	ENOTARGET = "ENOTARGET" // no element with that id; the detail is the id, or ids comma-separated
	EDETACHED = "EDETACHED" // the surface is detached (SPEC §5.5)
	EQUOTA    = "EQUOTA"    // over one of the host's limits (SPEC §13)
	EBUDGET   = "EBUDGET"   // the host gave up on work over its time budget
	EVERSION  = "EVERSION"  // a query lists no version the host speaks; the detail is those it speaks (SPEC §4)
)

// Reply is the host's answer to a command (SPEC §3.6).
type Reply struct {
	OK bool
	// Re is the action answered: "doc", "place", "q", ….
	Re string
	// N is the request number, if the command had one (option N).
	N int
	// Surface is the surface the command named, if any.
	Surface string
	// Cols and Rows are a placement's size, in a reply to place: with
	// Placement.Rows 0, the rows the host chose.
	Cols, Rows int
	// Code and Detail say what went wrong, when not OK: empty when the
	// error's body does not decode.
	Code, Detail string
	// Caps are the capabilities an ok reply to a query carries: nil when
	// it carries none, or they do not decode.
	Caps *Caps
	// Message is the reply as it came.
	Message Message
}

// ReplyOf returns the message as a reply, if it is one, its body read.
func ReplyOf(m Message) (Reply, bool) {
	a := Get(m.Control, "a")
	if a != "ok" && a != "err" {
		return Reply{}, false
	}
	n, _ := strconv.Atoi(Get(m.Control, "n"))
	c, _ := strconv.Atoi(Get(m.Control, "c"))
	rows, _ := strconv.Atoi(Get(m.Control, "r"))
	r := Reply{OK: a == "ok", Re: Get(m.Control, "re"), N: n, Surface: Get(m.Control, "s"), Cols: c, Rows: rows, Message: m}
	switch {
	case !r.OK:
		var e errorBody
		if decodeBody(m.Payload, &e) {
			r.Code, r.Detail = e.Code, e.Detail
		}
	case r.Re == "q" && len(m.Payload) > 0:
		if caps, ok := CapsOf(m.Payload); ok {
			r.Caps = &caps
		}
	}
	return r, true
}

// CapsOf reads capabilities from the body of a reply to a query (SPEC §4),
// and reports whether it decoded: what a relay that stored or was handed
// them reads them with. Raw is a copy of body.
func CapsOf(body []byte) (Caps, bool) {
	var caps Caps
	if !decodeBody(body, &caps) {
		return Caps{}, false
	}
	caps.Raw = slices.Clone(body)
	return caps, true
}

// Speaks reports whether a query lists Version among the versions the
// program speaks (v, SPEC §4). A host that speaks only Version answers it
// with its capabilities, and any other query with EVERSION: ReplyQuery.
func Speaks(query Control) bool {
	for v := range strings.SplitSeq(Get(query, "v"), ",") {
		if v == Version {
			return true
		}
	}
	return false
}

// ReplyQuery is a host's answer to a query (SPEC §4), for a host that
// speaks only Version: caps, as ReplyCaps writes them, when the query lists
// Version, and an EVERSION error naming Version when it does not. caps.V
// is Version.
func ReplyQuery(query Control, caps Caps) string {
	n := Get(query, "n")
	if !Speaks(query) {
		return ReplyErr(n, "", "q", EVERSION, Version)
	}
	return ReplyCaps(n, caps)
}

// ReplyOK is a host's success reply (SPEC §3.6), as a host or a relay
// answering for one writes it to a program: n and surface echo the
// command's, each left out when "", re names the action answered, extra
// keys follow it (a placement's c and r), and body is its msgpack, if any.
// Never compressed (SPEC §3.3).
func ReplyOK(n, surface, re string, extra Control, body []byte) string {
	return EncodePlain(replyControl("ok", n, surface, re, extra), body)
}

// ReplyErr is a host's error reply (SPEC §3.6): the code and detail as its
// body.
func ReplyErr(n, surface, re, code, detail string) string {
	return EncodePlain(replyControl("err", n, surface, re, nil), encodeBody(&errorBody{Code: code, Detail: detail}))
}

// ReplyCaps is a host's answer to a query numbered n (SPEC §4): caps.Raw
// as it is when caps has one, so that a relay passes on the fields it does
// not know, and caps written otherwise.
func ReplyCaps(n string, caps Caps) string {
	body := caps.Raw
	if body == nil {
		body = encodeBody(&caps)
	}
	return ReplyOK(n, "", "q", nil, body)
}

func replyControl(a, n, surface, re string, extra Control) Control {
	ctl := Control{{"a", a}}
	if n != "" {
		ctl = append(ctl, KV{"n", n})
	}
	if surface != "" {
		ctl = append(ctl, KV{"s", surface})
	}
	ctl = append(ctl, KV{"re", re})
	return append(ctl, extra...)
}

// Err is the reply as an error: nil when OK, else an *Error.
func Err(r Reply) error {
	if r.OK {
		return nil
	}
	return &Error{Code: r.Code, Detail: r.Detail, Re: r.Re, Surface: r.Surface}
}

// Error is an error reply (SPEC §3.6).
type Error struct {
	Code    string // EINVAL, ENOENT, …
	Detail  string
	Re      string // the action answered
	Surface string
}

// Error says what was refused and why: "hotty: delta card: ENOTARGET (go)".
func (e *Error) Error() string {
	s := "hotty: " + e.Re
	if e.Surface != "" {
		s += " " + e.Surface
	}
	s += ": " + e.Code
	if e.Detail != "" {
		s += " (" + e.Detail + ")"
	}
	return s
}

// CellCSS is a cell's size in CSS pixels: what a document's layout and an
// SVG's viewBox are measured in. Before a host has said, or if it said
// nothing, it is a usual 9×18.
func CellCSS(c Caps) (w, h float64) {
	if c.Cell == nil || c.Cell.W <= 0 || c.Cell.H <= 0 {
		return 9, 18
	}
	scale := c.Scale
	if scale <= 0 {
		scale = 1
	}
	return float64(c.Cell.W) / scale, float64(c.Cell.H) / scale
}

// Supports reports whether the host supports a delta op. A host that lists
// no ops is taken to support them all.
func Supports(c Caps, op Op) bool {
	if len(c.Ops) == 0 {
		return true
	}
	for _, o := range c.Ops {
		if o == string(op) {
			return true
		}
	}
	return false
}

// Sends reports whether the host sends an event kind. A host that lists no
// kinds is taken to send them all. Drag in its events stands for
// dragstart, drag and dragend (SPEC §4).
func Sends(c Caps, kind string) bool {
	if len(c.Events) == 0 {
		return true
	}
	if kind == EventDragStart || kind == EventDragEnd {
		kind = EventDrag
	}
	for _, k := range c.Events {
		if k == kind {
			return true
		}
	}
	return false
}

// Drags reports whether the host sends drags (SPEC §9.1): drag in its
// events, which stands for dragstart, drag and dragend. Unlike Sends, a
// host that lists no kinds is taken not to, since drags came after the
// first hosts: a program offers another way to do what its drags do.
func Drags(c Caps) bool {
	for _, k := range c.Events {
		if k == EventDrag {
			return true
		}
	}
	return false
}

// Hovers reports whether the host sends hover (SPEC §9.4). Like Drags, a
// host that lists no kinds is taken not to: without it a program never
// hears the pointer leave for a surface, and clears what it lit on the
// next key or press instead.
func Hovers(c Caps) bool {
	for _, k := range c.Events {
		if k == EventHover {
			return true
		}
	}
	return false
}

// Light reports whether the terminal's colour scheme is light.
func Light(c Caps) bool { return c.Scheme == "light" }
