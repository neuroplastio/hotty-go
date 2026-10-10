// hottyedit: a text field edited as a host edits one. A module of its own
// for Unicode's grapheme segmentation.
module github.com/neuroplastio/hotty-go/hottyedit

go 1.24.0

require (
	github.com/clipperhouse/uax29/v2 v2.7.0
	github.com/neuroplastio/hotty-go v0.0.0-20261010214137-53d0f02f7a89
)

require (
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.5 // indirect
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace github.com/neuroplastio/hotty-go => ../
