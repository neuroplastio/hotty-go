// The example programs: a module of their own, so that what they import is
// theirs, not the SDK's. They build against the SDK beside them.
module github.com/neuroplastio/hotty-go/examples

go 1.25.0

require (
	charm.land/bubbletea/v2 v2.0.8
	github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886
	github.com/neuroplastio/hotty-go v0.0.0-20261009104144-1dc9beac6b47
	github.com/neuroplastio/hotty-go/hottytea v0.0.0-20261009104144-1dc9beac6b47
	github.com/neuroplastio/hotty-go/hottyterm v0.0.0-20261009104144-1dc9beac6b47
	github.com/neuroplastio/hotty-go/hottytest v0.0.0-20261009104144-1dc9beac6b47
	github.com/yuin/goldmark v1.8.6
)

require (
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/vt v0.0.0-20261001101533-953920dd3285 // indirect
	github.com/neuroplastio/hotty-go/hottyedit v0.0.0-20261009104144-1dc9beac6b47 // indirect
)

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/neuroplastio/hotty-go/hottyvt v0.0.0-20261009104144-1dc9beac6b47
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace (
	github.com/neuroplastio/hotty-go => ../
	github.com/neuroplastio/hotty-go/hottyedit => ../hottyedit
	github.com/neuroplastio/hotty-go/hottytea => ../hottytea
	github.com/neuroplastio/hotty-go/hottyterm => ../hottyterm
	github.com/neuroplastio/hotty-go/hottytest => ../hottytest
	github.com/neuroplastio/hotty-go/hottyvt => ../hottyvt
)
