package hotty

import (
	"strconv"
	"strings"

	"github.com/tinylib/msgp/msgp"
)

// Query asks whether the terminal is a hotty host that speaks Version,
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
// using hotty after it fell back to cells. A host answers at once, as it
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
	ctl := Control{{"a", "doc"}, {"s", surface}}
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
// typing.
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
// for the host to judge.
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
	// Press, Fit and Hover belong to the surface, not to the placement
	// (SPEC §5.2): On turns the event on (1), Off turns it off (0), and
	// the zero value, absent, sends nothing and leaves it as it was.
	//
	// Press asks for a press event wherever the user presses in the
	// placement, on text and empty space too (p).
	Press Flag
	// Fit asks for a fit event whenever the rows the document needs at
	// this width change from the rows last heard, starting from this
	// placement's (f): an image or font arrived, a resource was
	// replaced, a delta landed. The placement keeps its size; placing the
	// surface again is the program's to do.
	Fit Flag
	// Hover asks for a hover event each time the element with an id under
	// the pointer changes, and when the pointer leaves the window (v):
	// for a hint of the program's own, or to clear what it lit outside
	// the surface when the pointer goes onto it (SPEC §9.4).
	Hover Flag
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
	for _, f := range [...]struct {
		key  string
		flag Flag
	}{{"p", p.Press}, {"f", p.Fit}, {"v", p.Hover}} {
		switch f.flag {
		case On:
			ctl = With(ctl, f.key, "1")
		case Off:
			ctl = With(ctl, f.key, "0")
		}
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
	p.Press, p.Fit, p.Hover = flagOf(m.Control, "p"), flagOf(m.Control, "f"), flagOf(m.Control, "v")
	p.KeepCursor = Get(m.Control, "C") == "1"
	return p, nil
}

// flagOf reads a placement's p, f or v: 1 is On, 0 is Off, and anything
// else, absent included, is absent (SPEC §5.2).
func flagOf(c Control, k string) Flag {
	switch Get(c, k) {
	case "1":
		return On
	case "0":
		return Off
	}
	return 0
}

// CursorBelow is the cursor's move a placement without KeepCursor makes
// (SPEC §5.2): rows index operations (IND) and a carriage return, to the
// start of the line below the placement. A relay that keeps a placement
// from the screen it emulates feeds it this instead.
func CursorBelow(rows int) string {
	return strings.Repeat("\x1bD", max(rows, 0)) + "\r"
}

// DeltaOf reads a delta command's ops (SPEC §6.1), as a host or a relay
// does, checking the whole body first: it is an *Error with Code EINVAL,
// and with HasOp the index of the first op at fault, when the body does not
// decode (SPEC §3.3) or has no ops, an op is not a map, holds op, t, k or v
// as anything but a str, or names an op there is none of, an op lacks the
// t or k it needs (an empty one is none), or a reveal has a value other
// than start or end. A value an op does not take is ignored, and so is a
// key this package does not know.
func DeltaOf(m Message) ([]Op, error) {
	fail := func(detail string, at int) ([]Op, error) {
		e := &Error{Code: EINVAL, Detail: detail, Re: Get(m.Control, "a"), Surface: Get(m.Control, "s")}
		if at >= 0 {
			e.Op, e.HasOp = at, true
		}
		return nil, e
	}
	if Get(m.Control, "a") != "delta" {
		return fail("not a delta command", -1)
	}
	var d deltaBody
	if !decodeBody(m.Payload, &d) {
		if at := opAtFault(m.Payload); at >= 0 {
			return fail("op is not a map of strs", at)
		}
		return fail("body does not decode", -1)
	}
	if len(d.Ops) == 0 {
		return fail("no ops", -1)
	}
	for i, op := range d.Ops {
		var v string
		if op.Value != nil {
			v = *op.Value
		}
		switch {
		case !validOps[op.Op]:
			return fail("unknown op "+op.Op, i)
		case op.Target == "" && op.Op != opMorph:
			return fail("missing t", i)
		case op.Key == "" && (op.Op == opAttr || op.Op == opUnattr || op.Op == opVar):
			return fail("missing k", i)
		case op.Op == opReveal && op.Value != nil && v != RevealStart && v != RevealEnd:
			return fail("reveal "+v, i)
		}
	}
	return d.Ops, nil
}

