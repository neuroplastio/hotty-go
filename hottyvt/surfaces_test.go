package hottyvt_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/hottyvt"
)

// What a full-screen program that speaks HOTTY writes, in the chunks a pty
// would hand over: cells around a card, the card's document, its
// placement, deltas, a resource, a window as it scrolls, a hide, another
// card, and a delete.
var hottySession = []string{
	"\x1b[?1049h\x1b[2J\x1b[Hagent\r\n",
	hotty.Doc("card", `<style>.k { color: red } #st { font-weight: bold } body { margin: 0 }</style>`+
		`<div class="k"><b id="st">running</b> <img src="cid:logo"></div>`),
	hotty.Res("logo", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)),
	hotty.PlaceAt("card", 2, 1, hotty.Placement{Cols: 10, Rows: 3}),
	"\x1b[5;1Hbelow",
	hotty.SetText("card", "st", "done"),
	hotty.Delta("card", hotty.OpAppend, "st", "", []byte(`<i id="n">1</i>`)),
	hotty.SetVar("card", "st", "w", "40%"),
	hotty.SetAttr("card", "st", "title", "the state"),
	// Scrolled: the card's top row out of view, through a window.
	hotty.PlaceAt("card", 2, 0, hotty.Placement{Cols: 10, Rows: 3, Window: hotty.Window{X: 0, Y: 1, W: 10, H: 2}}),
	hotty.Hide("card"),
	hotty.Doc("plan", `<ol id="steps"><li>one</li></ol>`),
	hotty.PlaceAt("plan", 0, 2, hotty.Placement{Cols: 12, Rows: 2, Z: 1}),
	hotty.Delta("plan", hotty.OpInner, "steps", "", []byte(`<li>one</li><li id="two">two</li>`)),
	hotty.PlaceAt("card", 1, 1, hotty.Placement{Cols: 10, Rows: 3}),
	hotty.Doc("card", `<p id="st">again</p>`),
	hotty.MorphTo("plan", "", `<li id="two">two!</li>`),
	hotty.Del("card"),
	"\x1b[?1049l",
}

// The surface, given the screen once and its deltas after every chunk, is
// at every step the document the screen makes from scratch: surfaces and
// all.
func TestSurfaceDeltasKeepTheSurfaceCurrent(t *testing.T) {
	h := hottytest.New(t)
	s := screen(t, 20, 6)
	twin := screen(t, 20, 6)
	send(t, h, hotty.Doc("vt", s.HTML()))
	for i, chunk := range hottySession {
		_, _ = s.WriteString(chunk)
		_, _ = twin.WriteString(chunk)
		send(t, h, hotty.Sync(s.Delta("vt")...))
		send(t, h, hotty.Doc("fresh", twin.HTML()))
		if got, want := sorted(h.Surface("vt").HTML()), sorted(h.Surface("fresh").HTML()); got != want {
			t.Fatalf("after chunk %d:\n got %s\nwant %s", i, got, want)
		}
	}
	if errs := h.Errors(); len(errs) > 0 {
		t.Errorf("the host refused: %v", errs)
	}
}

