package hotty

// The types of the bodies a host sends (SPEC §3.3), each field as its type,
// read and written by the codec msgp generates (wire_gen.go). They have no
// other methods: what is done with them is a function beside them (gov P-3).

//go:generate go run github.com/tinylib/msgp@v1.6.5 -file wire.go -o wire_gen.go -marshal=false -tests=false -unexported
//msgp:newtime
//msgp:limit arrays:65536 maps:65536

// Caps is what a host says about itself in its reply to a query (SPEC §4).
type Caps struct {
	// V is the protocol version the host implements: "0.2".
	V string `msg:"v"`
	// Ops are the delta ops it supports.
	Ops []string `msg:"ops,omitempty"`
	// Events are the event kinds it sends.
	Events []string `msg:"events,omitempty"`
	// Cell is a cell's size in device pixels; nil when the host did not
	// say.
	Cell *Cell `msg:"cell,omitempty"`
	// Scale is device pixels per CSS pixel.
	Scale float64 `msg:"scale,omitempty"`
	// Scheme is the terminal's colour scheme: "dark" or "light".
	Scheme string `msg:"scheme,omitempty"`
	// Limits are the host's limits, such as "surfaces" (a count) and
	// "resources" (bytes) (SPEC §13).
	Limits map[string]int `msg:"limits,omitempty"`
	// Net is the host's network policy, from directive to the sources it
	// allows (SPEC §7.2): empty when it fetches nothing.
	Net map[string][]string `msg:"net,omitempty"`
	// Scroll is true when a document can ask to scroll (Scroll, SPEC §5.1);
	// a host that does not says nothing, and clips.
	Scroll bool `msg:"scroll,omitempty"`
	// Passthrough is true when the pointer passes through the parts of a
	// surface that take no pointer (SPEC §9.3); a host that does not says
	// nothing, and every window takes the pointer wherever it is.
	Passthrough bool `msg:"passthrough,omitempty"`
	// Steps is true when a drag of an element with data-steps says where
	// in the element the pointer is (Drag.X and Drag.Y, SPEC §9.1); a host
	// that does not says nothing, and its drags carry cells only.
	Steps bool `msg:"steps,omitempty"`
	// Drop is true when an element with drop in its data-on can be a
	// drag's t without being dragged, and a drag says where in it the
	// pointer is when it has data-steps (Drag.TX and Drag.TY, SPEC §9.1); a host
	// that does not says nothing, and only an element that opts in to
	// drags is ever t.
	Drop bool `msg:"drop,omitempty"`
	// Count is true when a repeated click says its place in its run of
	// clicks (Event.Count, SPEC §9); a host that does not says nothing, and
	// every click reads as a single one.
	Count bool `msg:"count,omitempty"`
	// Mod is the modifier Mod stands for in bindings, "Control" or "Meta"
	// (SPEC §10.4); "" when the host did not say, which is Control.
	Mod string `msg:"mod,omitempty"`
	// Edit is the host's edit shortcuts, from each action to its keys
	// (SPEC §10.2); a host that reports them takes edit commands (Edit,
	// SPEC §10.6).
	Edit map[string][]string `msg:"edit,omitempty"`
	// Keys is the user's leader and sequence timeout (SPEC §10.5); nil
	// when the host reads no key sequences.
	Keys *Keys `msg:"keys,omitempty"`
	// Menu is true when the host shows a program's which-key menus (Menu,
	// SPEC §10.6).
	Menu bool `msg:"menu,omitempty"`
	// Host names the implementation, if it says.
	Host string `msg:"host,omitempty"`
	// Version is the implementation's version, with Host: dot-separated
	// numbers, compared one by one ("0.0.10" is after "0.0.9").
	Version string `msg:"version,omitempty"`

	// Raw is the body the capabilities were read from, fields this package
	// does not know included: what a relay announces to the programs
	// behind it (SPEC §2.7). Writing Caps writes the fields above, not Raw.
	Raw []byte `msg:"-"`
}

