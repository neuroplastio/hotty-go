package braille_test

import (
	"fmt"

	"github.com/neuroplastio/hotty-go/braille"
)

// A line chart in cells, for a terminal that is not a HOTTY host: values
// scaled to the canvas's dots, joined by lines, a row of text per row of
// cells. Row's style wraps runs of one ink, in a colour say.
func Example_lineChart() {
	values := []float64{3, 5, 4, 8, 6, 9, 7, 10}
	c := braille.New(16, 3) // 32×12 dots
	w, h := c.Size()
	lo, hi := 2.0, 10.0
	x := func(i int) int { return i * (w - 1) / (len(values) - 1) }
	y := func(v float64) int { return int(float64(h-1) * (1 - (v-lo)/(hi-lo))) }
	for i := 1; i < len(values); i++ {
		c.Line(x(i-1), y(values[i-1]), x(i), y(values[i]), 1)
	}
	for row := range 3 {
		fmt.Printf("|%s|\n", c.Row(row, nil))
	}
	// Output:
	// |      ⡠⡀ ⢀⡠⠢⣀⢀⠤⠊|
	// | ⣀⢄⡀⢀⠜ ⠈⠑⠁   ⠁  |
	// |⠊  ⠈⠁           |
}
