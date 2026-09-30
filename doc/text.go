package doc

import (
	"html"
	"strings"
	"unicode/utf8"
)

// textChunk is how many rows of text a block holds: Pages puts a few in a
// surface.
const textChunk = 100

// Text is plain text as a document: <pre>, wrapped at the width, in the
// terminal's font, a block per hundred rows or so. Control characters show
// as their pictures (␛ for ESC): a document shows text, it does not run it.
func Text(src []byte, o Options) *Doc {
	s := Printable(string(src))
	s = strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	all := strings.Split(s, "\n")
	cols := max(1, o.cols()-2)
	chunk := min(textChunk, o.PageRows()-1)
	d := &Doc{CSS: "main.doc { padding-bottom: 0; }"}
	for len(all) > 0 {
		n, rows := 0, 0
		for n < len(all) && (n == 0 || rows+monoRows(all[n:n+1], cols) <= chunk) {
			rows += monoRows(all[n:n+1], cols)
			n++
		}
		d.Blocks = append(d.Blocks, Block{
			HTML: `<pre class="text">` + html.EscapeString(strings.Join(all[:n], "\n")) + "</pre>",
			Rows: rows,
		})
		all = all[n:]
	}
	return d
}

// Printable is text with invalid UTF-8 replaced and control characters,
// other than tab and newline, shown as their pictures (U+2400…).
func Printable(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\t' && c != '\n' && c != '\r') || c == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20:
			return 0x2400 + r
		case r == 0x7f:
			return 0x2421
		}
		return r
	}, s)
}
