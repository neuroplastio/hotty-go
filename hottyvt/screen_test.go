package hottyvt_test

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/hottyvt"
)

// send writes commands to the host, as a program does.
func send(t *testing.T, h *hottytest.Host, cmds ...string) {
	t.Helper()
	for _, c := range cmds {
		if _, err := io.WriteString(h, c); err != nil {
			t.Fatal(err)
		}
	}
}

// decode is each command's action, target and payload.
func decode(cmds []string) []hotty.Message {
	var d hotty.Decoder
	out := make([]hotty.Message, 0, len(cmds))
	for _, c := range cmds {
		m, _ := d.Feed(c)
		out = append(out, m)
	}
	return out
}

func screen(t *testing.T, cols, rows int, opts ...hottyvt.Option) *hottyvt.Screen {
	t.Helper()
	s := hottyvt.New(cols, rows, opts...)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// What a session draws, in the chunks a pty would hand over: text and
// styles, the cursor moving, scrolling, clearing, the alternate screen, wide
// characters, hyperlinks, markup in the text, and a box with a bar in it.
var session = []string{
	"$ ls\r\n",
	"\x1b[1;34mdir\x1b[0m  \x1b[32mrun.sh\x1b[0m  <a&b>.txt\r\n$ ",
	"top\r\n",
	"\x1b[?1049h\x1b[2J\x1b[H\x1b[7m PID  CPU \x1b[0m\r\n",
	"  1   0.3\r\n  2  \x1b[38;5;196m99.0\x1b[0m\r\n",
	"\x1b[3;7H\x1b[38;2;10;200;30m12.5\x1b[0m",
	"\x1b[?1049l",
	"\x1b[4mlink:\x1b[24m \x1b]8;;https://example.com\x1b\\example\x1b]8;;\x1b\\\r\n",
	"界面 wide\r\n",
	"line\r\nline\r\nline\r\nscrolled\r\n",
	"\x1b[2J\x1b[Hcleared",
	"\r\n╭──╮\r\n│\x1b[34m█\x1b[0m░│\r\n╰──╯",
	"\x1b[?25l",
	"\x1b[?25h\x1b[2;3H",
}

// The surface, given the screen once and its deltas after every chunk, is
// at every step the document the screen makes from scratch.
func TestDeltasKeepTheSurfaceCurrent(t *testing.T) {
	h := hottytest.New(t)
	s := screen(t, 20, 6)
	twin := screen(t, 20, 6)
	send(t, h, hotty.Doc("vt", s.HTML()))
	for i, chunk := range session {
		_, _ = s.WriteString(chunk)
		_, _ = twin.WriteString(chunk)
		send(t, h, hotty.Sync(s.Delta("vt")...))
		send(t, h, hotty.Doc("fresh", twin.HTML()))
		if got, want := h.Surface("vt").HTML(), h.Surface("fresh").HTML(); got != want {
			t.Fatalf("after chunk %d %q:\n got %s\nwant %s", i, chunk, got, want)
		}
	}
}

// A frame where one row changes sends that row, and nothing else.
func TestDeltaIsTheRowsThatChanged(t *testing.T) {
	s := screen(t, 10, 4)
	_, _ = s.WriteString("one\r\ntwo\r\nthree")
	_ = s.HTML()
	if d := s.Delta("vt"); len(d) != 0 {
		t.Fatalf("nothing changed, got %d commands", len(d))
	}
	_, _ = s.WriteString("\x1b[2;1Htwo!") // row 1, and the cursor leaves row 2 for it
	ms := decode(s.Delta("vt"))
	if len(ms) != 2 {
		t.Fatalf("got %d commands, want 2: %v", len(ms), ms)
	}
	for _, m := range ms {
		if m.Get("a") != "delta" {
			t.Fatalf("not a delta: %v", m)
		}
	}
	if m := ms[0]; m.Get("t") != "vt-r1" || m.Get("op") != "inner" ||
		!strings.Contains(string(m.Payload), `two!<span class="vt-cur">`) {
		t.Errorf("row 1: %v %s", m.Control, m.Payload)
	}
	if m := ms[1]; m.Get("t") != "vt-r2" || m.Get("op") != "text" || string(m.Payload) != "three" {
		t.Errorf("row 2: %v %q", m.Control, m.Payload)
	}
}

func TestDeltaBeforeHTMLIsNothing(t *testing.T) {
	s := screen(t, 4, 2)
	_, _ = s.WriteString("x")
	if d := s.Delta("vt"); d != nil {
		t.Fatalf("got %v", d)
	}
}

func TestStyles(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"ansi", "\x1b[31mr", `<span style="color:var(--hotty-ansi-1)">r</span>`},
		{"bright", "\x1b[94mb", `<span style="color:var(--hotty-ansi-12)">b</span>`},
		{"indexed low", "\x1b[38;5;3mi", `<span style="color:var(--hotty-ansi-3)">i</span>`},
		{"indexed", "\x1b[48;5;196mi", `<span style="background:#ff0000">i</span>`},
		{"truecolor", "\x1b[38;2;1;2;255mt", `<span style="color:#0102ff">t</span>`},
		{"reverse", "\x1b[7mr", `<span style="color:var(--hotty-bg);background:var(--hotty-fg)">r</span>`},
		{"reverse colours", "\x1b[7;31;42mr", `<span style="color:var(--hotty-ansi-2);background:var(--hotty-ansi-1)">r</span>`},
		{"attributes", "\x1b[1;3;8;9ma", `<span class="vt-b vt-i vt-h vt-s">a</span>`},
		{"faint", "\x1b[2mf", `<span style="color:color-mix(in srgb, var(--hotty-fg) 60%, var(--hotty-bg))">f</span>`},
		{"faint in colour", "\x1b[2;31;44mf", `<span style="color:color-mix(in srgb, var(--hotty-ansi-1) 60%, var(--hotty-ansi-4));background:var(--hotty-ansi-4)">f</span>`},
		{"underline", "\x1b[4mu", `<span class="vt-u">u</span>`},
		{"curly underline in colour", "\x1b[4:3;58;2;255;0;0mu", `<span class="vt-u vt-u3" style="text-decoration-color:#ff0000">u</span>`},
		{"a run", "\x1b[32mabc\x1b[0md", `<span style="color:var(--hotty-ansi-2)">abc</span>d`},
		{"markup", "<b>&amp;", `&lt;b&gt;&amp;amp;`},
		{"background to the end", "a\x1b[41m  \x1b[0m", `a<span style="background:var(--hotty-ansi-1)">  </span>`},
		{"blank end dropped", "a   ", `a`},
		{"wide", "界x", `<span class="vt-w">界</span>x`},
		{"link", "\x1b]8;;https://e.com/?a=1&b=2\x1b\\go\x1b]8;;\x1b\\", `<a href="https://e.com/?a=1&amp;b=2" target="_blank">go</a>`},
		{"link not shown", "\x1b]8;;file:///etc/passwd\x1b\\f\x1b]8;;\x1b\\", `f`},
		{"box drawing", "╭─╮", `<span class="vt-k vt-k256d">╭</span><span class="vt-k vt-k2500">─</span><span class="vt-k vt-k256e">╮</span>`},
		{"a line's run is one element", "├───┤", `<span class="vt-k vt-k251c">├</span><span class="vt-k vt-k2500" style="--vt-n:3">───</span><span class="vt-k vt-k2524">┤</span>`},
		{"a corner's is not", "┼┼", `<span class="vt-k vt-k253c">┼</span><span class="vt-k vt-k253c">┼</span>`},
		{"text between", "│a│", `<span class="vt-k vt-k2502">│</span>a<span class="vt-k vt-k2502">│</span>`},
		{"a bar in colour", "\x1b[34m██\x1b[0m░", `<span style="color:var(--hotty-ansi-4)"><span class="vt-k vt-k2588" style="--vt-n:2">██</span></span><span class="vt-k vt-k2591">░</span>`},
		{"dashes and diagonals are the font's", "┄╱", `┄╱`},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := screen(t, 10, 2, hottyvt.HideCursor())
			_, _ = s.WriteString(c.in)
			want := `<div class="vt" id="vt" style="--vt-cols:10"><div id="vt-r0">` + c.want + `</div><div id="vt-r1"></div></div>`
			if got := s.HTML(); got != want {
				t.Errorf("\n got %s\nwant %s", got, want)
			}
		})
	}
}

