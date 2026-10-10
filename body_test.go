package hotty

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/tinylib/msgp/msgp"
)

// wellFormed takes one map of every form, nested up to MaxDepth, with
// nothing after it and nothing a host does not send in it (SPEC §3.3).
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
		{"81a161c0", false}, // nil
		{"82a161c2a162c3", true},
		{"81a161cc01", true},
		{"81a161d0ff", true},
		{"81a161cd0001", true},
		{"81a161ca3f800000", true},
		{"81a161cb3ff0000000000000", true},
		{"81a161cf0000000000000001", true},
		{"81a161cf001fffffffffffff", true},  // 2^53 - 1
		{"81a161cf0020000000000000", false}, // 2^53
		{"81a161d3ffe0000000000001", true},  // -(2^53 - 1)
		{"81a161d3ffe0000000000000", false}, // -2^53
		{"81a161ce ffffffff", true},
		{"81a161d90161", true},
		{"81a161da000161", true},
		{"81a161db0000000161", true},
		{"81a161c40100", true},
		{"81a161c5000100", true},
		{"81a161c600000001" + "00", true},
		{"81a161d40500", true}, // extensions no one defines, of every form
		{"81a161d5050000", true},
		{"81a161d60500000000", true},
		{"81a161d7050000000000000000", true},
		{"81a161d805" + strings.Repeat("00", 16), true},
		{"81a161c7010500", true},
		{"81a161c8000105ff", true},
		{"81a161c90000000105ff", true},
		{"81a161d6ffffffffff", true},                         // a timestamp of 4 bytes
		{"81a161d7ffee6b27fc00000000", true},                 // of 8, 999 999 999 ns
		{"81a161d7ffee6b280000000000", false},                // of 8, a whole second's ns
		{"81a161c70cff3b9ac9ff001fffffffffffff", true},       // of 12
		{"81a161c70cff3b9aca00" + "0000000000000000", false}, // of 12, a second's ns
		{"81a161c70cff00000000" + "0020000000000000", false}, // of 12, 2^53 s
		{"81a161d4ff00", false},                              // of 1
		{"81a161d5ff0000", false},                            // of 2
		{"81a161d8ff" + strings.Repeat("00", 16), false},     // of 16
		{"81a161c701ff00", false},
		{"81a161a3e282ac", true},              // "€"
		{"81a161a2c328", false},               // not UTF-8
		{"81a161a3eda080", false},             // a surrogate
		{"81a161a2c080", false},               // an overlong NUL
		{"81a2c328a161", false},               // a key not UTF-8
		{"8101a161", false},                   // an int key
		{"81c401 61a161", false},              // a bin key
		{"81c3a161", false},                   // a bool key
		{"81a1618101a162", false},             // an int key, deeper
		{"81a16191c0", false},                 // a nil, deeper
		{"81a16181a162c0", false},             // a nil as a map's value, deeper
		{"82a16101a16102", false},             // a key given twice
		{"82a16101d9016102", false},           // as a fixstr, then a str8
		{"82a16181a16101a16201", true},        // one name in two maps
		{"83a16101a16281a16101a16102", false}, // twice, a map between
		{"de0028a36b303001a36b303101a36b303201a36b303301a36b303401a36b303501a36b303601a36b303701a36b303801a36b303901a36b313001a36b313101a36b313201a36b313301a36b313401a36b313501a36b313601a36b313701a36b313801a36b313901a36b323001a36b323101a36b323201a36b323301a36b323401a36b323501a36b323601a36b323701a36b323801a36b323901a36b333001a36b333101a36b333201a36b333301a36b333401a36b333501a36b333601a36b333701a36b333801a36b333901", true},  // 40 keys, each once
		{"de0028a36b303001a36b303101a36b303201a36b303301a36b303401a36b303501a36b303601a36b303701a36b303801a36b303901a36b313001a36b313101a36b313201a36b313301a36b313401a36b313501a36b313601a36b313701a36b313801a36b313901a36b323001a36b323101a36b323201a36b323301a36b323401a36b323501a36b323601a36b323701a36b323801a36b323901a36b333001a36b333101a36b333201a36b333301a36b333401a36b333501a36b333601a36b333701a36b333801a36b303101", false}, // the 40th given before
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
		b, err := hex.DecodeString(strings.ReplaceAll(c.hex, " ", ""))
		if err != nil {
			t.Fatal(c.hex, err)
		}
		if got := wellFormed(b); got != c.want {
			t.Errorf("%s: %v, want %v", c.hex, got, c.want)
		}
	}
}

// What this package writes is a body a host may send: empty or full, no
// field is nil.
func TestEncodedBodiesWellFormed(t *testing.T) {
	href, yes, one := "#a", true, 1
	area := &Area{Col: -1, Row: 2, W: 3, H: 4}
	for _, v := range []interface {
		EncodeMsg(*msgp.Writer) error
	}{
		&Caps{}, &Caps{V: Version, Ops: []string{"text"}, Cell: &Cell{W: 9, H: 18}, Scale: 2,
			Limits: map[string]int{"surfaces": 8}, Net: map[string][]string{"img-src": nil}, Scroll: true},
		&errorBody{}, &errorBody{Code: EINVAL, Detail: "x"},
		&clickDetail{}, &clickDetail{Value: "v", Href: &href, URL: "u", Area: area},
		&pressDetail{}, &pressDetail{Area: area},
		&changeDetail{}, &changeDetail{Checked: &yes, Value: "on"},
		&inputDetail{}, &fitDetail{R: 3},
		&dragDetail{}, &dragDetail{C: 1, R: 2, Keys: []string{"shift"}, X: &one, Y: &one},
		&hoverDetail{}, &hoverDetail{C: &one, R: &one}, &hoverDetail{Out: true},
		&submitDetail{}, &submitDetail{"name": "Ada"},
		&Size{}, &Size{W: 720, H: 36},
	} {
		if b := encodeBody(v); !wellFormed(b) {
			t.Errorf("%T: % x is not a body a host sends", v, b)
		}
	}
}
