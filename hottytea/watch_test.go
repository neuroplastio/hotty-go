package hottytea

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
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

// --- the re-place inside the frame ---

// recorded is a Session whose output goes to buf, with the surfaces placed
// and their commands taken (written), as a host has them; the program is
// asked to relayout through got.
type recorded struct {
	s   *Session
	w   io.Writer
	buf *bytes.Buffer
	got chan tea.Msg
}

func record(t *testing.T, height int, surfaces []Surface) *recorded {
	t.Helper()
	r := &recorded{s: native(), buf: &bytes.Buffer{}, got: make(chan tea.Msg, 16)}
	r.w = r.s.Watch(r.buf)
	r.s.Attach(func(m tea.Msg) { r.got <- m })
	r.s.Update(tea.WindowSizeMsg{Width: 95, Height: height})
	for i := range surfaces {
		surfaces[i].Doc = func() string { return "<p>x</p>" }
	}
	r.s.Layout(surfaces)
	r.s.Flush()()
	return r
}

func (r *recorded) write(t *testing.T, p string) string {
	t.Helper()
	r.buf.Reset()
	n, err := r.w.Write([]byte(p))
	if err != nil || n != len(p) {
		t.Fatalf("Write returned %d, %v for %d bytes", n, err, len(p))
	}
	return r.buf.String()
}

// noRelayout fails when the watcher asked the program to lay out again.
func (r *recorded) noRelayout(t *testing.T) {
	t.Helper()
	select {
	case m := <-r.got:
		t.Fatalf("asked for %#v", m)
	case <-time.After(30 * time.Millisecond):
	}
}

// place is the command that places surface s again at its rows.
func place(s Surface) string {
	return hotty.PlaceAt(s.Name, s.Rect.X, s.Rect.Y, hotty.Placement{Cols: s.Rect.W, Rows: s.Rect.H})
}

// threeSurfaces: a at rows 2-3, b at rows 10-12, c at row 20.
func threeSurfaces() (list []Surface, a, b, c Surface) {
	a = Surface{Name: "a", Rect: Rect{0, 2, 10, 2}}
	b = Surface{Name: "b", Rect: Rect{0, 10, 10, 3}}
	c = Surface{Name: "c", Rect: Rect{4, 20, 10, 1}}
	return []Surface{a, b, c}, a, b, c
}

func TestARegionScrollIsRepairedBeforeTheESU(t *testing.T) {
	list, _, b, _ := threeSurfaces()
	r := record(t, 30, list)
	// Region rows 9..14 (1-based 10;15): b meets it, a and c do not.
	frame := "\x1b[?2026h\x1b[10;15r\x1b[10;1H\x1b[2T\x1b[1;30r\x1b[?2026l"
	want := "\x1b[?2026h\x1b[10;15r\x1b[10;1H\x1b[2T\x1b[1;30r" + place(b) + "\x1b[?2026l"
	if got := r.write(t, frame); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	r.noRelayout(t)
	if r.s.Count.Relayouts != 0 {
		t.Fatalf("relayouts %d", r.s.Count.Relayouts)
	}
	r.s.Update(nil)
	if r.s.Count.Repairs != 1 {
		t.Fatalf("repairs %d", r.s.Count.Repairs)
	}
}

func TestAScrollMeetingNoSurfaceInjectsNothing(t *testing.T) {
	list, _, _, _ := threeSurfaces()
	r := record(t, 30, list)
	frame := "\x1b[?2026h\x1b[5;8r\x1b[5;1H\x1b[2T\x1b[1;30r\x1b[?2026l"
	if got := r.write(t, frame); got != frame {
		t.Fatalf("got %q", got)
	}
	r.noRelayout(t)
	// DECSTBM alone moves nothing.
	if got := r.write(t, "\x1b[?2026h\x1b[10;15r\x1b[1;30r\x1b[?2026l"); got != "\x1b[?2026h\x1b[10;15r\x1b[1;30r\x1b[?2026l" {
		t.Fatalf("got %q", got)
	}
}

func TestEraseDisplayIsRepairedBeforeTheESUForEverySurface(t *testing.T) {
	list, a, b, c := threeSurfaces()
	r := record(t, 30, list)
	got := r.write(t, "\x1b[?2026h\x1b[H\x1b[2Jcells\x1b[?2026l")
	want := "\x1b[?2026h\x1b[H\x1b[2Jcells" + place(a) + place(b) + place(c) + "\x1b[?2026l"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	r.noRelayout(t)
}

func TestAFrameWithoutESUGetsItsCommandsAfterTheWrite(t *testing.T) {
	list, _, b, _ := threeSurfaces()
	r := record(t, 30, list)
	cut := "\x1b[?2026h\x1b[10;15r\x1b[10;1H\x1b[2T\x1b[1;30r"
	if got := r.write(t, cut); got != cut+place(b) {
		t.Fatalf("got %q", got)
	}
	r.noRelayout(t)
	// Nothing was held: a later write, with the ESU, goes as it is.
	for _, later := range []string{"cells", "\x1b[?2026l", "\x1b[?2026h\x1b[3;1Hx\x1b[?2026l"} {
		if got := r.write(t, later); got != later {
			t.Fatalf("a later write %q came out %q", later, got)
		}
	}
	// And without synchronized output at all, the same.
	if got := r.write(t, "\x1b[10;15r\x1b[10;1H\x1bM"); got != "\x1b[10;15r\x1b[10;1H\x1bM"+place(b) {
		t.Fatalf("got %q", got)
	}
}