// A drawn character stays in its element, for the text a reader copies.
func TestDrawnKeepTheirText(t *testing.T) {
	s := screen(t, 8, 2, hottyvt.HideCursor())
	_, _ = s.WriteString("╭──╮ ok\r\n│█░│")
	h := hottytest.New(t)
	send(t, h, hotty.Doc("box", "<style>"+hottyvt.CSS+"</style>"+s.HTML()))
	if got := h.Surface("box").TextOf(s.RowID(0)) + "\n" + h.Surface("box").TextOf(s.RowID(1)); got != "╭──╮ ok\n│█░│" {
		t.Errorf("the surface's text is %q", got)
	}
	if got := s.Text(); got != "╭──╮ ok\n│█░│" {
		t.Errorf("Text() = %q", got)
	}
}

func TestCursor(t *testing.T) {
	s := screen(t, 6, 2)
	_, _ = s.WriteString("ab\x1b[1;2H")
	if got := s.HTML(); !strings.Contains(got, `a<span class="vt-cur">b</span>`) {
		t.Errorf("on a character: %s", got)
	}
	_, _ = s.WriteString("\x1b[2;4H")
	if got := s.HTML(); !strings.Contains(got, `<div id="vt-r1">   <span class="vt-cur"> </span></div>`) {
		t.Errorf("past the end of the row: %s", got)
	}
	_, _ = s.WriteString("\x1b[?25l")
	if got := s.HTML(); strings.Contains(got, "vt-cur") {
		t.Errorf("hidden by the program: %s", got)
	}
	h := screen(t, 6, 2, hottyvt.HideCursor())
	if got := h.HTML(); strings.Contains(got, "vt-cur") {
		t.Errorf("HideCursor: %s", got)
	}
}

