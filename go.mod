// hotty-go: the HOTTY protocol (package hotty), which needs msgp's runtime
// for a host's msgpack bodies (SDK.md §2.1) and myelinkey, which names and
// reads keys (SPEC §10.4), and the packages that need
// nothing more: chart, form, series, braille, blocks.
// The packages that need more are modules of their own, so that a program
// takes on only what it imports: hottyterm, hottytea and hottytest.
module github.com/neuroplastio/hotty-go

go 1.24.0

require (
	github.com/neuroplastio/myelin/myelinkey v0.1.0
	github.com/tinylib/msgp v1.6.5
)

require github.com/philhofer/fwd v1.2.0 // indirect
