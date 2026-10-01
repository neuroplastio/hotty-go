package blocks

import (
	"math"
	"testing"
)

func TestColumn(t *testing.T) {
	for _, c := range []struct {
		h    float64
		want string
	}{
		{0, "   "},
		{0.01, "  ▁"}, // above 0 shows
		{0.5, "  ▄"},
		{1, "  █"},
		{1.25, " ▂█"},
		{2.99, "███"},
		{9, "███"},
	} {
		if got := string(Column(c.h, 3)); got != c.want {
			t.Errorf("%v: %q, want %q", c.h, got, c.want)
		}
	}
}

// An infinite or huge height overflowed the int it was rounded to, and
// drew an eighth or nothing; a column of no rows panicked.
func TestColumnOutOfRange(t *testing.T) {
	for _, c := range []struct {
		h    float64
		want string
	}{
		{math.Inf(1), "███"},
		{1e300, "███"},
		{math.Inf(-1), "   "},
		{math.NaN(), "   "},
		{-2, "   "},
	} {
		if got := string(Column(c.h, 3)); got != c.want {
			t.Errorf("%v: %q, want %q", c.h, got, c.want)
		}
	}
	if got := Column(2, -1); len(got) != 0 {
		t.Errorf("a column of -1 rows: %q", string(got))
	}
}
