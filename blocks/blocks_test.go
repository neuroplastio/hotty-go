package blocks

import "testing"

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
