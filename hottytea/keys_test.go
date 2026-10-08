package hottytea

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// Bubble Tea's decoder and KeyName name the keys of the vectors' input as
// hotty.DecodeKeys does (SPEC §10.4).
func TestKeyNameAgreesWithDecodeKeys(t *testing.T) {
	for _, input := range []string{
		"a", "A", " ", "!", "é",
		"\x01", "\x05", "\x17", "\x7f", "\x08", "\r", "\t", "\x1b",
		"\x1bb", "\x1bB", "\x1b\x7f", "\x1b<", "\x1b>", "\x1bd",
		"\x1b[A", "\x1b[D", "\x1bOC", "\x1b[H", "\x1b[F", "\x1b[3~", "\x1b[5~", "\x1b[6~", "\x1b[Z",
		"\x1b[1;3D", "\x1b[1;5C", "\x1b[1;2D", "\x1b[1;6D", "\x1b[3;5~", "\x1b[3;3~", "\x1b[1;5H", "\x1b[1;5F",
		"\x1b[97;5u", "\x1b[97;6u", "\x1b[13;2u", "\x1b[127;5u", "\x1b[9;5u", "\x1b[97;9u",
	} {
		want := hotty.DecodeKeys([]byte(input))
		var d uv.EventDecoder
		_, ev := d.Decode([]byte(input))
		k, ok := ev.(uv.KeyPressEvent)
		if !ok {
			t.Errorf("%q: Bubble Tea read %T", input, ev)
			continue
		}
		if got := KeyName(tea.Key(k)); len(want) != 1 || got != want[0] {
			t.Errorf("%q: KeyName = %q, DecodeKeys = %q", input, got, want)
		}
	}
}

func TestKeyName(t *testing.T) {
	for _, c := range []struct {
		key  tea.Key
		want string
	}{
		{tea.Key{Code: 'a', Text: "a"}, "a"},
		{tea.Key{Code: 'a', ShiftedCode: 'A', Mod: tea.ModShift, Text: "A"}, "A"},
		{tea.Key{Code: 'a', Mod: tea.ModCtrl | tea.ModShift}, "Control+A"},
		{tea.Key{Code: '1', Mod: tea.ModShift}, "Shift+1"},
		{tea.Key{Code: '1', Mod: tea.ModShift, Text: "!"}, "!"},
		{tea.Key{Code: tea.KeySpace, Mod: tea.ModCtrl}, "Control+Space"},
		{tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}, "Shift+Enter"},
		{tea.Key{Code: tea.KeyLeft, Mod: tea.ModSuper}, "Meta+ArrowLeft"},
		{tea.Key{Code: tea.KeyExtended, Text: "é"}, "é"},
		{tea.Key{Code: tea.KeyF13}, ""},
	} {
		if got := KeyName(c.key); got != c.want {
			t.Errorf("KeyName(%+v) = %q, want %q", c.key, got, c.want)
		}
	}
}
