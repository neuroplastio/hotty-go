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
// them as well as they fit a cell. Box-drawing characters and block
// elements are drawn to fill their cells, as a terminal draws them, so
// borders and bars meet across rows; the dashed lines and the diagonals
// are the font's.
//
// # What it leaves out
//
// The scrollback: the screen shows what a terminal of its size would, and
// nothing that scrolled off. Images (kitty graphics, sixel), blinking, and
// the colours a program sets for the terminal's default foreground and
// background (OSC 10, 11), which would fight the host's theme. The
// emulator's answers to queries (the cursor's position, device attributes)
// are dropped, unless Replies takes them.
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
// Box-drawing characters and block elements are drawn by it (draw.go).
// Add it to the document's stylesheet once, however many screens it shows.
const CSS = `
.vt { --vt-scale: 1; display: block; box-sizing: content-box; overflow: hidden;
  font-family: var(--hotty-font, monospace); font-size: calc(var(--vt-scale) * 1rem);
  line-height: calc(var(--vt-scale) * var(--hotty-cell-h));
  letter-spacing: calc(var(--vt-scale) * var(--hotty-cell-w) - 1ch);
  width: calc(var(--vt-cols) * var(--vt-scale) * var(--hotty-cell-w)); white-space: pre;
  color: var(--hotty-fg); background: var(--hotty-bg); }
.vt > div { height: calc(var(--vt-scale) * var(--hotty-cell-h)); overflow: hidden; }
.vt a { color: inherit; }
.vt .vt-w { display: inline-block; letter-spacing: 0; text-align: center;
  width: calc(2 * var(--vt-scale) * var(--hotty-cell-w)); }
.vt .vt-b { font-weight: bold; }
.vt .vt-i { font-style: italic; }
.vt .vt-h { visibility: hidden; }
.vt .vt-s { text-decoration-line: line-through; }
.vt .vt-u { text-decoration-line: underline; }
.vt .vt-u.vt-s { text-decoration-line: underline line-through; }
.vt .vt-u2 { text-decoration-style: double; }
.vt .vt-u3 { text-decoration-style: wavy; }
.vt .vt-u4 { text-decoration-style: dotted; }
.vt .vt-u5 { text-decoration-style: dashed; }
.vt .vt-cur { color: var(--hotty-bg); background: var(--hotty-fg); }
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

// Replies takes the emulator's answers to the queries in what it is given
// (device attributes, the cursor's position, a colour), for a program that
// runs in it and waits for them: write them to its input. They are
// written from a goroutine of the Screen's, so w must be safe for that.
// Without it they are dropped.
func Replies(w io.Writer) Option { return func(s *Screen) { s.replies = w } }

// New makes a screen of cols by rows cells (at least one of each), blank,
// with the cursor at the top left. Close it when done with it.
func New(cols, rows int, opts ...Option) *Screen {
	s := &Screen{id: DefaultID, cursor: true, scale: 1, replies: io.Discard, drained: make(chan struct{})}
	for _, o := range opts {
		o(s)
	}
	s.emu = vt.NewEmulator(max(cols, 1), max(rows, 1))
	s.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { s.hidden = !visible },
		Title:            func(t string) { s.title = t },
	})
	// The emulator writes its answers to a pipe, and Write waits until
	// they are read.
	go func() {
		defer close(s.drained)
		_, _ = io.Copy(s.replies, s.emu)
	}()
	return s
}

// Write gives the screen output, as a terminal's pty would. It never fails.
func (s *Screen) Write(p []byte) (int, error) { return s.emu.Write(p) }

// WriteString is Write for a string.
func (s *Screen) WriteString(p string) (int, error) { return s.emu.WriteString(p) }

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
// now: one per row that changed, none when nothing did. After a Resize, it
// is the whole element. Before HTML, it is nothing: there is no element to
// change. Send a frame's commands together, in hotty.Sync, for the host to
// show it whole.
func (s *Screen) Delta(surface string) []string {
	if s.sent == nil {
		return nil
	}
	cols, rows := s.Size()
	if cols != s.sentCols || rows != len(s.sent) {
		return []string{hotty.MorphTo(surface, s.id, s.HTML())}
	}
	var out []string
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

// run is cells next to each other that are drawn alike.
type run struct {
	style  uv.Style
	link   string
	wide   bool
	cursor bool
	text   strings.Builder
	// The drawn character the run ends with (draw.go), and how many of it,
	// while more of it may follow.
	k rune
	n int
}

// add puts a cell's content at the run's end.
func (r *run) add(content string) {
	k, kind := drawKind(content)
	if kind&tiles != 0 && k == r.k {
		r.n++
		return
	}
	r.flushDrawn()
	if kind != 0 {
		r.k, r.n = k, 1
		return
	}
	r.text.WriteString(html.EscapeString(content))
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
	flush := func() {
		if r != nil {
			r.write(&b)
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
		wide := c.Width > 1
		if r == nil || wide || r.wide || atCursor || r.cursor || r.link != link || !r.style.Equal(&c.Style) {
			flush()
			r = &run{style: c.Style, link: link, wide: wide, cursor: atCursor}
		}
		r.add(content(c))
	}
	flush()
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

func (r *run) write(b *strings.Builder) {
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
	if r.wide {
		class = append(class, "vt-w")
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
	if len(class) > 0 || len(style) > 0 {
		var open strings.Builder
		open.WriteString("<span")
		if len(class) > 0 {
			open.WriteString(` class="` + strings.Join(class, " ") + `"`)
		}
		if len(style) > 0 {
			open.WriteString(` style="` + html.EscapeString(strings.Join(style, ";")) + `"`)
		}
		text = open.String() + ">" + text + "</span>"
	}
	if r.link != "" {
		text = `<a href="` + html.EscapeString(r.link) + `" target="_blank">` + text + `</a>`
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
