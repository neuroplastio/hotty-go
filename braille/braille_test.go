package braille

import "testing"

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
