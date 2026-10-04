// Package hottyvt shows a terminal's screen on a HOTTY surface: what a
// program would write to a terminal goes in, and HTML comes out, first as
// an element of a document and then as deltas for the rows that changed.
//
// A Screen is a terminal emulator (github.com/charmbracelet/x/vt) with a
// size of its own, apart from the terminal the program runs in. Write gives
// it output, as a terminal's pty would: a recording played back, a command
// run in a pseudo-terminal, a program's own drawing. HTML is the screen as
// it is now, to put in a document; Delta is the commands that bring a
// surface showing it up to now, one per row that changed, so a frame where
// a clock ticks costs one row (SPEC §6).
//
//	s := hottyvt.New(80, 24)
//	defer s.Close()
//	t.Print("demo", "<style>"+hottyvt.CSS+"</style>"+s.HTML(), hotty.Placement{Cols: 80})
//	for chunk := range output {
//		s.Write(chunk)
//		t.Send(hotty.Sync(s.Delta("demo")...)) // a frame, shown whole
//	}
//
// # Looks
//
// The screen takes the terminal's look from the host stylesheet (SPEC §8):
// its font (--hotty-font), its font size, its row height (--hotty-cell-h),
// its foreground and background, and its 16 colours (--hotty-ansi-0 to
// 15), so a screen looks like the terminal it is shown in. The colours of
// the 256-colour palette above 15 and 24-bit colours are drawn as given.
// Add CSS to the document's stylesheet.
//
// The custom property --vt-scale on the screen's element sizes it: at 1
// (the default) a cell of the screen is a cell of the terminal, at 0.5 it
// is half one. For a screen of 120 columns in a placement of 80, set it to
// 80/120 (SetScale, or Scale before HTML). The screen's columns are the
// terminal's cells, scaled, so the fit is exact; the font's own shapes fill
// them as well as they fit a cell. A character the font lacks, which the
// browser sets in a fallback font of another width, and a bold face wider
// than the regular one move nothing after them: each run of text sits in a
// box as wide as its cells (CSS). Box-drawing characters and block elements
// are drawn to fill their cells, as a terminal draws them, so borders and
// bars meet across rows; the dashed lines and the diagonals are the font's.
//
// # Surfaces
//
// A program that speaks HOTTY, recorded in a HOTTY host, made surfaces as
// well as cells, and the screen shows them as a host would (SPEC §5–§7): it
// reads the HOTTY messages in what it is given, keeps each surface's
// document, placement and resources, and shows each placed surface above
// the rows, at its cells, laid out at its size and scaled with the screen.
// What the surfaces do reaches the screen's surface as Delta's commands: a
// delta to a surface is the same delta to the surface's element in the
// screen's, so a card whose clock ticks costs what it cost the program; a
// placement is its box's style; a resource is a resource of the program
// showing the screen, named with the screen's id (Resources). A placement
// moves with its line as the screen scrolls and goes when that line leaves
// the screen, and one made on the alternate screen goes with it, as its
// surface does (SPEC §5.4).
//
// Each surface's document is rewritten to live in the screen's beside the
// others: its ids are prefixed with its element's (id-d1-…), its selectors
// scoped to that element, and what it says of its root (html, body, :root)
// is said of the element. The rules of the document the screen is in reach
// the surfaces' elements too, so keep them to its own classes.
//
// The surfaces show as a recording does, as detached ones (SPEC §5.5) that
// report nothing: a link that is not a hyperlink loses its href, so that a
// click on it does not reach the program showing the screen as a click on
// a link of its own, and data-on goes. The screen answers no message; the
// program that was recorded had its answers. So a placement with auto rows
// moves the cursor by rows the screen guesses from its text, where the host
// laid the document out (the document shows at its own height), and media
// queries and viewport units see the screen's surface, not the placement.
// NoSurfaces makes a screen that shows the cells alone, as a terminal that
// is not a host does.
//
// # What it leaves out
//
// The scrollback: the screen shows what a terminal of its size would, and
// nothing that scrolled off. Images (kitty graphics, sixel), blinking, and
// the colours a program sets for the terminal's default foreground and
// background (OSC 10, 11), which would fight the host's theme. The
// emulator's answers to queries (the cursor's position, device attributes)
// are dropped, unless Replies takes them; HOTTY's are not made. Text is the
// cells', without the surfaces' text.
//
// This package does no I/O but for those answers: like the hotty package,
// it returns commands for the program to send.
package hottyvt

