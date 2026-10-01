// Package doc turns files into HOTTY documents: Markdown, HTML, CSV and
// TSV, JSON, images and plain text become blocks of HTML that a program
// places as surfaces (SPEC §5), with the resources they refer to (§7.1).
//
// A document is a list of top-level blocks, each with an estimate of the
// rows it takes. Pages groups them into surfaces of a few hundred rows, so
// a long document never reaches the 1000 rows a surface may have (§5.2)
// and the first surface can go out before the rest is laid out.
//
// Every block is inert (Inert): nothing in it reports what the user does,
// and its only links are hyperlinks the terminal opens itself (SPEC §9).
// A document left in scrollback after its program exits must be, or clicks
// in it would be reported to whatever reads the terminal next. The
// exception is a manual page's blocks (ManPage.Blocks), which keep their
// man: links for a viewer.
//
// The package does no I/O. What a document refers to (a Markdown file's
// images) comes through Options.ReadFile.
package doc

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"math"
	"strings"
)

// Block is one top-level block of a document.
type Block struct {
	// HTML is the block's markup, a fragment for the document's body.
	HTML string
	// Rows is an estimate of the rows it takes at Options.Cols. It is
	// generous: Pages uses it to keep surfaces well under 1000 rows.
	Rows int
	// Res are the resources the block refers to as cid:<id>, to be sent
	// before it (hotty.Res).
	Res []Resource
	// Text is the block's text, a paragraph a line, for a program that
	// also draws it in cells. Only ManPage.Blocks sets it.
	Text string
}

// Resource is bytes a document refers to as cid:<ID> (SPEC §7.1).
type Resource struct {
	// ID is what the document's markup names it by, after cid:.
	ID string
	// Type is its MIME type: "image/png".
	Type string
	// Data is its bytes, as hotty.Res sends them.
	Data []byte
}

// Doc is a converted document.
type Doc struct {
	// CSS is the document's own stylesheet: it goes after the shared one
	// (CSS), so it wins (Page). Nothing in it ends a <style> element.
	CSS string
	// Blocks are its top-level blocks, in order.
	Blocks []Block
	// Cols and Rows fix the surface's size in cells (an image); 0 is the
	// terminal's width and rows that fit the content (r=auto).
	Cols, Rows int
	// Rest says what was left out, in a line for cells: "1,234 more rows".
	Rest string
	// Class is the class of the element that stands for the body (Body):
	// "doc" when empty. An HTML file's is "page", with the file's own
	// body class.
	Class string
}

// Options are what a conversion needs to know about where the document
// goes and where it came from.
type Options struct {
	// Cols is the width in cells the document is laid out at: estimates
	// and images are sized to it. 0 is 80.
	Cols int
	// CellW and CellH are a cell's size in CSS pixels; 0 is 9×18.
	CellW, CellH float64
	// Highlight, if set, returns a code block's lines as HTML, or false to
	// leave the language plain.
	Highlight func(lang, code string) (html string, ok bool)
	// ReadFile reads a file a document refers to by a relative name
	// (resolved by the caller against the document's directory), such as a
	// Markdown file's images. nil leaves them out.
	ReadFile func(name string) ([]byte, error)
	// ResPrefix makes resource ids the program's own, so that two runs
	// never share one: the process's surface prefix, such as
	// "mytool-4121". An id is the prefix, the kind, and a hash of the
	// bytes ("mytool-4121-img-3f2a…"): two images never share one, in one
	// document or several, and the same image is one resource.
	ResPrefix string
	// Screen is the terminal's height in rows, 0 if unknown. Pages and
	// images are made to fit it (PageRows), so that a surface placed
	// inline is never taller than the screen: a host may not show one that
	// is whole (hotty-blitz's polyfill draws it with Unicode placeholders,
	// reserved by moving the cursor, which stops at the screen's edges).
	Screen int
}

func (o *Options) cols() int {
	if o.Cols <= 0 {
		return 80
	}
	return o.Cols
}