// A placed surface is a box at its cells over the rows, with the document
// in it, its ids, styles and resources the screen's.
func TestSurfaceShowsAtItsCells(t *testing.T) {
	h := hottytest.New(t)
	s := screen(t, 20, 6)
	send(t, h, hotty.Doc("vt", s.HTML()))
	for _, chunk := range hottySession[:6] {
		_, _ = s.WriteString(chunk)
	}
	send(t, h, hotty.Sync(s.Delta("vt")...))
	vt := h.Surface("vt")
	if got := vt.TextOf("vt-d1-st"); got != "done" {
		t.Errorf("the card's state: %q", got)
	}
	if got, _ := vt.Attr("vt-p1", "style"); got != "--vt-px:2;--vt-py:1;--vt-pc:10;--vt-pr:3" {
		t.Errorf("the box: %q", got)
	}
	if got, _ := vt.Attr("vt-d1", "class"); got != "vt-d" {
		t.Errorf("the document's element: %q", got)
	}
	doc := vt.HTML()
	for _, want := range []string{
		`#vt-d1 .k{ color: red }`, `#vt-d1 #vt-d1-st{ font-weight: bold }`, `#vt-d1{ margin: 0 }`,
		`src="cid:vt-logo"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("no %q in %s", want, doc)
		}
	}
	if _, data, ok := h.Resource("vt-logo"); !ok || !strings.Contains(string(data), "<svg") {
		t.Errorf("the resource: %v %q", ok, data)
	}
	if got := strings.TrimSpace(vt.TextOf("vt-r4")); got != "below" {
		t.Errorf("the rows: %q", got)
	}
	// The resources, for a host that has not had them.
	if res := decode(s.Resources()); len(res) != 1 || hotty.Get(res[0].Control, "id") != "vt-logo" {
		t.Errorf("Resources: %v", res)
	}
}

// A placement's window, z, hide and delete are its box's style, or its
// box's going.
func TestSurfacePlacements(t *testing.T) {
	s := screen(t, 20, 6)
	_ = s.HTML()
	steps := []struct {
		write string
		box   string // the card's box's style after it; "" for no change
	}{
		{"\x1b[?1049h" + hotty.Doc("card", "<p>x</p>"), ""},
		{hotty.PlaceAt("card", 3, 2, hotty.Placement{Cols: 8, Rows: 4}), "--vt-px:3;--vt-py:2;--vt-pc:8;--vt-pr:4"},
		{hotty.PlaceAt("card", 3, 0, hotty.Placement{Cols: 8, Rows: 4, Window: hotty.Window{X: 1, Y: 2, W: 6, H: 2}, Z: -3}),
			"--vt-px:3;--vt-py:0;--vt-pc:8;--vt-pr:4;--vt-wx:1;--vt-wy:2;--vt-pw:6;--vt-ph:2;z-index:998"},
		// Refused where it was recorded (EINVAL): nothing changes.
		{hotty.PlaceAt("card", 0, 0, hotty.Placement{Cols: 8, Rows: 4, Window: hotty.Window{X: 4, Y: 0, W: 6, H: 2}}), ""},
		{hotty.PlaceAt("card", 0, 0, hotty.Placement{Cols: 1001, Rows: 4}), ""},
		{hotty.Hide("card"), "display:none"},
		{hotty.PlaceAt("card", 0, 0, hotty.Placement{Cols: 5}), "--vt-px:0;--vt-py:0;--vt-pc:5"},
	}
	for i, st := range steps {
		_, _ = s.WriteString(st.write)
		var box string
		for _, m := range decode(s.Delta("vt")) {
			switch {
			case hotty.Get(m.Control, "op") == "attr" && hotty.Get(m.Control, "t") == "vt-p1":
				box = string(m.Payload)
			case hotty.Get(m.Control, "op") == "append" && strings.Contains(string(m.Payload), `style="display:none"`):
			case hotty.Get(m.Control, "a") == "delta" && !strings.HasPrefix(hotty.Get(m.Control, "t"), "vt-r"):
				t.Errorf("step %d: %v %q", i, m.Control, m.Payload)
			}
		}
		if box != st.box {
			t.Errorf("step %d: box %q, want %q", i, box, st.box)
		}
	}
	_, _ = s.WriteString(hotty.Del("card"))
	if ms := decode(s.Delta("vt")); len(ms) != 1 || hotty.Get(ms[0].Control, "op") != "remove" || hotty.Get(ms[0].Control, "t") != "vt-p1" {
		t.Errorf("a delete: %v", ms)
	}
}

// A surface placed among a shell's output moves with its line as the
// screen scrolls, and goes when its lines leave the screen.
func TestSurfaceScrollsWithItsLine(t *testing.T) {
	s := screen(t, 20, 6)
	_ = s.HTML()
	_, _ = s.WriteString("$ show\r\n" + hotty.Doc("doc", "<p>hi</p>", hotty.Detached()) +
		hotty.Place("doc", hotty.Placement{Cols: 10, Rows: 2}) + "$ ")
	style := func() string {
		var st string
		for _, m := range decode(s.Delta("vt")) {
			if hotty.Get(m.Control, "op") == "attr" && hotty.Get(m.Control, "t") == "vt-p1" {
				st = string(m.Payload)
			}
		}
		return st
	}
	if got := style(); got != "--vt-px:0;--vt-py:1;--vt-pc:10;--vt-pr:2" {
		t.Fatalf("the box: %q", got)
	}
	if got := strings.Split(s.Text(), "\n"); len(got) != 4 || got[3] != "$" {
		t.Errorf("the cursor did not go below the placement: %q", got)
	}
	_, _ = s.WriteString("ls\r\na\r\nb\r\n$ ")
	if got := style(); got != "--vt-px:0;--vt-py:0;--vt-pc:10;--vt-pr:2" {
		t.Errorf("one row scrolled: %q", got)
	}
	_, _ = s.WriteString("ls\r\na\r\nb\r\n$ ")
	if got := style(); got != "display:none" {
		t.Errorf("scrolled off: %q", got)
	}
}

// A placement made on the alternate screen goes with it, and its surface
// too; the main screen's comes back.
func TestSurfaceAndTheAlternateScreen(t *testing.T) {
	s := screen(t, 20, 6)
	_ = s.HTML()
	_, _ = s.WriteString(hotty.Doc("main", "<p>m</p>") + hotty.PlaceAt("main", 0, 0, hotty.Placement{Cols: 4, Rows: 1}))
	_, _ = s.WriteString("\x1b[?1049h" + hotty.Doc("alt", "<p>a</p>") + hotty.PlaceAt("alt", 0, 0, hotty.Placement{Cols: 4, Rows: 1}))
	if html := s.HTML(); !strings.Contains(html, `id="vt-p1" style="display:none"`) || !strings.Contains(html, `id="vt-p2" style="--vt-px:0`) {
		t.Fatalf("on the alternate screen: %s", html)
	}
	_, _ = s.WriteString("\x1b[?1049l")
	if html := s.HTML(); strings.Contains(html, "vt-p2") || !strings.Contains(html, `id="vt-p1" style="--vt-px:0`) {
		t.Fatalf("back on the main screen: %s", html)
	}
	_, _ = s.WriteString("\x1bc")
	if html := s.HTML(); strings.Contains(html, "vt-p") {
		t.Fatalf("after a reset: %s", html)
	}
	// The next surface has a number of its own: a late delta to one gone
	// reaches nothing.
	_, _ = s.WriteString(hotty.Doc("x", "<p>x</p>"))
	if html := s.HTML(); !strings.Contains(html, `id="vt-p3"`) {
		t.Fatalf("a new surface: %s", html)
	}
}

