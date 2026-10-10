// hottytest: a HOTTY host for tests. A module of its own for x/net/html.
module github.com/neuroplastio/hotty-go/hottytest

go 1.24.0

require (
	github.com/neuroplastio/hotty-go v0.0.0-20261010155830-c8e0cc1d3198
	github.com/neuroplastio/hotty-go/hottyedit v0.0.0-20261010155830-c8e0cc1d3198
	golang.org/x/net v0.39.0
)

require github.com/clipperhouse/uax29/v2 v2.7.0 // indirect

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace github.com/neuroplastio/hotty-go => ../

replace github.com/neuroplastio/hotty-go/hottyedit => ../hottyedit