// MenuDepth is how deep a menu's items nest, the top level's being one deep
// (SPEC §10.7). A menu nested deeper is EINVAL.
const MenuDepth = 8

// MenuOf reads an a=menu command's menu (SPEC §10.7), as a host or a relay
// does: nil, and no error, for a command with no body, which leaves the
// program no menu. A body that is no menu, holds a field of another type,
// or nests its items deeper than MenuDepth is an *Error with Code EINVAL.
// A command with show is not a menu: MenuShowOf reads it.
func MenuOf(m Message) (*MenuTree, error) {
	if Get(m.Control, "a") != "menu" {
		return nil, &Error{Code: EINVAL, Detail: "not a menu command", Re: Get(m.Control, "a"), Surface: Get(m.Control, "s")}
	}
	if len(m.Payload) == 0 {
		return nil, nil
	}
	var t MenuTree
	if !decodeBody(m.Payload, &t) {
		return nil, &Error{Code: EINVAL, Detail: "body does not decode", Re: "menu"}
	}
	if menuDeep(t.Items) > MenuDepth {
		return nil, &Error{Code: EINVAL, Detail: "items nest deeper than eight", Re: "menu"}
	}
	return &t, nil
}

// menuDeep is how deep items nest, the first level being one.
func menuDeep(items []MenuItem) int {
	d := 0
	for _, it := range items {
		d = max(d, menuDeep(it.Items))
	}
	if len(items) == 0 {
		return 0
	}
	return d + 1
}

// MenuShowOf reads an a=menu command's show body (SPEC §10.7), as a host or
// a relay does: the keys typed, as the program names them, whose level to
// show. No body, or none in it, is the top level: nil, and no error. A
// command with show=0 has none to read. A body that is no map, or whose seq
// is not an array of str, is an *Error with Code EINVAL.
func MenuShowOf(m Message) ([]string, error) {
	if Get(m.Control, "a") != "menu" {
		return nil, &Error{Code: EINVAL, Detail: "not a menu command", Re: Get(m.Control, "a"), Surface: Get(m.Control, "s")}
	}
	if len(m.Payload) == 0 {
		return nil, nil
	}
	var k MenuKeys
	if !decodeBody(m.Payload, &k) {
		return nil, &Error{Code: EINVAL, Detail: "seq is not an array of str", Re: "menu"}
	}
	return k.Seq, nil
}

// TouchMax is as many areas as an a=touch may name (SPEC §9.6). A body with
// more is EINVAL.
const TouchMax = 256

// TouchOf reads an a=touch command's areas (SPEC §9.6), as a host or a
// relay does: nil, and no error, for a command with no body or no areas,
// which leaves none. A body that is no map of areas, an area without a
// field or with one of another type, C or R under 0, W or H under 1, a
// Drag other than "x", "y" and "xy", or more than TouchMax areas, is an
// *Error with Code EINVAL.
func TouchOf(m Message) ([]TouchArea, error) {
	if Get(m.Control, "a") != "touch" {
		return nil, &Error{Code: EINVAL, Detail: "not a touch command", Re: Get(m.Control, "a"), Surface: Get(m.Control, "s")}
	}
	if len(m.Payload) == 0 {
		return nil, nil
	}
	fail := func(detail string) ([]TouchArea, error) {
		return nil, &Error{Code: EINVAL, Detail: detail, Re: "touch"}
	}
	var t touchBody
	if !decodeBody(m.Payload, &t) {
		return fail("body is no map of areas")
	}
	if !touchFields(m.Payload) {
		return fail("no areas, or an area without a field")
	}
	if len(t.Areas) > TouchMax {
		return fail("more than 256 areas")
	}
	for _, a := range t.Areas {
		switch {
		case a.C < 0 || a.R < 0:
			return fail("c or r under 0")
		case a.W < 1 || a.H < 1:
			return fail("w or h under 1")
		case a.Drag != "x" && a.Drag != "y" && a.Drag != "xy":
			return fail("drag " + a.Drag)
		}
	}
	if len(t.Areas) == 0 {
		return nil, nil
	}
	return t.Areas, nil
}

