package asciicast_test

import (
	"fmt"
	"strings"

	"github.com/neuroplastio/hotty-go/hottyvt"
	"github.com/neuroplastio/hotty-go/hottyvt/asciicast"
)

// A recording played into a screen of its size. A player waits until each
// event's time first; this one only looks at where it ends.
func Example() {
	c, err := asciicast.Decode(strings.NewReader(`{"version": 3, "term": {"cols": 20, "rows": 2}}
[0.0, "o", "$ make"]
[0.4, "m", "Build"]
[0.9, "o", "\r\nok  build 0.4s"]
`))
	if err != nil {
		panic(err)
	}
	s := hottyvt.New(c.Cols, c.Rows, hottyvt.HideCursor())
	defer s.Close()
	for _, ev := range c.Events {
		switch ev.Code {
		case asciicast.Output:
			_, _ = s.WriteString(ev.Data)
		case asciicast.Marker:
			fmt.Printf("%v %s\n", ev.Time, ev.Data)
		}
	}
	fmt.Println(s.Text())
	fmt.Println(c.Duration())
	// Output:
	// 400ms Build
	// $ make
	// ok  build 0.4s
	// 1.3s
}
