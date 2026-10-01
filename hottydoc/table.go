package hottydoc

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Table is a CSV or TSV file read: the first row as the header, then at
// most MaxRecords rows.
type Table struct {
	// Head is the first row: the columns' names.
	Head []string
	// Rows are the rows after it, each as many cells as it had.
	Rows [][]string
	// More is how many rows there were after MaxRecords.
	More int
	// Numeric marks the columns whose every value is a number: they are
	// right-aligned, in tabular figures.
	Numeric []bool
	// Widths are each column's widest value, in characters.
	Widths []int
}

// MaxRecords is the most rows a table shows.
const MaxRecords = 1000

// ReadTable reads a CSV (comma ',') or TSV (comma '\t') file. TSV fields
// are split at tabs, with no quoting. On a malformed record it returns the
// rows before it with the error.
func ReadTable(src []byte, comma rune) (*Table, error) {
	t := &Table{}
	add := func(rec []string) {
		if t.Head == nil {
			t.Head = rec
			return
		}
		if len(t.Rows) < MaxRecords {
			t.Rows = append(t.Rows, rec)
		} else {
			t.More++
		}
	}
	var err error
	if comma == '\t' {
		s := strings.TrimSuffix(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
		if s != "" {
			for _, line := range strings.Split(s, "\n") {
				add(strings.Split(line, "\t"))
			}
		}
	} else {
		r := csv.NewReader(bytes.NewReader(src))
		r.Comma, r.FieldsPerRecord, r.LazyQuotes = comma, -1, true
		for {
			rec, e := r.Read()
			if e == io.EOF {
				break
			}
			if e != nil {
				err = e
				break
			}
			add(rec)
		}
	}
	t.measure()
	return t, err
}

func (t *Table) measure() {
	n := len(t.Head)
	for _, r := range t.Rows {
		n = max(n, len(r))
	}
	t.Widths = make([]int, n)
	t.Numeric = make([]bool, n)
	seen := make([]bool, n)
	for c := range n {
		t.Numeric[c] = true
		t.Widths[c] = utf8.RuneCountInString(cell(t.Head, c))
	}
	for _, r := range t.Rows {
		for c := range n {
			v := strings.TrimSpace(cell(r, c))
			t.Widths[c] = max(t.Widths[c], utf8.RuneCountInString(cell(r, c)))
			if v == "" {
				continue
			}
			seen[c] = true
			if !Number(v) {
				t.Numeric[c] = false
			}
		}
	}
	for c := range n {
		t.Numeric[c] = t.Numeric[c] && seen[c]
	}
}

func cell(r []string, c int) string {
	if c < len(r) {
		return r[c]
	}
	return ""
}

// Cell is row r's value in column c, or "" when the row is short.
func (t *Table) Cell(r, c int) string { return cell(t.Rows[r], c) }

// Number reports whether a value reads as a number: digits with a sign, a
// decimal point, an exponent, thousands separators, or a trailing %.
func Number(v string) bool {
	v = strings.TrimSuffix(strings.TrimSpace(v), "%")
	v = strings.ReplaceAll(strings.ReplaceAll(v, ",", ""), "_", "")
	if v == "" {
		return false
	}
	_, err := strconv.ParseFloat(v, 64)
	return err == nil
}

// MaxColumn is the widest a column is drawn, in cells; a longer value is
// cut with an ellipsis.
const MaxColumn = 48

// ColumnWidths are the columns' widths in cells, padding included, made to
// fit cols: the widest give way first, down to 4 cells.
func (t *Table) ColumnWidths(cols, pad int) []int {
	w := make([]int, len(t.Widths))
	total := 0
	for i, n := range t.Widths {
		w[i] = min(max(n, 1), MaxColumn) + pad
		total += w[i]
	}
	for total > cols {
		widest := -1
		for i := range w {
			if w[i] > 4 && (widest < 0 || w[i] > w[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			break
		}
		w[widest]--
		total--
	}
	return w
}

// Doc is the table as a document: blocks of rows with the header on top of
// each, in columns sized to their values, numeric ones right-aligned, and
// every other row shaded. The last block says how many rows were left out.
func (t *Table) Doc(o Options) *Doc {
	d := &Doc{}
	cols := o.cols() - 2
	widths := t.ColumnWidths(cols, 2)
	total := 0
	for _, w := range widths {
		total += w
	}
	var colgroup strings.Builder
	colgroup.WriteString("<colgroup>")
	for _, w := range widths {
		fmt.Fprintf(&colgroup, `<col style="width:calc(%d * var(--doc-col))">`, w)
	}
	colgroup.WriteString("</colgroup>")
	class := func(c int) string {
		if c < len(t.Numeric) && t.Numeric[c] {
			return ` class="num"`
		}
		return ""
	}
	head := func() string {
		var b strings.Builder
		b.WriteString("<thead><tr>")
		for c := range widths {
			fmt.Fprintf(&b, "<th%s>%s</th>", class(c), html.EscapeString(Printable(cell(t.Head, c))))
		}
		b.WriteString("</tr></thead>")
		return b.String()
	}()
	// A block of rows fills a page, with its header and the line after.
	chunk := max(4, o.PageRows()-4)
	for start := 0; start < len(t.Rows) || start == 0; start += chunk {
		end := min(start+chunk, len(t.Rows))
		var b strings.Builder
		fmt.Fprintf(&b, `<table class="csv" style="width:calc(%d * var(--doc-col))">`, total)
		b.WriteString(colgroup.String() + head + "<tbody>")
		for r := start; r < end; r++ {
			if r%2 == 1 {
				b.WriteString(`<tr class="shade">`)
			} else {
				b.WriteString("<tr>")
			}
			for c := range widths {
				fmt.Fprintf(&b, "<td%s>%s</td>", class(c), html.EscapeString(Printable(t.Cell(r, c))))
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody></table>")
		rows := end - start + 1
		if end == len(t.Rows) && t.More > 0 {
			d.Rest = MoreRows(t.More)
			b.WriteString(`<p class="more">… ` + d.Rest + `</p>`)
			rows += 2
		}
		d.Blocks = append(d.Blocks, Block{HTML: b.String(), Rows: rows + 1})
		if end >= len(t.Rows) {
			break
		}
	}
	return d
}

// MoreRows says how many rows were left out: "1,234 more rows".
func MoreRows(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if n == 1 {
		return s + " more row"
	}
	return s + " more rows"
}