// FieldOf reads an a=field command's change (SPEC §6.4), as a host or a
// relay does, checking what it can without the control: it is an *Error
// with Code EINVAL when t is missing, the body does not decode (SPEC §3.3)
// or is not such a map, rev is missing, no field but rev is there, Value
// comes with Text, From or To, From or To comes without the other or
// without Text, From is past To, or a position is under 0. The host checks
// the rest against the control: whether each field is for it, and whether
// each position falls inside its value, between code points.
func FieldOf(m Message) (FieldChange, error) {
	fail := func(detail string) (FieldChange, error) {
		return FieldChange{}, &Error{Code: EINVAL, Detail: detail, Re: Get(m.Control, "a"), Surface: Get(m.Control, "s")}
	}
	if Get(m.Control, "a") != "field" {
		return fail("not a field command")
	}
	if Get(m.Control, "t") == "" {
		return fail("missing t")
	}
	var b fieldBody
	switch {
	case !decodeBody(m.Payload, &b):
		return fail("body does not decode")
	case b.Rev == nil:
		return fail("missing rev")
	case b.Sel != nil && len(b.Sel) != 2:
		return fail("sel is not two ints")
	}
	c := FieldChange{Rev: *b.Rev, Value: b.Value, From: b.From, To: b.To, Text: b.Text, Sel: pair(b.Sel), Checked: b.Checked}
	text := c.Text != nil || c.From != nil || c.To != nil
	switch {
	case c.Value == nil && !text && c.Sel == nil && c.Checked == nil:
		return fail("no field but rev")
	case c.Value != nil && text:
		return fail("value with text, from or to")
	case (c.From == nil) != (c.To == nil) || c.From != nil && c.Text == nil:
		return fail("from or to without the other, or without text")
	case c.From != nil && *c.From > *c.To:
		return fail("from past to")
	case c.From != nil && *c.From < 0, c.Sel != nil && (c.Sel[0] < 0 || c.Sel[1] < 0):
		return fail("a position under 0")
	}
	return c, nil
}

// pair is two ints as an anchor and a caret; nil unless there are two.
func pair(ints []int) *[2]int {
	if len(ints) != 2 {
		return nil
	}
	return &[2]int{ints[0], ints[1]}
}

// touchFields reports whether a touch body that decodes gives its areas,
// and each area all five of its fields: the codec reads a field it is not
// given as its zero value. A body's maps give each key once (wellFormed).
func touchFields(body []byte) bool {
	n, b, err := msgp.ReadMapHeaderBytes(body)
	for ; n > 0 && err == nil; n-- {
		var key string
		if key, b, err = msgp.ReadStringBytes(b); err != nil {
			return false
		}
		if key != "areas" {
			b, err = msgp.Skip(b)
			continue
		}
		var areas, fields uint32
		if areas, b, err = msgp.ReadArrayHeaderBytes(b); err != nil {
			return false
		}
		for ; areas > 0; areas-- {
			if fields, b, err = msgp.ReadMapHeaderBytes(b); err != nil {
				return false
			}
			have := 0
			for ; fields > 0; fields-- {
				if key, b, err = msgp.ReadStringBytes(b); err != nil {
					return false
				}
				switch key {
				case "c", "r", "w", "h", "drag":
					have++
				}
				if b, err = msgp.Skip(b); err != nil {
					return false
				}
			}
			if have != 5 {
				return false
			}
		}
		return true
	}
	return false
}

// validOps are the delta ops there are.
var validOps = map[string]bool{
	opMorph: true, opInner: true, opReplace: true, opAppend: true, opPrepend: true, opBefore: true,
	opAfter: true, opRemove: true, opAttr: true, opUnattr: true, opText: true, opVar: true, opReveal: true,
}

