// hottytest: a hotty host for tests. A module of its own for x/net/html.
module neuroplast.io/hotty-go/hottytest

go 1.24.0

require (
	github.com/clipperhouse/uax29/v2 v2.7.0
	github.com/tinylib/msgp v1.6.5
	golang.org/x/net v0.39.0
	neuroplast.io/hotty-go v0.7.2
	neuroplast.io/hotty-go/hottyedit v0.7.2
	neuroplast.io/myelin/myelinkey v0.2.0
)

require (
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace neuroplast.io/hotty-go => ../

replace neuroplast.io/hotty-go/hottyedit => ../hottyedit
