package hottytest

import (
	"slices"
	"strconv"
	"strings"
)

// screen is the cells, as far as a test needs them: lines of runes, a
// cursor, and the sequences that move it, erase, insert, delete and
// scroll. Every rune is one cell wide; colours and other attributes are
// dropped.
//
// The main screen's lines grow downward without limit: the rows lines from
// top are on view, and those above them are the scrollback. The alternate
// screen (fixed) is rows lines, and what scrolls off it is lost.
type screen struct {
	lines      [][]rune
	top        int // the first line on view
	col, row   int // the cursor; row is an index in lines
	cols, rows int
	fixed      bool   // the alternate screen
	saved      [2]int // DECSC: the cursor's column and row on view
	margins    [2]int // DECSTBM: the scrolling region's first and last rows on view; both 0 for all of them
	last       rune   // the last rune put, for REP
}

// line is the cursor's line, made if it was not yet.
func (s *screen) line() []rune { return s.lineAt(s.row) }

func (s *screen) lineAt(i int) []rune {
	for len(s.lines) <= i {
		s.lines = append(s.lines, nil)
	}
	return s.lines[i]
}

func (s *screen) put(r rune) {
	if s.col >= s.cols {
		s.index()
		s.col = 0
	}
	l := s.line()
	for len(l) < s.col {
		l = append(l, ' ')
	}
	if s.col < len(l) {
		l[s.col] = r
	} else {
		l = append(l, r)
	}
	s.lines[s.row] = l
	s.col++
	s.last = r
}

func (s *screen) control(c byte) {
	switch c {
	case '\r':
		s.col = 0
	case '\n', '\v', '\f':
		// A terminal maps LF to CR LF when it is not raw (ONLCR); a
		// program in raw mode writes CR LF itself. Either way a line
		// starts.
		s.index()
		s.col = 0
	case '\b':
		s.col = max(0, min(s.col, s.cols-1)-1)
	case '\t':
		s.col = min((s.col/8+1)*8, s.cols-1)
	}
}

// region is the scrolling region's first and last rows on view.
func (s *screen) region() (first, last int) {
	if s.margins == [2]int{} {
		return 0, s.rows - 1
	}
	return s.margins[0], s.margins[1]
}

// index is IND: down a row, scrolling the region up at its last row.
func (s *screen) index() {
	_, last := s.region()
	switch v := s.row - s.top; {
	case v == last:
		s.scrollUp(1)
	case v < s.rows-1:
		s.row++
	}
	s.line()
}

// reverseIndex is RI: up a row, scrolling the region down at its first.
func (s *screen) reverseIndex() {
	first, _ := s.region()
	switch v := s.row - s.top; {
	case v == first:
		s.scrollDown(1)
	case v > 0:
		s.row--
	}
}

// scrollUp moves the region's lines up n rows. On the main screen, when
// the region is all of it, the lines leave the view for the scrollback,
// and the cursor stays on its row on view.
func (s *screen) scrollUp(n int) {
	first, last := s.region()
	if !s.fixed && first == 0 && last == s.rows-1 {
		s.top += n
		s.row += n
		s.lineAt(s.top + s.rows - 1)
		return
	}
	s.deleteLines(s.top+first, s.top+last, n)
}

// scrollDown moves the region's lines down n rows: blank lines come in at
// its top, and those pushed past its bottom are lost.
func (s *screen) scrollDown(n int) {
	first, last := s.region()
	s.insertLines(s.top+first, s.top+last, n)
}

// insertLines inserts n blank lines at line a, in the lines a to b: those
// pushed past b are lost.
func (s *screen) insertLines(a, b, n int) {
	s.lineAt(b)
	n = min(n, b-a+1)
	copy(s.lines[a+n:b+1], s.lines[a:b+1-n])
	for i := a; i < a+n; i++ {
		s.lines[i] = nil
	}
}

// deleteLines deletes n lines at line a, in the lines a to b: blank lines
// come in at b.
func (s *screen) deleteLines(a, b, n int) {
	s.lineAt(b)
	n = min(n, b-a+1)
	copy(s.lines[a:b+1-n], s.lines[a+n:b+1])
	for i := b + 1 - n; i <= b; i++ {
		s.lines[i] = nil
	}
}

// moveTo puts the cursor at a row on view and a column, within the screen.
func (s *screen) moveTo(row, col int) {
	s.row = s.top + min(max(row, 0), s.rows-1)
	s.col = min(max(col, 0), s.cols-1)
	s.line()
}

