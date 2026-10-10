// Hello prints a line of HTML in the terminal, and a line of text where
// the terminal cannot show HTML.
//
// It is the smallest HOTTY program: open the terminal, ask whether it is a
// host, print a document at the cursor, and read the host's replies before
// exiting, so that none is left for the shell.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyterm"
)

func main() {
	open := func() (*hottyterm.Term, error) { return hottyterm.Open("hello") }
	os.Exit(run(context.Background(), os.Stdout, os.Stderr, open))
}

func run(ctx context.Context, stdout, stderr io.Writer, open func() (*hottyterm.Term, error)) int {
	t, err := open()
	if err != nil { // no terminal at all: a pipe, a cron job
		fmt.Fprintln(stdout, "Hello.")
		return 0
	}
	defer t.Close()
	if !t.Detect(ctx) {
		fmt.Fprintln(stdout, "Hello. (This terminal is not a HOTTY host, so this is text.)")
		return 0
	}
	// Surfaces are placed at the cursor: start on a line of their own.
	_ = t.LineStart(ctx)
	// The host stylesheet gives every document the terminal's colours
	// and font (SPEC §8).
	_, err = t.Print("hello", `<p style="margin: 0">Hello, <b style="color: var(--hotty-ansi-5)">HTML</b> in the terminal.</p>`,
		hotty.Placement{Cols: 40, Rows: 1})
	if err != nil {
		fmt.Fprintln(stderr, "hello:", err)
		return 1
	}
	// The host replies to a command it refuses: the first reply is the
	// cause (EQUOTA: no room for another surface), the rest follow from it.
	replies, err := t.Fence(ctx)
	if len(replies) > 0 {
		err = hotty.Err(replies[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, "hello:", err)
		return 1
	}
	return 0
}
