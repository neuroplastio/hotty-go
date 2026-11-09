package hottyedit

import (
	"math/rand"
	"strings"
	"testing"

	"neuroplast.io/hotty-go"
)

// rowText is the text of the rows, as Wrap's doc says they cover it.
func rowText(text string, rows []Row) []string {
	c := chars(text)
	var out []string
	for _, r := range rows {
		out = append(out, strings.Join(c[r.Start:r.End], ""))
	}
	return out
}

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		width      int
		want       []string
	}{
		{"empty", "", 5, []string{""}},
		{"fits", "hello", 6, []string{"hello"}},
		{"at spaces", "aaaa bbbb cccc", 8, []string{"aaaa ", "bbbb ", "cccc"}},
		{"spaces that fit stay", "aaa bbb ccc", 7, []string{"aaa bbb ", "ccc"}},
		{"hanging space", "aaaa bbbb", 4, []string{"aaaa ", "bbbb", ""}},
		{"long word", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"url after words", "see http://example.com/long", 10, []string{"see ", "http://exa", "mple.com/l", "ong"}},
		{"lines", "ab\n\ncd", 5, []string{"ab", "", "cd"}},
		{"full row gets a place for the caret", "abcd", 4, []string{"abcd", ""}},
		{"wide", "世界世界", 4, []string{"世界", "世界", ""}},
		{"wide wider than width", "世a", 1, []string{"世", "a", ""}},
		{"cluster kept whole", "e\u0301e\u0301e\u0301", 2, []string{"e\u0301e\u0301", "e\u0301"}},
		{"emoji cluster", "👨‍👩‍👧👨‍👩‍👧", 2, []string{"👨‍👩‍👧", "👨‍👩‍👧", ""}},
		{"no wrap", "a b c\nd", 0, []string{"a b c", "d"}},
		{"crlf", "ab\r\ncd", 9, []string{"ab", "cd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rowText(tc.text, Wrap(tc.text, tc.width))
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("Wrap(%q, %d) = %q, want %q", tc.text, tc.width, got, tc.want)
			}
		})
	}
}

const alphabet = "ab c\n  世e\u0301👨‍👩‍👧\r\nxyz-_/"

func randomText(r *rand.Rand) string {
	var b strings.Builder
	parts := chars(alphabet)
	for range r.Intn(40) {
		b.WriteString(parts[r.Intn(len(parts))])
	}
	return b.String()
}

// Wrapping and putting the rows back, with their line breaks, is the text;
// the rows are in order, whole, and none is wider than the width but for
// spaces that hang.
func TestWrapProperties(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for range 3000 {
		text, width := randomText(r), r.Intn(9)
		c := chars(text)
		rows := Wrap(text, width)
		if len(rows) == 0 {
			t.Fatalf("no rows for %q", text)
		}
		var b strings.Builder
		at := 0
		for i, row := range rows {
			if row.Start != at || row.End < row.Start {
				t.Fatalf("%q width %d: row %d is %v, expected a start at %d", text, width, i, row, at)
			}
			b.WriteString(strings.Join(c[row.Start:row.End], ""))
			at = row.End
			if !row.Soft && i < len(rows)-1 {
				b.WriteString(c[at])
				at++
			}
			if row.Cols != cells(c[row.Start:row.End]) {
				t.Fatalf("%q: row %v has Cols wrong", text, row)
			}
			// Only spaces that hang, or a lone character, pass the width.
			if tr := chars(strings.TrimRight(strings.Join(c[row.Start:row.End], ""), " ")); width > 0 && len(tr) > 1 && cells(tr) > width {
				t.Fatalf("%q width %d: row %v is too wide", text, width, row)
			}
		}
		if at != len(c) || b.String() != text {
			t.Fatalf("%q width %d: rows put back are %q", text, width, b.String())
		}
	}
}

// The caret stays on a character boundary, in the field, and on a valid row,
// whatever keys come, and a goal column survives short rows.
func TestMovesProperties(t *testing.T) {
	km := hotty.Resolve(hotty.Platform{}, true, hotty.TerminalKeys)
	keys := []string{"left", "right", "up", "down", "home", "end", "pageup", "pagedown", "shift+left", "shift+up", "shift+down", "shift+right", "ctrl+w", "backspace", "delete", "a", "space", "enter", "ctrl+z", "ctrl+y", "alt+left", "alt+right"}
	r := rand.New(rand.NewSource(2))
	for range 500 {
		f := Field{Value: randomText(r), Multiline: true, Width: 1 + r.Intn(8), Rows: 1 + r.Intn(4)}
		f.Caret = r.Intn(len(chars(f.Value)) + 1)
		top := 0
		for range 40 {
			f.Key(km, keys[r.Intn(len(keys))])
			n := len(chars(f.Value))
			if f.Caret < 0 || f.Caret > n {
				t.Fatalf("caret %d outside 0..%d in %q", f.Caret, n, f.Value)
			}
			row, col := f.Cursor()
			rows := f.Wrap()
			if row < 0 || row >= len(rows) || col < 0 || col >= f.Width {
				t.Fatalf("cursor %d,%d outside %d rows of width %d", row, col, len(rows), f.Width)
			}
			if p := rows[row]; f.Caret < p.Start || f.Caret > p.End {
				t.Fatalf("caret %d is not on row %v", f.Caret, p)
			}
			top = f.Scroll(top)
			if top < 0 || row < top || row >= top+f.Rows {
				t.Fatalf("scroll %d does not show row %d of %d", top, row, f.Rows)
			}
		}
	}
}

