package hottyvt_test

import (
	"fmt"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyvt"
)

// show prints what commands do: each delta's operation, target and payload.
func show(cmds []string) {
	var d hotty.Decoder
	for _, cmd := range cmds {
		m, _ := d.Feed(cmd)
		fmt.Printf("%s %s %s\n", m.Get("op"), m.Get("t"), m.Payload)
	}
}

// A command's output, live in a document: the screen goes once, as HTML,
// and each frame after that sends the rows it changed.
func Example() {
	s := hottyvt.New(24, 3, hottyvt.HideCursor())
	defer s.Close()
	_, _ = s.WriteString("$ make\r\n")
	doc := "<style>" + hottyvt.CSS + "</style>" + s.HTML()
	_ = hotty.Doc("build", doc) // and a placement, once

	// The command prints: one row changes.
	_, _ = s.WriteString("\x1b[32mok\x1b[0m  build 0.4s")
	show(s.Delta("build"))
	// Output:
	// inner vt-r1 <span style="color:var(--hotty-ansi-2)">ok</span>  build 0.4s
}

// A session recorded at 120 columns, shown in a placement of 80: the
// screen is scaled to two thirds of the terminal's cells.
func ExampleScreen_SetScale() {
	s := hottyvt.New(120, 30)
	defer s.Close()
	_ = hotty.Doc("cast", "<style>"+hottyvt.CSS+"</style>"+s.HTML())
	show([]string{s.SetScale("cast", 80.0/120)})
	// Output:
	// var vt 0.667
}

// The screen as text: for a terminal that is not a host, or a pipe.
func ExampleScreen_Text() {
	s := hottyvt.New(20, 4)
	defer s.Close()
	_, _ = s.WriteString("\x1b[1mtotal\x1b[0m 3\r\nloading...\r\x1b[Kdone")
	fmt.Println(s.Text())
	// Output:
	// total 3
	// done
}