// Keys is how a host reads key sequences (SPEC §10.5).
type Keys struct {
	// Leader is the user's leader, a key sequence: what <leader> stands for.
	Leader string `msg:"leader,omitempty"`
	// Timeout is how long a sequence waits for its next key, in
	// milliseconds.
	Timeout int `msg:"timeout,omitempty"`
}

// MenuLevel is a which-key level a program shows while its key sequence
// waits (Menu, SPEC §10.6), in myelin's names and order: a myelin Menu
// encodes as one.
type MenuLevel struct {
	// Seq is the keys typed so far, named as SPEC §10.4 names them.
	Seq []string `msg:"seq"`
	// Label is their group's label: "+file".
	Label string `msg:"label,omitempty"`
	// Items are the keys that can follow them.
	Items []MenuItem `msg:"items"`
}

// MenuItem is a key that can follow a menu's sequence.
type MenuItem struct {
	// Key is the key, named as SPEC §10.4 names it.
	Key string `msg:"key"`
	// Label is what it does; empty leaves it to the host.
	Label string `msg:"label,omitempty"`
	// Group is set when more keys follow it.
	Group bool `msg:"group,omitempty"`
	// Enabled is set when it does anything now.
	Enabled bool `msg:"enabled"`
}

// Cell is a cell's size in device pixels.
type Cell struct {
	W int `msg:"w"`
	H int `msg:"h"`
}

// Area is the cells an element covers (SPEC §9), counted as a drag's are:
// Col and Row are the first column and row it touches, from 0 at the
// surface's top left (not its window's), and W and H the columns and rows
// it spans. An element partly clipped or scrolled away has its whole area,
// so Col and Row may be negative or past the surface.
type Area struct {
	Col int `msg:"c"`
	Row int `msg:"r"`
	W   int `msg:"w"`
	H   int `msg:"h"`
}

// Size is a surface's size in CSS pixels, in a resize event.
type Size struct {
	W float64 `msg:"w"`
	H float64 `msg:"h"`
}

// errorBody is an error reply's body (SPEC §3.6).
type errorBody struct {
	Code   string `msg:"code"`
	Detail string `msg:"detail,omitempty"`
}

// The detail of each event kind (SPEC §9), as the host sends it.

type clickDetail struct {
	Value string  `msg:"value,omitempty"`
	Href  *string `msg:"href,omitempty"`
	URL   string  `msg:"url,omitempty"`
	Area  *Area   `msg:"area,omitempty"`
	Count int     `msg:"count,omitempty"`
}

type pressDetail struct {
	Area *Area `msg:"area,omitempty"`
}

type changeDetail struct {
	Checked *bool  `msg:"checked,omitempty"`
	Value   string `msg:"value"`
}

type inputDetail struct {
	Value string `msg:"value"`
}

type fitDetail struct {
	R int `msg:"r"`
}

type dragDetail struct {
	C    int      `msg:"c"`
	R    int      `msg:"r"`
	Keys []string `msg:"keys"`
	X    *int     `msg:"x,omitempty"`
	Y    *int     `msg:"y,omitempty"`
	TX   *int     `msg:"tx,omitempty"`
	TY   *int     `msg:"ty,omitempty"`
}

type hoverDetail struct {
	C   *int `msg:"c,omitempty"`
	R   *int `msg:"r,omitempty"`
	Out bool `msg:"out,omitempty"`
}

type viewDetail struct {
	In  map[string]viewArea `msg:"in"`
	End bool                `msg:"end"`
}

// viewArea is an Area that may lack a field: a view leaves such an
// element out (SDK.md §3.9).
type viewArea struct {
	C *int `msg:"c,omitempty"`
	R *int `msg:"r,omitempty"`
	W *int `msg:"w,omitempty"`
	H *int `msg:"h,omitempty"`
}

type commandDetail struct {
	Name string  `msg:"name"`
	Key  string  `msg:"key,omitempty"`
	Text *string `msg:"text,omitempty"`
}

// submitDetail is a form's fields by name.
type submitDetail map[string]string
