package hottyterm

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// ultraviolet's decoder and KeyName name the keys of the vectors' input as
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
			t.Errorf("%q: ultraviolet read %T", input, ev)
			continue
		}
		if got := KeyName(uv.Key(k)); len(want) != 1 || got != want[0] {
			t.Errorf("%q: KeyName = %q, DecodeKeys = %q", input, got, want)
		}
	}
}

func TestKeyName(t *testing.T) {
	for _, c := range []struct {
		key  uv.Key
		want string
	}{
		{uv.Key{Code: 'a', Text: "a"}, "a"},
		{uv.Key{Code: 'a', ShiftedCode: 'A', Mod: uv.ModShift, Text: "A"}, "A"},
		{uv.Key{Code: 'a', Mod: uv.ModCtrl | uv.ModShift}, "Control+A"},
		{uv.Key{Code: '1', Mod: uv.ModShift}, "Shift+1"},
		{uv.Key{Code: '1', Mod: uv.ModShift, Text: "!"}, "!"},
		{uv.Key{Code: uv.KeySpace, Mod: uv.ModCtrl}, "Control+Space"},
		{uv.Key{Code: uv.KeyEnter, Mod: uv.ModShift}, "Shift+Enter"},
		{uv.Key{Code: uv.KeyLeft, Mod: uv.ModSuper}, "Meta+ArrowLeft"},
		{uv.Key{Code: uv.KeyExtended, Text: "é"}, "é"},
		{uv.Key{Code: uv.KeyF13}, ""},
	} {
		if got := KeyName(c.key); got != c.want {
			t.Errorf("KeyName(%+v) = %q, want %q", c.key, got, c.want)
		}
	}
}
