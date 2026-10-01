package series

import "math"

// Axis is a value axis: its ends, and the step between its ticks.
type Axis struct {
	// Lo and Hi are its ends, multiples of Step unless fixed (Scale).
	Lo, Hi float64
	// Step is the distance between ticks, a round number (1, 2, 2.5 or 5
	// times a power of ten); 0 when no round step fits.
	Step float64
}

// Ticks are the multiples of Step from Lo to Hi.
func (a Axis) Ticks() []float64 {
	if a.Step <= 0 || a.Hi <= a.Lo {
		return []float64{a.Lo, a.Hi}
	}
	var out []float64
	for k := math.Ceil(a.Lo/a.Step - 1e-9); k*a.Step <= a.Hi+a.Step*1e-9; k++ {
		out = append(out, clean(k*a.Step, a.Step))
	}
	return out
}

// Frac is where v lies on the axis: 0 at Lo, 1 at Hi.
func (a Axis) Frac(v float64) float64 {
	if a.Hi == a.Lo {
		return 0
	}
	return (v - a.Lo) / (a.Hi - a.Lo)
}

// Decimals is how many decimals the step needs: 0 for 5, 1 for 0.5, 2 for 0.25.
func (a Axis) Decimals() int { return decimals(a.Step) }

func decimals(step float64) int {
	if step <= 0 || math.IsNaN(step) || math.IsInf(step, 0) {
		return 0
	}
	for d := 0; d < 12; d++ {
		p := math.Pow(10, float64(d))
		if math.Abs(step*p-math.Round(step*p)) < 1e-6*step*p {
			return d
		}
	}
	return 12
}

// clean rounds away the float error of k*step (0.30000000000000004). A step
// too small for 12 decimals is left alone: rounding would make it 0.
func clean(v, step float64) float64 {
	d := decimals(step)
	if d >= 12 {
		return v + 0
	}
	p := math.Pow(10, float64(d))
	return math.Round(v*p)/p + 0 // + 0: no -0
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Nice is the smallest round number (1, 2, 2.5 or 5 times a power of ten)
// at least v; 1 for v <= 0.
func Nice(v float64) float64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*p >= v*(1-1e-12) {
			return m * p
		}
	}
	return 10 * p
}

// niceBelow is the largest round number at most v.
func niceBelow(v float64) float64 {
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{10, 5, 2.5, 2, 1} {
		if m*p <= v*(1+1e-12) {
			return m * p
		}
	}
	return p
}

// Scale fits an axis to data that keeps changing, such as the window of a
// chart that streams. The axis starts at 0 when every value is positive (and
// ends at 0 when every value is negative), has some headroom, and its ends
// are multiples of its step. It grows as soon as a value falls outside it,
// and shrinks only once the data spans less than a third of it: it does not
// jump on every sample.
type Scale struct {
	// Min and Max fix an end; NaN fits it to the data.
	Min, Max float64
	// Ticks is about how many steps the axis has; 0 is 4.
	Ticks int

	axis Axis
	ok   bool
}

// NewScale is a Scale with both ends fitted to the data.
func NewScale() *Scale { return &Scale{Min: math.NaN(), Max: math.NaN()} }

// Fit returns the axis for data from lo to hi. lo > hi, or an end that is
// NaN or infinite, is no data: the axis stays as it was.
func (s *Scale) Fit(lo, hi float64) Axis {
	fixLo, fixHi := !math.IsNaN(s.Min), !math.IsNaN(s.Max)
	if lo > hi || !finite(lo) || !finite(hi) {
		if s.ok {
			return s.axis
		}
		if !fixLo && !fixHi {
			return Axis{0, 1, 0.25}
		}
		lo, hi = 0, 1
		if fixLo {
			lo = s.Min
			hi = math.Max(hi, lo+1)
		}
		if fixHi {
			hi = s.Max
			lo = math.Min(lo, hi-1)
		}
	}
	// Where the axis must reach: the data, from or to 0.
	dlo, dhi := lo, hi
	if lo >= 0 {
		dlo = 0
	}
	if hi <= 0 {
		dhi = 0
	}
	if fixLo {
		dlo = s.Min
	}
	if fixHi {
		dhi = s.Max
	}
	if s.ok {
		a := s.axis
		fits := a.Lo <= dlo && dhi <= a.Hi
		tight := (dhi - dlo) >= (a.Hi-a.Lo)/3
		if fits && tight && (!fixLo || a.Lo == s.Min) && (!fixHi || a.Hi == s.Max) {
			return a
		}
	}
	s.axis, s.ok = s.nice(dlo, dhi, fixLo || lo >= 0, fixHi || hi <= 0), true
	return s.axis
}

