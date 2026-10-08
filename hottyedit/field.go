// Package hottyedit edits a text field as a HOTTY host edits one (SPEC
// §10.2), for a program that draws its fields in cells: the same actions,
// the same words and lines, the same caret.
//
// A program gives its surfaces a keymap in data-keys (hotty.TerminalKeys,
// say), and its cells rendition looks keys up in the same keymap, resolved
// with hotty.Resolve, and does what they say to a Field:
//
//	km := hotty.Resolve(false, hotty.TerminalKeys)
//	f := hottyedit.Field{Value: "foo bar"}
//	switch a, changed := f.Key(km, "Control+w"); {
//	case a == "":
//		// the program's key
//	case a == hotty.Submit:
//		// submit the form
//	case changed:
//		// report the input
//	}
//
// Characters are grapheme clusters (Unicode UAX #29): the caret counts
// them. A field in cells does not wrap, so its rows are its lines.
package hottyedit

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/neuroplastio/hotty-go"
)

// Field is a text field's value and caret.
type Field struct {
	// Value is the field's text.
	Value string
	// Caret is where the caret is: a count of characters from the start.
	// One past the end is the end.
	Caret int
	// Multiline is a textarea's or an editing host's: it has lines, and
	// the actions that only such fields have (hotty.Action.Multiline).
	Multiline bool
	// Password is a password input's: its value is one word.
	Password bool
	// Rows is how many rows the field shows, for page-up and page-down; 1
	// when less.
	Rows int

	// goal is the place along the row a run of row moves keeps, plus one;
	// 0 when no run is under way.
	goal int
}

// Do does an action, and reports whether the value changed: then the
// program reports an input event. Submit and Program change nothing; they
// are the program's to act on. An action only a multi-line field has does
// nothing in an input.
func (f *Field) Do(a hotty.Action) (changed bool) {
	c := chars(f.Value)
	p := min(max(f.Caret, 0), len(c))
	switch a {
	case hotty.LinePrevious, hotty.LineNext, hotty.PageUp, hotty.PageDown:
	default:
		f.goal = 0
	}
	if a.Multiline() && !f.Multiline {
		return false
	}
	start, end := f.line(c, p)
	rows := max(f.Rows, 1)
	switch a {
	case hotty.CharBackward:
		f.Caret = max(p-1, 0)
	case hotty.CharForward:
		f.Caret = min(p+1, len(c))
	case hotty.WordBackward:
		f.Caret = f.wordBackward(c, p)
	case hotty.WordForward:
		f.Caret = f.wordForward(c, p)
	case hotty.LineStart:
		f.Caret = start
	case hotty.LineEnd:
		f.Caret = end
	case hotty.LinePrevious:
		f.Caret = f.row(c, p, -1)
	case hotty.LineNext:
		f.Caret = f.row(c, p, 1)
	case hotty.PageUp:
		f.Caret = f.row(c, p, -rows)
	case hotty.PageDown:
		f.Caret = f.row(c, p, rows)
	case hotty.InputStart:
		f.Caret = 0
	case hotty.InputEnd:
		f.Caret = len(c)
	case hotty.DeleteCharBackward:
		return f.delete(c, max(p-1, 0), p)
	case hotty.DeleteCharForward:
		return f.delete(c, p, min(p+1, len(c)))
	case hotty.DeleteWordBackward:
		return f.delete(c, f.wordBackward(c, p), p)
	case hotty.DeleteWordForward:
		return f.delete(c, p, f.wordForward(c, p))
	case hotty.DeleteToLineStart:
		return f.delete(c, start, p)
	case hotty.DeleteToLineEnd:
		return f.delete(c, p, end)
	case hotty.Newline:
		return f.Type("\n")
	}
	return false
}

// Type types text at the caret, and reports whether the value changed.
func (f *Field) Type(text string) (changed bool) {
	f.goal = 0
	if text == "" {
		return false
	}
	c := chars(f.Value)
	p := min(max(f.Caret, 0), len(c))
	before := strings.Join(c[:p], "") + text
	f.Value = before + strings.Join(c[p:], "")
	f.Caret = len(chars(before))
	return true
}

// Key does what a keymap says the field does with a key (hotty.Keymap's
// Lookup): it types a character, or does an action. It returns the action
// (hotty.Insert for a character), "" when the key is not the field's, and
// whether the value changed.
func (f *Field) Key(m *hotty.Keymap, key string) (a hotty.Action, changed bool) {
	switch a = m.Lookup(key); a {
	case "":
		return "", false
	case hotty.Insert:
		k, _ := hotty.ParseKey(key)
		switch i := strings.LastIndexByte(k, '+'); {
		case k == "+" || strings.HasSuffix(k, "++"):
			k = "+"
		case k == "Space" || strings.HasSuffix(k, "+Space"):
			k = " "
		case i >= 0:
			k = k[i+1:]
		}
		return a, f.Type(k)
	}
	return a, f.Do(a)
}

// delete deletes the characters from a to b, and leaves the caret at a.
func (f *Field) delete(c []string, a, b int) bool {
	f.Caret = a
	if a >= b {
		return false
	}
	f.Value = strings.Join(c[:a], "") + strings.Join(c[b:], "")
	return true
}

// line is the start and the end of the line p is on. An input's value is
// one line.
func (f *Field) line(c []string, p int) (start, end int) {
	if !f.Multiline {
		return 0, len(c)
	}
	start, end = p, p
	for start > 0 && !isBreak(c[start-1]) {
		start--
	}
	for end < len(c) && !isBreak(c[end]) {
		end++
	}
	return start, end
}

// row moves by rows: to the row by rows away, as near as it can to the
// place along the row where the run of moves began; past the first row to
// the start, past the last to the end.
func (f *Field) row(c []string, p, by int) int {
	var lines [][2]int
	start := 0
	for i, ch := range c {
		if isBreak(ch) {
			lines = append(lines, [2]int{start, i})
			start = i + 1
		}
	}
	lines = append(lines, [2]int{start, len(c)})
	r := 0
	for r < len(lines)-1 && p > lines[r][1] {
		r++
	}
	if f.goal == 0 {
		f.goal = p - lines[r][0] + 1
	}
	switch t := r + by; {
	case t < 0:
		return 0
	case t >= len(lines):
		return len(c)
	default:
		return lines[t][0] + min(f.goal-1, lines[t][1]-lines[t][0])
	}
}

func (f *Field) wordBackward(c []string, p int) int {
	if f.Password {
		return 0
	}
	for p > 0 && isSpace(c[p-1]) {
		p--
	}
	for p > 0 && !isSpace(c[p-1]) {
		p--
	}
	return p
}

func (f *Field) wordForward(c []string, p int) int {
	if f.Password {
		return len(c)
	}
	for p < len(c) && isSpace(c[p]) {
		p++
	}
	for p < len(c) && !isSpace(c[p]) {
		p++
	}
	return p
}

// chars splits text into its characters, grapheme clusters.
func chars(s string) []string {
	var out []string
	for g := graphemes.FromString(s); g.Next(); {
		out = append(out, g.Value())
	}
	return out
}

// isSpace reports whether a character is a space: its first code point is
// Unicode white space. A line break is one.
func isSpace(ch string) bool {
	r, _ := utf8.DecodeRuneInString(ch)
	return unicode.Is(unicode.White_Space, r)
}

// isBreak reports whether a character is a line break.
func isBreak(ch string) bool { return ch == "\n" || ch == "\r\n" || ch == "\r" }