func TestTwoFramesInOneWriteAreRepairedEach(t *testing.T) {
	list, a, b, _ := threeSurfaces()
	r := record(t, 30, list)
	f1 := "\x1b[?2026h\x1b[2;5r\x1b[2;1H\x1bM\x1b[1;30r"
	f2 := "\x1b[?2026h\x1b[10;15r\x1b[10;1H\x1bM\x1b[1;30r"
	got := r.write(t, f1+"\x1b[?2026l"+f2+"\x1b[?2026l")
	want := f1 + place(a) + "\x1b[?2026l" + f2 + place(b) + "\x1b[?2026l"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestInjectedBytesLeaveTheCursorRowAlone(t *testing.T) {
	list, _, _, _ := threeSurfaces()
	r := record(t, 30, list)
	r.s.watch.scan([]byte("\x1b[12;1H\x1b7\x1b[7;1H")) // a saved cursor, and a row
	frame := "\x1b[?2026h\x1b[20;1H\x1b[2J\x1b[25;1H\x1b[?2026l"
	ref := &watcher{}
	ref.resized(30)
	ref.scan([]byte("\x1b[12;1H\x1b7\x1b[7;1H"))
	ref.scan([]byte(frame))
	got := r.write(t, frame)
	if !strings.Contains(got, "a=place") {
		t.Fatalf("nothing injected: %q", got)
	}
	w := &r.s.watch
	if w.row != ref.row || !w.rowKnown || w.saved != ref.saved || w.row != 24 || w.saved != 11 {
		t.Fatalf("row %d saved %d; without the injection %d, %d", w.row, w.saved, ref.row, ref.saved)
	}
}

// A writer that takes at most max bytes a write.
type shortWriter struct {
	bytes.Buffer
	max int
}

func (s *shortWriter) Write(p []byte) (int, error) {
	if len(p) > s.max {
		n, _ := s.Buffer.Write(p[:s.max])
		return n, io.ErrShortWrite
	}
	return s.Buffer.Write(p)
}

func TestAShortWriteReturnsTheCallersCount(t *testing.T) {
	list, _, b, _ := threeSurfaces()
	frame := "\x1b[?2026h\x1b[10;15r\x1b[10;1H\x1b[2T\x1b[1;30r\x1b[?2026l"
	at := strings.Index(frame, "\x1b[?2026l")
	for _, max := range []int{3, at, at + 5, at + len(place(b)), at + len(place(b)) + 4} {
		h := native()
		sw := &shortWriter{max: max}
		w := h.Watch(sw)
		h.Update(tea.WindowSizeMsg{Height: 30})
		for i := range list {
			list[i].Doc = func() string { return "x" }
		}
		h.Layout(list)
		h.Flush()()
		n, err := w.Write([]byte(frame))
		// frame[:at], the placement, then the ESU: the caller's count is
		// what of its own bytes went out.
		l := len(place(b))
		want := min(max, at)
		if max > at+l {
			want = min(len(frame), at+max-at-l)
		}
		if err != io.ErrShortWrite || n != want {
			t.Errorf("max %d: %d, %v; want %d", max, n, err, want)
		}
	}
}

func TestWriteCountIsTheCallers(t *testing.T) {
	list, _, _, _ := threeSurfaces()
	r := record(t, 30, list)
	frame := "\x1b[?2026h\x1b[2J\x1b[?2026l"
	n, err := r.w.Write([]byte(frame))
	if err != nil || n != len(frame) || r.buf.Len() <= len(frame) {
		t.Fatalf("n %d, err %v, wrote %d", n, err, r.buf.Len())
	}
	if r.s.Written() != r.buf.Len() {
		t.Fatalf("Written %d of %d", r.s.Written(), r.buf.Len())
	}
}

func TestAPendingDocumentFallsBackToARelayout(t *testing.T) {
	h := native()
	got := make(chan tea.Msg, 4)
	h.Attach(func(m tea.Msg) { got <- m })
	var out bytes.Buffer
	w := h.Watch(&out)
	h.Update(tea.WindowSizeMsg{Height: 30})
	h.Layout([]Surface{{Name: "a", Rect: Rect{0, 2, 10, 2}, Doc: func() string { return "x" }}})
	// Its document has not been written: nothing to place with yet.
	_, _ = w.Write([]byte("\x1b[?2026h\x1b[2J\x1b[?2026l"))
	if out.String() != "\x1b[?2026h\x1b[2J\x1b[?2026l" {
		t.Fatalf("injected %q", out.String())
	}
	select {
	case m := <-got:
		if m != (erasedMsg{}) {
			t.Fatalf("got %#v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no relayout asked for")
	}
}

func TestTheSnapshotFollowsWhatIsPlaced(t *testing.T) {
	list, a, b, c := threeSurfaces()
	r := record(t, 30, list)
	erase := "\x1b[?2026h\x1b[2J\x1b[?2026l"
	count := func() int { return strings.Count(r.write(t, erase), "a=place") }
	if count() != 3 {
		t.Fatal("three placed")
	}
	r.s.Delete("a")
	if count() != 2 {
		t.Fatal("a deleted")
	}
	list[1].Keep = true
	r.s.Layout([]Surface{list[2]}) // b is kept (hidden), a gone
	if got := r.write(t, erase); got != "\x1b[?2026h\x1b[2J"+place(c)+"\x1b[?2026l" {
		t.Fatalf("one placed: %q", got)
	}
	_ = a
	_ = b
	r.s.DetachAll()
	if got := r.write(t, erase); got != erase {
		t.Fatalf("detached: %q", got)
	}
	r.s.Close()
	if got := r.write(t, erase); got != erase {
		t.Fatalf("closed: %q", got)
	}
}

// frames are the synchronized updates in a recording.
func frames(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range regexp.MustCompile(`(?s)\x1b\[\?2026h.*?\x1b\[\?2026l`).FindAll(data, -1) {
		out = append(out, string(f))
	}
	return out
}

// marginSurfaces are the surfaces margin had on screen when it was recorded
// (testdata/hover-sel.bin, from script): where its a=place commands put them.
func marginSurfaces(t *testing.T) []Surface {
	t.Helper()
	data, err := os.ReadFile("testdata/hover-sel.bin")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\x1b7\x1b\[(\d+);(\d+)H\x1b\]7279;a=place:s=([^:]+):c=(\d+):r=(\d+)(?::x=(\d+):y=(\d+):w=(\d+):h=(\d+))?`)
	seen := map[string]bool{}
	var out []Surface
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		if seen[m[3]] {
			break // the second round of re-places
		}
		seen[m[3]] = true
		n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
		s := Surface{Name: m[3], Rect: Rect{n(2) - 1, n(1) - 1, n(4), n(5)}}
		if m[6] != "" { // a window of the surface: its rows from y, h of them
			s.Rect.Y -= n(7)
			s.Clip = &Rect{s.Rect.X, s.Rect.Y + n(7), n(8), n(9)}
		}
		out = append(out, s)
	}
	if len(out) != 13 {
		t.Fatalf("%d surfaces in the recording", len(out))
	}
	return out
}

func TestMarginsRecordingIsRepairedInsideItsFrames(t *testing.T) {
	surfaces := marginSurfaces(t)
	r := record(t, 55, slices.Clone(surfaces))
	sorted := slices.Clone(surfaces)
	slices.SortFunc(sorted, func(a, b Surface) int { return strings.Compare(a.Name, b.Name) })

	// Surfaces on the region of the scroll frames, rows 34 to 47 (1-based).
	// A clipped surface covers only the window's rows.
	var inRegion []string
	for _, s := range sorted {
		at := s.Rect
		if s.Clip != nil {
			at = at.Intersect(*s.Clip)
		}
		if at.Y <= 46 && at.Y+at.H-1 >= 33 {
			inRegion = append(inRegion, s.Name)
		}
	}
	if len(inRegion) != 8 {
		t.Fatalf("%d surfaces in the region: %v", len(inRegion), inRegion)
	}

	scrolled := 0
	for _, f := range frames(t, "hover-sel.bin") {
		got := r.write(t, f)
		if !strings.Contains(f, "\x1b[34;47r") {
			if got != f {
				t.Fatalf("a frame without a scroll was changed: %q", f)
			}
			continue
		}
		scrolled++
		esu := strings.LastIndex(got, "\x1b[?2026l")
		if esu != len(got)-len("\x1b[?2026l") {
			t.Fatalf("the ESU is not last: %q", got)
		}
		inj := got[len(f)-len("\x1b[?2026l") : esu]
		if got[:len(f)-len("\x1b[?2026l")] != f[:len(f)-len("\x1b[?2026l")] {
			t.Fatalf("the frame was changed: %q", got)
		}
		var names []string
		for _, m := range regexp.MustCompile(`a=place:s=([^:]+):`).FindAllStringSubmatch(inj, -1) {
			names = append(names, m[1])
		}
		if !slices.Equal(names, inRegion) {
			t.Fatalf("re-placed %v, want %v", names, inRegion)
		}
		if strings.Contains(inj, ":p=") || strings.Contains(inj, ":f=") || strings.Contains(inj, ":v=") {
			t.Fatalf("flags in %q", inj)
		}
	}
	if scrolled != 10 {
		t.Fatalf("%d scroll frames, want 10", scrolled)
	}
	r.noRelayout(t)
}

func TestMarginsControlRecordingGetsNothingInjected(t *testing.T) {
	r := record(t, 55, marginSurfaces(t))
	fs := frames(t, "hover-now2.bin")
	if len(fs) < 15 {
		t.Fatalf("%d frames", len(fs))
	}
	for _, f := range fs {
		if got := r.write(t, f); got != f {
			t.Fatalf("injected into %q: %q", f, got)
		}
	}
	r.noRelayout(t)
}