import (
	"html"
	"image/color"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"github.com/neuroplastio/hotty-go"
)

// DefaultID is the id of a screen made without ID.
const DefaultID = "vt"

// CSS lays a screen out: a block of its rows, in the terminal's font,
// scaled by --vt-scale. Its columns are the terminal's cells
// (--hotty-cell-w), scaled, whatever the font's own advance: letter-spacing
// makes up the difference, so the screen keeps its width and its columns
// line up as they do in a terminal, which draws each character in its cell.
// A bold or an italic face can be wider than the regular one, so the
// spacing is worked out again in each, from its own 1ch. What the spacing
// cannot answer for (a character from a fallback font, an advance the
// browser rounds) stays in its box (vt-t), and a wide character in its own
// (vt-w): each as wide as its cells, so the next starts at its column.
// Box-drawing characters and block elements are drawn by it (draw.go).
// Add it to the document's stylesheet once, however many screens it shows.
const CSS = `
.vt { --vt-scale: 1; display: block; box-sizing: content-box; overflow: hidden; position: relative; isolation: isolate;
  font-family: var(--hotty-font, monospace); font-size: calc(var(--vt-scale) * 1rem);
  line-height: calc(var(--vt-scale) * var(--hotty-cell-h));
  letter-spacing: calc(var(--vt-scale) * var(--hotty-cell-w) - 1ch);
  width: calc(var(--vt-cols) * var(--vt-scale) * var(--hotty-cell-w)); white-space: pre;
  color: var(--hotty-fg); background: var(--hotty-bg); }
.vt > div { height: calc(var(--vt-scale) * var(--hotty-cell-h)); overflow: hidden; }
.vt > div > a { color: inherit; }
.vt .vt-b { font-weight: bold; }
.vt .vt-i { font-style: italic; }
.vt .vt-b, .vt .vt-i { letter-spacing: calc(var(--vt-scale) * var(--hotty-cell-w) - 1ch); }
.vt .vt-t, .vt .vt-w { display: inline-block; vertical-align: top;
  width: calc(var(--vt-n, 1) * var(--vt-scale) * var(--hotty-cell-w));
  height: calc(var(--vt-scale) * var(--hotty-cell-h)); }
.vt .vt-w { --vt-n: 2; letter-spacing: 0; text-align: center; }
.vt .vt-h { visibility: hidden; }
.vt .vt-s { text-decoration-line: line-through; }
.vt .vt-u { text-decoration-line: underline; }
.vt .vt-u.vt-s { text-decoration-line: underline line-through; }
.vt .vt-u2 { text-decoration-style: double; }
.vt .vt-u3 { text-decoration-style: wavy; }
.vt .vt-u4 { text-decoration-style: dotted; }
.vt .vt-u5 { text-decoration-style: dashed; }
.vt .vt-cur { color: var(--hotty-bg); background: var(--hotty-fg); }
.vt > .vt-p { position: absolute; overflow: hidden; z-index: 1001;
  left: calc(var(--vt-px) * var(--vt-scale) * var(--hotty-cell-w));
  top: calc(var(--vt-py) * var(--vt-scale) * var(--hotty-cell-h));
  width: calc(var(--vt-pw, var(--vt-pc)) * var(--vt-scale) * var(--hotty-cell-w));
  height: calc(var(--vt-ph, var(--vt-pr)) * var(--vt-scale) * var(--hotty-cell-h)); }
.vt-p > .vt-v { all: initial; display: block; position: relative; overflow: hidden;
  width: calc(var(--vt-pc) * var(--hotty-cell-w)); height: calc(var(--vt-pr) * var(--hotty-cell-h));
  transform-origin: 0 0; transform: scale(var(--vt-scale))
    translate(calc(-1 * var(--vt-wx, 0) * var(--hotty-cell-w)), calc(-1 * var(--vt-wy, 0) * var(--hotty-cell-h)));
  font-family: var(--hotty-font, monospace); font-size: 1rem; line-height: var(--hotty-cell-h);
  color: var(--hotty-fg); background: var(--hotty-bg); color-scheme: inherit; }
.vt-v > .vt-d { display: block; }
` + drawCSS

