package hottytea

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/neuroplastio/hotty-go"
)

// keyNames are the W3C key values of the keys that are not characters.
var keyNames = map[rune]string{
	tea.KeyEnter: "Enter", tea.KeyTab: "Tab", tea.KeyEscape: "Escape", tea.KeyBackspace: "Backspace",
	tea.KeyDelete: "Delete", tea.KeyInsert: "Insert",
	tea.KeyUp: "ArrowUp", tea.KeyDown: "ArrowDown", tea.KeyLeft: "ArrowLeft", tea.KeyRight: "ArrowRight",
	tea.KeyHome: "Home", tea.KeyEnd: "End", tea.KeyPgUp: "PageUp", tea.KeyPgDown: "PageDown",
	tea.KeyF1: "F1", tea.KeyF2: "F2", tea.KeyF3: "F3", tea.KeyF4: "F4", tea.KeyF5: "F5", tea.KeyF6: "F6",
	tea.KeyF7: "F7", tea.KeyF8: "F8", tea.KeyF9: "F9", tea.KeyF10: "F10", tea.KeyF11: "F11", tea.KeyF12: "F12",
	tea.KeyLeftShift: "Shift", tea.KeyRightShift: "Shift", tea.KeyLeftCtrl: "Control", tea.KeyRightCtrl: "Control",
	tea.KeyLeftAlt: "Alt", tea.KeyRightAlt: "Alt", tea.KeyLeftSuper: "Meta", tea.KeyRightSuper: "Meta",
	tea.KeyLeftMeta: "Meta", tea.KeyRightMeta: "Meta",
	tea.KeyKpEnter: "Enter", tea.KeyKpLeft: "ArrowLeft", tea.KeyKpRight: "ArrowRight", tea.KeyKpUp: "ArrowUp",
	tea.KeyKpDown: "ArrowDown", tea.KeyKpHome: "Home", tea.KeyKpEnd: "End", tea.KeyKpPgUp: "PageUp",
	tea.KeyKpPgDown: "PageDown", tea.KeyKpInsert: "Insert", tea.KeyKpDelete: "Delete",
}

// KeyName is a Bubble Tea key as SPEC §10.4 names it, so that a program
// that edits a field in cells looks it up in the keymap it gives its
// surfaces (hotty.Keymap.Lookup, hottyedit.Field.Key): "a", "A", "Space",
// "Control+a", "Alt+ArrowLeft", "Shift+Enter". Bubble Tea reads the bytes
// as SPEC §10.4 does, so a key has the name a host gives it. "" for a key
// with no name there.
func KeyName(k tea.Key) string {
	var mods []string
	if k.Mod&tea.ModCtrl != 0 {
		mods = append(mods, "Control")
	}
	if k.Mod&tea.ModAlt != 0 {
		mods = append(mods, "Alt")
	}
	if k.Mod&(tea.ModMeta|tea.ModSuper) != 0 {
		mods = append(mods, "Meta")
	}
	shift := k.Mod&tea.ModShift != 0
	var value string
	switch name, named := keyNames[k.Code]; {
	case named:
		value = name
	case k.Code == tea.KeySpace:
		value = "Space"
	case k.Code == tea.KeyExtended:
		value = k.Text
	case shift && k.ShiftedCode != 0:
		value = string(k.ShiftedCode)
	case shift && k.Text != "" && len(mods) == 0:
		value = k.Text
	case shift:
		value = string(unicode.ToUpper(k.Code))
	default:
		value = string(k.Code)
	}
	// Shift shows in a character it changed (SPEC §10.4).
	if _, named := keyNames[k.Code]; shift && (named || k.Code == tea.KeySpace || value == string(k.Code)) {
		mods = append(mods, "Shift")
	}
	name, ok := hotty.ParseKey(strings.Join(append(mods, value), "+"))
	if !ok {
		return ""
	}
	return name
}
