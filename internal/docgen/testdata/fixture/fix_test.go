package fix_test

import (
	"fmt"

	"example.com/fix"
)

func Example() {
	fmt.Println(fix.Small)
	// Output: 1
}

// A story told by an example.
func Example_story() {
	t := fix.NewThing()
	fmt.Println(t.Do())
	// Output:
	// done
}

func ExampleThing_Do() {
	fmt.Println(fix.NewThing().Do())
	// Output: done
}
