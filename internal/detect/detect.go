// Package detect holds what hottyterm and hottytea agree on when they ask a
// terminal whether it is a HOTTY host (SPEC §4): which reply answers the
// query, and how long to wait for it.
package detect

import (
	"time"

	"github.com/neuroplastio/hotty-go"
)

// N numbers the query: a reply that does not echo it answers someone
// else's.
const N = 1

// How long to wait: for any answer at all; after the terminal's DA1 answer,
// which every terminal sends, for a HOTTY reply that may still be on its
// way (the DA1 may answer an earlier question); and after a host's reply,
// for the DA1 answer behind it, which would otherwise be left for whoever
// reads the terminal next.
const (
	Timeout    = 1500 * time.Millisecond
	AfterDA1   = 150 * time.Millisecond
	AfterReply = 300 * time.Millisecond
)

// Answer reports whether r answers the query numbered n, with the host's
// capabilities.
func Answer(r hotty.Reply, n int) (hotty.Caps, bool) {
	if r.N != n {
		return hotty.Caps{}, false
	}
	return r.Caps()
}
