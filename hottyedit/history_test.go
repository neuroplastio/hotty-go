package hottyedit

import (
	"strings"
	"testing"

	"neuroplast.io/hotty-go"
)

func key(t *testing.T, f *Field, keys ...string) {
	t.Helper()
	km := hotty.Resolve(hotty.Platform{}, f.Multiline, hotty.TerminalKeys)
	for _, k := range keys {
		if a, _ := f.Key(km, k); a == "" {
			t.Fatalf("key %q is not the field's", k)
		}
	}
}

func TestEnter(t *testing.T) {
	f := Field{Value: "ab", Caret: 1, Multiline: true}
	key(t, &f, "enter")
	key(t, &f, "shift+enter")
	if f.Value != "a\n\nb" || f.Caret != 3 {
		t.Fatalf("%q %d", f.Value, f.Caret)
	}
	// An input: enter and shift+enter are the program's.
	km := hotty.Resolve(hotty.Platform{}, false, hotty.TerminalKeys)
	in := Field{Value: "ab"}
	if a, ch := in.Key(km, "enter"); a != hotty.Submit || ch {
		t.Fatal(a, ch)
	}
	if a, ch := in.Key(km, "shift+enter"); a != hotty.Submit || ch || in.Value != "ab" {
		t.Fatal(a, ch)
	}
}

func TestPaste(t *testing.T) {
	f := Field{Multiline: true}
	f.Paste("a\r\nb\rc\td\n")
	if f.Value != "a\nb\nc    d\n" || f.Caret != 11 {
		t.Fatalf("%q %d", f.Value, f.Caret)
	}
	in := Field{Value: "xy", Caret: 1}
	in.Paste("1\n2")
	if in.Value != "x1 2y" {
		t.Fatalf("%q", in.Value)
	}
	if in.Paste("") {
		t.Fatal("an empty paste changed it")
	}
}

func TestUndoRuns(t *testing.T) {
	f := Field{Multiline: true}
	km := hotty.Resolve(hotty.Platform{}, true, hotty.TerminalKeys)
	for _, k := range []string{"a", "b", "c"} {
		f.Key(km, k)
	}
	f.Key(km, "left")
	f.Key(km, "x") // a new run, since the caret moved
	f.Key(km, "y")
	f.Paste("PP")
	f.Key(km, "backspace")
	f.Key(km, "backspace")
	steps := []string{f.Value}
	for f.Undo() {
		steps = append(steps, f.Value)
	}
	if got, want := strings.Join(steps, "|"), "abxyc|abxyPPc|abxyc|abc|"; got != want {
		t.Fatalf("undo steps %q, want %q", got, want)
	}
	if !f.Redo() || f.Value != "abc" || f.Caret != 2 {
		t.Fatalf("redo %q %d", f.Value, f.Caret)
	}
	f.Key(km, "z")
	if f.Redo() {
		t.Fatal("a new edit keeps what was undone")
	}
	if a, ch := f.Key(km, "ctrl+z"); a != hotty.Undo || !ch || f.Value != "abc" {
		t.Fatalf("%q %v", f.Value, a)
	}
}

func TestHistoryBounded(t *testing.T) {
	f := Field{Multiline: true}
	for range 3 * maxHistory {
		f.Paste("x")
	}
	n := 0
	for f.Undo() {
		n++
	}
	if n != maxHistory {
		t.Fatalf("%d steps kept", n)
	}
}

// A Field stays comparable: a patch release does not break the API.
var _ = map[Field]bool{}

func TestFieldCopies(t *testing.T) {
	// A zero Field, and a literal as pkg/cells builds one, edits and undoes.
	var z Field
	z.Type("a")
	z.Type("b")
	if !z.Undo() || z.Value != "" {
		t.Fatalf("%q", z.Value)
	}
	if (&Field{Value: "x"}).Undo() || (&Field{}).Redo() {
		t.Fatal("undo with no history")
	}
	f := Field{Value: "ab", Caret: 2, Multiline: true}
	g := f // a copy before any edit: its own history
	g.Type("c")
	if f.Value != "ab" || !g.Undo() || g.Value != "ab" {
		t.Fatalf("%q %q", f.Value, g.Value)
	}
	// A copy after one is usable, and shares what was done.
	f.Type("d")
	h := f
	h.Paste("e")
	if !h.Undo() || h.Value != "abd" || !h.Undo() || h.Value != "ab" {
		t.Fatalf("%q", h.Value)
	}
	// Rebuilt by literal from a value and a caret: a fresh editor.
	r := Field{Value: h.Value, Caret: h.Caret}
	if r.Undo() {
		t.Fatal("a fresh field has no history")
	}
	r.Type("!")
	if r.Value != "ab!" || !r.Undo() || r.Value != "ab" {
		t.Fatalf("%q", r.Value)
	}
}

// A program's own binding of the undo keys wins: the keymap says what a key
// is, and a key it binds to Program or to another action is not undo.
func TestProgramBindingWins(t *testing.T) {
	for _, bind := range []string{"ctrl+z=program", "ctrl+z=submit", "ctrl+y=program", "ctrl+shift+z=program"} {
		km := hotty.Resolve(hotty.Platform{}, true, hotty.TerminalKeys, bind)
		key := strings.SplitN(bind, "=", 2)[0]
		f := Field{Multiline: true}
		f.Paste("abc")
		a, changed := f.Key(km, key)
		if a == hotty.Undo || a == hotty.Redo || changed || f.Value != "abc" {
			t.Errorf("%s: action %q changed %v value %q: the program's binding lost", bind, a, changed, f.Value)
		}
	}
	// The other undo key keeps working beside the program's one.
	km := hotty.Resolve(hotty.Platform{}, true, hotty.TerminalKeys, "ctrl+z=program")
	f := Field{Multiline: true}
	f.Paste("abc")
	if a, ch := f.Key(km, "ctrl+y"); a != hotty.Redo || ch {
		t.Fatal(a, ch)
	}
	// And bound by the program to undo, it is undo.
	km = hotty.Resolve(hotty.Platform{}, true, hotty.TerminalKeys, "ctrl+q=undo")
	if a, ch := f.Key(km, "ctrl+q"); a != hotty.Undo || !ch || f.Value != "" {
		t.Fatalf("%q %v %v", f.Value, a, ch)
	}
}
