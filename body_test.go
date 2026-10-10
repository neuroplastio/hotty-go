package hotty

import (
	"encoding/hex"
	"strings"
	"testing"
)

// wellFormed takes one map of every form, nested up to MaxDepth, and
// nothing after it.
func TestWellFormed(t *testing.T) {
	nest := func(n int) string { // the map, then n-1 arrays of one inside it
		return "81a178" + strings.Repeat("91", n-2) + "90"
	}
	for _, c := range []struct {
		hex  string
		want bool
	}{
		{"80", true},
		{"de0000", true},
		{"df00000000", true},
		{"81a1617f", true},
		{"81a161e0", true},
		{"81a161c0", true},
		{"82a161c2a162c3", true},
		{"81a161cc01", true},
		{"81a161d0ff", true},
		{"81a161cd0001", true},
		{"81a161ca3f800000", true},
		{"81a161cb3ff0000000000000", true},
		{"81a161cf0000000000000001", true},
		{"81a161d90161", true},
		{"81a161da000161", true},
		{"81a161db0000000161", true},
		{"81a161c40100", true},
		{"81a161c5000100", true},
		{"81a161c600000001" + "00", true},
		{"81a161d4ff00", true},
		{"81a161d5ff0000", true},
		{"81a161d6ff00000000", true},
		{"81a161d7ff0000000000000000", true},
		{"81a161d8ff" + strings.Repeat("00", 16), true},
		{"81a161c701ff00", true},
		{"81a161c80001ff00", true},
		{"81a161c900000001ff00", true},
		{"81a161dc000101", true},
		{"81a161dd0000000101", true},
		{"81a161de0001a162", false}, // a map of one that has only its key
		{nest(MaxDepth), true},
		{nest(MaxDepth + 1), false},
		{"", false},
		{"90", false},               // an array
		{"a161", false},             // a string
		{"80c0", false},             // something after the map
		{"81a161", false},           // cut short
		{"81a161c1", false},         // no type starts with 0xc1
		{"81a161d9", false},         // a str8 without its length
		{"81a161da00", false},       // a str16 with half its length
		{"81a161db0000", false},     // a str32 with half its length
		{"81a161d905", false},       // a str8 longer than what is left
		{"81a161dcffff", false},     // more elements than bytes
		{"81a161dfffffffff", false}, // more pairs than bytes
	} {
		b, err := hex.DecodeString(c.hex)
		if err != nil {
			t.Fatal(c.hex, err)
		}
		if got := wellFormed(b); got != c.want {
			t.Errorf("%s: %v, want %v", c.hex, got, c.want)
		}
	}
}
