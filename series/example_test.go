package series_test

import (
	"fmt"
	"strings"

	"github.com/neuroplastio/hotty-go/series"
)

// A CSV stream with a header: each column is a series named by it, and the
// column of words labels each sample. A Set keeps the last samples for the
// chart and the statistics of all of them for its legend.
func Example_csvWithHeader() {
	input := `time,cpu,mem
10:00,12.5,40
10:01,30,41
10:02,18.25,41`

	var p series.Parser
	set := series.NewSet(60, false)
	for _, line := range strings.Split(input, "\n") {
		if s, ok := p.Line(line); ok {
			set.Add(s)
		}
	}
	set.SetNames(p.Names())

	dec := p.Decimals()
	for i, name := range set.Names() {
		st := set.Stats(i)
		fmt.Printf("%s: last %.*f, min %.*f, max %.*f\n", name, dec[i], st.Last, dec[i], st.Min, dec[i], st.Max)
	}
	_, last := set.Last()
	fmt.Println("first label:", last[0].Label)
	// Output:
	// cpu: last 18.25, min 12.50, max 30.00
	// mem: last 41, min 40, max 41
	// first label: 10:00
}

// KEY=value pairs anywhere in a line, as ping prints them: Keys names the
// series and picks them out.
func Example_pingTimes() {
	input := `64 bytes from 1.1.1.1: icmp_seq=1 ttl=57 time=11.8 ms
64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=12.4 ms
Request timeout for icmp_seq 3
64 bytes from 1.1.1.1: icmp_seq=4 ttl=57 time=10.9 ms`

	p := series.Parser{Keys: []string{"time"}}
	for _, line := range strings.Split(input, "\n") {
		if s, ok := p.Line(line); ok {
			fmt.Println(s.Values[0])
		}
	}
	// Output:
	// 11.8
	// 12.4
	// 10.9
}

// An axis for a chart that streams: it starts at 0 for positive data, has
// round ticks, and stays put while new values fit it.
func ExampleScale() {
	s := series.NewScale()
	a := s.Fit(3, 97)
	fmt.Println(a.Lo, a.Hi, a.Ticks())
	fmt.Println(s.Fit(1, 110) == a) // still fits: the axis does not jump
	fmt.Println(s.Fit(1, 180).Hi)   // outside: it grows at once
	// Output:
	// 0 125 [0 25 50 75 100 125]
	// true
	// 200
}

// A histogram of latencies in round bins.
func ExampleHist() {
	latencies := []float64{12, 14, 15, 15, 16, 18, 22, 25, 31, 48}
	b := series.Hist([][]float64{latencies}, nil, 4)
	for i := range b.N() {
		fmt.Printf("%g–%g: %g\n", b.Edge(i), b.Edge(i+1), b.Counts[0][i])
	}
	// Output:
	// 10–20: 6
	// 20–30: 2
	// 30–40: 1
	// 40–50: 1
}
