package hotty

// The types of the bodies a host sends (SPEC §3.3), each field as its type,
// read and written by the codec msgp generates (wire_gen.go). They have no
// other methods: what is done with them is a function beside them (gov P-3).

//go:generate msgp -file wire.go -o wire_gen.go -marshal=false -tests=false -unexported
//msgp:newtime
//msgp:limit arrays:65536 maps:65536
//msgp:tuple FormField

// Caps is what a host says about itself in its reply to a query (SPEC §4),
// and, with only the fields that changed, in a caps event (SPEC §9).
type Caps struct {
	// V is the protocol version the host implements: "0.5".
	V string `msg:"v"`
	// CellCSS is a cell's size in CSS pixels (SPEC §5.3); the zero Size
	// when the host did not say.
	CellCSS Size `msg:"cellcss,omitempty"`
	// Scale is device pixels per CSS pixel: a hint for the resolution of
	// images.
	Scale float64 `msg:"scale,omitempty"`
	// Scheme is the terminal's colour scheme: "dark" or "light".
	Scheme string `msg:"scheme,omitempty"`
	// Mod is the modifier mod stands for in bindings, "ctrl" or "super"
	// (SPEC §10.4); "" when the host did not say, which is ctrl.
	Mod string `msg:"mod,omitempty"`
	// Features are the optional features the host has (SPEC §4): see
	// HasFeature.
	Features []string `msg:"features,omitempty"`
	// Limits are the host's limits, such as "surfaces" (a count) and
	// "resources" (bytes) (SPEC §13).
	Limits map[string]int `msg:"limits,omitempty"`
	// Net is the host's network policy, from directive to the sources it
	// allows (SPEC §7.2): empty when it fetches nothing.
	Net map[string][]string `msg:"net,omitempty"`
	// Edit is the host's edit shortcuts, from each action to its keys
	// (SPEC §10.2); a host that reports them takes edit commands (Edit,
	// SPEC §10.6).
	Edit map[string][]string `msg:"edit,omitempty"`
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

// MenuTree is what a program can do (Menu, SPEC §10.7): a myelin Tree,
// each binding given a Name, its groups' items nested in them.
type MenuTree struct {
	// Label is what the menu is for: "Files". Empty leaves it to the host.
	Label string `msg:"label,omitempty"`
	// Items are the menu's top level, in the order to show them.
	Items []MenuItem `msg:"items"`
}

// MenuItem is an action of a menu, or a group of them (SPEC §10.7). Items
// nest at most MenuDepth deep, the top level's being one deep.
type MenuItem struct {
	// Key is the key that reaches it after its group's, named as SPEC §10.4
	// names it; empty for an item no key reaches.
	Key string `msg:"key,omitempty"`
	// Label is what it does.
	Label string `msg:"label,omitempty"`
	// Name is the command a pick sends; empty for an item only its keys
	// reach.
	Name string `msg:"name,omitempty"`
	// Enabled is set when it does anything now.
	Enabled bool `msg:"enabled"`
	// Items are the items after it, which make it a group.
	Items []MenuItem `msg:"items,omitempty"`
}

// MenuKeys is the body of an a=menu with show=1: the keys typed so far,
// named as SPEC §10.4 names them, whose level of the menu to show (SPEC
// §10.7). MenuShow writes it; MenuShowOf reads it.
type MenuKeys struct {
	Seq []string `msg:"seq"`
}

// TouchArea is cells of the screen where a finger drags as the mouse does
// (Touch, SPEC §9.6). C and R are the first column and row it covers,
// counted from 0 at the screen's top left (a mouse report counts from 1),
// and W and H the columns and rows it spans, at least 1 each. Drag is the
// way a finger drags in it: "x" across, "y" up and down, "xy" either. A
// horizontal slider's is "x".
type TouchArea struct {
	C    int    `msg:"c"`
	R    int    `msg:"r"`
	W    int    `msg:"w"`
	H    int    `msg:"h"`
	Drag string `msg:"drag"`
}

// fieldBody is the body of an a=field (SPEC §6.4), as FieldChange says
// it: Rev is nil when the body has none, and Sel holds the anchor and the
// caret.
type fieldBody struct {
	Rev     *int    `msg:"rev"`
	Value   *string `msg:"value,omitempty"`
	From    *int    `msg:"from,omitempty"`
	To      *int    `msg:"to,omitempty"`
	Text    *string `msg:"text,omitempty"`
	Sel     []int   `msg:"sel,omitempty"`
	Checked *bool   `msg:"checked,omitempty"`
}

// touchBody is the body of an a=touch (SPEC §9.6). Touch writes it;
// TouchOf reads it, checking each area has all its fields.
type touchBody struct {
	Areas []TouchArea `msg:"areas"`
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

// Size is a size in CSS pixels: a cell's, in the capabilities (SPEC §4).
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
	Area  *Area   `msg:"area,omitempty"`
	Mods  int     `msg:"mods,omitempty"`
	Href  *string `msg:"href,omitempty"`
	URL   string  `msg:"url,omitempty"`
	Value string  `msg:"value,omitempty"`
	Count int     `msg:"count,omitempty"`
}

type pressDetail struct {
	Area *Area `msg:"area,omitempty"`
	Mods int   `msg:"mods,omitempty"`
}

type changeDetail struct {
	Checked *bool  `msg:"checked,omitempty"`
	Value   string `msg:"value"`
	Rev     int    `msg:"rev"`
}

type inputDetail struct {
	Value string `msg:"value"`
	Rev   int    `msg:"rev"`
	Sel   []int  `msg:"sel,omitempty"`
}

type caretDetail struct {
	Sel []int `msg:"sel,omitempty"`
	Rev int   `msg:"rev"`
}

type fitDetail struct {
	R int `msg:"r"`
}

type dragDetail struct {
	C    int  `msg:"c"`
	R    int  `msg:"r"`
	Mods int  `msg:"mods,omitempty"`
	X    *int `msg:"x,omitempty"`
	Y    *int `msg:"y,omitempty"`
	TX   *int `msg:"tx,omitempty"`
	TY   *int `msg:"ty,omitempty"`
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

// FormField is one field of a submitted form: its name and its value
// (SPEC §9). On the wire it is the array [name, value].
type FormField struct{ Name, Value string }

// submitDetail is a form's fields, in tree order.
type submitDetail struct {
	Fields []FormField `msg:"fields"`
}

// Op is one change of a delta (SPEC §6.1): what to do, to which element,
// with which name, and with which value. The builders OpMorph, OpAttr and
// the rest make them. Value has presence, not just content: it is absent
// for an op that takes none, and present, even empty, for one that does
// (SDK.md §3.4.3).
type Op struct {
	// Op names what it does: "morph", "text", ….
	Op string `msg:"op"`
	// Target is the id of the element, as the document has it; empty for
	// a morph of the whole tree, and sent only when not.
	Target string `msg:"t,omitempty"`
	// Key is the name of the attribute or the custom property, for attr,
	// unattr and var.
	Key string `msg:"k,omitempty"`
	// Value is the HTML, the text or the value; nil when the op has none.
	Value *string `msg:"v,omitempty"`
}

// deltaBody is a delta's body: its ops, in the order they apply.
type deltaBody struct {
	Ops []Op `msg:"ops"`
}
