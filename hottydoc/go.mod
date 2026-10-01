// hottydoc: files as HOTTY documents. A module of its own for goldmark and
// x/net/html.
module github.com/neuroplastio/hotty-go/hottydoc

go 1.26.8

require (
	github.com/neuroplastio/hotty-go v0.0.0-20261001200251-dc50cf1e26ae
	github.com/yuin/goldmark v1.8.6
	golang.org/x/net v0.39.0
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace github.com/neuroplastio/hotty-go => ../
