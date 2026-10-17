package hottytest

import (
	"testing"

	"github.com/neuroplastio/hotty-go"
)

func TestKeys(t *testing.T) {
	h := shown(t, "k", `<form id=f>`+
		`<input id=name name=name data-on=input>`+
		`<input id=skip tabindex=-1><input id=off disabled>`+
		`<textarea id=notes name=notes></textarea>`+
		`<input id=ok name=ok type=checkbox value=yes>`+
		`<select id=size name=size><option>small<option>medium<option>large</select>`+
		`<button id=go type=button>Go</button>`+
		`<div inert><button id=dead>Dead</button></div>`+
		`</form>`)

	// No surface has the keyboard: the key is the program's.
	if h.Key("a") {
		t.Error("a key with no surface focused was used")
	}
	expect(t, sent(h), `"a"`)

	// The program gives the keyboard; tab goes in tree order, past what
	// is disabled, inert, or out of the Tab order.
	send(h, hotty.Focus("k", "name"))
	h.drain()
	for _, k := range []string{"shift+a", "d", "a", "backspace", "space"} {
		if !h.Key(k) {
			t.Errorf("%q was not used in a text field", k)
		}
	}
	if !h.Key("shift+a") {
		t.Error("shift+a was not used")
	}
	expect(t, sent(h),
		`ev input t=name {"value":"A"}`, `ev input t=name {"value":"Ad"}`, `ev input t=name {"value":"Ada"}`,
		`ev input t=name {"value":"Ad"}`, `ev input t=name {"value":"Ad "}`, `ev input t=name {"value":"Ad A"}`)
	var order []string
	for range 4 {
		h.Key("tab")
		order = append(order, h.Surface("k").Focused())
	}
	if want := []string{"notes", "ok", "size", "go"}; !equalStrings(order, want) {
		t.Errorf("Tab order %v, want %v", order, want)
	}
	expect(t, sent(h), `ev change t=name {"value":"Ad A"}`,
		"ev focus t=notes", "ev focus t=ok", "ev focus t=size", "ev focus t=go")

	// shift+tab goes back; a key with ctrl is the program's.
	h.Key("shift+tab")
	if got := h.Surface("k").Focused(); got != "size" {
		t.Errorf("shift+tab: %q", got)
	}
	if h.Key("ctrl+s") {
		t.Error("ctrl+s was used")
	}
	expect(t, sent(h), "ev focus t=size", `"\x13"`)

	// A select picks by the arrows and by the first letter.
	h.Key("arrow_down")
	h.Key("l")
	h.Key("home")
	h.Key("arrow_up")
	expect(t, sent(h), `ev change t=size {"value":"medium"}`, `ev change t=size {"value":"large"}`, `ev change t=size {"value":"small"}`)

	// space checks a checkbox; enter on a button clicks it.
	h.Key("shift+tab")
	h.Key("space")
	h.Key("tab")
	h.Key("tab")
	h.Key("enter")
	expect(t, sent(h), "ev focus t=ok", `ev change t=ok {"checked":true,"value":"yes"}`,
		"ev focus t=size", "ev focus t=go", "ev click t=go")

	// Past the last element the surface loses the keyboard.
	h.Key("tab")
	expect(t, sent(h), "ev blur t=")
	if h.Key("tab") {
		t.Error("tab with no surface focused was used")
	}
	expect(t, sent(h), `"\t"`)

	// A textarea takes enter as a new line; enter in a field submits.
	send(h, hotty.Focus("k", "notes"))
	h.Key("h")
	h.Key("enter")
	h.Key("i")
	h.Key("arrow_up")
	send(h, hotty.Focus("k", "name"))
	h.Key("enter")
	expect(t, sent(h), `ev change t=notes {"value":"h\ni"}`,
		`ev submit t=f {"fields":[["name","Ad A"],["notes","h\ni"],["ok","yes"],["size","small"]]}`)

	// escape is never the surface's.
	if h.Key("escape") {
		t.Error("escape was used")
	}
	expect(t, sent(h), `"\x1b"`)
}

// Shift selects in a text field, and typing replaces the selection; Fill
// leaves the caret at the end of its text with nothing selected (SPEC
// §10.2).
func TestKeysSelect(t *testing.T) {
	h := shown(t, "k", `<input id=name data-on=input>`)
	send(h, hotty.Focus("k", "name"))
	for _, k := range []string{"a", "b", "c", "shift+arrow_left", "shift+arrow_left", "x"} {
		h.Key(k)
	}
	if err := h.Fill("k", "name", "foo"); err != nil {
		t.Fatal(err)
	}
	h.Key("shift+home")
	h.Key("shift+arrow_right")
	h.Key("y")
	expect(t, sent(h), `ev input t=name {"value":"a"}`, `ev input t=name {"value":"ab"}`, `ev input t=name {"value":"abc"}`,
		`ev input t=name {"value":"ax"}`, `ev input t=name {"value":"foo"}`, `ev input t=name {"value":"fy"}`)
}

