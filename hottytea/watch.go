package hottytea

import (
	"bytes"
	"io"
	"os"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Some of what Bubble Tea's renderer writes can take placements with it.
// Erasing the display (ED 2, ED 3) removes kitty images, and with them the
// surfaces of a host built on kitty images, or the polyfill's. Scrolling a
// region (DECSTBM with SU, SD, IL, DL) moves or clips them, and so does a
// reverse index at the top row, or a line feed at the bottom one. The
// renderer does these on its own: a full repaint after a resize or once it
// learns a terminal mode, a region scroll to move lines cheaply. The program
// never sees it happen, so the Session reads the output on its way out, and
// after such a frame places every surface again.
//
// An RI below the top row scrolls nothing: the renderer writes it as a cheap
// cursor up by one, on every frame that moves the cursor up a row (a hover
// does). Taking that for a scroll would place every surface again on each of
// them, and they would jitter. So the watcher follows the cursor's row, as
// far as the bytes tell: cursor movement, line feeds, save and restore. It
// counts an RI only at the top row or with the row unknown, and an LF (or IND,
// NEL) only at the bottom row, once the Session has told it the height; until
// then a line feed makes the row unknown, since it may have scrolled.
//
// Printable text and its wrap are not followed. A wrap makes the row
// underestimated: for an RI that errs toward a needless relayout, which is
// what every RI did before; for an LF at the bottom it is a missed scroll,
// which was missed before.

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

// wrote runs after every write: a waiting ping goes right after a frame.
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

// scan reports whether p erases or scrolls, and sends erasedMsg once until
// the Session has handled it.
func (w *watcher) scan(p []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	hit := false
	for _, c := range p {
		switch w.state {
		case 0:
			if c == 0x1b {
				w.state = 'e'
			} else if c >= 0x0a && c <= 0x0c { // LF, VT, FF
				hit = w.down(1) || hit
			}
		case 'e':
			switch c {
			case '[':
				w.state, w.private, w.dec, w.param = '[', false, false, w.param[:0]
			case ']', 'P', '_', '^', 'X': // OSC, DCS, APC, PM, SOS: skip to ST
				w.state, w.strEsc = ']', false
			case 'M': // RI: scrolls at the top row, else moves up
				hit = w.up(1) || hit
				w.state = 0
			case 'D', 'E': // IND, NEL: scroll at the bottom row, else move down
				hit = w.down(1) || hit
				w.state = 0
			case '7':
				w.saved, w.savedKnown = w.row, w.rowKnown
				w.state = 0
			case '8':
				w.row, w.rowKnown = w.saved, w.savedKnown
				w.state = 0
			case 'c': // RIS
				w.rowKnown = false
				w.state = 0
			default:
				w.state = 0
			}
		case '[':
			switch {
			case c >= 0x40 && c <= 0x7e: // the final byte
				if !w.private {
					hit = w.csi(c) || hit
				} else if w.dec && (c == 'h' || c == 'l') {
					for _, n := range w.nums() {
						if n == 47 || n == 1047 || n == 1049 { // the other screen
							w.rowKnown = false
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
	if hit && !w.pending && w.send != nil {
		w.pending = true
		// Never block the renderer's goroutine on the program's loop.
		go w.send(erasedMsg{})
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

// up moves the cursor n rows up, and reports a scroll: an RI at the top row,
// or with the row unknown.
func (w *watcher) up(n int) bool {
	if !w.rowKnown {
		return true
	}
	scrolls := w.row == 0
	w.row -= n
	w.clamp()
	return scrolls
}

// down moves the cursor n rows down, and reports a scroll: a line feed at
// the bottom row, known to be.
func (w *watcher) down(n int) bool {
	if !w.rowKnown {
		return false
	}
	if w.height == 0 {
		// A line feed may scroll, and without the height there is no telling
		// whether it left the cursor on the same row: the row is lost.
		w.rowKnown = false
		return false
	}
	scrolls := w.row == w.height-1
	w.row += n
	w.clamp()
	return scrolls
}

// csi follows the cursor through a CSI without a private marker, and reports
// whether it erases or scrolls.
func (w *watcher) csi(c byte) bool {
	p := w.nums()
	switch c {
	case 'J':
		return string(w.param) == "2" || string(w.param) == "3"
	case 'S', 'T', 'L', 'M':
		return true
	case 'r': // DECSTBM homes the cursor
		w.at(0)
		return true
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
	return false
}

// resized tells the watcher the screen's height. The cursor may have moved
// with the resize; the repaint that follows places it again.
func (w *watcher) resized(height int) {
	w.mu.Lock()
	w.height = height
	w.rowKnown = false
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
	n, err := x.out.Write(p)
	x.w.scan(p[:n])
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
	n, err := x.f.Write(p)
	x.w.scan(p[:n])
	x.w.wrote(p[:n], x.f.Write)
	return n, err
}
