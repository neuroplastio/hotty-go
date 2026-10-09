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

	// The program gives the keyboard; Tab goes in tree order, past what
	// is disabled, inert, or out of the Tab order.
	send(h, hotty.Focus("k", "name"))
	h.drain()
	for _, k := range []string{"A", "d", "a", "Backspace", "Space"} {
		if !h.Key(k) {
			t.Errorf("%q was not used in a text field", k)
		}
	}
	if !h.Key("Shift+A") {
		t.Error("Shift+A was not used")
	}
	expect(t, sent(h),
		`ev input t=name {"value":"A"}`, `ev input t=name {"value":"Ad"}`, `ev input t=name {"value":"Ada"}`,
		`ev input t=name {"value":"Ad"}`, `ev input t=name {"value":"Ad "}`, `ev input t=name {"value":"Ad A"}`)
	var order []string
	for range 4 {
		h.Key("Tab")
		order = append(order, h.Surface("k").Focused())
	}
	if want := []string{"notes", "ok", "size", "go"}; !equalStrings(order, want) {
		t.Errorf("Tab order %v, want %v", order, want)
	}
	expect(t, sent(h), `ev change t=name {"value":"Ad A"}`)

	// Shift+Tab goes back; a key with Control is the program's.
	h.Key("Shift+Tab")
	if got := h.Surface("k").Focused(); got != "size" {
		t.Errorf("Shift+Tab: %q", got)
	}
	if h.Key("Control+s") {
		t.Error("Control+s was used")
	}
	expect(t, sent(h), `"\x13"`)

	// A select picks by the arrows and by the first letter.
	h.Key("ArrowDown")
	h.Key("l")
	h.Key("Home")
	h.Key("ArrowUp")
	expect(t, sent(h), `ev change t=size {"value":"medium"}`, `ev change t=size {"value":"large"}`, `ev change t=size {"value":"small"}`)

	// Space checks a checkbox; Enter on a button clicks it.
	h.Key("Shift+Tab")
	h.Key(" ")
	h.Key("Tab")
	h.Key("Tab")
	h.Key("Enter")
	expect(t, sent(h), `ev change t=ok {"checked":true,"value":"yes"}`, "ev click t=go")

	// Past the last element the surface loses the keyboard.
	h.Key("Tab")
	expect(t, sent(h), "ev blur t=")
	if h.Key("Tab") {
		t.Error("Tab with no surface focused was used")
	}
	expect(t, sent(h), `"\t"`)

	// A textarea takes Enter as a new line; Enter in a field submits.
	send(h, hotty.Focus("k", "notes"))
	h.Key("h")
	h.Key("Enter")
	h.Key("i")
	h.Key("ArrowUp")
	send(h, hotty.Focus("k", "name"))
	h.Key("Enter")
	expect(t, sent(h), `ev change t=notes {"value":"h\ni"}`,
		`ev submit t=f {"name":"Ad A","notes":"h\ni","ok":"yes","size":"small"}`)

	// Escape is never the surface's.
	if h.Key("Escape") {
		t.Error("Escape was used")
	}
	expect(t, sent(h), `"\x1b"`)
}

// A key the focused element's keymap binds to program reaches the program,
// whatever the element: its keymap is read from data-keys as a field's is,
// with no default keymap, and outside a text field its other bindings do
// nothing, except that a nearer one takes a key back (SPEC §10.2).
func TestKeysForTheProgram(t *testing.T) {
	h := shown(t, "k", `<div data-keys="ArrowDown=program End=program Space=program Enter=program Tab=program">`+
		`<select id=s><option>a<option>b<option>c</select>`+
		`<select id=own data-keys="End=line-end ArrowUp=program"><option>a<option>b<option>c</select>`+
		`<button id=b data-keys="Enter=submit">B</button>`+
		`<input id=c type=checkbox>`+
		`<a id=l href=/x>L</a>`+
		`<details><summary id=m>M</summary></details>`+
		`<input id=d type=date data-keys="ArrowLeft=program">`+
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
	for _, k := range []string{"ArrowDown", "Shift+ArrowDown", "End", "Space", "Enter"} {
		if h.Key(k) {
			t.Errorf("%s, bound to program, was used by a select", k)
		}
	}
	expect(t, sent(h), `"\x1b[B\x1b[B\x1b[F \r"`)
	press("c", "ArrowUp")
	expect(t, sent(h), `ev change t=s {"value":"c"}`, `ev change t=s {"value":"b"}`)

	// Tab cannot be bound: it moves focus. A nearer binding to another
	// action takes End back, and the select uses it.
	press("Tab")
	if got := h.Surface("k").Focused(); got != "own" {
		t.Fatalf("Tab, bound to program, moved focus to %q", got)
	}
	h.drain()
	press("End", "ArrowUp", "ArrowDown")
	expect(t, sent(h), `ev change t=own {"value":"c"}`, `"\x1b[A\x1b[B"`)

	// A button clicks with Enter, which it binds to an action of its own,
	// and gives Space to the program.
	send(h, hotty.Focus("k", "b"))
	h.drain()
	press("Enter", "Space")
	expect(t, sent(h), "ev click t=b", `" "`)

	// A checkbox, a link and a summary give the program the keys that
	// would toggle or click them.
	for _, c := range []struct{ id, key, seq string }{{"c", "Space", `" "`}, {"l", "Enter", `"\r"`}, {"m", "Space", `" "`}} {
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
	if h.Key("ArrowLeft") || !h.Key("Backspace") {
		t.Error("a date input used ArrowLeft, bound to program, or not Backspace")
	}
	expect(t, sent(h), `"\x1b[D"`)

	// A text field's keymap starts from the default one: End is still the
	// program's, and the field edits with the rest.
	send(h, hotty.Focus("k", "f"))
	h.drain()
	press("x", "End", "ArrowLeft", "y")
	expect(t, sent(h), `ev input t=f {"value":"x"}`, `"\x1b[F"`, `ev input t=f {"value":"yx"}`)

	// Outside the div, the keys are the select's again. The field commits
	// as focus leaves it.
	send(h, hotty.Focus("k", "free"))
	press("ArrowDown")
	expect(t, sent(h), `ev change t=f {"value":"yx"}`, `ev change t=free {"value":"b"}`)
}

// A select picks with its keys, past disabled options, by the first letter
// of an option's label; each pick sends input, with data-on~=input, and
// change at once (SPEC §10.2, selects). Space and Enter do nothing: this
// host shows no list.
func TestSelectKeys(t *testing.T) {
	h := shown(t, "k", `<select id=s data-on=input><option>a<option label=Bee>b<option disabled>c<option>d</select>`)
	send(h, hotty.Focus("k", "s"))
	h.drain()
	for _, k := range []string{"ArrowDown", "ArrowDown", "ArrowDown", "B", "PageDown", "PageUp", "ArrowUp", "Space", "Enter"} {
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
		"a": "a", "Enter": "\r", "Shift+Tab": "\x1b[Z", "Control+c": "\x03", "Alt+x": "\x1bx",
		"ArrowLeft": "\x1b[D", "+": "+", "Control++": "", "F1": "", "Hyper+a": "", "Space": " ",
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
