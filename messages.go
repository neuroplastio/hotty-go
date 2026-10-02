package hotty

import (
	"encoding/json"
	"strconv"
)

// Event kinds (SPEC §9). A program ignores kinds it does not know: later
// versions add some.
const (
	EventClick  = "click"  // a button, link, summary or data-on~=click element was activated
	EventChange = "change" // a control's value was committed, or a box toggled
	EventInput  = "input"  // an edit of a control with data-on~=input
	EventSubmit = "submit" // a form was submitted; the detail is its fields
	EventPress  = "press"  // a press anywhere in a placement made with Press
	EventFocus  = "focus"  // the surface took the keyboard
	EventBlur   = "blur"   // the surface gave the keyboard back
	EventResize = "resize" // the surface's pixel size changed, its cells did not
	EventFit    = "fit"    // the rows the document needs changed, on a placement made with Fit
	EventHover  = "hover"  // the element under the pointer changed, or the pointer left, on a placement made with Hover

	// A drag (SPEC §9.1), on an element with data-on~=drag. In the host's
	// events (Caps.Drags), EventDrag stands for all three.
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
	// Detail is the event's JSON detail, if any.
	Detail json.RawMessage
}

// Event returns the message as an event, if it is one.
func (m Message) Event() (Event, bool) {
	if m.Get("a") != "ev" {
		return Event{}, false
	}
	return Event{Surface: m.Get("s"), Kind: m.Get("e"), Target: m.Get("t"), Detail: m.Payload}, true
}

// Value is the "value" of the event's detail: a control's value, or the
// value attribute of what was clicked. "" when it has none.
func (e Event) Value() string {
	var d struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(e.Detail, &d)
	return d.Value
}

// Checked is a checkbox's or a radio button's state in a change event; ok
// is false for any other event.
func (e Event) Checked() (checked, ok bool) {
	var d struct {
		Checked *bool `json:"checked"`
	}
	if json.Unmarshal(e.Detail, &d) != nil || d.Checked == nil {
		return false, false
	}
	return *d.Checked, true
}

// Link is a clicked link's href, as the document has it, and its URL
// resolved against the document's base (SPEC §7.3); url is "" when the
// href resolves to nothing outside the document. ok is false for an event
// that is not a link's click.
func (e Event) Link() (href, url string, ok bool) {
	var d struct {
		Href *string `json:"href"`
		URL  string  `json:"url"`
	}
	if e.Kind != EventClick || json.Unmarshal(e.Detail, &d) != nil || d.Href == nil {
		return "", "", false
	}
	return *d.Href, d.URL, true
}

// Size is a resize event's new size in CSS pixels; ok is false for any
// other event.
func (e Event) Size() (w, h float64, ok bool) {
	var d struct {
		W, H *float64
	}
	if e.Kind != EventResize || json.Unmarshal(e.Detail, &d) != nil || d.W == nil || d.H == nil {
		return 0, 0, false
	}
	return *d.W, *d.H, true
}

// FitRows is the rows a fit event says the document needs now; ok is false
// for any other event.
func (e Event) FitRows() (rows int, ok bool) {
	var d struct{ R *int }
	if e.Kind != EventFit || json.Unmarshal(e.Detail, &d) != nil || d.R == nil || *d.R < 1 {
		return 0, false
	}
	return *d.R, true
}

// Hover is where a hover event says the pointer is (SPEC §9.4).
type Hover struct {
	// Out is true when the pointer left the window: onto the cells or
	// another surface, through a part that lets it through, out of the
	// terminal, or pressed with Alt. Col and Row are then 0.
	Out bool
	// Col and Row are the surface's cell under the pointer when the
	// element changed, from 0 at its top left (not its window's).
	Col, Row int
}

// Hover is a hover event's detail; the element is the event's Target,
// empty over nothing with an id and when Out. ok is false for any other
// event.
func (e Event) Hover() (Hover, bool) {
	if e.Kind != EventHover {
		return Hover{}, false
	}
	var d struct {
		C, R *int
		Out  bool
	}
	if json.Unmarshal(e.Detail, &d) != nil {
		return Hover{}, false
	}
	if d.Out {
		return Hover{Out: true}, true
	}
	if d.C == nil || d.R == nil {
		return Hover{}, false
	}
	return Hover{Col: *d.C, Row: *d.R}, true
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
}

// Has reports whether a modifier key was held: "shift", "ctrl", "alt" or
// "meta".
func (d Drag) Has(key string) bool {
	for _, k := range d.Keys {
		if k == key {
			return true
		}
	}
	return false
}

