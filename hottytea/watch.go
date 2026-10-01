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
// region (DECSTBM with SU, SD, IL, DL, RI) moves or clips them. The renderer
// does both on its own: a full repaint after a resize or once it learns a
// terminal mode, a region scroll to move lines cheaply. The program never sees
// it happen, so the Session reads the output on its way out, and after such a
// frame places every surface again.

// watcher scans the output for those sequences. It keeps its parser state
// across writes, since a sequence can be split between two.
type watcher struct {
	mu      sync.Mutex
	state   byte // 0 ground, 'e' after ESC, '[' in a CSI, ']' in a string
	private bool // the CSI has a private marker (?, >, =, <)
	param   []byte
	strEsc  bool // an ESC inside a string: ST may follow

	send    func(tea.Msg)
	pending bool

	// A ping waiting for the next frame (Session.Ping), and when the last one
	// was written.
	ping     []byte
	pingWant time.Time
	pingAt   time.Time
	out      func([]byte) (int, error)
	written  int // every byte written, frames and HOTTY alike
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

// sendPing writes the waiting ping. The caller holds w.mu.
func (w *watcher) sendPing() {
	if w.out == nil {
		return
	}
	_, _ = w.out(w.ping)
	w.written += len(w.ping)
	w.ping = nil
	w.pingAt = time.Now()
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
			}
		case 'e':
			switch c {
			case '[':
				w.state, w.private, w.param = '[', false, w.param[:0]
			case ']', 'P', '_', '^', 'X': // OSC, DCS, APC, PM, SOS: skip to ST
				w.state, w.strEsc = ']', false
			case 'M': // RI: scrolls at the top margin
				hit = true
				w.state = 0
			default:
				w.state = 0
			}
		case '[':
			switch {
			case c >= 0x40 && c <= 0x7e: // the final byte
				if !w.private {
					switch c {
					case 'J':
						hit = hit || string(w.param) == "2" || string(w.param) == "3"
					case 'S', 'T', 'L', 'M', 'r':
						hit = true
					}
				}
				w.state = 0
			case c == '?' || c == '>' || c == '=' || c == '<':
				w.private = true
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

// handled lets the next erase through.
func (w *watcher) handled() {
	w.mu.Lock()
	w.pending = false
	w.mu.Unlock()
}

// Watch has the Session read what the program writes: give the writer it
// returns to Bubble Tea (tea.WithOutput). Attach the program before it
// runs.
func (h *Session) Watch(out io.Writer) io.Writer {
	return writer{out, &h.watch}
}

// WatchFile is Watch for a terminal: Bubble Tea still finds a terminal
// there (term.File), so it sizes it and sets it to raw mode.
func (h *Session) WatchFile(f *os.File) *File {
	return &File{f: f, w: &h.watch}
}

// Written counts every byte written to the terminal: frames and HOTTY.
func (h *Session) Written() int {
	h.watch.mu.Lock()
	defer h.watch.mu.Unlock()
	return h.watch.written
}

// Attach gives the Session the program to tell when the screen was erased:
// s.Attach(prog.Send).
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

// File is a terminal whose output the Session reads.
type File struct {
	f *os.File
	w *watcher
}

func (x *File) Read(p []byte) (int, error) { return x.f.Read(p) }
func (x *File) Close() error               { return x.f.Close() }
func (x *File) Fd() uintptr                { return x.f.Fd() }

func (x *File) Write(p []byte) (int, error) {
	n, err := x.f.Write(p)
	x.w.scan(p[:n])
	x.w.wrote(p[:n], x.f.Write)
	return n, err
}
