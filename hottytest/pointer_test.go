package hottytest

import (
	"testing"

	"neuroplast.io/hotty-go"
)

func TestAuxPointerOwnership(t *testing.T) {
	h := shown(t, "s", `<input id=f><button id=a value="" data-on="aux drag">A</button>
<button id=b data-on=aux>B</button><a id=link target=_blank href=/x data-on=rightbutton>Link</a>`)
	send(h, hotty.Place("s", hotty.Placement{Cols: 12, Rows: 3, Press: hotty.On}), hotty.Focus("s", "f"))
	h.drain()

	middle, primary := hotty.ButtonMiddle, hotty.ButtonPrimary
	if err := h.PointerDown("s", "a", middle, 0, 0); err != nil {
		t.Fatal(err)
	}
	_ = h.Click("s", "b", "alt")
	_ = h.Press("s", "b")
	_ = h.PointerUp("s", "a", middle, 1, 0, "alt")
	expect(t, sent(h), `ev auxclick t=a {"button":1,"c":1,"mods":2,"r":0,"value":""}`)
	if focus := h.Surface("s").Focused(); focus != "f" {
		t.Fatalf("focus = %q after Alt on release", focus)
	}

	_ = h.PointerDown("s", "a", middle, 0, 0)
	_ = h.PointerUp("s", "b", middle, 4, 0)
	_ = h.ContextMenu("s", "link", 0, 1)
	_ = h.AuxClick("s", "link", hotty.ButtonSecondary, 0, 1)
	expect(t, sent(h))

	if err := h.DragStartButton("s", "a", middle, 0, 0); err != nil {
		t.Fatal(err)
	}
	_ = h.Click("s", "b")
	_ = h.Press("s", "b")
	_ = h.DragEnd("a", 2, 0)
	expect(t, sent(h), `ev dragstart t=a {"button":1,"c":0,"r":0}`, `ev dragend t=a {"c":2,"r":0}`)

	_ = h.PointerDown("s", "b", middle, 3, 0, "shift")
	_ = h.PointerDown("s", "b", primary, 3, 0, "alt")
	_ = h.PointerUp("s", "b", middle, 3, 0)
	_ = h.Click("s", "b")
	_ = h.PointerUp("s", "b", primary, 3, 0)
	expect(t, sent(h))
	if !h.Key("x") {
		t.Fatal("Shift press or suppressed second press took the keyboard")
	}
	_ = h.PointerDown("s", "a", middle, 0, 0, "alt")
	_ = h.PointerUp("s", "a", middle, 0, 0)
	if h.Key("z") {
		t.Fatal("Alt press retained the keyboard")
	}
	expect(t, sent(h), `ev change t=f {"rev":1,"value":"x"}`, "ev blur t=", `"z"`)
}