// opAtFault is the index of the first element of a body's ops that is not
// a map, or holds a key of an op as another type than its own, or -1: a
// body that is not one map holding an array of ops, or whose ops all
// decode, has none.
func opAtFault(body []byte) int {
	if !wellFormed(body) {
		return -1
	}
	n, b, err := msgp.ReadMapHeaderBytes(body)
	for ; n > 0 && err == nil; n-- {
		var key string
		if key, b, err = msgp.ReadStringBytes(b); err != nil {
			break
		}
		if key != "ops" {
			b, err = msgp.Skip(b)
			continue
		}
		var ops uint32
		if ops, b, err = msgp.ReadArrayHeaderBytes(b); err != nil {
			break
		}
		for i := 0; i < int(ops); i++ {
			if msgp.NextType(b) != msgp.MapType {
				return i
			}
			rest, err := msgp.Skip(b)
			if err != nil {
				break
			}
			var op Op
			if !decodeBody(b[:len(b)-len(rest)], &op) {
				return i
			}
			b = rest
		}
		break
	}
	return -1
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

// The delta ops (SPEC §6.1).
const (
	opMorph   = "morph"   // morph the target into the value; without a target, top-level elements by id
	opInner   = "inner"   // morph the target's children into the value's nodes
	opReplace = "replace" // replace the target with the value's nodes, without morphing
	opAppend  = "append"  // add the value's nodes as the target's last children
	opPrepend = "prepend" // add the value's nodes as the target's first children
	opBefore  = "before"  // insert the value's nodes before the target
	opAfter   = "after"   // insert the value's nodes after the target
	opRemove  = "remove"  // remove the target
	opAttr    = "attr"    // set the target's attribute named by the key
	opUnattr  = "unattr"  // remove the target's attribute named by the key
	opText    = "text"    // replace the target's children with one text node
	opVar     = "var"     // set the custom property --key on the target
	opReveal  = "reveal"  // scroll the target into view; the value says where
)

// valued is an op with a value, present even when empty.
func valued(op, target, key, value string) Op {
	return Op{Op: op, Target: target, Key: key, Value: &value}
}

// OpMorph morphs an element into html, or with no target, each top-level
// element of html into the document's element with its id (SPEC §6.2).
func OpMorph(target, html string) Op { return valued(opMorph, target, "", html) }

// OpInner morphs the children of an element into the nodes of html.
func OpInner(target, html string) Op { return valued(opInner, target, "", html) }

// OpReplace replaces an element with the nodes of html, without morphing.
func OpReplace(target, html string) Op { return valued(opReplace, target, "", html) }

// OpAppend adds the nodes of html as an element's last children.
func OpAppend(target, html string) Op { return valued(opAppend, target, "", html) }

// OpPrepend adds the nodes of html as an element's first children.
func OpPrepend(target, html string) Op { return valued(opPrepend, target, "", html) }

// OpBefore inserts the nodes of html before an element.
func OpBefore(target, html string) Op { return valued(opBefore, target, "", html) }

// OpAfter inserts the nodes of html after an element.
func OpAfter(target, html string) Op { return valued(opAfter, target, "", html) }

// OpRemove removes an element. It has no value.
func OpRemove(target string) Op { return Op{Op: opRemove, Target: target} }

// OpAttr sets an attribute on an element.
func OpAttr(target, name, value string) Op { return valued(opAttr, target, name, value) }

// OpUnattr removes an attribute from an element. It has no value.
func OpUnattr(target, name string) Op { return Op{Op: opUnattr, Target: target, Key: name} }

// OpText replaces an element's children with one text node: a clock, a
// count. Hosts make it cheap (SPEC §6.1).
func OpText(target, text string) Op { return valued(opText, target, "", text) }

// OpVar sets the custom property --name on an element: the cheap way to
// move a bar or a needle every frame, with CSS that reads it.
func OpVar(target, name, value string) Op { return valued(opVar, target, name, value) }

// OpReveal scrolls an element into view (SPEC §6.1): where is RevealStart,
// RevealEnd or RevealNearest, which has the op no value.
func OpReveal(target, where string) Op {
	if where == RevealNearest {
		return Op{Op: opReveal, Target: target}
	}
	return valued(opReveal, target, "", where)
}

// Delta changes one surface's document (SPEC §6.1): the ops, in the order
// they apply, which the host shows as one. A program that changes a
// surface in one frame sends one Delta (SDK.md §2.9). A Delta of no ops is
// sent, and the host refuses it (EINVAL). It is never answered, unless an
// option asks; a reply to an op that failed names it, by its index.
func Delta(surface string, ops []Op, opts ...ReplyOption) string {
	if ops == nil {
		ops = []Op{}
	}
	return command(Control{{"a", "delta"}, {"s", surface}}, encodeBody(&deltaBody{Ops: ops}), NoReply, opts)
}

// SetText replaces an element's children with one text node: a clock, a
// count.
func SetText(surface, target, text string, opts ...ReplyOption) string {
	return Delta(surface, []Op{OpText(target, text)}, opts...)
}

// SetVar sets the custom property --name on an element.
func SetVar(surface, target, name, value string, opts ...ReplyOption) string {
	return Delta(surface, []Op{OpVar(target, name, value)}, opts...)
}

// SetAttr sets an attribute on an element.
func SetAttr(surface, target, name, value string, opts ...ReplyOption) string {
	return Delta(surface, []Op{OpAttr(target, name, value)}, opts...)
}

// RemoveAttr removes an attribute from an element.
func RemoveAttr(surface, target, name string, opts ...ReplyOption) string {
	return Delta(surface, []Op{OpUnattr(target, name)}, opts...)
}

// MorphTo morphs an element into html, or with no target, each top-level
// element of html into the document's element with its id. Morphing keeps
// what the user is doing in what stays: focus, the text being typed, an
// open <details> (SPEC §6.2).
func MorphTo(surface, target, html string, opts ...ReplyOption) string {
	return Delta(surface, []Op{OpMorph(target, html)}, opts...)
}

// Where Reveal brings an element (SPEC §6.1).
const (
	RevealNearest = ""      // as little as brings it into view
	RevealStart   = "start" // its top at the top of each box that holds it
	RevealEnd     = "end"   // its bottom at the bottom
)

// Reveal scrolls an element into view (SPEC §6.1): each box that holds
// it, the innermost first, along the axes the document scrolls (Scroll).
// where is RevealStart, RevealEnd or RevealNearest. It moves no focus, so
// a list beside the document keeps the keyboard: a table of contents that
// jumps to a section. Elements that report whether they are in view
// report it after (EventView).
func Reveal(surface, target, where string, opts ...ReplyOption) string {
	return Delta(surface, []Op{OpReveal(target, where)}, opts...)
}

// FieldChange is what an a=field changes in a control (SPEC §6.4), made
// against the control's revision, which the program heard in its last
// event (Event.Rev). Each field but Rev is nil when absent, so that an
// empty Value or Text and a position 0 are sent. Positions count the bytes
// of the value's UTF-8.
type FieldChange struct {
	// Rev is the control's revision the change is made against.
	Rev int
	// Value is the new value, of any control but a checkbox or a radio
	// button.
	Value *string
	// From and To are the range of a text field's value that Text
	// replaces.
	From, To *int
	// Text replaces a text field's selection, or with From and To, that
	// range, as a paste does.
	Text *string
	// Sel is a text field's selection after the change: its anchor, then
	// its caret. Without it the caret goes after the text put in, or to the
	// end of a Value.
	Sel *[2]int
	// Checked checks a checkbox or a radio button, or unchecks it.
	Checked *bool
}

// SetField changes a control as the user does (SPEC §6.4): its value, its
// selection, whether it is checked, if the user has not changed it since
// the revision the change is made against (FieldChange.Rev, which the
// control's events carry). A field that completes what the user types
// sends it, and keeps its id, its caret and its history. The host changes
// nothing, and answers ESTALE with the control's revision, when the user
// changed it first: the program then decides again from the event it
// hears next, and does not resend. It answers errors, unless an option says
// otherwise. It checks nothing in the change: the host does.
//
// It writes one msgpack map: rev, then the fields given, in the order
// value, from, to, text, sel, checked.
func SetField(surface, target string, change FieldChange, opts ...ReplyOption) string {
	b := fieldBody{Rev: &change.Rev, Value: change.Value, From: change.From, To: change.To, Text: change.Text, Checked: change.Checked}
	if change.Sel != nil {
		b.Sel = change.Sel[:]
	}
	return command(Control{{"a", "field"}, {"s", surface}, {"t", target}}, encodeBody(&b), ReplyOnError, opts)
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
// It is never answered, unless an option asks: a reply nobody reads would
// reach the shell as typing.
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

// Edit takes the edit commands named (SPEC §10.6): "select-all", "copy",
// "cut", "paste", "undo", "redo". Their keys, the host's edit shortcuts
// (Caps.Edit), then reach the program as EventCommand in place of their
// bytes, and the terminal's menus send them with no key. Each Edit replaces
// the last; none takes nothing. It holds for the screen it is sent on: a
// full-screen program sends it after switching to the alternate screen,
// and leaving it drops it.
func Edit(names []string, opts ...ReplyOption) string {
	return command(Control{{"a", "edit"}, {"take", strings.Join(names, ",")}}, nil, NoReply, opts)
}

// Menu tells the host what the program can do now (SPEC §10.7): its menu, a
// tree of actions with the keys that reach them and the names a pick sends.
// It lasts until the program sends another, and with nil the program has
// none. A host shows and picks from it when its capabilities have the
// feature menu (HasFeature(caps, FeatureMenu)); on one that does not, the
// program shows its menu itself. The tree is kept within MenuDepth levels.
func Menu(tree *MenuTree, opts ...ReplyOption) string {
	var body []byte
	if tree != nil {
		t := MenuTree{Label: tree.Label, Items: menuCopy(tree.Items)}
		body = encodeBody(&t)
	}
	return command(Control{{"a", "menu"}}, body, NoReply, opts)
}

// menuCopy is items for the wire: the top level's array is always there,
// and a group's only when it has items.
func menuCopy(items []MenuItem) []MenuItem {
	out := make([]MenuItem, len(items))
	for i, it := range items {
		out[i] = it
		out[i].Items = nil
		if len(it.Items) > 0 {
			out[i].Items = menuCopy(it.Items)
		}
	}
	return out
}

// MenuShow shows the level of the program's menu that the keys typed reach
// (SPEC §10.7): seq names them as SPEC §10.4 does, "Space" and "f" for a
// leader and a group. With none it shows the top level. A level shows until
// the program shows another, or MenuHide.
func MenuShow(seq []string, opts ...ReplyOption) string {
	var body []byte
	if len(seq) > 0 {
		body = encodeBody(&MenuKeys{Seq: seq})
	}
	return command(Control{{"a", "menu"}, {"show", "1"}}, body, NoReply, opts)
}

// MenuHide hides the level shown (SPEC §10.7). The menu stays.
func MenuHide(opts ...ReplyOption) string {
	return command(Control{{"a", "menu"}, {"show", "0"}}, nil, NoReply, opts)
}

// Touch names the cells where a finger drags as the mouse does (SPEC §9.6),
// so that what the program drags with the mouse in its cells, a slider, a
// finger drags too. While the program has the terminal report the mouse's
// buttons, a touch that begins in an area and first moves the way it drags
// is the primary button: pressed where it began, moved with the finger,
// released where it lifts. A tap there presses and releases. Elsewhere, and
// the other way, a finger scrolls as it did.
//
// The areas last until the program sends others, and with none it leaves
// none. They are cells of the screen it is sent on: the main and the
// alternate screen keep their own, leaving the alternate screen drops its,
// and a full reset drops both. So the program sends them again when what it
// drew moves, the terminal's size changes, or it switches screens, and sends
// Touch(nil) when it ends. It keeps to TouchMax areas, W and H at least 1. A
// host that takes no touch accepts it, and nothing comes of it.
func Touch(areas []TouchArea, opts ...ReplyOption) string {
	var body []byte
	if len(areas) > 0 {
		body = encodeBody(&touchBody{Areas: areas})
	}
	return command(Control{{"a", "touch"}}, body, NoReply, opts)
}

// MenuLevelOf is the level of tree that the keys seq reach (SPEC §10.7): its
// items, a group's, for a host to show those that have a key. From the top
// level, each key of seq enters the first group (an item with Items) at the
// level whose Key is that key, compared as ReadKey compares: with Mod read
// as p reads it. With no keys it is the top level. ok is false when the keys
// reach no group, or tree is nil.
func MenuLevelOf(tree *MenuTree, p Platform, seq []string) (items []MenuItem, ok bool) {
	if tree == nil {
		return nil, false
	}
	items = tree.Items
	for _, key := range seq {
		typed, ok := p.ReadKey(key)
		if !ok {
			return nil, false
		}
		next := -1
		for i, it := range items {
			if len(it.Items) == 0 {
				continue
			}
			if c, ok := p.ReadKey(it.Key); ok && c == typed {
				next = i
				break
			}
		}
		if next < 0 {
			return nil, false
		}
		items = items[next].Items
	}
	return items, true
}

// MenuFind is the first item of tree named name, depth first (SPEC §10.7),
// or nil: a pick of name is for it. An item with no name, as name "" asks
// for, is never found.
func MenuFind(tree *MenuTree, name string) *MenuItem {
	if tree == nil || name == "" {
		return nil
	}
	return menuFind(tree.Items, name)
}

func menuFind(items []MenuItem, name string) *MenuItem {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
		if it := menuFind(items[i].Items, name); it != nil {
			return it
		}
	}
	return nil
}