func TestIDs(t *testing.T) {
	s := screen(t, 3, 1, hottyvt.ID("demo"))
	if s.ElementID() != "demo" || s.RowID(4) != "demo-r4" {
		t.Errorf("ids %q %q", s.ElementID(), s.RowID(4))
	}
	if got := s.HTML(); !strings.HasPrefix(got, `<div class="vt" id="demo" `) || !strings.Contains(got, `id="demo-r0"`) {
		t.Errorf("html %s", got)
	}
}

func TestResizeSendsTheElement(t *testing.T) {
	h := hottytest.New(t)
	s := screen(t, 8, 2, hottyvt.HideCursor())
	_, _ = s.WriteString("kept")
	send(t, h, hotty.Doc("vt", s.HTML()))
	s.Resize(5, 3)
	if c, r := s.Size(); c != 5 || r != 3 {
		t.Fatalf("size %d×%d", c, r)
	}
	ms := decode(s.Delta("vt"))
	if len(ms) != 1 || ms[0].Get("op") != "morph" || ms[0].Get("t") != "vt" {
		t.Fatalf("got %v", ms)
	}
	send(t, h, s.Delta("vt")...) // nothing more
	_, _ = s.WriteString("\x1b[3;1Hnew")
	send(t, h, hotty.MorphTo("vt", "vt", string(ms[0].Payload)))
	send(t, h, s.Delta("vt")...)
	if got := h.Surface("vt").TextOf("vt-r2"); got != "new" {
		t.Errorf("row 2 after the resize: %q", got)
	}
	if got := h.Surface("vt").TextOf("vt-r0"); got != "kept" {
		t.Errorf("row 0 after the resize: %q", got)
	}
}

func TestScale(t *testing.T) {
	s := screen(t, 4, 1, hottyvt.Scale(0.5))
	if cmd := s.SetScale("vt", 0.25); cmd != "" {
		t.Errorf("before HTML, a command: %q", cmd)
	}
	if got := s.HTML(); !strings.Contains(got, `style="--vt-cols:4;--vt-scale:0.25"`) {
		t.Errorf("html %s", got)
	}
	m := decode([]string{s.SetScale("vt", 2.0/3)})[0]
	if m.Get("op") != "var" || m.Get("t") != "vt" || m.Get("k") != "vt-scale" || string(m.Payload) != "0.667" {
		t.Errorf("SetScale: %v %q", m.Control, m.Payload)
	}
	if got := s.HTML(); !strings.Contains(got, "--vt-scale:0.667") {
		t.Errorf("html after SetScale %s", got)
	}
	if d := s.Delta("vt"); len(d) != 0 {
		t.Errorf("SetScale sent already, Delta has %d", len(d))
	}
	one := screen(t, 4, 1, hottyvt.Scale(1))
	if got := one.HTML(); strings.Contains(got, "vt-scale") {
		t.Errorf("scale 1 is the default: %s", got)
	}
}

func TestTitle(t *testing.T) {
	s := screen(t, 4, 1)
	if s.Title() != "" {
		t.Errorf("before one: %q", s.Title())
	}
	_, _ = s.WriteString("\x1b]2;make test\x07")
	if s.Title() != "make test" {
		t.Errorf("OSC 2: %q", s.Title())
	}
	_, _ = s.WriteString("\x1b]0;vim\x1b\\")
	if s.Title() != "vim" {
		t.Errorf("OSC 0: %q", s.Title())
	}
}

func TestText(t *testing.T) {
	s := screen(t, 10, 4)
	_, _ = s.WriteString("\x1b[31mred\x1b[0m   \r\n界x\r\n\r\n")
	if got, want := s.Text(), "red\n界x"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// syncBuffer is a bytes.Buffer for two goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// A query in the output does not stop the screen, and its answer goes to
// Replies when there is one.
func TestReplies(t *testing.T) {
	done := make(chan struct{})
	quiet := screen(t, 4, 2)
	go func() {
		defer close(done)
		_, _ = quiet.WriteString("\x1b[6n\x1b[cok")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write waited for the answers to be read")
	}
	if quiet.Text() != "ok" {
		t.Errorf("text %q", quiet.Text())
	}

	var got syncBuffer
	s := hottyvt.New(4, 2, hottyvt.Replies(&got))
	_, _ = s.WriteString("ab\x1b[6n")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got.String() != "\x1b[1;3R" {
		t.Errorf("replies %q", got.String())
	}
}

func TestWriteAfterClose(t *testing.T) {
	s := hottyvt.New(4, 1)
	_ = s.Close()
	n, _ := s.Write([]byte("x"))
	_ = n // the emulator drops it; what matters is that it returns
}
