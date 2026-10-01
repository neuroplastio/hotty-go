// Package fix is a fixture for docgen's tests. A [Thing] holds an
// [sub.Item], and writes to a [strings.Builder].
//
// # Usage
//
// Make one and use it:
//
//	t := fix.NewThing()
//	t.Do()
//
// The shell way:
//
//	$ fix --now | cat
package fix

import "strings"

// Sizes of things.
const (
	Small = 1 // a small thing
	Large = 2
)

// Thing does things.
type Thing struct {
	// Name is the thing's name.
	Name   string
	hidden int
	b      strings.Builder
}

// NewThing makes a Thing.
func NewThing() *Thing { return &Thing{} }

// Do does the thing; see [Thing.Quiet] and [Small].
func (t *Thing) Do() string { return "done" }

func (t *Thing) Quiet() {}

func Bare() {}

var Loose = 3
