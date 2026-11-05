package hottytea

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Some of what Bubble Tea's renderer writes can take placements with it.
// Erasing the display (ED 2, ED 3) removes kitty images, and with them the
// surfaces of a host built on kitty images, or the polyfill's. Scrolling a
// region (SU, SD, IL, DL inside the margins DECSTBM sets) moves or clips
// them, and so does a reverse index at the top margin, or a line feed at the
// bottom one. The renderer does these on its own: a full repaint after a
// resize or once it learns a terminal mode, a region scroll to move lines
// cheaply (hard scrolling, which Bubble Tea v2 has no option to turn off).
// The program never sees it happen, so the Session reads the output on its
// way out.
//
// The re-place goes inside the frame that moved the surfaces, before its
// ESU. A host draws a synchronized update (mode 2026) whole: it applies the
// scroll and the placements together, so the surfaces never show scrolled.
// A re-place that went out after the ESU, as a message to the program and a
// layout and a flush, showed the scrolled state first and the snap back
// after it: a jitter, once per scrolling frame. So the output writer does
// it itself. The Session keeps a snapshot of what it would send to place
// each surface on screen again, with the rows it covers; the watcher follows
// the scroll region, collects the rows each scroll or erase affects, and
// writes the snapshot's commands for the surfaces on those rows right
// before the ESU that ends the frame (once per frame, if a write holds
// several). A write with such a hit and no ESU after it, whether the
// terminal has no synchronized output or the frame was cut short, gets the
// commands right after it. Nothing waits on a later write: a frame whose
// ESU never comes, or a program that dies mid-frame, has still had its
// placements. The bytes injected are not counted in what Write returns, and
// not read by the watcher: they save and restore the cursor around a move,
// which leaves its row where it was.
//
// Only what the snapshot cannot repair goes to the program, as erasedMsg:
// a hit when the Session has nothing in the snapshot, or a surface whose
// document has been queued but not yet written, which the host does not
// know yet. The Session then places every surface again, as before.
//
// An RI below the top margin scrolls nothing: the renderer writes it as a
// cheap cursor up by one, on every frame that moves the cursor up a row (a
// hover does). Taking that for a scroll would place every surface again on
// each of them. So the watcher follows the cursor's row, as far as the bytes
// tell: cursor movement, line feeds, save and restore. It counts an RI only
// at the top margin or with the row unknown, and an LF (or IND, NEL) only at
// the bottom margin, once it knows where that is (a margin set by DECSTBM, or
// the Session's height from tea.WindowSizeMsg); until then a line feed makes
// the row unknown, since it may have scrolled. Where the row, the height or
// the margins are unknown, a scroll covers the whole screen.
//
// Printable text and its wrap are not followed. A wrap makes the row
// underestimated: for an RI that errs toward a needless repair, which is
// what every RI did before; for an LF at the bottom it is a missed scroll,
// which was missed before.

// snapEntry is what the Session would send to place a surface again, and
// the rows on screen it covers. skip marks a surface whose document is not
// on the host yet: it cannot be placed until it is.
type snapEntry struct {
	cmd    string
	y0, y1 int
	skip   bool
}

// affected is the rows of the screen one or more hits moved or erased: the
// span from the first to the last.
type affected struct {
	any, all bool
	lo, hi   int
}

func (a *affected) every() { a.any, a.all = true, true }

func (a *affected) add(lo, hi int) {
	if !a.any || lo < a.lo {
		a.lo = lo
	}
	if !a.any || hi > a.hi {
		a.hi = hi
	}
	a.any = true
}

func (a affected) meets(y0, y1 int) bool { return a.all || (a.any && y0 <= a.hi && y1 >= a.lo) }

// watcher scans the output for those sequences. It keeps its parser state
// across writes, since a sequence can be split between two.
type watcher struct {
	mu      sync.Mutex
	state   byte // 0 ground, 'e' after ESC, '[' in a CSI, ']' in a string
	private bool // the CSI has a private marker (?, >, =, <)
	dec     bool // ... and it is ?
	param   []byte
	strEsc  bool // an ESC inside a string: ST may follow

	// The cursor's row, from 0, once known; the saved one (DECSC, CSI s);
	// and the screen's height, 0 until the Session knows it.
	row, saved           int
	rowKnown, savedKnown bool
	height               int

	// The scroll region DECSTBM set, from 0 (regBot -1: down to the last
	// row); without regSet it is the whole screen.
	regSet         bool
	regTop, regBot int

	// What the hits since the last repair affected, the snapshot to repair
	// them from, and how many frames were repaired.
	aff     affected
	snap    []snapEntry
	repairs int

	send    func(tea.Msg)
	pending bool

	// A ping waiting for the next frame (Session.Ping), and when the last
	// one was written; out writes it, once anything has been written.
	ping    []byte
	pingAt  time.Time
	out     func([]byte) (int, error)
	written int // every byte written, frames and hotty alike
}