func (o *Options) cell() (w, h float64) {
	w, h = o.CellW, o.CellH
	if w <= 0 || h <= 0 {
		return 9, 18
	}
	return w, h
}

// PageRows is how many rows a surface aims for (Pages): Target, or less
// to fit the screen.
func (o *Options) PageRows() int {
	if o.Screen > 0 {
		return min(Target, max(8, o.Screen-3))
	}
	return Target
}

// imageRows is the tallest an image is shown: MaxImageRows, or less to fit
// the screen.
func (o *Options) imageRows() int {
	if o.Screen > 0 {
		return min(MaxImageRows, max(4, o.Screen-4))
	}
	return MaxImageRows
}

// resID names a resource by its bytes (ResPrefix).
func (o *Options) resID(kind string, data []byte) string {
	p := o.ResPrefix
	if p == "" {
		p = "doc"
	}
	sum := sha256.Sum256(data)
	return p + "-" + kind + "-" + hex.EncodeToString(sum[:8])
}

// Target is how many rows Pages aims for in a surface: small enough that an
// estimate three times too low still fits a surface, large enough that a
// long document takes few surfaces.
const Target = 300

// MaxRows is the most rows a surface may have (SPEC §5.2).
const MaxRows = 1000

// Pages groups blocks into surfaces of about target rows, keeping the order
// and never cutting a block. A block larger than target is a surface of its
// own.
func Pages(blocks []Block, target int) [][]Block {
	if target <= 0 {
		target = Target
	}
	var pages [][]Block
	var cur []Block
	rows := 0
	for _, b := range blocks {
		if len(cur) > 0 && rows+b.Rows > target {
			pages = append(pages, cur)
			cur, rows = nil, 0
		}
		cur = append(cur, b)
		rows += b.Rows
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	return pages
}

// Body is a page's blocks as one body, in the element the stylesheet styles
// (CSS): <main class="doc">.
func (d *Doc) Body(page []Block) string {
	class := d.Class
	if class == "" {
		class = "doc"
	}
	var b strings.Builder
	b.WriteString(`<main class="` + html.EscapeString(class) + `">`)
	for _, blk := range page {
		b.WriteString(blk.HTML)
	}
	b.WriteString(`</main>`)
	return b.String()
}

// Page is a page's document, for hotty.Doc or hotty.DocDetached: the
// package's stylesheet (CSS), then the document's own, and the page's Body.
func (d *Doc) Page(page []Block) string {
	return "<!doctype html><html><head><style>" + CSS + styleText(d.CSS) + "</style></head><body>" +
		d.Body(page) + "</body></html>"
}

// styleText makes CSS safe in a <style> element: each "<" is written as
// the escape \3c, the same character to CSS, so that nothing in it ends
// the element and starts markup (</style><script>).
func styleText(css string) string { return strings.ReplaceAll(css, "<", `\3c `) }

// Resources are the resources a page's blocks refer to, each once.
func Resources(page []Block) []Resource {
	var out []Resource
	seen := map[string]bool{}
	for _, b := range page {
		for _, r := range b.Res {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// Rows is the sum of the blocks' estimates.
func Rows(blocks []Block) int {
	n := 0
	for _, b := range blocks {
		n += b.Rows
	}
	return n
}

// charsPerCell is how many characters of the documents' sans text fit a
// terminal cell, rounded down: 14px text averages about 7px a character,
// and cells are about 9px wide.
const charsPerCell = 1.15

// textRows estimates the rows of n characters of running text in a box of
// cols cells.
func textRows(n, cols int) int {
	if n <= 0 {
		return 1
	}
	per := max(1, int(float64(cols)*charsPerCell))
	return int(math.Ceil(float64(n) / float64(per)))
}

// monoRows is the rows of lines of monospace text wrapped at cols cells.
func monoRows(lines []string, cols int) int {
	cols = max(1, cols)
	n := 0
	for _, l := range lines {
		w := len([]rune(strings.ReplaceAll(l, "\t", "        ")))
		n += max(1, (w+cols-1)/cols)
	}
	return n
}
