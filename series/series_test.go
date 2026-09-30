package series

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// parse runs lines through a parser and returns the samples, NaN as -1 so
// they compare.
func parse(p *Parser, text string) [][]float64 {
	var out [][]float64
	for _, line := range strings.Split(text, "\n") {
		s, ok := p.Line(line)
		if !ok {
			continue
		}
		row := make([]float64, len(s.Values))
		for i, v := range s.Values {
			row[i] = v
			if math.IsNaN(v) {
				row[i] = -1
			}
		}
		out = append(out, row)
	}
	return out
}

func TestColumns(t *testing.T) {
	var p Parser
	got := parse(&p, "1 2 3\n4\t5\t6\n7,8,9\n  10   11  \n\n")
	want := [][]float64{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, {10, 11}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if names := p.Names(); !reflect.DeepEqual(names, []string{"", "", ""}) {
		t.Errorf("names %q: no header, no names", names)
	}
}

func TestHeader(t *testing.T) {
	var p Parser
	var labels []string
	for _, line := range strings.Split("time,p50,p99\n00:00,12,80\n00:05,13.5,\"91\"\n00:10,,70", "\n") {
		if s, ok := p.Line(line); ok {
			labels = append(labels, s.Label)
		}
	}
	if !reflect.DeepEqual(p.Names(), []string{"p50", "p99"}) {
		t.Errorf("names %q", p.Names())
	}
	if !reflect.DeepEqual(labels, []string{"00:00", "00:05", "00:10"}) {
		t.Errorf("labels %q: the column of words labels each sample", labels)
	}
	p = Parser{}
	got := parse(&p, "time,p50,p99\n00:00,12,80\n00:10,,70")
	if want := [][]float64{{12, 80}, {-1, 70}}; !reflect.DeepEqual(got, want) {
		t.Errorf("an empty field is a gap in its own series: got %v, want %v", got, want)
	}
	// Spaces, and a header with a column that never has numbers.
	p = Parser{}
	got = parse(&p, "host load mem\nweb1 0.5 71\nweb2 0.7 64")
	if want := [][]float64{{0.5, 71}, {0.7, 64}}; !reflect.DeepEqual(got, want) || !reflect.DeepEqual(p.Names(), []string{"load", "mem"}) {
		t.Errorf("got %v %q", got, p.Names())
	}
}

func TestJunk(t *testing.T) {
	var p Parser
	got := parse(&p, "12\nthe answer is 42, or so\nnothing here\n-3.5e1 NaN Inf 0x1p4 1_000 7%\n+.5")
	want := [][]float64{{12}, {42}, {-35, 16}, {0.5}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if p.Names()[0] != "" {
		t.Error("a first line with a number is not a header")
	}
}

func TestKeys(t *testing.T) {
	p := Parser{Keys: []string{"time", "ttl"}}
	got := parse(&p, strings.Join([]string{
		"PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.",
		"64 bytes from 1.1.1.1: icmp_seq=1 ttl=57 time=12.3 ms",
		"64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=9.87ms",
		`{"level":"info"} time="4.5"`,
		"Request timeout for icmp_seq 3",
	}, "\n"))
	want := [][]float64{{12.3, 57}, {9.87, 57}, {4.5, -1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !reflect.DeepEqual(p.Names(), []string{"time", "ttl"}) {
		t.Errorf("names %q", p.Names())
	}
	if !reflect.DeepEqual(p.Decimals(), []int{2, 0}) {
		t.Errorf("decimals %v", p.Decimals())
	}
}

func TestDecimals(t *testing.T) {
	var p Parser
	parse(&p, "1 2.5 3e2\n4 5.25 1.5e-3")
	if got := p.Decimals(); !reflect.DeepEqual(got, []int{0, 2, 4}) {
		t.Errorf("decimals %v", got)
	}
}

func TestWindowSlides(t *testing.T) {
	s := NewSet(5, false)
	for i := 1; i <= 8; i++ {
		s.Add(Sample{Values: []float64{float64(i)}})
	}
	start, last := s.Last()
	var vals []float64
	for _, sm := range last {
		vals = append(vals, sm.Values[0])
	}
	if start != 3 || !reflect.DeepEqual(vals, []float64{4, 5, 6, 7, 8}) {
		t.Errorf("window: start %d, %v", start, vals)
	}
	st := s.Stats(0)
	if st.N != 8 || st.Last != 8 || st.Min != 1 || st.Max != 8 || st.Mean() != 4.5 {
		t.Errorf("stats are over every sample, not the window: %+v", st)
	}
	// A series that appears later is NaN before it.
	s.Add(Sample{Values: []float64{9, 90}})
	_, last = s.Last()
	if !math.IsNaN(last[0].Values[1]) || last[4].Values[1] != 90 {
		t.Errorf("a new series: %v", last)
	}
}

func TestKeptSamples(t *testing.T) {
	s := NewSet(2, true)
	n := KeepMax + KeepMax/2
	for i := range n {
		s.Add(Sample{Values: []float64{float64(i % 10)}})
	}
	vals, w := s.Kept(0)
	if len(vals) != KeepMax || math.Abs(w-1.5) > 1e-9 {
		t.Fatalf("kept %d at weight %v", len(vals), w)
	}
	b := Hist([][]float64{vals}, []float64{w}, 10)
	for i, c := range b.Counts[0] {
		if math.Abs(c-float64(n)/10) > float64(n)/10*0.05 {
			t.Errorf("bin %d: %v, want about %d: the sample keeps the shape", i, c, n/10)
		}
	}
}

func TestScale(t *testing.T) {
	s := NewScale()
	a := s.Fit(3, 97)
	if a.Lo != 0 || a.Hi != 125 || a.Step != 25 {
		t.Errorf("positive data starts at 0, with headroom, on round steps: %+v", a)
	}
	// Inside, the axis stays put.
	for v := 50.0; v <= 110; v += 3 {
		if b := s.Fit(1, v); b != a {
			t.Fatalf("data up to %v moved the axis to %+v", v, b)
		}
	}
	// A value outside grows it at once.
	if b := s.Fit(1, 130); b.Hi < 130 || b.Hi > 200 {
		t.Errorf("grow: %+v", b)
	}
	// Data that fills less than a third shrinks it.
	b := s.Fit(1, 20)
	if b.Hi > 30 || b.Hi < 20 {
		t.Errorf("shrink: %+v", b)
	}
	// Negative values move the bottom below 0.
	if c := s.Fit(-12, 20); c.Lo > -12 || c.Lo < -20 {
		t.Errorf("negative: %+v", c)
	}
	// All negative: the axis ends at 0.
	if c := NewScale().Fit(-80, -5); c.Hi != 0 || c.Lo > -80 {
		t.Errorf("all negative: %+v", c)
	}
	// Fixed ends.
	f := &Scale{Min: -1, Max: 1}
	if c := f.Fit(-5, 5); c.Lo != -1 || c.Hi != 1 {
		t.Errorf("fixed: %+v", c)
	}
	f = &Scale{Min: 10, Max: math.NaN()}
	if c := f.Fit(12, 40); c.Lo != 10 || c.Hi < 40 {
		t.Errorf("fixed min: %+v", c)
	}
	// No data yet.
	if c := NewScale().Fit(math.Inf(1), math.Inf(-1)); c.Lo != 0 || c.Hi != 1 {
		t.Errorf("empty: %+v", c)
	}
	// One value, or a flat line.
	if c := NewScale().Fit(5, 5); c.Lo != 0 || c.Hi < 5 {
		t.Errorf("flat: %+v", c)
	}
}

func TestTicks(t *testing.T) {
	for _, c := range []struct {
		a    Axis
		want string
	}{
		{Axis{0, 1, 0.25}, "[0 0.25 0.5 0.75 1]"},
		{Axis{0, 0.3, 0.1}, "[0 0.1 0.2 0.3]"},
		{Axis{-1, 1, 0.5}, "[-1 -0.5 0 0.5 1]"},
		{Axis{-0.7, 0.9, 0.5}, "[-0.5 0 0.5]"},
	} {
		if got := fmt.Sprint(c.a.Ticks()); got != c.want {
			t.Errorf("%+v: %s, want %s", c.a, got, c.want)
		}
	}
	for step, want := range map[float64]int{0.25: 2, 2.5: 1, 25: 0, 0.1: 1, 5e-7: 7} {
		if d := (Axis{Step: step}).Decimals(); d != want {
			t.Errorf("decimals of %v: %d, want %d", step, d, want)
		}
	}
}

func TestHist(t *testing.T) {
	b := Hist([][]float64{{0, 1, 1, 2, 9.99, 10}}, nil, 5)
	if b.Lo != 0 || b.Width != 2 || b.N() != 5 {
		t.Fatalf("bins %+v", b)
	}
	if want := []float64{3, 1, 0, 0, 2}; !reflect.DeepEqual(b.Counts[0], want) {
		t.Errorf("counts %v, want %v (the last bin keeps its end)", b.Counts[0], want)
	}
	if b.Edge(5) != 10 {
		t.Errorf("edge %v", b.Edge(5))
	}
	// Whole numbers in as many bins: one each.
	b = Hist([][]float64{{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}}, nil, 10)
	if want := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}; b.Width != 1 || !reflect.DeepEqual(b.Counts[0], want) {
		t.Errorf("0…9 in 10 bins: %+v", b)
	}
	// Round edges, and never more bins than asked.
	b = Hist([][]float64{{0.13, 0.91}, {0.5}}, nil, 20)
	if b.N() > 20 || b.Width != 0.05 || b.Lo != 0.1 || len(b.Counts) != 2 {
		t.Errorf("bins %+v", b)
	}
	if b = Hist([][]float64{{3, 3}}, nil, 20); b.N() != 1 || b.Counts[0][0] != 2 {
		t.Errorf("one value: %+v", b)
	}
	if b = Hist([][]float64{nil}, nil, 20); b.N() != 0 {
		t.Errorf("no values: %+v", b)
	}
}