// frameEnd ends every frame Bubble Tea writes to a terminal that has
// synchronized output (mode 2026).
var frameEnd = []byte("\x1b[?2026l")

// wrote runs after every write: a waiting ping goes right after a frame. p
// is what the caller wrote.
func (w *watcher) wrote(p []byte, out func([]byte) (int, error)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.out = out
	w.written += len(p)
	if w.ping != nil && bytes.Contains(p, frameEnd) {
		w.sendPing()
	}
}

// sendPing writes the waiting ping, and reports whether it could: not
// before anything has been written. The caller holds w.mu.
func (w *watcher) sendPing() bool {
	if w.out == nil {
		return false
	}
	_, _ = w.out(w.ping)
	w.written += len(w.ping)
	w.ping = nil
	w.pingAt = time.Now()
	return true
}

// setSnap replaces the snapshot.
func (w *watcher) setSnap(snap []snapEntry) {
	w.mu.Lock()
	w.snap = snap
	w.mu.Unlock()
}

// sent says the documents queued so far are on the host.
func (w *watcher) sent() {
	w.mu.Lock()
	for i := range w.snap {
		w.snap[i].skip = false
	}
	w.mu.Unlock()
}

// repaired is the number of frames repaired.
func (w *watcher) repaired() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.repairs
}

// scan reads p for hits, as through does, and writes nothing.
func (w *watcher) scan(p []byte) {
	_, _ = w.through(p, func(b []byte) (int, error) { return len(b), nil })
}

// through reads p, and writes it with write, with the commands that place
// again the surfaces its hits moved: before the ESU that ends the frame
// they are in, or after p when no ESU follows. It returns what the caller
// wrote, never the injected bytes. The caller does not hold w.mu.
func (w *watcher) through(p []byte, write func([]byte) (int, error)) (int, error) {
	w.mu.Lock()
	var (
		buf      []byte   // p with the commands in it, once there are any
		injected [][2]int // where each is in buf, and its length
		copied   int      // p[:copied] is in buf
		fallback bool
	)
	inject := func(at int, cmds string) {
		if cmds == "" {
			return
		}
		if buf == nil {
			buf = make([]byte, 0, len(p)+len(cmds))
		}
		buf = append(buf, p[copied:at]...)
		copied = at
		injected = append(injected, [2]int{len(buf), len(cmds)})
		buf = append(buf, cmds...)
	}
	for pos := 0; ; {
		i := bytes.Index(p[pos:], frameEnd)
		if i < 0 {
			w.follow(p[pos:])
			cmds, fb := w.repair()
			fallback = fallback || fb
			inject(len(p), cmds)
			break
		}
		w.follow(p[pos : pos+i])
		cmds, fb := w.repair()
		fallback = fallback || fb
		inject(pos+i, cmds)
		pos += i + len(frameEnd)
	}
	if fallback && !w.pending && w.send != nil {
		w.pending = true
		// Never block the renderer's goroutine on the program's loop.
		go w.send(erasedMsg{})
	}
	w.mu.Unlock()

	if buf == nil {
		n, err := write(p)
		if n < len(p) {
			w.mu.Lock()
			w.rowKnown = false // a short write: p was scanned whole
			w.mu.Unlock()
		}
		return n, err
	}
	buf = append(buf, p[copied:]...)
	n, err := write(buf)
	// What the caller wrote: n less the injected bytes among them.
	kept := n
	for _, in := range injected {
		kept -= min(in[1], max(0, n-in[0]))
	}
	w.mu.Lock()
	if n < len(buf) {
		w.rowKnown = false // a short write: p was scanned whole
	}
	w.written += n - kept // the injected bytes; wrote counts the caller's
	w.mu.Unlock()
	return kept, err
}

// repair takes the rows the hits so far affected, and returns the commands
// that place the surfaces on them again, and whether the program must lay
// out again instead: nothing to repair with, or a surface that is not on
// the host yet. The caller holds w.mu.
func (w *watcher) repair() (cmds string, fallback bool) {
	a := w.aff
	w.aff = affected{}
	if !a.any {
		return "", false
	}
	if len(w.snap) == 0 {
		return "", true
	}
	var b strings.Builder
	n := 0
	for _, e := range w.snap {
		switch {
		case !a.meets(e.y0, e.y1):
		case e.skip:
			fallback = true
		default:
			b.WriteString(e.cmd)
			n++
		}
	}
	if n > 0 {
		w.repairs++
	}
	return b.String(), fallback
}

