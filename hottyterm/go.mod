// hottyterm: hotty for a program in a terminal. A module of its own for the
// terminal libraries it uses (ultraviolet).
module neuroplast.io/hotty-go/hottyterm

go 1.25.0

require neuroplast.io/hotty-go v0.7.1

require (
	github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886
	github.com/charmbracelet/x/term v0.2.2
	golang.org/x/sys v0.47.0
	neuroplast.io/hotty-go/hottytest v0.7.1
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
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/tinylib/msgp v1.6.5 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	neuroplast.io/hotty-go/hottyedit v0.7.1 // indirect
	neuroplast.io/myelin/myelinkey v0.2.0 // indirect
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace neuroplast.io/hotty-go => ../

replace neuroplast.io/hotty-go/hottytest => ../hottytest

replace neuroplast.io/hotty-go/hottyedit => ../hottyedit