// Screen is a terminal's screen shown on a surface. It is not safe for
// concurrent use: Write, HTML and Delta from one goroutine, or under one
// lock.
type Screen struct {
	id      string
	emu     *vt.Emulator
	cursor  bool
	scale   float64
	replies io.Writer
	drained chan struct{}

	// What the surface was last given: each row's markup, and the columns
	// of the element they are in. sent is nil before HTML.
	sent     []string
	sentCols int

	// hidden is whether the program hid the cursor (DECTCEM).
	hidden bool
	// title is the title the program last set (OSC 0, 2).
	title string

	// The surfaces the program made, and what the screen's surface has not
	// heard of them yet (surfaces.go).
	noSurfaces bool
	dec        hotty.Decoder
	held       []byte // the end of what Write was given, which may begin a HOTTY message
	surfaces   map[string]*surface
	order      []*surface // in the order they were made: their stacking
	made       int        // surfaces made, for their numbers
	res        map[string]resource
	resOrder   []string
	resOut     []string // resource commands for the next Delta
	ops        []op     // deltas for the next Delta
	stamps     int      // the rows' stamps, as they follow output
}

// Option sets up a Screen.
type Option func(*Screen)

// ID names the screen's element, and makes its rows' ids (id-r0, id-r1, …):
// for a document with more than one screen. Without it, DefaultID.
func ID(id string) Option { return func(s *Screen) { s.id = id } }

// Scale is the --vt-scale the screen's element starts with (Looks, in the
// package's documentation).
func Scale(scale float64) Option { return func(s *Screen) { s.scale = scale } }

// HideCursor leaves the cursor out, even where the program shows it. A
// screen shows it otherwise, as a block, wherever the program left it
// visible.
func HideCursor() Option { return func(s *Screen) { s.cursor = false } }

// NoSurfaces makes the screen a terminal that is not a HOTTY host: it
// shows the cells alone, and ignores the HOTTY messages in what it is
// given, as such a terminal does.
func NoSurfaces() Option { return func(s *Screen) { s.noSurfaces = true } }

// Replies takes the emulator's answers to the queries in what it is given
// (device attributes, the cursor's position, a colour), for a program that
// runs in it and waits for them: write them to its input. They are
// written from a goroutine of the Screen's, so w must be safe for that.
// Without it they are dropped.
func Replies(w io.Writer) Option { return func(s *Screen) { s.replies = w } }

// New makes a screen of cols by rows cells (at least one of each), blank,
// with the cursor at the top left. Close it when done with it.
func New(cols, rows int, opts ...Option) *Screen {
	s := &Screen{id: DefaultID, cursor: true, scale: 1, replies: io.Discard, drained: make(chan struct{}),
		surfaces: map[string]*surface{}, res: map[string]resource{}}
	for _, o := range opts {
		o(s)
	}
	s.emu = vt.NewEmulator(max(cols, 1), max(rows, 1))
	s.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { s.hidden = !visible },
		Title:            func(t string) { s.title = t },
		AltScreen:        s.altScreen,
	})
	// A full reset (RIS) deletes every surface (SPEC §5.4); the emulator's
	// own handler resets the rest.
	s.emu.RegisterEscHandler('c', func() bool { s.dropAll(); return false })
	// The emulator writes its answers to a pipe, and Write waits until
	// they are read.
	go func() {
		defer close(s.drained)
		_, _ = io.Copy(s.replies, s.emu)
	}()
	return s
}

// Write gives the screen output, as a terminal's pty would. It never fails.
func (s *Screen) Write(p []byte) (int, error) {
	if s.noSurfaces {
		return s.emu.Write(p)
	}
	s.feed(p)
	return len(p), nil
}