// erase blanks the cursor's line from column a to before b.
func (s *screen) erase(a, b int) {
	l := s.line()
	if b >= len(l) {
		s.lines[s.row] = l[:min(a, len(l))]
		return
	}
	for i := a; i < b; i++ {
		l[i] = ' '
	}
}

// csi carries out the CSI sequences that move the cursor, erase, insert,
// delete and scroll.
func (s *screen) csi(params string, final byte) {
	if strings.HasPrefix(params, "?") || strings.HasPrefix(params, ">") || strings.HasPrefix(params, "=") ||
		strings.HasPrefix(params, "<") {
		return
	}
	nums := []int{}
	for p := range strings.SplitSeq(params, ";") {
		n, _ := strconv.Atoi(p)
		nums = append(nums, n)
	}
	arg := func(i, def int) int {
		if i < len(nums) && nums[i] > 0 {
			return nums[i]
		}
		return def
	}
	n := arg(0, 1)
	s.line()
	v := s.row - s.top // the cursor's row on view
	switch final {
	case 'A':
		s.moveTo(v-n, s.col)
	case 'B', 'e':
		s.moveTo(v+n, s.col)
	case 'C', 'a':
		s.moveTo(v, s.col+n)
	case 'D':
		s.moveTo(v, min(s.col, s.cols-1)-n)
	case 'E':
		s.moveTo(v+n, 0)
	case 'F':
		s.moveTo(v-n, 0)
	case 'G', '`':
		s.moveTo(v, n-1)
	case 'd':
		s.moveTo(n-1, s.col)
	case 'H', 'f':
		s.moveTo(arg(0, 1)-1, arg(1, 1)-1)
	case 'I':
		s.col = min((s.col/8+n)*8, s.cols-1)
	case 'K':
		switch arg(0, 0) {
		case 0:
			s.erase(s.col, s.cols)
		case 1:
			s.erase(0, s.col+1)
		case 2:
			s.lines[s.row] = nil
		}
	case 'J':
		switch arg(0, 0) {
		case 0:
			s.erase(s.col, s.cols)
			for i := s.row + 1; i < min(len(s.lines), s.top+s.rows); i++ {
				s.lines[i] = nil
			}
		case 1:
			s.erase(0, s.col+1)
			for i := s.top; i < s.row; i++ {
				s.lines[i] = nil
			}
		case 2:
			for i := s.top; i < min(len(s.lines), s.top+s.rows); i++ {
				s.lines[i] = nil
			}
		case 3:
			s.lines = s.lines[s.top:]
			s.row -= s.top
			s.top = 0
		}
	case 'X':
		s.erase(s.col, min(s.col+n, s.cols))
	case '@':
		l := s.line()
		if s.col < len(l) {
			l = slices.Insert(l, s.col, []rune(strings.Repeat(" ", n))...)
			s.lines[s.row] = l[:min(len(l), s.cols)]
		}
	case 'P':
		l := s.line()
		if s.col < len(l) {
			s.lines[s.row] = slices.Delete(l, s.col, min(s.col+n, len(l)))
		}
	case 'L', 'M':
		if first, last := s.region(); v >= first && v <= last {
			if final == 'L' {
				s.insertLines(s.row, s.top+last, n)
			} else {
				s.deleteLines(s.row, s.top+last, n)
			}
			s.col = 0
		}
	case 'S':
		s.scrollUp(n)
	case 'T':
		s.scrollDown(n)
	case 'b':
		if s.last != 0 {
			for range min(n, s.cols*s.rows) {
				s.put(s.last)
			}
		}
	case 'r':
		first, last := arg(0, 1)-1, arg(1, s.rows)-1
		if first < last && last < s.rows {
			s.margins = [2]int{first, last}
			if first == 0 && last == s.rows-1 {
				s.margins = [2]int{}
			}
			s.moveTo(0, 0)
		}
	case 's':
		if params == "" {
			s.save()
		}
	case 'u':
		s.restore()
	}
	s.line()
}

// save is DECSC: the cursor's place on view.
func (s *screen) save() { s.saved = [2]int{s.col, s.row - s.top} }

// restore is DECRC.
func (s *screen) restore() { s.moveTo(s.saved[1], s.saved[0]) }

// String is the screen as text: a line a row, trailing spaces and empty
// rows at the end left out.
func (s *screen) String() string {
	out := make([]string, len(s.lines))
	for i, l := range s.lines {
		out[i] = strings.TrimRight(string(l), " ")
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
