package hottytea

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// KeyName names a Bubble Tea key as hottyterm.KeyName does.
func TestKeyName(t *testing.T) {
	for _, c := range []struct {
		key  tea.Key
		want string
	}{
		{tea.Key{Code: 'a', Text: "a"}, "a"},
		{tea.Key{Code: 'a', ShiftedCode: 'A', Mod: tea.ModShift, Text: "A"}, "shift+a"},
		{tea.Key{Code: 'a', Mod: tea.ModCtrl | tea.ModShift}, "ctrl+shift+a"},
		{tea.Key{Code: '1', Mod: tea.ModShift}, "shift+1"},
		{tea.Key{Code: '1', Mod: tea.ModShift, Text: "!"}, "!"},
		{tea.Key{Code: tea.KeySpace, Mod: tea.ModCtrl}, "ctrl+space"},
		{tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}, "shift+enter"},
		{tea.Key{Code: tea.KeyLeft, Mod: tea.ModSuper}, "super+arrow_left"},
		{tea.Key{Code: tea.KeyExtended, Text: "é"}, "é"},
		{tea.Key{Code: tea.KeyF13}, ""},
	} {
		if got := KeyName(c.key); got != c.want {
			t.Errorf("KeyName(%+v) = %q, want %q", c.key, got, c.want)
		}
	}
}