// WriteString is Write for a string.
func (s *Screen) WriteString(p string) (int, error) { return s.Write([]byte(p)) }

// Close stops the screen: what it is given afterwards is lost, and Replies
// gets nothing more.
func (s *Screen) Close() error {
	// The emulator's Close marks it closed, unsynchronised, under the
	// goroutine reading its answers. Ending the pipe first ends that
	// goroutine, and the mark is then nobody else's.
	if pw, ok := s.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.Close()
		<-s.drained
	}
	err := s.emu.Close()
	<-s.drained
	return err
}

// ElementID is the id of the screen's element.
func (s *Screen) ElementID() string { return s.id }

// RowID is the id of the screen's row y, counted from 0 at the top.
func (s *Screen) RowID(y int) string { return s.id + "-r" + strconv.Itoa(y) }

// Title is the title the program last gave its window (OSC 0 or 2), ""
// before it gives one: for a frame around the screen to show, as a
// terminal's window does.
func (s *Screen) Title() string { return s.title }

// Size is the screen's size in cells.
func (s *Screen) Size() (cols, rows int) { return s.emu.Width(), s.emu.Height() }

// Resize changes the screen's size, as a terminal's window would: what is
// on it is kept where it fits. The next Delta sends the whole element.
func (s *Screen) Resize(cols, rows int) { s.emu.Resize(max(cols, 1), max(rows, 1)) }

