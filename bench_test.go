package hotty

import (
	"fmt"
	"strings"
	"testing"
)

// markup is n bytes of rows that differ, as a program's documents and
// deltas do: they compress about as well as real ones.
func markup(n int) []byte {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, `<div id="r%d" class="row"><span class="k">key %d</span><span class="v">%d</span></div>`, i, i, i*7919%1000)
	}
	return []byte(b.String()[:n])
}

// BenchmarkEncode encodes payloads of the sizes a program sends: deltas
// and documents of 1 to 30 KB, and a stylesheet of 236 KB. b.SetBytes
// counts the payload; the wire bytes are reported as wire/op.
func BenchmarkEncode(b *testing.B) {
	for _, n := range []int{1 << 10, 3 << 10, 6 << 10, 30 << 10, 236 << 10} {
		p := markup(n)
		ctl := Control{{"a", "delta"}, {"s", "card"}}
		b.Run(fmt.Sprintf("%dKB", n>>10), func(b *testing.B) {
			b.SetBytes(int64(n))
			b.ReportAllocs()
			var wire int
			for b.Loop() {
				wire = len(Encode(ctl, p))
			}
			b.ReportMetric(float64(wire), "wire/op")
		})
	}
}
