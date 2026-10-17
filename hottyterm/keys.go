package hottyterm

import (
	"strings"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// keyNames are the physical key names (myelin key names §2) of the keys that
// are not characters. The modifier keys are none: bytes name no modifier
// tapped on its own (SPEC §10.4).
var keyNames = map[rune]string{
	uv.KeyEnter: "enter", uv.KeyTab: "tab", uv.KeyEscape: "escape", uv.KeyBackspace: "backspace",
	uv.KeyDelete: "delete", uv.KeyInsert: "insert",
	uv.KeyUp: "arrow_up", uv.KeyDown: "arrow_down", uv.KeyLeft: "arrow_left", uv.KeyRight: "arrow_right",
	uv.KeyHome: "home", uv.KeyEnd: "end", uv.KeyPgUp: "page_up", uv.KeyPgDown: "page_down",
	uv.KeyF1: "f1", uv.KeyF2: "f2", uv.KeyF3: "f3", uv.KeyF4: "f4", uv.KeyF5: "f5", uv.KeyF6: "f6",
	uv.KeyF7: "f7", uv.KeyF8: "f8", uv.KeyF9: "f9", uv.KeyF10: "f10", uv.KeyF11: "f11", uv.KeyF12: "f12",
	uv.KeyKpEnter: "enter", uv.KeyKpLeft: "arrow_left", uv.KeyKpRight: "arrow_right", uv.KeyKpUp: "arrow_up",
	uv.KeyKpDown: "arrow_down", uv.KeyKpHome: "home", uv.KeyKpEnd: "end", uv.KeyKpPgUp: "page_up",
	uv.KeyKpPgDown: "page_down", uv.KeyKpInsert: "insert", uv.KeyKpDelete: "delete",
}

// KeyName is a key as SPEC §10.4 names it from the bytes a program reads, so
// that a program that reads keys from Events (uv.KeyPressEvent) looks it up
// in the keymap it gives its surfaces (hotty.Keymap.Lookup,
// hottyedit.Field.Key): "a", "shift+a", "space", "ctrl+a", "alt+arrow_left",
// "shift+enter". ultraviolet reads the bytes as SPEC §10.4 does, so a key has
// the name hotty.DecodeKeys gives it: one name, the character the bytes carry
// or the physical key. "" for a key with no name there.
func KeyName(k uv.Key) string {
	var mods []string
	if k.Mod&(uv.ModMeta|uv.ModSuper) != 0 {
		mods = append(mods, "super")
	}
	if k.Mod&uv.ModCtrl != 0 {
		mods = append(mods, "ctrl")
	}
	if k.Mod&uv.ModAlt != 0 {
		mods = append(mods, "alt")
	}
	shift := k.Mod&uv.ModShift != 0
	var value string
	switch name, named := keyNames[k.Code]; {
	case named:
		value = name
	case k.Code == uv.KeySpace:
		value = "space"
	case k.Code == uv.KeyExtended:
		value = k.Text
	case shift && k.ShiftedCode != 0:
		value, shift = string(k.ShiftedCode), false
	case shift && k.Text != "" && len(mods) == 0:
		value, shift = k.Text, false
	case k.Code > unicode.MaxRune:
		return ""
	default:
		// A capital without shift is its small letter, as Caps Lock makes it.
		value = string(unicode.ToLower(k.Code))
	}
	// A capital is shift with its letter (SPEC §10.4); any other character
	// with Shift in it shows no shift, but a key that types none does.
	if r, n := utf8.DecodeRuneInString(value); n == len(value) && unicode.ToLower(r) != r && unicode.ToUpper(unicode.ToLower(r)) == r {
		value, shift = string(unicode.ToLower(r)), true
	}
	if shift {
		mods = append(mods, "shift")
	}
	name, ok := hotty.ParseKey(strings.Join(append(mods, value), "+"))
	if !ok {
		return ""
	}
	return name
}
