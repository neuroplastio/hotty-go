// hottyedit: a text field edited as a host edits one. A module of its own
// for Unicode's grapheme segmentation.
module neuroplast.io/hotty-go/hottyedit

go 1.24.0

require (
	github.com/clipperhouse/displaywidth v0.11.0
	github.com/clipperhouse/uax29/v2 v2.7.0
	neuroplast.io/hotty-go v0.7.2
)

require (
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.5 // indirect
	neuroplast.io/myelin/myelinkey v0.2.0 // indirect
)

// The SDK's modules build against each other as they are in this
// repository; a release requires the versions it tags.
replace neuroplast.io/hotty-go => ../