// A message cut into chunks, in writes cut anywhere, is the message.
func TestSurfaceInPieces(t *testing.T) {
	big := strings.Repeat("<p>lorem ipsum dolor sit amet</p>", 600) // chunked, and compressed
	out := hotty.Doc("big", big) + hotty.PlaceAt("big", 0, 0, hotty.Placement{Cols: 20, Rows: 6}) + "x\x1b]7279"
	s := screen(t, 20, 6)
	for len(out) > 0 {
		n := min(7, len(out))
		_, _ = s.WriteString(out[:n])
		out = out[n:]
	}
	_, _ = s.WriteString("0;not hotty\x07y")
	// Longer than any chunk, a sequence is no message, and shows nothing.
	_, _ = s.WriteString("\x1b]7279;a=doc:s=huge;" + strings.Repeat("A", 70<<10))
	_, _ = s.WriteString(strings.Repeat("A", 10) + "\x1b\\")
	html := s.HTML()
	if strings.Count(html, "lorem ipsum") != 600 || !strings.Contains(html, `style="--vt-px:0;--vt-py:0`) {
		t.Fatalf("the document: %d paragraphs", strings.Count(html, "lorem ipsum"))
	}
	if got := s.Text(); got != "xy" || strings.Contains(s.HTML(), "vt-p2") {
		t.Errorf("the cells: %q", got)
	}
}

// A surface played in a screen reports nothing: its links that are not the
// terminal's lose their href, and its elements their data-on.
func TestSurfaceIsShownNotUsed(t *testing.T) {
	s := screen(t, 20, 6)
	_, _ = s.WriteString(hotty.Doc("d", `<base href="https://example.com/docs/">`+
		`<a id="go" href="/next">next</a> <a href="guide" target="_blank">guide</a>`+
		`<button id="b" data-on="click" autofocus>ok</button> <label for="b">l</label>`))
	html := s.HTML()
	for _, want := range []string{`<a id="vt-d1-go">next</a>`, `<a href="https://example.com/docs/guide" target="_blank">guide</a>`,
		`<button id="vt-d1-b">ok</button>`, `<label for="vt-d1-b">`} {
		if !strings.Contains(html, want) {
			t.Errorf("no %q in %s", want, html)
		}
	}
	_, _ = s.WriteString(hotty.SetAttr("d", "go", "href", "/other"))
	if ms := decode(s.Delta("vt")); len(ms) != 1 || hotty.Get(ms[0].Control, "op") != "unattr" || hotty.Get(ms[0].Control, "k") != "href" {
		t.Errorf("a link's href set: %v", ms)
	}
}

// A screen made with NoSurfaces is a terminal that is not a host.
func TestNoSurfaces(t *testing.T) {
	s := screen(t, 20, 6, hottyvt.NoSurfaces())
	_, _ = s.WriteString("a" + hotty.Doc("d", "<p>x</p>") + hotty.PlaceAt("d", 0, 0, hotty.Placement{Cols: 4, Rows: 1}) + "b")
	if html := s.HTML(); strings.Contains(html, "vt-p") || s.Text() != "ab" {
		t.Fatalf("%s %q", html, s.Text())
	}
}

// A screen nobody asks for deltas for a while keeps a bounded number of
// them, then sends the whole element.
func TestSurfaceDeltasNobodyAskedFor(t *testing.T) {
	h := hottytest.New(t)
	s := screen(t, 20, 6)
	send(t, h, hotty.Doc("vt", s.HTML()))
	_, _ = s.WriteString(hotty.Doc("d", `<p id="n">0</p>`))
	send(t, h, hotty.Sync(s.Delta("vt")...))
	big := strings.Repeat("x", 4096)
	for i := range 200 {
		_, _ = s.WriteString(hotty.Delta("d", hotty.OpInner, "n", "", []byte(big+strconv.Itoa(i))))
	}
	cmds := s.Delta("vt")
	ms := decode(cmds)
	if len(ms) != 1 || hotty.Get(ms[0].Control, "t") != "vt" || hotty.Get(ms[0].Control, "op") != "morph" {
		t.Fatalf("%d commands, the first %v", len(ms), ms[0].Control)
	}
	send(t, h, hotty.Sync(cmds...))
	if got := h.Surface("vt").TextOf("vt-d1-n"); got != big+"199" {
		t.Errorf("the surface: %d bytes", len(got))
	}
}