// Drag is a dragstart, drag or dragend event's detail; ok is false for any
// other event.
func (e Event) Drag() (Drag, bool) {
	switch e.Kind {
	case EventDragStart, EventDrag, EventDragEnd:
	default:
		return Drag{}, false
	}
	var d struct {
		C, R *int
		Keys []string
	}
	if json.Unmarshal(e.Detail, &d) != nil || d.C == nil || d.R == nil {
		return Drag{}, false
	}
	return Drag{Col: *d.C, Row: *d.R, Keys: d.Keys}, true
}

// Fields is a submit event's detail: the form's fields by name. A value
// that is not a string is given as its JSON, a null as nothing.
func (e Event) Fields() map[string]string {
	raw := map[string]any{}
	_ = json.Unmarshal(e.Detail, &raw)
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch v := v.(type) {
		case string:
			out[k] = v
		case nil:
		default:
			b, _ := json.Marshal(v)
			out[k] = string(b)
		}
	}
	return out
}

// Error codes, in a reply's Code (SPEC §3.6).
const (
	EINVAL    = "EINVAL"    // an unknown action or op, a bad name, a missing key, a value out of range
	ENOENT    = "ENOENT"    // no surface of that name
	ENOTARGET = "ENOTARGET" // no element with that id; the detail is the id, or ids comma-separated
	EDETACHED = "EDETACHED" // the surface is detached (SPEC §5.5)
	EQUOTA    = "EQUOTA"    // over one of the host's limits (SPEC §13)
	EBUDGET   = "EBUDGET"   // the host gave up on work over its time budget
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
	// Code and Detail say what went wrong, when not OK.
	Code, Detail string
	// Message is the reply as it came.
	Message Message
}

// Reply returns the message as a reply, if it is one.
func (m Message) Reply() (Reply, bool) {
	a := m.Get("a")
	if a != "ok" && a != "err" {
		return Reply{}, false
	}
	n, _ := strconv.Atoi(m.Get("n"))
	c, _ := strconv.Atoi(m.Get("c"))
	rows, _ := strconv.Atoi(m.Get("r"))
	r := Reply{OK: a == "ok", Re: m.Get("re"), N: n, Surface: m.Get("s"), Cols: c, Rows: rows, Message: m}
	if !r.OK {
		var e struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(m.Payload, &e)
		r.Code, r.Detail = e.Code, e.Detail
	}
	return r, true
}

// Err is the reply as an error: nil when OK, else an *Error.
func (r Reply) Err() error {
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

// Caps is what a host says about itself in its reply to a query (SPEC §4).
type Caps struct {
	// V is the protocol version the host implements: "0.1".
	V string `json:"v"`
	// Ops are the delta ops it supports.
	Ops []string `json:"ops"`
	// Events are the event kinds it sends.
	Events []string `json:"events"`
	// Cell is a cell's size in device pixels.
	Cell struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	} `json:"cell"`
	// Scale is device pixels per CSS pixel.
	Scale float64 `json:"scale"`
	// Scheme is the terminal's colour scheme: "dark" or "light".
	Scheme string `json:"scheme"`
	// Limits are the host's limits, such as "surfaces" (a count) and
	// "resources" (bytes) (SPEC §13).
	Limits map[string]int `json:"limits"`
	// Net is the host's network policy, from directive to the sources it
	// allows (SPEC §7.2): empty when it fetches nothing.
	Net map[string][]string `json:"net"`
	// Host names the implementation, if it says.
	Host string `json:"host"`
}

// Caps returns the capabilities a reply to a query carries.
func (r Reply) Caps() (Caps, bool) {
	if !r.OK || r.Re != "q" {
		return Caps{}, false
	}
	var c Caps
	if err := json.Unmarshal(r.Message.Payload, &c); err != nil {
		return Caps{}, false
	}
	return c, true
}

// CellCSS is a cell's size in CSS pixels: what a document's layout and an
// SVG's viewBox are measured in. Before a host has said, or if it said
// nothing, it is a usual 9×18.
func (c Caps) CellCSS() (w, h float64) {
	scale := c.Scale
	if scale <= 0 {
		scale = 1
	}
	w, h = c.Cell.W/scale, c.Cell.H/scale
	if w <= 0 || h <= 0 {
		return 9, 18
	}
	return w, h
}

// Supports reports whether the host supports a delta op. A host that lists
// no ops is taken to support them all.
func (c Caps) Supports(op Op) bool {
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
// kinds is taken to send them all.
func (c Caps) Sends(kind string) bool {
	if len(c.Events) == 0 {
		return true
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
func (c Caps) Drags() bool {
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
func (c Caps) Hovers() bool {
	for _, k := range c.Events {
		if k == EventHover {
			return true
		}
	}
	return false
}

// Light reports whether the terminal's colour scheme is light.
func (c Caps) Light() bool { return c.Scheme == "light" }
