// Package braille draws in terminal cells with the Unicode braille patterns
// (U+2800–U+28FF): each cell is two dots across and four down, so a line
// chart in text has four times the rows and twice the columns of its cells.
//
// A Canvas is dots, each set with an ink: a small number the program maps to
// a colour when it prints (Row). A cell takes the ink of the last dot set in
// it.
package braille

import "strings"

// Canvas is cols×rows cells of dots. Dot (0, 0) is the top-left one.
type Canvas struct {
	cols, rows int
	bits       []uint8
	ink        []uint8
}

// New is an empty canvas of cols×rows cells.
func New(cols, rows int) *Canvas {
	cols, rows = max(0, cols), max(0, rows)
	return &Canvas{cols: cols, rows: rows, bits: make([]uint8, cols*rows), ink: make([]uint8, cols*rows)}
}

// Size is the canvas in dots: twice its columns, four times its rows.
func (c *Canvas) Size() (w, h int) { return 2 * c.cols, 4 * c.rows }

// Cells is the canvas in cells.
func (c *Canvas) Cells() (cols, rows int) { return c.cols, c.rows }

// The bit of each dot in a cell, by row and column (the braille order).
var dotBit = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// Set sets the dot at (x, y) with ink. Dots outside the canvas are ignored.
func (c *Canvas) Set(x, y int, ink uint8) {
	if x < 0 || y < 0 || x >= 2*c.cols || y >= 4*c.rows {
		return
	}
	i := (y/4)*c.cols + x/2
	c.bits[i] |= dotBit[y%4][x%2]
	c.ink[i] = ink
}

// Line sets the dots of a line from (x0, y0) to (x1, y1), both included.
func (c *Canvas) Line(x0, y0, x1, y1 int, ink uint8) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := sign(x1-x0), sign(y1-y0)
	e := dx + dy
	for {
		c.Set(x0, y0, ink)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x0 += sx
		}
		if e2 <= dx {
			e += dx
			y0 += sy
		}
	}
}

// Cell is the pattern of a cell and its ink; ink 0 and the rune ' ' when
// no dot is set.
func (c *Canvas) Cell(col, row int) (r rune, ink uint8) {
	if col < 0 || row < 0 || col >= c.cols || row >= c.rows {
		return ' ', 0
	}
	i := row*c.cols + col
	if c.bits[i] == 0 {
		return ' ', 0
	}
	return rune(0x2800 + int(c.bits[i])), c.ink[i]
}

// Row is one row of cells as text. style, if not nil, wraps each run of
// cells with the same ink (never 0) — in a colour, say.
func (c *Canvas) Row(row int, style func(ink uint8, s string) string) string {
	var out, run strings.Builder
	var cur uint8
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if cur != 0 && style != nil {
			out.WriteString(style(cur, run.String()))
		} else {
			out.WriteString(run.String())
		}
		run.Reset()
	}
	for col := range c.cols {
		r, ink := c.Cell(col, row)
		if ink != cur && r != ' ' {
			flush()
			cur = ink
		}
		run.WriteRune(r)
	}
	flush()
	return out.String()
}

// String is the canvas as text, a line a row, without styles.
func (c *Canvas) String() string {
	rows := make([]string, c.rows)
	for r := range rows {
		rows[r] = c.Row(r, nil)
	}
	return strings.Join(rows, "\n")
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}