// follow reads p, noting what it erases or scrolls in w.aff and following
// the cursor and the margins. The caller holds w.mu.
func (w *watcher) follow(p []byte) {
	for _, c := range p {
		switch w.state {
		case 0:
			if c == 0x1b {
				w.state = 'e'
			} else if c >= 0x0a && c <= 0x0c { // LF, VT, FF
				w.down()
			}
		case 'e':
			switch c {
			case '[':
				w.state, w.private, w.dec, w.param = '[', false, false, w.param[:0]
			case ']', 'P', '_', '^', 'X': // OSC, DCS, APC, PM, SOS: skip to ST
				w.state, w.strEsc = ']', false
			case 'M': // RI: scrolls at the top margin, else moves up
				w.up()
				w.state = 0
			case 'D', 'E': // IND, NEL: scroll at the bottom margin, else move down
				w.down()
				w.state = 0
			case '7':
				w.saved, w.savedKnown = w.row, w.rowKnown
				w.state = 0
			case '8':
				w.row, w.rowKnown = w.saved, w.savedKnown
				w.state = 0
			case 'c': // RIS
				w.rowKnown, w.regSet = false, false
				w.state = 0
			default:
				w.state = 0
			}
		case '[':
			switch {
			case c >= 0x40 && c <= 0x7e: // the final byte
				if !w.private {
					w.csi(c)
				} else if w.dec && (c == 'h' || c == 'l') {
					for _, n := range w.nums() {
						if n == 47 || n == 1047 || n == 1049 { // the other screen
							w.rowKnown, w.regSet = false, false
						}
					}
				}
				w.state = 0
			case c == '?' || c == '>' || c == '=' || c == '<':
				w.private = true
				w.dec = c == '?'
			case len(w.param) < 16:
				w.param = append(w.param, c)
			}
		case ']':
			switch {
			case c == 0x07 || (w.strEsc && c == '\\'):
				w.state = 0
			default:
				w.strEsc = c == 0x1b
			}
		}
	}
}

// nums is the CSI's parameters; an empty or omitted one is 0.
func (w *watcher) nums() []int {
	var out []int
	n := 0
	for _, c := range w.param {
		switch {
		case c >= '0' && c <= '9':
			if n < 100000 {
				n = n*10 + int(c-'0')
			}
		case c == ';':
			out = append(out, n)
			n = 0
		}
	}
	return append(out, n)
}

// arg is parameter i, or def when it is omitted, empty or 0.
func arg(p []int, i, def int) int {
	if i < len(p) && p[i] > 0 {
		return p[i]
	}
	return def
}

// at puts the cursor on row r, from 0.
func (w *watcher) at(r int) {
	w.row, w.rowKnown = r, true
	w.clamp()
}

func (w *watcher) clamp() {
	if w.row < 0 {
		w.row = 0
	}
	if w.height > 0 && w.row >= w.height {
		w.row = w.height - 1
	}
}

// margins are the scroll region's top and bottom rows, from 0, as far as
// they are known: the top always is; the bottom is not without DECSTBM's
// or the height. A region that cannot be one is not known either.
func (w *watcher) margins() (top, bot int, ok bool) {
	if w.regSet {
		top = w.regTop
	}
	switch {
	case w.regSet && w.regBot >= 0:
		bot = w.regBot
	case w.height > 0:
		bot = w.height - 1
	default:
		return top, 0, false
	}
	if w.height > 0 && bot >= w.height {
		bot = w.height - 1
	}
	return top, bot, top < bot
}

// scrolled notes a scroll of the region: its rows, or every row when the
// margins are not known.
func (w *watcher) scrolled() {
	if top, bot, ok := w.margins(); ok {
		w.aff.add(top, bot)
	} else {
		w.aff.every()
	}
}

// up moves the cursor one row up (RI), and notes a scroll: at the top
// margin, or with the row unknown.
func (w *watcher) up() {
	if !w.rowKnown {
		w.scrolled()
		return
	}
	if top, _, _ := w.margins(); w.row == top {
		w.scrolled()
		return
	}
	w.row--
	w.clamp()
}

