package hotty

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
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
	// Detail is the event's JSON detail; nil when it has none, or when
	// what came is not JSON.
	Detail json.RawMessage
}

// Event returns the message as an event, if it is one.
func (m Message) Event() (Event, bool) {
	if m.Get("a") != "ev" {
		return Event{}, false
	}
	e := Event{Surface: m.Get("s"), Kind: m.Get("e"), Target: m.Get("t")}
	if len(m.Payload) > 0 && json.Valid(m.Payload) {
		e.Detail = m.Payload
	}
	return e, true
}

// Encode is the event as a host sends it (SPEC §9): what a relay writes
// to a program, the surface named as the program knows it. Never
// compressed (SPEC §3.3).
func (e Event) Encode() string {
	return EncodePlain(Control{{"a", "ev"}, {"s", e.Surface}, {"e", e.Kind}, {"t", e.Target}}, e.Detail)
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
	var d struct{ R *whole }
	if e.Kind != EventFit || json.Unmarshal(e.Detail, &d) != nil || d.R == nil || *d.R < 1 {
		return 0, false
	}
	return int(*d.R), true
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
		C, R *whole
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
	return Hover{Col: int(*d.C), Row: int(*d.R)}, true
}

// whole is a count of cells in a detail: a JSON number with no fractional
// part, however it is written, so 2.0 is 2 (SDK.md §3.9). Anything else
// fails the detail's unmarshalling, and the accessor reads nothing.
type whole int

var errNotWhole = errors.New("hotty: not a whole number")

func (w *whole) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	if f != math.Trunc(f) || f < math.MinInt32 || f > math.MaxInt32 {
		return errNotWhole
	}
	*w = whole(f)
	return nil
}

// Area is the cells an element covers (SPEC §9), counted as a drag's are:
// Col and Row are the first column and row it touches, from 0 at the
// surface's top left (not its window's), and W and H the columns and rows
// it spans. An element partly clipped or scrolled away has its whole area,
// so Col and Row may be negative or past the surface.
type Area struct{ Col, Row, W, H int }

// Area is the cells of the element a click or a press reports, a keyboard
// click's too: what a program places something next to the element by, as
// a browser places a select's list or a menu by its control. ok is false
// for any other event, and when the detail lacks any of the four.
func (e Event) Area() (Area, bool) {
	if e.Kind != EventClick && e.Kind != EventPress {
		return Area{}, false
	}
	var d struct {
		Area *struct{ C, R, W, H *whole }
	}
	if json.Unmarshal(e.Detail, &d) != nil || d.Area == nil {
		return Area{}, false
	}
	a := d.Area
	if a.C == nil || a.R == nil || a.W == nil || a.H == nil {
		return Area{}, false
	}
	return Area{Col: int(*a.C), Row: int(*a.R), W: int(*a.W), H: int(*a.H)}, true
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
		C, R *whole
		Keys []string
		X, Y json.RawMessage
	}
	if json.Unmarshal(e.Detail, &d) != nil || d.C == nil || d.R == nil {
		return Drag{}, false
	}
	drag := Drag{Col: int(*d.C), Row: int(*d.R), Keys: d.Keys}
	// A step that is not a whole number is absent, and the rest stands.
	drag.X, drag.HasX = step(d.X)
	drag.Y, drag.HasY = step(d.Y)
	return drag, true
}

