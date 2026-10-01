package series

import "math"

// Stats are one series' statistics over every sample it had a value in.
type Stats struct {
	// N is how many values it has had.
	N int
	// Last is the latest value; Min, Max and Sum are over all of them.
	Last, Min, Max, Sum float64
}

// Mean is the average value, NaN with none.
func (s Stats) Mean() float64 {
	if s.N == 0 {
		return math.NaN()
	}
	return s.Sum / float64(s.N)
}

func (s *Stats) add(v float64) {
	if s.N == 0 {
		s.Min, s.Max = v, v
	}
	s.N++
	s.Last, s.Sum = v, s.Sum+v
	s.Min, s.Max = math.Min(s.Min, v), math.Max(s.Max, v)
}

// KeepMax is how many values a series keeps for a histogram (Set.Keep)
// before it keeps a uniform sample of them instead.
const KeepMax = 1 << 20

// Set is series that grow a sample at a time: the last Window samples, the
// statistics of all of them, and with Keep, their values for a histogram.
// It is not safe for concurrent use.
type Set struct {
	window int
	keep   bool
	n      int      // samples added
	ring   []Sample // the last window samples, at n % window
	names  []string
	stats  []Stats
	kept   [][]float64
	rng    uint64
}

// NewSet keeps the last window samples (at least 2) and, if keep, every
// value for Kept.
func NewSet(window int, keep bool) *Set {
	return &Set{window: max(2, window), keep: keep, rng: 0x9e3779b97f4a7c15}
}

// Add adds a sample. Its Values are the Set's from now on.
func (s *Set) Add(sm Sample) {
	for len(s.stats) < len(sm.Values) {
		s.stats = append(s.stats, Stats{})
		if s.keep {
			s.kept = append(s.kept, nil)
		}
	}
	for i, v := range sm.Values {
		if math.IsNaN(v) {
			continue
		}
		s.stats[i].add(v)
		if s.keep {
			s.keepValue(i, v)
		}
	}
	if len(s.ring) < s.window {
		s.ring = append(s.ring, sm)
	} else {
		s.ring[s.n%s.window] = sm
	}
	s.n++
}

// keepValue keeps every value up to KeepMax, then a uniform sample of them
// all (reservoir sampling), so a histogram of an endless stream keeps its
// shape in bounded memory.
func (s *Set) keepValue(i int, v float64) {
	k := s.kept[i]
	if len(k) < KeepMax {
		s.kept[i] = append(k, v)
		return
	}
	s.rng ^= s.rng << 13
	s.rng ^= s.rng >> 7
	s.rng ^= s.rng << 17
	if j := s.rng % uint64(s.stats[i].N); j < KeepMax {
		k[j] = v
	}
}

// SetNames names the series, by index.
func (s *Set) SetNames(names []string) { s.names = append(s.names[:0], names...) }

// Names are the series' names, one per series ("" for none).
func (s *Set) Names() []string {
	out := make([]string, s.Width())
	copy(out, s.names)
	return out
}

// Len is how many samples have been added.
func (s *Set) Len() int { return s.n }

// Window is how many samples the Set keeps.
func (s *Set) Window() int { return s.window }

// Width is how many series there are.
func (s *Set) Width() int { return max(len(s.stats), len(s.names)) }

// Stats are series i's statistics.
func (s *Set) Stats(i int) Stats {
	if i < len(s.stats) {
		return s.stats[i]
	}
	return Stats{}
}

// Last are the samples in the window, oldest first, and the index of the
// first (counted from 0 over all samples added). Every sample has Width
// values, NaN where it had none.
func (s *Set) Last() (start int, samples []Sample) {
	start = max(0, s.n-s.window)
	w := s.Width()
	samples = make([]Sample, 0, len(s.ring))
	for k := start; k < s.n; k++ {
		sm := s.ring[k%s.window]
		vals := make([]float64, w)
		for i := range vals {
			vals[i] = math.NaN()
			if i < len(sm.Values) {
				vals[i] = sm.Values[i]
			}
		}
		samples = append(samples, Sample{Values: vals, Label: sm.Label})
	}
	return start, samples
}

// Kept are series i's values, for a histogram (NewSet with keep), and the
// weight of each: 1 while it has them all, more once it keeps a sample.
func (s *Set) Kept(i int) (values []float64, weight float64) {
	if i >= len(s.kept) || len(s.kept[i]) == 0 {
		return nil, 1
	}
	return s.kept[i], float64(s.stats[i].N) / float64(len(s.kept[i]))
}
