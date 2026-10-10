package hottyterm

import (
	"strings"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// keyNames are the W3C key values of the keys that are not characters.
var keyNames = map[rune]string{
	uv.KeyEnter: "Enter", uv.KeyTab: "Tab", uv.KeyEscape: "Escape", uv.KeyBackspace: "Backspace",
	uv.KeyDelete: "Delete", uv.KeyInsert: "Insert",
	uv.KeyUp: "ArrowUp", uv.KeyDown: "ArrowDown", uv.KeyLeft: "ArrowLeft", uv.KeyRight: "ArrowRight",
	uv.KeyHome: "Home", uv.KeyEnd: "End", uv.KeyPgUp: "PageUp", uv.KeyPgDown: "PageDown",
	uv.KeyF1: "F1", uv.KeyF2: "F2", uv.KeyF3: "F3", uv.KeyF4: "F4", uv.KeyF5: "F5", uv.KeyF6: "F6",
	uv.KeyF7: "F7", uv.KeyF8: "F8", uv.KeyF9: "F9", uv.KeyF10: "F10", uv.KeyF11: "F11", uv.KeyF12: "F12",
	uv.KeyLeftShift: "Shift", uv.KeyRightShift: "Shift", uv.KeyLeftCtrl: "Control", uv.KeyRightCtrl: "Control",
	uv.KeyLeftAlt: "Alt", uv.KeyRightAlt: "Alt", uv.KeyLeftSuper: "Meta", uv.KeyRightSuper: "Meta",
	uv.KeyLeftMeta: "Meta", uv.KeyRightMeta: "Meta",
	uv.KeyKpEnter: "Enter", uv.KeyKpLeft: "ArrowLeft", uv.KeyKpRight: "ArrowRight", uv.KeyKpUp: "ArrowUp",
	uv.KeyKpDown: "ArrowDown", uv.KeyKpHome: "Home", uv.KeyKpEnd: "End", uv.KeyKpPgUp: "PageUp",
	uv.KeyKpPgDown: "PageDown", uv.KeyKpInsert: "Insert", uv.KeyKpDelete: "Delete",
}

// KeyName is a key as SPEC §10.4 names it, so that a program that reads
// keys from Events (uv.KeyPressEvent) looks it up in the keymap it gives
// its surfaces (hotty.Keymap.Lookup, hottyedit.Field.Key): "a", "A",
// "Space", "Control+a", "Alt+ArrowLeft", "Shift+Enter". ultraviolet reads
// the bytes as SPEC §10.4 does, so a key has the name a host gives it. ""
// for a key with no name there.
func KeyName(k uv.Key) string {
	var mods []string
	if k.Mod&uv.ModCtrl != 0 {
		mods = append(mods, "Control")
	}
	if k.Mod&uv.ModAlt != 0 {
		mods = append(mods, "Alt")
	}
	if k.Mod&(uv.ModMeta|uv.ModSuper) != 0 {
		mods = append(mods, "Meta")
	}
	shift := k.Mod&uv.ModShift != 0
	var value string
	switch name, named := keyNames[k.Code]; {
	case named:
		value = name
	case k.Code == uv.KeySpace:
		value = "Space"
	case k.Code == uv.KeyExtended:
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
	if _, named := keyNames[k.Code]; shift && (named || k.Code == uv.KeySpace || value == string(k.Code)) {
		mods = append(mods, "Shift")
	}
	name, ok := hotty.ParseKey(strings.Join(append(mods, value), "+"))
	if !ok {
		return ""
	}
	return name
}