// Text is what the screen shows, as plain text: its rows, each without
// the spaces at its end, joined by newlines. For a program's rendition in
// cells, or in a pipe (SPEC §14).
func (s *Screen) Text() string {
	cols, rows := s.Size()
	lines := make([]string, rows)
	for y := range rows {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			c := s.emu.CellAt(x, y)
			if c == nil || c.Width == 0 {
				continue
			}
			b.WriteString(content(c))
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// HTML is the screen's element as the screen is now, to put in a document
// (with CSS in its stylesheet). Delta then sends what changes from here.
func (s *Screen) HTML() string {
	cols, rows := s.Size()
	s.sent = make([]string, rows)
	s.sentCols = cols
	var b strings.Builder
	b.WriteString(`<div class="vt" id="` + html.EscapeString(s.id) + `" style="` + s.vars() + `">`)
	for y := range rows {
		s.sent[y] = s.row(y)
		b.WriteString(`<div id="` + html.EscapeString(s.RowID(y)) + `">` + s.sent[y] + `</div>`)
	}
	b.WriteString(s.surfacesHTML())
	b.WriteString(`</div>`)
	return b.String()
}

func (s *Screen) vars() string {
	v := "--vt-cols:" + strconv.Itoa(s.emu.Width())
	if s.scale != 1 {
		v += ";--vt-scale:" + num(s.scale)
	}
	return v
}

// Delta is the commands that bring surface, whose document holds the
// screen's element as HTML or Delta last left it, to the screen as it is
// now: one per row that changed, and the program's surfaces' changes
// (Surfaces, in the package's documentation); none when nothing changed.
// After a Resize, it is the whole element. Before HTML, it is nothing:
// there is no element to change. Send a frame's commands together, in
// hotty.Sync, for the host to show it whole.
func (s *Screen) Delta(surface string) []string {
	if s.sent == nil {
		return nil
	}
	cols, rows := s.Size()
	if cols != s.sentCols || rows != len(s.sent) {
		res := s.resOut
		s.resOut = nil
		return append(res, hotty.MorphTo(surface, s.id, s.HTML()))
	}
	out := s.surfaceDelta(surface)
	for y := range rows {
		r := s.row(y)
		if r == s.sent[y] {
			continue
		}
		s.sent[y] = r
		if plain(r) {
			out = append(out, hotty.SetText(surface, s.RowID(y), html.UnescapeString(r)))
		} else {
			out = append(out, hotty.Delta(surface, hotty.OpInner, s.RowID(y), "", []byte(r)))
		}
	}
	return out
}

// SetScale changes the screen's --vt-scale, and is the command that tells
// surface, once the screen's element is in it; HTML has it from then on.
func (s *Screen) SetScale(surface string, scale float64) string {
	s.scale = scale
	if s.sent == nil {
		return ""
	}
	return hotty.SetVar(surface, s.id, "vt-scale", num(scale))
}

// plain reports whether a row's markup is text alone, with no element.
func plain(r string) bool { return !strings.ContainsRune(r, '<') }

// The kinds of run: text, set in the font and spaced to the cells; a
// glyph, one cell of a character that may come from another font; a wide
// character; and drawn characters (draw.go).
const (
	textRun = iota
	glyphRun
	wideRun
	drawnRun
)

// kind is the kind of run a cell's content goes in. Only ASCII is text:
// the terminal's font has all of it, at its own advance, which the
// letter-spacing makes a cell (CSS). Any other character may be set in a
// fallback font of another width.
func kind(c *uv.Cell, content string) int {
	switch {
	case c.Width > 1:
		return wideRun
	case len(content) == 1 && content[0] < utf8.RuneSelf:
		return textRun
	}
	if _, how := drawKind(content); how != 0 {
		return drawnRun
	}
	return glyphRun
}

// run is cells next to each other that are drawn alike, of one kind.
type run struct {
	style  uv.Style
	link   string
	kind   int
	cursor bool
	cells  int
	text   strings.Builder
	// The drawn character the run ends with (draw.go), and how many of it,
	// while more of it may follow.
	k rune
	n int
}

// add puts a cell's content at the run's end.
func (r *run) add(content string) {
	r.cells++
	if r.kind != drawnRun {
		r.text.WriteString(html.EscapeString(content))
		return
	}
	k, how := drawKind(content)
	if how&tiles != 0 && k == r.k {
		r.n++
		return
	}
	r.flushDrawn()
	r.k, r.n = k, 1
}

func (r *run) flushDrawn() {
	if r.n > 0 {
		r.text.WriteString(drawElement(r.k, r.n))
		r.k, r.n = 0, 0
	}
}

// row is the markup of row y: its cells as text, in spans where they are
// drawn other than the terminal's text is, without the blank cells at its
// end.
func (s *Screen) row(y int) string {
	cols := s.emu.Width()
	cur := s.emu.CursorPosition()
	showCursor := s.cursor && !s.hidden && cur.Y == y
	// The last cell drawn: where the row's blank end starts.
	end := 0
	for x := 0; x < cols; x++ {
		c := s.emu.CellAt(x, y)
		if (showCursor && x == cur.X) || (c != nil && !blank(c)) {
			end = x + 1
		}
	}
	var b strings.Builder
	var r *run
	flush := func(last bool) {
		if r != nil {
			r.write(&b, last)
			r = nil
		}
	}
	for x := 0; x < end; x++ {
		c := s.emu.CellAt(x, y)
		if c == nil {
			c = &uv.EmptyCell
		}
		if c.Width == 0 {
			continue // the second half of a wide character
		}
		atCursor := showCursor && x == cur.X
		link := linkURL(c.Link.URL)
		text := content(c)
		k := kind(c, text)
		if r == nil || k != r.kind || k == glyphRun || k == wideRun || atCursor || r.cursor ||
			r.link != link || !r.style.Equal(&c.Style) {
			flush(false)
			r = &run{style: c.Style, link: link, kind: k, cursor: atCursor}
		}
		r.add(text)
	}
	flush(true)
	return b.String()
}

// blank reports whether a cell draws nothing: a space, or nothing, with no
// background, decoration or link.
func blank(c *uv.Cell) bool {
	return (c.Content == "" || c.Content == " ") && c.Width <= 1 && c.Style.Bg == nil &&
		c.Style.Attrs&uv.AttrReverse == 0 && c.Style.Underline == uv.UnderlineNone &&
		c.Style.Attrs&uv.AttrStrikethrough == 0 && c.Link.URL == ""
}

func content(c *uv.Cell) string {
	if c.Content == "" {
		return " "
	}
	return c.Content
}

// linkURL is a hyperlink's address when it is one a surface shows as a
// link: absolute, over http or https (SPEC §9: the terminal's to open).
func linkURL(u string) string {
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}
	return ""
}

// write puts the run's markup at b's end. Text and a glyph that are not the
// row's last run are in a box their cells wide (vt-t), so that what follows
// starts at its column whatever the font's advance; a wide character's
// element is such a box, and so is each drawn one.
func (r *run) write(b *strings.Builder, last bool) {
	r.flushDrawn()
	var class []string
	var style []string
	st := r.style
	fg, bg := colorCSS(st.Fg), colorCSS(st.Bg)
	if st.Attrs&uv.AttrReverse != 0 {
		fg, bg = or(bg, "var(--hotty-bg)"), or(fg, "var(--hotty-fg)")
	}
	if st.Attrs&uv.AttrFaint != 0 {
		// Faint is the colour part of the way to the background, as
		// terminals draw it: opacity would fade the background too, and
		// some hosts leave it out on text.
		fg = "color-mix(in srgb, " + or(fg, "var(--hotty-fg)") + " 60%, " + or(bg, "var(--hotty-bg)") + ")"
	}
	if r.cursor {
		class = append(class, "vt-cur")
	} else {
		if fg != "" {
			style = append(style, "color:"+fg)
		}
		if bg != "" {
			style = append(style, "background:"+bg)
		}
	}
	switch {
	case r.kind == wideRun:
		class = append(class, "vt-w")
	case (r.kind == textRun || r.kind == glyphRun) && !last:
		class = append(class, "vt-t")
		if r.cells > 1 {
			style = append(style, "--vt-n:"+strconv.Itoa(r.cells))
		}
	}
	for _, a := range [...]struct {
		bit   uint8
		class string
	}{{uv.AttrBold, "vt-b"}, {uv.AttrItalic, "vt-i"}, {uv.AttrConceal, "vt-h"}, {uv.AttrStrikethrough, "vt-s"}} {
		if st.Attrs&a.bit != 0 {
			class = append(class, a.class)
		}
	}
	if st.Underline != uv.UnderlineNone {
		class = append(class, "vt-u")
		if st.Underline >= uv.UnderlineDouble && st.Underline <= uv.UnderlineDashed {
			class = append(class, "vt-u"+strconv.Itoa(int(st.Underline)))
		}
		if uc := colorCSS(st.UnderlineColor); uc != "" {
			style = append(style, "text-decoration-color:"+uc)
		}
	}
	text := r.text.String()
	// A link is the run's element itself: a box in it would not take its
	// underline (text decorations stop at an inline-block).
	tag := "span"
	var attrs strings.Builder
	if r.link != "" {
		tag = "a"
		attrs.WriteString(` href="` + html.EscapeString(r.link) + `" target="_blank"`)
	}
	if len(class) > 0 {
		attrs.WriteString(` class="` + strings.Join(class, " ") + `"`)
	}
	if len(style) > 0 {
		attrs.WriteString(` style="` + html.EscapeString(strings.Join(style, ";")) + `"`)
	}
	if attrs.Len() > 0 {
		text = "<" + tag + attrs.String() + ">" + text + "</" + tag + ">"
	}
	b.WriteString(text)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// colorCSS is a cell's colour in CSS: the terminal's own for the 16 ANSI
// colours, as given for any other, "" for the default.
func colorCSS(c color.Color) string {
	switch c := c.(type) {
	case nil:
		return ""
	case ansi.BasicColor:
		return "var(--hotty-ansi-" + strconv.Itoa(int(c)) + ")"
	case ansi.IndexedColor:
		if c < 16 {
			return "var(--hotty-ansi-" + strconv.Itoa(int(c)) + ")"
		}
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return "transparent"
	}
	return "#" + hex2(r>>8) + hex2(g>>8) + hex2(b>>8)
}

func hex2(v uint32) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>4&0xf], digits[v&0xf]})
}

// num is a scale in CSS: at most three decimals.
func num(v float64) string {
	v = math.Round(v*1000) / 1000
	return strconv.FormatFloat(v, 'f', -1, 64)
}