func step(raw json.RawMessage) (int, bool) {
	var w whole
	if raw == nil || json.Unmarshal(raw, &w) != nil {
		return 0, false
	}
	return int(w), true
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

// ReplyOK is a host's success reply (SPEC §3.6), as a host or a relay
// answering for one writes it to a program: n and surface echo the
// command's, each left out when "", re names the action answered, extra
// keys follow it (a placement's c and r), and body is its JSON, if any.
// Never compressed (SPEC §3.3).
func ReplyOK(n, surface, re string, extra Control, body []byte) string {
	return EncodePlain(replyControl("ok", n, surface, re, extra), body)
}

// ReplyErr is a host's error reply (SPEC §3.6): the code and detail as its
// JSON body.
func ReplyErr(n, surface, re, code, detail string) string {
	body, _ := json.Marshal(struct {
		Code   string `json:"code"`
		Detail string `json:"detail,omitempty"`
	}{code, detail})
	return EncodePlain(replyControl("err", n, surface, re, nil), body)
}

// ReplyCaps is a host's answer to a query numbered n (SPEC §4), with the
// capabilities' JSON as it is: a relay passes on Caps.Raw, so that fields
// it does not know reach the program.
func ReplyCaps(n string, caps json.RawMessage) string {
	return ReplyOK(n, "", "q", nil, caps)
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
// Each field is read on its own (SDK.md §2.7): one of an unexpected type
// is ignored, as one the program does not know is, and so is an element of
// a list or a map.
type Caps struct {
	// V is the protocol version the host implements: "0.1".
	V string `json:"v"`
	// Ops are the delta ops it supports.
	Ops []string `json:"ops,omitempty"`
	// Events are the event kinds it sends.
	Events []string `json:"events,omitempty"`
	// Cell is a cell's size in device pixels.
	Cell struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	} `json:"cell,omitzero"`
	// Scale is device pixels per CSS pixel.
	Scale float64 `json:"scale,omitempty"`
	// Scheme is the terminal's colour scheme: "dark" or "light".
	Scheme string `json:"scheme,omitempty"`
	// Limits are the host's limits, such as "surfaces" (a count) and
	// "resources" (bytes) (SPEC §13).
	Limits map[string]int `json:"limits,omitempty"`
	// Net is the host's network policy, from directive to the sources it
	// allows (SPEC §7.2): empty when it fetches nothing.
	Net map[string][]string `json:"net,omitempty"`
	// Scroll is true when a document can ask to scroll (Scroll, SPEC §5.1);
	// a host that does not says nothing, and clips.
	Scroll bool `json:"scroll,omitempty"`
	// Passthrough is true when the pointer passes through the parts of a
	// surface that take no pointer (SPEC §9.3); a host that does not says
	// nothing, and every window takes the pointer wherever it is.
	Passthrough bool `json:"passthrough,omitempty"`
	// Steps is true when a drag of an element with data-steps says where
	// in the element the pointer is (Drag.X and Drag.Y, SPEC §9.1); a host
	// that does not says nothing, and its drags carry cells only.
	Steps bool `json:"steps,omitempty"`
	// Host names the implementation, if it says.
	Host string `json:"host,omitempty"`
	// Version is the implementation's version, with Host: dot-separated
	// numbers, compared one by one ("0.0.10" is after "0.0.9").
	Version string `json:"version,omitempty"`

	// Raw is the JSON the capabilities were read from, fields this
	// package does not know included: what a relay announces to the
	// programs behind it (SPEC §2.7). Marshalling Caps writes the fields
	// above, not Raw.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON reads capabilities leniently: a field of an unexpected
// type is left zero, and the rest are read. JSON that is not an object is
// an error, and null changes nothing.
func (c *Caps) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return err
	}
	*c = Caps{Raw: slices.Clone(json.RawMessage(data))}
	field := func(k string, v any) { _ = json.Unmarshal(raw[k], v) }
	field("v", &c.V)
	c.Ops = stringList(raw["ops"])
	c.Events = stringList(raw["events"])
	var cell struct{ W, H *float64 }
	if field("cell", &cell); cell.W != nil && cell.H != nil {
		c.Cell.W, c.Cell.H = *cell.W, *cell.H
	}
	field("scale", &c.Scale)
	field("scheme", &c.Scheme)
	var limits map[string]json.RawMessage
	field("limits", &limits)
	for k, v := range limits {
		var n int
		if json.Unmarshal(v, &n) == nil {
			if c.Limits == nil {
				c.Limits = map[string]int{}
			}
			c.Limits[k] = n
		}
	}
	var net map[string]json.RawMessage
	field("net", &net)
	for k, v := range net {
		var list []json.RawMessage
		if json.Unmarshal(v, &list) == nil {
			if c.Net == nil {
				c.Net = map[string][]string{}
			}
			c.Net[k] = stringList(v)
		}
	}
	field("scroll", &c.Scroll)
	field("passthrough", &c.Passthrough)
	field("steps", &c.Steps)
	field("host", &c.Host)
	field("version", &c.Version)
	return nil
}

// stringList reads a JSON list's strings, leaving out what is not one.
func stringList(data json.RawMessage) []string {
	var list []json.RawMessage
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	var out []string
	for _, v := range list {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out = append(out, s)
		}
	}
	return out
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
// kinds is taken to send them all. Drag in its events stands for
// dragstart, drag and dragend (SPEC §4).
func (c Caps) Sends(kind string) bool {
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
