// hottytest: a HOTTY host for tests. A module of its own for x/net/html.
module github.com/neuroplastio/hotty-go/hottytest

go 1.26.8

require (
	github.com/neuroplastio/hotty-go v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.39.0
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace github.com/neuroplastio/hotty-go => ../
