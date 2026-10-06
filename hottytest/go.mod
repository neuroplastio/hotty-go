// hottytest: a HOTTY host for tests. A module of its own for x/net/html.
module github.com/neuroplastio/hotty-go/hottytest

go 1.24.0

require (
	github.com/neuroplastio/hotty-go v0.0.0-20261006212032-1d0167307b0b
	golang.org/x/net v0.39.0
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace github.com/neuroplastio/hotty-go => ../
