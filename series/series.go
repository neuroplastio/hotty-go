// Package series reads numbers from a stream of text, as plotting tools do
// (plothot, youplot, asciigraph): lines of numbers separated by spaces, tabs
// or commas, a column a series, and a first line with no numbers naming them.
// It also keeps what a live chart needs: the last N samples, statistics over
// all of them, an axis that grows to fit without jumping on every sample
// (Scale), and histogram bins (Hist).
//
// It does no I/O: a program reads lines and hands them to a Parser, and adds
// what comes back to a Set.
package series

import (
	"math"
	"strconv"
	"strings"
)

// Sample is one line's numbers: Values[i] is series i's, NaN where the line
// had none for it. Label is the line's text in a column of words (a time in
// a CSV file), when the input has a header and such a column.
type Sample struct {
	Values []float64
	Label  string
}

// Parser turns lines into samples. The zero Parser reads columns; set Keys
// to read KEY=value pairs instead.
//
// Columns: a line is split at commas if it has any, else at tabs, else at
// runs of spaces. Fields that are not numbers are skipped, so a line of
// prose with one number in it still gives one. Without a header, series i is
// the line's i-th number. With a header (a first line with no numbers), a
// series is a column of the header, named by it, and the first column of
// words in the data (a time, a day) labels each sample.
//
// Keys: series i is Keys[i], its value the number at the start of what
// follows "KEY=" anywhere in the line: `ping … time=12.3 ms` gives 12.3 for
// the key time.
type Parser struct {
	// Keys, if set, name the series and take them from KEY=value.
	Keys []string

	started  bool   // a line with content was seen
	header   []string // the header's fields, by column
	col      []int  // header mode: series -> column
	byCol    map[int]int
	labelCol int // header mode: the column that labels samples; -1 none, -2 not yet known
	width    int // series seen so far
	dec      []int
}

// Line parses one line, without its line ending. ok is false when the line
// gives no sample: blank, no numbers, or the header.
func (p *Parser) Line(line string) (s Sample, ok bool) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return Sample{}, false
	}
	if len(p.Keys) > 0 {
		return p.keys(line)
	}
	fields := split(line)
	first := !p.started
	p.started = true
	nums := 0
	for _, f := range fields {
		if _, ok := Number(f); ok {
			nums++
		}
	}
	if nums == 0 {
		if first {
			p.header = fields
			p.byCol = map[int]int{}
			p.labelCol = -2
		}
		// No numbers in its fields; prose split at a comma may still have
		// some between the words.
		if first || p.header != nil || !strings.Contains(line, ",") {
			return Sample{}, false
		}
		fields = strings.FieldsFunc(line, isSep)
		for _, f := range fields {
			if _, ok := Number(f); ok {
				nums++
			}
		}
		if nums == 0 {
			return Sample{}, false
		}
		return p.ordinal(fields), true
	}
	if p.header != nil {
		return p.columns(fields), true
	}
	return p.ordinal(fields), true
}

// ordinal: series i is the line's i-th number.
func (p *Parser) ordinal(fields []string) Sample {
	var vals []float64
	for _, f := range fields {
		if v, ok := Number(f); ok {
			p.saw(len(vals), f)
			vals = append(vals, v)
		}
	}
	p.width = max(p.width, len(vals))
	return Sample{Values: vals}
}

// columns: a series per header column, in the order their numbers first
// appear.
func (p *Parser) columns(fields []string) Sample {
	if p.labelCol == -2 {
		p.labelCol = -1
		for c, f := range fields {
			if _, ok := Number(f); !ok && f != "" {
				p.labelCol = c
				break
			}
		}
	}
	vals := make([]float64, p.width, max(p.width, len(fields)))
	for i := range vals {
		vals[i] = math.NaN()
	}
	var label string
	for c, f := range fields {
		v, ok := Number(f)
		if !ok {
			if c == p.labelCol {
				label = f
			}
			continue
		}
		i, seen := p.byCol[c]
		if !seen {
			i = p.width
			p.byCol[c] = i
			p.col = append(p.col, c)
			p.width++
			vals = append(vals, math.NaN())
		}
		vals[i] = v
		p.saw(i, f)
	}
	return Sample{Values: vals, Label: label}
}

func (p *Parser) keys(line string) (Sample, bool) {
	vals := make([]float64, len(p.Keys))
	found := false
	for i := range vals {
		vals[i] = math.NaN()
	}
	for _, tok := range strings.FieldsFunc(line, isSep) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		for i, key := range p.Keys {
			if k != key {
				continue
			}
			if n, text, ok := prefixNumber(strings.Trim(v, `"'`)); ok {
				vals[i] = n
				found = true
				p.saw(i, text)
			}
		}
	}
	p.width = len(p.Keys)
	return Sample{Values: vals}, found
}

// Names are the series' names so far, by index: the header's columns, or the
// keys; "" for a series with no name.
func (p *Parser) Names() []string {
	if len(p.Keys) > 0 {
		return append([]string(nil), p.Keys...)
	}
	out := make([]string, p.width)
	for i, c := range p.col {
		if c < len(p.header) {
			out[i] = p.header[c]
		}
	}
	return out
}

// Width is how many series the lines so far have had.
func (p *Parser) Width() int { return p.width }

// Decimals are how many decimals each series' numbers have been written
// with, at most: 1 for 12.5, 0 for 12, 4 for 1.5e-3. Printing values with
// them keeps the input's precision, and figures that line up.
func (p *Parser) Decimals() []int {
	out := make([]int, p.width)
	copy(out, p.dec)
	return out
}

func (p *Parser) saw(i int, num string) {
	for len(p.dec) <= i {
		p.dec = append(p.dec, 0)
	}
	p.dec[i] = max(p.dec[i], decimalsOf(num))
}

// decimalsOf is how many decimals a number is written with.
func decimalsOf(num string) int {
	mant, exp := num, 0
	if i := strings.IndexAny(num, "eE"); i >= 0 {
		mant = num[:i]
		exp, _ = strconv.Atoi(num[i+1:])
	}
	d := 0
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		d = len(mant) - i - 1
	}
	return max(0, min(12, d-exp))
}

func isSep(r rune) bool { return r == ' ' || r == '\t' || r == ',' }

// split cuts a line into fields: at commas if it has any, else at tabs, else
// at runs of spaces. Fields are trimmed of spaces and quotes.
func split(line string) []string {
	var fields []string
	switch {
	case strings.Contains(line, ","):
		fields = strings.Split(line, ",")
	case strings.Contains(line, "\t"):
		fields = strings.Split(line, "\t")
	default:
		return strings.Fields(line)
	}
	for i, f := range fields {
		fields[i] = strings.Trim(strings.TrimSpace(f), `"`)
	}
	return fields
}

// Number parses a field that is a number and nothing else: 12, -3.5, 1e6,
// +.5. NaN and infinities are not numbers here.
func Number(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	switch c := s[0]; {
	case c >= '0' && c <= '9', c == '-', c == '+', c == '.':
	default:
		return 0, false
	}
	if strings.IndexByte(s, '_') >= 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// prefixNumber is the number at the start of s, and its text: "12.3ms" is
// 12.3.
func prefixNumber(s string) (float64, string, bool) {
	end := 0
	for end < len(s) && strings.IndexByte("+-.0123456789eE", s[end]) >= 0 {
		end++
	}
	for ; end > 0; end-- {
		if v, ok := Number(s[:end]); ok {
			return v, s[:end], true
		}
	}
	return 0, "", false
}
