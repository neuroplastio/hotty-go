// Package blocks draws bars in terminal cells with the block elements
// (U+2581–U+2588): a column's height in eighths of a row, as a bar chart or a
// histogram in text has it. Lines are better drawn with braille dots
// (package braille).
package blocks

import "math"

var eighths = []rune(" ▁▂▃▄▅▆▇█")

// Bar is the cell at row (counted from 0 at the bottom) of a bar h rows
// high: a full block below its top, the eighth that reaches its top, and a
// space above. A bar above 0 shows at least an eighth, so a small count is
// not taken for none, and an infinite h is a full column.
func Bar(h float64, row int) rune {
	if h <= 0 || math.IsNaN(h) {
		return ' '
	}
	// In floats until it is between 0 and 8: a huge h would overflow an
	// int.
	e := math.Max(0, math.Min(8, math.Round((h-float64(row))*8)))
	if row == 0 {
		e = math.Max(e, 1)
	}
	return eighths[int(e)]
}

// Column is a bar h rows high in a column of rows cells, top first; none
// when rows is 0 or less.
func Column(h float64, rows int) []rune {
	out := make([]rune, max(0, rows))
	for i := range out {
		out[i] = Bar(h, rows-1-i)
	}
	return out
}
