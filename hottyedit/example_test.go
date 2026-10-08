package hottyedit_test

import (
	"fmt"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyedit"
)

// A field in cells, edited with the keymap the program gives its surfaces.
func ExampleField_Key() {
	km := hotty.Resolve(false, hotty.TerminalKeys)
	f := hottyedit.Field{Value: "hello wrld", Caret: 10}
	f.Key(km, "Alt+b")     // word-backward: hello |wrld
	f.Key(km, "Control+f") // char-forward: hello w|rld
	f.Key(km, "o")         // types it
	fmt.Printf("%q %d\n", f.Value, f.Caret)
	f.Key(km, "Control+w") // delete-word-backward
	fmt.Printf("%q %d\n", f.Value, f.Caret)
	// Output:
	// "hello world" 8
	// "hello rld" 6
}
