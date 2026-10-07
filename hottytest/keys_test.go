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
