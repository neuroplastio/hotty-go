package hottytea

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

func TestWatcherFindsErasesAndScrolls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		want   bool
	}{
		{"erase display", []string{"\x1b[?2026h\x1b[H\x1b[2J hello"}, true},
		{"erase split between writes", []string{"\x1b[H\x1b", "[", "2", "J"}, true},
		{"region scroll", []string{"\x1b[10;36r\x1b[10;1H\x1b[2T\x1b[1;37r"}, true},
		{"reverse index at the top", []string{"\x1b[H\x1bM"}, true},
		{"erase below only", []string{"\x1b[J\x1b[0J\x1b[K\x1b[5X"}, false},
		{"private modes", []string{"\x1b[?2026h\x1b[?1049h\x1b[?25l\x1b[>4;2m\x1b[=1;1u"}, false},
		{"cursor and colours", []string{"\x1b7\x1b[2;1H\x1b[38;2;1;2;3mabc\x1b[m\x1b8"}, false},
		{"a hotty payload", []string{"\x1b]7279;a=doc:s=x;MltTTUJb", "WzJKG1syVA==\x1b\\"}, false},
		{"a title with an ESC-like body", []string{"\x1b]2;[2J\x07text"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan tea.Msg, 4)
			w := &watcher{send: func(m tea.Msg) { got <- m }}
			for _, s := range tc.writes {
				w.scan([]byte(s))
			}
			select {
			case <-got:
				if !tc.want {
					t.Fatal("reported an erase")
				}
			case <-time.After(50 * time.Millisecond):
				if tc.want {
					t.Fatal("missed an erase")
				}
			}
		})
	}
}

func TestWatcherReportsOnceUntilHandled(t *testing.T) {
	got := make(chan tea.Msg, 4)
	w := &watcher{send: func(m tea.Msg) { got <- m }}
	w.scan([]byte("\x1b[2J"))
	w.scan([]byte("\x1b[2J"))
	<-got
	select {
	case <-got:
		t.Fatal("a second report before the first was handled")
	case <-time.After(30 * time.Millisecond):
	}
	w.handled()
	w.scan([]byte("\x1b[2J"))
	select {
	case <-got:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("no report after handled")
	}
}

// scanned feeds the writes to a watcher that knows the screen's height
// (0: not yet), and reports whether it asked for a relayout.
func scanned(height int, writes ...string) bool {
	got := make(chan tea.Msg, 4)
	w := &watcher{send: func(m tea.Msg) { got <- m }}
	if height > 0 {
		w.resized(height)
	}
	for _, s := range writes {
		w.scan([]byte(s))
	}
	select {
	case <-got:
		return true
	case <-time.After(30 * time.Millisecond):
		return false
	}
}

