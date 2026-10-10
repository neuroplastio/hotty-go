package hottytea

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go/hottyterm"
)

// KeyName is a Bubble Tea key as SPEC §10.4 names it, so that a program
// that edits a field in cells looks it up in the keymap it gives its
// surfaces (hotty.Keymap.Lookup, hottyedit.Field.Key): "a", "A", "Space",
// "Control+a", "Alt+ArrowLeft", "Shift+Enter". Bubble Tea's key is
// ultraviolet's, and this is hottyterm.KeyName's name for it. "" for a key
// with no name there.
func KeyName(k tea.Key) string {
	return hottyterm.KeyName(uv.Key(k))
}