// nice is an axis from about lo to hi. An end that is not pinned (to 0 or
// fixed) gets 5% of headroom, then moves out to a multiple of the step.
func (s *Scale) nice(lo, hi float64, pinLo, pinHi bool) Axis {
	ticks := s.Ticks
	if ticks <= 0 {
		ticks = 4
	}
	if hi <= lo {
		switch {
		case !pinHi:
			hi = lo + math.Max(1, math.Abs(lo)*0.1)
		case !pinLo:
			lo = hi - math.Max(1, math.Abs(hi)*0.1)
		default:
			hi = lo + 1
		}
	}
	span := hi - lo
	if !finite(span) {
		// Too wide for a float: no round step, and no headroom.
		return Axis{Lo: lo, Hi: hi}
	}
	if !pinHi && finite(hi+span*0.05) {
		hi += span * 0.05
	}
	if !pinLo && finite(lo-span*0.05) {
		lo -= span * 0.05
	}
	// The smallest round step that keeps to about ticks steps. Each try
	// takes the next round number, so a few dozen reach any span a float
	// holds; the cap is for ends a step cannot round out to.
	var a Axis
	step := niceBelow((hi - lo) / float64(ticks))
	for range 64 {
		a = Axis{Lo: lo, Hi: hi, Step: step}
		if !math.IsNaN(s.Min) {
			a.Lo = s.Min
		} else {
			a.Lo = clean(math.Floor(lo/step+1e-9)*step, step)
		}
		if !math.IsNaN(s.Max) {
			a.Hi = s.Max
		} else {
			a.Hi = clean(math.Ceil(hi/step-1e-9)*step, step)
		}
		if (a.Hi-a.Lo)/step <= float64(ticks)+1+1e-9 {
			return a
		}
		step = Nice(step * 1.001)
	}
	return Axis{Lo: lo, Hi: hi}
}

// Bins are a histogram's: Counts[s][b] is how many of series s's values fall
// in bin b, which runs from Lo+b*Width, included, to the next, excluded (the
// last includes its end).
type Bins struct {
	// Lo is the first bin's lower edge, and Width every bin's width.
	Lo, Width float64
	// Counts are each series' counts, a bin each, weighted.
	Counts [][]float64
}

// N is the number of bins.
func (b Bins) N() int {
	if len(b.Counts) == 0 {
		return 0
	}
	return len(b.Counts[0])
}

// Edge is bin i's lower edge (i = N: the last one's upper edge).
func (b Bins) Edge(i int) float64 { return clean(b.Lo+float64(i)*b.Width, b.Width) }

// Hist bins every series' values in at most n bins of one round width
// (Nice), shared by all of them, from the smallest value to the largest.
// weights[s], if given, is what each of series s's values counts for
// (Set.Kept). NaN and infinite values are left out. It returns no bins when
// there are no values.
func Hist(values [][]float64, weights []float64, n int) Bins {
	n = max(1, n)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, vs := range values {
		for _, v := range vs {
			if finite(v) {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	if lo > hi {
		return Bins{}
	}
	var width, start float64
	count := 1
	if hi == lo {
		width = Nice(math.Max(math.Abs(lo)*0.1, 1e-9))
		if lo == 0 {
			width = 1
		}
		start = math.Floor(lo/width) * width
	} else {
		// Values so far apart that no round width a float holds spans
		// them in n bins go in one bin.
		start, width = lo, hi-lo
		for w, try := Nice(hi/float64(n)-lo/float64(n)), 0; try < 64 && finite(w); w, try = Nice(w*1.01), try+1 {
			st := math.Floor(lo/w+1e-9) * w
			c := math.Floor((hi-st)/w+1e-9) + 1
			if !finite(c) {
				continue
			}
			// The largest value, on an edge, has a bin of its own if there
			// is room, else goes in the last, which then includes its end.
			if e := st + (c-1)*w; c == float64(n+1) && math.Abs(hi-e) < w*1e-9 {
				c--
			}
			if c <= float64(n) {
				start, width, count = st, w, int(c)
				break
			}
		}
	}
	b := Bins{Lo: clean(start, width), Width: width, Counts: make([][]float64, len(values))}
	for s, vs := range values {
		w := 1.0
		if s < len(weights) && weights[s] > 0 {
			w = weights[s]
		}
		b.Counts[s] = make([]float64, count)
		for _, v := range vs {
			if !finite(v) {
				continue
			}
			i := int(math.Floor((v-b.Lo)/width + 1e-9))
			i = max(0, min(count-1, i))
			b.Counts[s][i] += w
		}
	}
	return b
}
