package hottyvt

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Box-drawing characters (U+2500–U+257F) and block elements (U+2580–U+259F)
// are drawn as a terminal draws them, to fill their cells, rather than set
// in the font: a font's │ or █ is as tall as its glyphs, and a terminal's
// rows are taller, so a border or a scrollbar set in it breaks between
// rows. Each such character is an element of its cell's size (vt-k, and
// vt-kXXXX by its code point): its lines are the borders of its ::before
// and ::after, which the browser puts on whole pixels, so they meet the
// next cell's; a block's shape is its background. The character stays in
// the element, unseen, for copying. A run of one character that draws
// alike in every cell (─, █, ░) is one element, as wide as the run
// (--vt-n). The dashed lines and the diagonals are set in the font.
//
// draw_gen.go holds the CSS and the characters, generated from the tables
// in draw_test.go: go test -run TestDrawGenerated -update.

// drawKinds is, for each code point from drawFirst, 0 for a character set
// in the font, drawn for one that is drawn, and drawn|tiles for one that
// draws alike in every cell.
var drawKinds [drawLast - drawFirst + 1]uint8

const (
	drawFirst = 0x2500
	drawLast  = 0x259f
	drawn     = 1
	tiles     = 2
)

func init() {
	for _, r := range drawnRunes {
		drawKinds[r-drawFirst] |= drawn
	}
	for _, r := range tilingRunes {
		drawKinds[r-drawFirst] |= tiles
	}
}

// drawKind is how the cell's content is drawn: 0 when it is set in the font.
func drawKind(content string) (rune, uint8) {
	r, n := utf8.DecodeRuneInString(content)
	if n != len(content) || r < drawFirst || r > drawLast {
		return 0, 0
	}
	return r, drawKinds[r-drawFirst]
}

// drawElement is the element that draws n of r, side by side.
func drawElement(r rune, n int) string {
	code := strconv.FormatInt(int64(r), 16)
	var b strings.Builder
	b.WriteString(`<span class="vt-k vt-k` + code + `"`)
	if n > 1 {
		b.WriteString(` style="--vt-n:` + strconv.Itoa(n) + `"`)
	}
	b.WriteString(">" + strings.Repeat(string(r), n) + "</span>")
	return b.String()
}