func TestHomeEndByRow(t *testing.T) {
	f := Field{Value: "aaaa bbbb cccc", Multiline: true, Width: 8, Caret: 7}
	key(t, &f, "home")
	if f.Caret != 5 {
		t.Fatalf("home: %d, want the wrapped row's start 5", f.Caret)
	}
	key(t, &f, "end")
	if f.Caret != 9 {
		t.Fatalf("end on a wrapped row: %d, want after its last word, 9", f.Caret)
	}
	key(t, &f, "down", "end")
	if f.Caret != 14 {
		t.Fatalf("end: %d, want 14", f.Caret)
	}
	u := Field{Value: "abcdefgh", Multiline: true, Width: 4, Caret: 8}
	key(t, &u, "end")
	if u.Caret != 8 {
		t.Fatal("end on the last row", u.Caret)
	}
	key(t, &u, "left", "home", "up", "end")
	if u.Caret != 3 {
		t.Fatalf("end on a row with no space: %d, want 3 (4 is the next row's)", u.Caret)
	}
}

func TestUpDownGoalColumn(t *testing.T) {
	f := Field{Value: "abcdef\nx\nabcdef", Multiline: true, Width: 20, Caret: 5}
	key(t, &f, "down")
	if f.Caret != 8 {
		t.Fatalf("down to the short row: %d", f.Caret)
	}
	key(t, &f, "down")
	if f.Caret != 14 {
		t.Fatalf("the goal column 5 comes back: %d, want 14", f.Caret)
	}
	key(t, &f, "up", "up")
	if f.Caret != 5 {
		t.Fatalf("and back up: %d", f.Caret)
	}
	key(t, &f, "up")
	if f.Caret != 0 {
		t.Fatalf("past the first row: %d", f.Caret)
	}
	key(t, &f, "down", "down", "down")
	if f.Caret != 15 {
		t.Fatalf("past the last row: %d", f.Caret)
	}

	// Across wrapped rows, in cells.
	w := Field{Value: "世界世界世界", Multiline: true, Width: 4, Caret: 1}
	key(t, &w, "down")
	if row, col := w.Cursor(); row != 1 || col != 2 || w.Caret != 3 {
		t.Fatalf("cursor %d,%d caret %d", row, col, w.Caret)
	}
}

func TestCursorAndScroll(t *testing.T) {
	f := Field{Value: "aaaa bbbb cccc dddd", Multiline: true, Width: 8, Rows: 2, Caret: 19}
	row, col := f.Cursor()
	if row != 3 || col != 4 {
		t.Fatalf("cursor %d,%d, want 3,4", row, col)
	}
	top := f.Scroll(0)
	if top != 2 {
		t.Fatalf("scroll %d, want 2", top)
	}
	if f.Scroll(top) != 2 {
		t.Fatal("scroll moved while the caret is in view")
	}
	f.Caret = 0
	if f.Scroll(top) != 0 {
		t.Fatal("scroll did not come back")
	}
	// The caret at the end of a full row is on the empty row after it.
	g := Field{Value: "abcd", Width: 4, Caret: 4}
	if row, col := g.Cursor(); row != 1 || col != 0 {
		t.Fatalf("full row: %d,%d", row, col)
	}
	// Spaces that hang keep the column inside the width.
	h := Field{Value: "ab       cd", Width: 4, Caret: 6}
	if _, col := h.Cursor(); col != 3 {
		t.Fatalf("hanging: col %d", col)
	}
}

func TestSelectAcrossRows(t *testing.T) {
	f := Field{Value: "aaaa bbbb cccc", Multiline: true, Width: 8, Caret: 2}
	key(t, &f, "shift+down")
	if s, e := f.Selection(); s != 2 || e != 7 {
		t.Fatalf("selection %d..%d", s, e)
	}
	key(t, &f, "shift+up")
	if _, ok := f.Anchor(); ok {
		t.Fatal("back at the anchor selects nothing")
	}
	key(t, &f, "shift+down", "shift+end")
	if s, e := f.Selection(); s != 2 || e != 9 {
		t.Fatalf("selection %d..%d", s, e)
	}
	f.Type("Z")
	if f.Value != "aaZ cccc" {
		t.Fatalf("%q", f.Value)
	}
}
