// hottyedit: a text field edited as a host edits one. A module of its own
// for Unicode's grapheme segmentation.
module github.com/neuroplastio/hotty-go/hottyedit

go 1.24.0

require (
	github.com/clipperhouse/uax29/v2 v2.7.0
	github.com/neuroplastio/hotty-go v0.0.0-20261007135124-ea9639477c56
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace github.com/neuroplastio/hotty-go => ../