func TestWatcherFollowsTheCursor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		height int
		writes []string
		want   bool
	}{
		{"RI below the top row moves the cursor", 24, []string{"\x1b[5;1H\x1bM"}, false},
		{"RI after CUD and VPA", 24, []string{"\x1b[3;1H\x1b[2B\x1bM", "\x1b[9d\x1bM"}, false},
		{"RI at the top row scrolls", 24, []string{"\x1b[H\x1bM"}, true},
		{"RI at the top after CUU", 24, []string{"\x1b[3;1H\x1b[2A\x1bM", "\x1b[9;1H\x1b[99A\x1bM"}, true},
		{"RI after CUP with an empty row", 24, []string{"\x1b[;5H\x1bM"}, true},
		{"RI with the row unknown scrolls", 24, []string{"\x1bM"}, true},
		{"RI with the height unknown, row known", 0, []string{"\x1b[5;1H\x1bM"}, false},
		{"LF at the bottom row scrolls", 24, []string{"\x1b[24;1H\n"}, true},
		{"LF at the bottom via CUP past it", 24, []string{"\x1b[99;1H\n"}, true},
		{"VT, FF, IND and NEL at the bottom", 24, []string{"\x1b[24;1H\v"}, true},
		{"FF at the bottom", 24, []string{"\x1b[24;1H\f"}, true},
		{"IND at the bottom", 24, []string{"\x1b[24;1H\x1bD"}, true},
		{"NEL at the bottom", 24, []string{"\x1b[24;1H\x1bE"}, true},
		{"LF above the bottom row", 24, []string{"\x1b[23;1H\n"}, false},
		{"LF moves down to the bottom, then scrolls", 24, []string{"\x1b[22;1H\n\n", "\n"}, true},
		{"LF at the bottom with the height unknown", 0, []string{"\x1b[24;1H\n"}, false},
		{"height unknown: LF loses the row, so an RI scrolls", 0, []string{"\x1b[5;1H\n\x1bM"}, true},
		{"LF with the row unknown", 24, []string{"\n"}, false},
		{"DECSTBM homes the cursor", 24, []string{"\x1b[5;1H\x1b[1;20r\x1bM"}, true},
		{"restore (ESC 7, ESC 8)", 24, []string{"\x1b[H\x1b7\x1b[9;1H\x1b8\x1bM"}, true},
		{"restore (CSI s, CSI u)", 24, []string{"\x1b[9;1H\x1b[s\x1b[H\x1b[u\x1bM"}, false},
		{"alternate screen makes the row unknown", 24, []string{"\x1b[5;1H\x1b[?1049h\x1bM"}, true},
		{"RIS makes the row unknown", 24, []string{"\x1b[5;1H\x1bc\x1bM"}, true},
		{"CNL and CPL", 24, []string{"\x1b[H\x1b[3E\x1b[1F\x1bM"}, false},
		{"the row survives a split CUP", 24, []string{"\x1b[1", "2;", "4", "H\x1b", "M"}, false},
		{"a split CUP to the top still scrolls", 24, []string{"\x1b[5;1H", "\x1b[", "H", "\x1b", "M"}, true},
		{"a split LF after a split CUP at the bottom", 24, []string{"\x1b[2", "4;1", "H\n"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanned(tc.height, tc.writes...); got != tc.want {
				t.Fatalf("asked for a relayout = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWatcherResizeForgetsTheRow(t *testing.T) {
	w := &watcher{}
	w.scan([]byte("\x1b[5;1H"))
	if !w.rowKnown || w.row != 4 {
		t.Fatalf("row %d known %v", w.row, w.rowKnown)
	}
	w.resized(10)
	if w.rowKnown || w.height != 10 {
		t.Fatalf("after a resize: row known %v, height %d", w.rowKnown, w.height)
	}
}

// uvFrames renders a screen with ultraviolet's own renderer, then the same one
// with the gutter bar moved to another row, and returns both renders' bytes:
// the second is what a hover writes, and starts where the first left the cursor.
func uvFrames(t *testing.T, from, to int) (first, second string) {
	t.Helper()
	const w, h = 40, 24
	var out bytes.Buffer
	r := uv.NewTerminalRenderer(&out, []string{"TERM=xterm-256color"})
	r.SetFullscreen(true)
	screen := func(bar int) *uv.RenderBuffer {
		b := uv.NewRenderBuffer(w, h)
		for y := 0; y < h; y++ {
			b.SetCell(0, y, &uv.Cell{Content: " ", Width: 1})
		}
		b.SetCell(0, 1, &uv.Cell{Content: "T", Width: 1})
		b.SetCell(2, bar, &uv.Cell{Content: "|", Width: 1})
		return b
	}
	r.Render(screen(from))
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	first = out.String()
	out.Reset()
	r.Render(screen(to))
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	return first, out.String()
}

func TestWatcherLeavesAHoverFrameAlone(t *testing.T) {
	// The bar moves up a row and down a row; at least one direction makes
	// the renderer write an RI, so the test proves what it claims.
	sawRI := false
	for _, mv := range [][2]int{{12, 11}, {11, 12}, {5, 4}, {4, 5}} {
		first, frame := uvFrames(t, mv[0], mv[1])
		sawRI = sawRI || strings.Contains(frame, "\x1bM")
		got := make(chan tea.Msg, 4)
		w := &watcher{send: func(m tea.Msg) { got <- m }}
		w.resized(24)
		w.scan([]byte("\x1b[?2026h" + first + "\x1b[?2026l"))
		time.Sleep(20 * time.Millisecond)
		for len(got) > 0 { // the first render's own erase, if any
			<-got
		}
		w.handled()
		w.scan([]byte("\x1b[?2026h" + frame + "\x1b[?2026l"))
		select {
		case <-got:
			t.Errorf("bar %d -> %d: asked for a relayout on %q", mv[0], mv[1], frame)
		case <-time.After(30 * time.Millisecond):
		}
	}
	if !sawRI {
		t.Fatal("no hover frame had an RI: the test no longer tests the fix")
	}
}
