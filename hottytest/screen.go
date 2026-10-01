package hottytest

import (
	"strconv"
	"strings"
)

// screen is the cells, as far as a test needs them: lines of runes that
// grow downward without limit (the scrollback is the screen), a cursor,
// and the sequences that move it or erase. Every rune is one cell wide;
// colours and other attributes are dropped.
type screen struct {
	lines    [][]rune
	col, row int
	cols     int
	saved    [2]int
}

func (s *screen) line() []rune {
	for len(s.lines) <= s.row {
		s.lines = append(s.lines, nil)
	}
	return s.lines[s.row]
}

func (s *screen) put(r rune) {
	l := s.line()
	if s.cols > 0 && s.col >= s.cols {
		s.row++
		s.col = 0
		l = s.line()
	}
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
}

func (s *screen) control(c byte) {
	switch c {
	case '\r':
		s.col = 0
	case '\n':
		// A terminal maps LF to CR LF when it is not raw (ONLCR); a
		// program in raw mode writes CR LF itself. Either way a line
		// starts.
		s.row++
		s.col = 0
		s.line()
	case '\b':
		s.col = max(0, s.col-1)
	case '\t':
		s.col = (s.col/8 + 1) * 8
	}
}

// csi carries out the CSI sequences that move the cursor or erase.
func (s *screen) csi(params string, final byte) {
	if strings.HasPrefix(params, "?") || strings.HasPrefix(params, ">") || strings.HasPrefix(params, "=") {
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
	switch final {
	case 'A':
		s.row = max(0, s.row-arg(0, 1))
	case 'B':
		s.row += arg(0, 1)
	case 'C':
		s.col += arg(0, 1)
	case 'D':
		s.col = max(0, s.col-arg(0, 1))
	case 'E':
		s.row += arg(0, 1)
		s.col = 0
	case 'F':
		s.row = max(0, s.row-arg(0, 1))
		s.col = 0
	case 'G':
		s.col = arg(0, 1) - 1
	case 'H', 'f':
		s.row, s.col = arg(0, 1)-1, arg(1, 1)-1
	case 'K':
		l := s.line()
		switch arg(0, 0) {
		case 0:
			if s.col < len(l) {
				s.lines[s.row] = l[:s.col]
			}
		case 1:
			for i := 0; i < min(s.col+1, len(l)); i++ {
				l[i] = ' '
			}
		case 2:
			s.lines[s.row] = nil
		}
	case 'J':
		switch arg(0, 0) {
		case 0:
			s.csi("", 'K')
			if s.row+1 < len(s.lines) {
				s.lines = s.lines[:s.row+1]
			}
		case 2, 3:
			s.lines = nil
		}
	}
	s.line()
}

// String is the screen as text: a line a row, trailing spaces and empty
// rows at the end left out.
func (s *screen) String() string {
	out := make([]string, len(s.lines))
	for i, l := range s.lines {
		out[i] = strings.TrimRight(string(l), " ")
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