// down moves the cursor one row down (LF, VT, FF, IND, NEL), and notes a
// scroll at the bottom margin, known to be.
func (w *watcher) down() {
	if !w.rowKnown {
		return
	}
	_, bot, ok := w.margins()
	if !ok {
		// A line feed may scroll, and without the bottom there is no telling
		// whether it left the cursor on the same row: the row is lost.
		w.rowKnown = false
		return
	}
	switch {
	case w.row == bot:
		w.scrolled()
	case w.row < bot || w.height > 0:
		w.row++
		w.clamp()
	default: // below the margin, with the last row unknown
		w.rowKnown = false
	}
}

// shifted notes what IL and DL move: the lines from the cursor's row to the
// bottom margin, if the cursor is in the region; the whole region if the row
// is not known.
func (w *watcher) shifted() {
	top, bot, ok := w.margins()
	switch {
	case !ok:
		w.aff.every()
	case !w.rowKnown:
		w.aff.add(top, bot)
	case w.row >= top && w.row <= bot:
		w.aff.add(w.row, bot)
	}
}

// csi follows the cursor through a CSI without a private marker, and notes
// what it erases or scrolls.
func (w *watcher) csi(c byte) {
	p := w.nums()
	switch c {
	case 'J':
		if string(w.param) == "2" || string(w.param) == "3" {
			w.aff.every()
		}
	case 'S', 'T':
		w.scrolled()
	case 'L', 'M':
		w.shifted()
	case 'r': // DECSTBM sets the margins and homes the cursor; it moves nothing
		w.regSet, w.regTop, w.regBot = true, arg(p, 0, 1)-1, arg(p, 1, 0)-1
		if len(w.param) == 0 {
			w.regSet = false
		}
		w.at(0)
	case 'H', 'f':
		w.at(arg(p, 0, 1) - 1)
	case 'd':
		w.at(arg(p, 0, 1) - 1)
	case 'A', 'F':
		if w.rowKnown {
			w.row -= arg(p, 0, 1)
			w.clamp()
		}
	case 'B', 'E':
		if w.rowKnown {
			w.row += arg(p, 0, 1)
			w.clamp()
		}
	case 's':
		w.saved, w.savedKnown = w.row, w.rowKnown
	case 'u':
		w.row, w.rowKnown = w.saved, w.savedKnown
	}
}

// resized tells the watcher the screen's height. The cursor may have moved
// with the resize, and the margins are reset; the repaint that follows
// places the surfaces again.
func (w *watcher) resized(height int) {
	w.mu.Lock()
	w.height = height
	w.rowKnown = false
	w.regSet = false // a resize resets the margins
	w.mu.Unlock()
}

// handled lets the next erase through.
func (w *watcher) handled() {
	w.mu.Lock()
	w.pending = false
	w.mu.Unlock()
}

// Watch has the Session read what the program writes: give the writer it
// returns to Bubble Tea (tea.WithOutput), and Attach the program before it
// runs. For the terminal's own file, use WatchFile.
func (h *Session) Watch(out io.Writer) io.Writer {
	return writer{out, &h.watch}
}

// WatchFile is Watch for a terminal's file, such as os.Stdout: what it
// returns is still a terminal to Bubble Tea, which then sizes it and sets
// it to raw mode.
func (h *Session) WatchFile(f *os.File) *File {
	return &File{f: f, w: &h.watch}
}

// Written counts every byte written through Watch or WatchFile: frames and
// hotty commands alike.
func (h *Session) Written() int {
	h.watch.mu.Lock()
	defer h.watch.mu.Unlock()
	return h.watch.written
}

// Attach gives the Session the program to tell when the screen was erased
// or scrolled under the surfaces: s.Attach(prog.Send), before prog.Run.
// Without it the Session never asks for a relayout.
func (h *Session) Attach(send func(tea.Msg)) {
	h.watch.mu.Lock()
	h.watch.send = send
	h.watch.mu.Unlock()
}

type writer struct {
	out io.Writer
	w   *watcher
}

func (x writer) Write(p []byte) (int, error) {
	n, err := x.w.through(p, x.out.Write)
	x.w.wrote(p[:n], x.out.Write)
	return n, err
}

// File is a terminal whose output the Session reads (WatchFile).
type File struct {
	f *os.File
	w *watcher
}

// Read reads the terminal.
func (x *File) Read(p []byte) (int, error) { return x.f.Read(p) }

// Close closes the terminal's file.
func (x *File) Close() error { return x.f.Close() }

// Fd is the terminal's file descriptor, so that Bubble Tea finds a
// terminal.
func (x *File) Fd() uintptr { return x.f.Fd() }

// Write writes to the terminal, and reads what was written.
func (x *File) Write(p []byte) (int, error) {
	n, err := x.w.through(p, x.f.Write)
	x.w.wrote(p[:n], x.f.Write)
	return n, err
}
