// hottyterm: HOTTY for a program in a terminal. A module of its own for the
// terminal libraries it uses (ultraviolet).
module github.com/neuroplastio/hotty-go/hottyterm

go 1.25.0

require github.com/neuroplastio/hotty-go v0.0.0-20261010222028-ea80c4c35faf

require (
	github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886
	github.com/charmbracelet/x/term v0.2.2
	github.com/neuroplastio/hotty-go/hottytest v0.0.0-20261010222028-ea80c4c35faf
	golang.org/x/sys v0.47.0
)

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/neuroplastio/hotty-go/hottyedit v0.0.0-20261010222028-ea80c4c35faf // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/tinylib/msgp v1.6.5 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace github.com/neuroplastio/hotty-go => ../

replace github.com/neuroplastio/hotty-go/hottytest => ../hottytest

replace github.com/neuroplastio/hotty-go/hottyedit => ../hottyedit
