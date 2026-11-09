package hottyedit

import "github.com/clipperhouse/displaywidth"

// Row is one visual row of a wrapped value: the characters from Start to
// End, not counting the line break that ends a hard row.
type Row struct {
	// Start and End are character indices: the row is the characters
	// [Start, End).
	Start, End int
	// Cols is the row's width in cells, hanging spaces included.
	Cols int
	// Soft is true when the row ends because the text wrapped, so that
	// the next row goes on from End; a hard row ends at a line break, or
	// at the end of the value, and End is the break's index.
	Soft bool
}

// Wrap breaks text into visual rows of at most width cells, as a field with
// that Width draws it: at each line break, and where a line is too wide,
// after its last space that fits, or anywhere once no space is left (a long
// URL). Spaces at the end of a row hang past the width rather than start
// the next row. A line that fills its last row to the width gets an empty
// row after it, so the caret at its end has a place inside the width. A
// character wider than width has a row of its own.
//
// The rows cover every character: the ones between rows are the line breaks
// after each hard row but the last. width < 1 wraps nothing: the rows are the
// lines. An empty text is one empty row.
func Wrap(text string, width int) []Row {
	return wrap(chars(text), width)
}

// Wrap is Wrap of the field's Value at its Width.
func (f *Field) Wrap() []Row { return wrap(chars(f.Value), f.Width) }

// wrap is Wrap, for text already in characters.
func wrap(c []string, width int) []Row {
	rows := make([]Row, 0, 4)
	start := 0
	for i := 0; i <= len(c); i++ {
		if i < len(c) && !isBreak(c[i]) {
			continue
		}
		rows = wrapLine(rows, c, start, i, width)
		start = i + 1
	}
	return rows
}

// wrapLine adds the rows of the line c[s:e].
func wrapLine(rows []Row, c []string, s, e, width int) []Row {
	if width < 1 {
		return append(rows, Row{s, e, cells(c[s:e]), false})
	}
	rs, cur, lastBreak := s, 0, s
	for i := s; i < e; i++ {
		w := cellWidth(c[i])
		if isSpace(c[i]) {
			cur += w
			lastBreak = i + 1
			continue
		}
		for cur+w > width && i > rs {
			at := lastBreak
			if at <= rs {
				at = i
			}
			rows = append(rows, Row{rs, at, cells(c[rs:at]), true})
			rs = at
			cur = cells(c[rs:i])
		}
		cur += w
	}
	// A last row that fills the width has the caret's place after it.
	rows = append(rows, Row{rs, e, cur, cur >= width && e > rs})
	if cur >= width && e > rs {
		rows = append(rows, Row{e, e, 0, false})
	}
	return rows
}

// cellWidth is a character's width in cells.
func cellWidth(ch string) int { return displaywidth.String(ch) }

// cells is the width of characters in cells.
func cells(c []string) (n int) {
	for _, ch := range c {
		n += cellWidth(ch)
	}
	return n
}

// rowOf is the index of the row the caret at p is on: the one whose start
// it is at or after, and whose end it is before; a hard row's end is its
// own.
func rowOf(rows []Row, p int) int {
	for i, r := range rows {
		if p < r.End || !r.Soft && p == r.End {
			return i
		}
	}
	return len(rows) - 1
}

// colAt is the column in cells of the caret at p on row r.
func colAt(r Row, c []string, p int) int { return cells(c[r.Start:min(max(p, r.Start), r.End)]) }

// posAt is the position on row r as near as it can be to a column without
// passing it. A row that wrapped has no position at its end: that is the
// next row's start.
func posAt(r Row, c []string, col int) int {
	hi := r.End
	if r.Soft {
		hi = max(r.End-1, r.Start)
	}
	p, x := r.Start, 0
	for p < hi {
		w := cellWidth(c[p])
		if x+w > col {
			break
		}
		x += w
		p++
	}
	return p
}

// rowEnd is where End takes the caret on row r: its end, or on a row that
// wrapped, after the last word, or before the last character when it has no
// space.
func rowEnd(r Row, c []string) int {
	if !r.Soft {
		return r.End
	}
	p := r.End
	for p > r.Start && isSpace(c[p-1]) {
		p--
	}
	if p == r.End {
		p = max(r.End-1, r.Start)
	}
	return p
}

// Cursor is the visual row and the column, in cells, of the caret in the
// rows Wrap makes: where a rendition puts the terminal cursor. The column
// stays inside the width, whatever hangs past it.
func (f *Field) Cursor() (row, col int) {
	c := chars(f.Value)
	p := min(max(f.Caret, 0), len(c))
	rows := wrap(c, f.Width)
	row = rowOf(rows, p)
	col = colAt(rows[row], c, p)
	if f.Width > 0 {
		col = min(col, f.Width-1)
	}
	return row, col
}

// Scroll is the scroll offset in rows, the first row shown, for a view of
// Rows rows (1 when less) that keeps the caret's row in view: top when it
// is, and otherwise the nearest that does. The offset stays within the rows,
// so that the view is full when there are enough.
func (f *Field) Scroll(top int) int {
	row, _ := f.Cursor()
	n := len(wrap(chars(f.Value), f.Width))
	view := max(f.Rows, 1)
	top = min(top, row)
	top = max(top, row-view+1)
	top = min(top, n-view)
	return max(top, 0)
}