// A key the focused element's keymap binds to program reaches the program,
// whatever the element: its keymap is read from data-keys as a field's is,
// with no default keymap, and outside a text field its other bindings do
// nothing, except that a nearer one takes a key back (SPEC §10.2).
func TestKeysForTheProgram(t *testing.T) {
	h := shown(t, "k", `<div data-keys="arrow_down=program end=program space=program enter=program tab=program">`+
		`<select id=s><option>a<option>b<option>c</select>`+
		`<select id=own data-keys="end=line-end arrow_up=program"><option>a<option>b<option>c</select>`+
		`<button id=b data-keys="enter=submit">B</button>`+
		`<input id=c type=checkbox>`+
		`<a id=l href=/x>L</a>`+
		`<details><summary id=m>M</summary></details>`+
		`<input id=d type=date data-keys="arrow_left=program">`+
		`<input id=f data-on=input>`+
		`</div>`+
		`<select id=free><option>a<option>b</select>`)
	press := func(keys ...string) {
		t.Helper()
		for _, k := range keys {
			h.Key(k)
		}
	}

	// A select gives the program what the keymap binds to program, Shift
	// with it, and picks with the rest.
	send(h, hotty.Focus("k", "s"))
	h.drain()
	for _, k := range []string{"arrow_down", "shift+arrow_down", "end", "space", "enter"} {
		if h.Key(k) {
			t.Errorf("%s, bound to program, was used by a select", k)
		}
	}
	expect(t, sent(h), `"\x1b[B\x1b[B\x1b[F \r"`)
	press("c", "arrow_up")
	expect(t, sent(h), `ev change t=s {"value":"c"}`, `ev change t=s {"value":"b"}`)

	// tab cannot be bound: it moves focus. A nearer binding to another
	// action takes end back, and the select uses it.
	press("tab")
	if got := h.Surface("k").Focused(); got != "own" {
		t.Fatalf("Tab, bound to program, moved focus to %q", got)
	}
	h.drain()
	press("end", "arrow_up", "arrow_down")
	expect(t, sent(h), `ev change t=own {"value":"c"}`, `"\x1b[A\x1b[B"`)

	// A button clicks with Enter, which it binds to an action of its own,
	// and gives space to the program.
	send(h, hotty.Focus("k", "b"))
	h.drain()
	press("enter", "space")
	expect(t, sent(h), "ev click t=b", `" "`)

	// A checkbox, a link and a summary give the program the keys that
	// would toggle or click them.
	for _, c := range []struct{ id, key, seq string }{{"c", "space", `" "`}, {"l", "enter", `"\r"`}, {"m", "space", `" "`}} {
		send(h, hotty.Focus("k", c.id))
		h.drain()
		if h.Key(c.key) {
			t.Errorf("#%s used %s, bound to program", c.id, c.key)
		}
		expect(t, sent(h), c.seq)
	}

	// A date input gives the program a key its own keymap would use.
	send(h, hotty.Focus("k", "d"))
	h.drain()
	if h.Key("arrow_left") || !h.Key("backspace") {
		t.Error("a date input used arrow_left, bound to program, or not backspace")
	}
	expect(t, sent(h), `"\x1b[D"`)

	// A text field's keymap starts from the default one: end is still the
	// program's, and the field edits with the rest.
	send(h, hotty.Focus("k", "f"))
	h.drain()
	press("x", "end", "arrow_left", "y")
	expect(t, sent(h), `ev input t=f {"value":"x"}`, `"\x1b[F"`, `ev input t=f {"value":"yx"}`)

	// Outside the div, the keys are the select's again. The field commits
	// as focus leaves it.
	send(h, hotty.Focus("k", "free"))
	press("arrow_down")
	expect(t, sent(h), `ev change t=f {"value":"yx"}`, `ev change t=free {"value":"b"}`)
}

