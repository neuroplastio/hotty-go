package hottyedit_test

import (
	"fmt"

	"neuroplast.io/hotty-go"
	"neuroplast.io/hotty-go/hottyedit"
)

// A field in cells, edited with the keymap the program gives its surfaces.
func ExampleField_Key() {
	km := hotty.Resolve(hotty.Platform{}, false, hotty.TerminalKeys)
	f := hottyedit.Field{Value: "hello wrld", Caret: 10}
	f.Key(km, "alt+b")  // word-backward: hello |wrld
	f.Key(km, "ctrl+f") // char-forward: hello w|rld
	f.Key(km, "o")      // types it
	fmt.Printf("%q %d\n", f.Value, f.Caret)
	f.Key(km, "ctrl+w") // delete-word-backward
	fmt.Printf("%q %d\n", f.Value, f.Caret)
	f.Key(km, "shift+end") // line-end, selecting: hello [rld]
	start, end := f.Selection()
	fmt.Println(start, end)
	f.Key(km, "shift+z") // types in place of the selection
	fmt.Printf("%q %d\n", f.Value, f.Caret)
	// Output:
	// "hello world" 8
	// "hello rld" 6
	// 6 9
	// "hello Z" 7
}

// A multi-line field wrapped to a width: the caret's row and column, for the
// terminal cursor, and the scroll offset of a view of two rows.
func ExampleField_Cursor() {
	km := hotty.Resolve(hotty.Platform{}, true, hotty.TerminalKeys)
	f := hottyedit.Field{Value: "the quick brown fox", Multiline: true, Width: 10, Rows: 2}
	f.Key(km, "ctrl+end") // input-end
	row, col := f.Cursor()
	fmt.Println(row, col, f.Scroll(0))
	f.Key(km, "up") // by visual row, the column kept
	row, col = f.Cursor()
	fmt.Println(row, col, f.Scroll(1))
	// Output:
	// 1 9 0
	// 0 9 0
}
