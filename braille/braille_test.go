package braille

import (
	"math"
	"testing"
	"time"
)

func TestDots(t *testing.T) {
	c := New(2, 1)
	for y := range 4 {
		c.Set(0, y, 1) // the left column of the first cell
	}
	c.Set(3, 3, 2) // the bottom-right dot of the second
	c.Set(9, 9, 1) // outside: ignored
	if got := c.String(); got != "⡇⢀" {
		t.Errorf("got %q", got)
	}
	if _, ink := c.Cell(1, 0); ink != 2 {
		t.Errorf("ink %d", ink)
	}
	if w, h := c.Size(); w != 4 || h != 4 {
		t.Errorf("size %d×%d", w, h)
	}
}

func TestLine(t *testing.T) {
	c := New(4, 2)
	w, h := c.Size()
	c.Line(0, h-1, w-1, 0, 1) // a diagonal, bottom-left to top-right
	want := "  ⡠⠊\n⡠⠊  "
	if got := c.String(); got != want {
		t.Errorf("diagonal:\n%s\nwant:\n%s", got, want)
	}
	// Every column has a dot: no gaps in a steep or a shallow line.
	c = New(10, 1)
	c.Line(0, 0, 19, 3, 1)
	for col := range 10 {
		if r, _ := c.Cell(col, 0); r == ' ' {
			t.Errorf("column %d is empty", col)
		}
	}
}

func TestRowStyles(t *testing.T) {
	c := New(4, 1)
	c.Set(0, 0, 1)
	c.Set(2, 0, 1)
	c.Set(6, 0, 2)
	got := c.Row(0, func(ink uint8, s string) string { return "<" + string('0'+ink) + ">" + s + "</>" })
	// A blank cell after a run stays in it: fewer styles to print.
	if want := "<1>⠁⠁ </><2>⠁</>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A line with an end far outside the canvas, as a NaN or an overflow makes
// one, walked every dot of its length: a hang. The part inside is drawn.
func TestLineFarOutside(t *testing.T) {
	done := make(chan string)
	go func() {
		c := New(4, 2)
		c.Line(0, 0, 1<<40, 1<<40, 1)     // down and right, off the corner
		c.Line(0, 7, math.MinInt, 7, 2)   // from the bottom-left dot to the far left
		c.Line(-1<<50, -5, -1<<49, -5, 3) // all of it outside
		done <- c.String()
	}()
	select {
	case got := <-done:
		// The diagonal crosses the first rows' top-left dots; the bottom
		// row keeps its first dot.
		if want := "⠑⢄  \n⡀ ⠑⢄"; got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("Line walked a line far outside the canvas")
	}
}

// A line that leaves the canvas nearby is not cut: it has the dots it has
// on a canvas large enough to hold it, where nothing is cut.
func TestLineNearOutside(t *testing.T) {
	small := New(4, 2) // 8×8 dots
	small.Line(-3, -2, 9, 9, 1)
	// The same line on a larger canvas, moved by 5 cells across and 3
	// down (10 and 12 dots), lies inside it.
	big := New(20, 10)
	big.Line(-3+10, -2+12, 9+10, 9+12, 1)
	for col := range 4 {
		for row := range 2 {
			a, _ := small.Cell(col, row)
			b, _ := big.Cell(col+5, row+3)
			if a != b {
				t.Errorf("cell %d,%d: %q, want %q", col, row, a, b)
			}
		}
	}
}

func TestEmptyAndCells(t *testing.T) {
	c := New(-1, 3)
	if cols, rows := c.Cells(); cols != 0 || rows != 3 {
		t.Errorf("Cells = %d×%d", cols, rows)
	}
	c.Line(0, 0, 5, 5, 1) // nowhere to draw: nothing happens
	if got := c.String(); got != "\n\n" {
		t.Errorf("an empty canvas: %q", got)
	}
	if r, ink := New(2, 2).Cell(5, 0); r != ' ' || ink != 0 {
		t.Errorf("a cell outside: %q %d", r, ink)
	}
}