// With no element focused there is no keymap: a surface that has the
// keyboard with nothing to focus reads no data-keys, and every key but Tab
// reaches the program (SPEC §10.2).
func TestKeysWithNothingFocused(t *testing.T) {
	h := shown(t, "k", `<div data-keys="arrow_down=line-next enter=submit space=program"><p>text</p></div>`)
	send(h, hotty.Focus("k", ""))
	h.drain()
	for _, k := range []string{"arrow_down", "enter", "space", "a"} {
		if h.Key(k) {
			t.Errorf("%s was used with nothing focused", k)
		}
	}
	expect(t, sent(h), `"\x1b[B\r a"`)
	if s := h.Surface("k"); s.Focused() != "" || h.keyboard != s {
		t.Errorf("focus %q; the surface has the keyboard: %v", s.Focused(), h.keyboard == s)
	}
	// tab finds nothing to focus, and gives the keyboard back.
	h.Key("tab")
	expect(t, sent(h), "ev blur t=")
}

// An element that only reports clicks uses no keys: space and enter on a
// span with data-on=click are the program's (SPEC §10.2). tab from an
// element focused out of the Tab order goes on from where it is, as a
// browser's does.
func TestKeysClickSpanAndTabFromHeading(t *testing.T) {
	h := shown(t, "k", `<button id=a>A</button><h2 id=h tabindex=-1>Head</h2>`+
		`<span id=link role=link tabindex=0 data-on=click>Link</span><button id=b>B</button>`)
	send(h, hotty.Focus("k", "h"))
	h.drain()
	h.Key("tab")
	if got := h.Surface("k").Focused(); got != "link" {
		t.Errorf("tab from the heading: %q", got)
	}
	for _, k := range []string{"space", "enter"} {
		if h.Key(k) {
			t.Errorf("%s was used on a span", k)
		}
	}
	expect(t, sent(h), "ev focus t=link", `" \r"`)
	send(h, hotty.Focus("k", "h"))
	h.drain()
	h.Key("shift+tab")
	expect(t, sent(h), "ev focus t=a")
}

// A select picks with its keys, past disabled options, by the first letter
// of an option's label; each pick sends input, with data-on~=input, and
// change at once (SPEC §10.2, selects). space and enter do nothing: this
// host shows no list.
func TestSelectKeys(t *testing.T) {
	h := shown(t, "k", `<select id=s data-on=input><option>a<option label=Bee>b<option disabled>c<option>d</select>`)
	send(h, hotty.Focus("k", "s"))
	h.drain()
	for _, k := range []string{"arrow_down", "arrow_down", "arrow_down", "b", "page_down", "page_up", "arrow_up", "space", "enter"} {
		if !h.Key(k) {
			t.Errorf("%s was not used by a select", k)
		}
	}
	expect(t, sent(h),
		`ev input t=s {"value":"b"}`, `ev change t=s {"value":"b"}`,
		`ev input t=s {"value":"d"}`, `ev change t=s {"value":"d"}`,
		`ev input t=s {"value":"b"}`, `ev change t=s {"value":"b"}`,
		`ev input t=s {"value":"d"}`, `ev change t=s {"value":"d"}`,
		`ev input t=s {"value":"a"}`, `ev change t=s {"value":"a"}`)

	// Choosing from a list, as the user does with the pointer, is a pick
	// too.
	_ = h.Choose("k", "s", "d")
	expect(t, sent(h), `ev input t=s {"value":"d"}`, `ev change t=s {"value":"d"}`)
}

func TestKeySequence(t *testing.T) {
	for key, want := range map[string]string{
		"a": "a", "enter": "\r", "shift+tab": "\x1b[Z", "ctrl+c": "\x03", "alt+x": "\x1bx",
		"arrow_left": "\x1b[D", "+": "+", "ctrl++": "", "F1": "", "Hyper+a": "", "space": " ",
	} {
		if got := keySequence(key); got != want {
			t.Errorf("%q: %q, want %q", key, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A host whose capabilities say mod is super reads keymaps as a Mac does
// (SPEC §10.2, §10.4): Command keys are the field's, and a host on ctrl
// leaves them to the program.
func TestKeysOnAMac(t *testing.T) {
	h := shown(t, "k", `<input id=t data-on=input value="one two">`, Caps(hotty.Caps{Mod: "super"}))
	send(h, hotty.Focus("k", "t"))
	h.drain()
	h.Key("super+arrow_left")
	h.Key("x")
	h.Key("super+a")
	h.Key("z")
	h.Key("super+backspace")
	expect(t, sent(h), `ev input t=t {"value":"xone two"}`, `ev input t=t {"value":"z"}`, `ev input t=t {"value":""}`)

	h = shown(t, "k", `<input id=t data-on=input value="one two">`)
	send(h, hotty.Focus("k", "t"))
	h.drain()
	if h.Key("super+a") {
		t.Error("super+a was the field's where mod is ctrl")
	}
}
