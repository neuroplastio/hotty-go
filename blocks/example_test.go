package blocks_test

import (
	"fmt"
	"strings"

	"github.com/neuroplastio/hotty-go/blocks"
)

// A histogram in cells: each bin a column, its height in eighths of a row,
// scaled so the fullest bin fills the chart.
func Example_histogram() {
	counts := []float64{2, 5, 9, 14, 11, 6, 3, 1}
	const rows = 4
	peak := 14.0
	cols := make([][]rune, len(counts))
	for i, n := range counts {
		cols[i] = blocks.Column(n/peak*rows, rows)
	}
	for r := range rows {
		var line strings.Builder
		for _, col := range cols {
			line.WriteRune(col[r])
			line.WriteRune(col[r]) // two cells a bin
		}
		fmt.Printf("|%s|\n", line.String())
	}
	// Output:
	// |      ██▁▁      |
	// |    ▅▅████      |
	// |  ▃▃██████▆▆    |
	// |▅▅██████████▇▇▂▂|
}
