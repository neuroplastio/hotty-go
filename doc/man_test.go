package doc

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The groff HTML of testdata/sample.1, made with groff 1.24.1 (the
// comment with the creation date left out).
func TestCleanManGroff(t *testing.T) {
	src, err := os.ReadFile("testdata/sample.groff.html")
	if err != nil {
		t.Fatal(err)
	}
	m, err := CleanMan(src, "sample", "1")
	if err != nil {
		t.Fatal(err)
	}
	checkSample(t, m)
}

// checkSample checks the clean page of testdata/sample.1.
func checkSample(t *testing.T, m *ManPage) {
	t.Helper()
	var titles []string
	for _, s := range m.Sections {
		titles = append(titles, s.Title)
	}
	if strings.Join(titles, "|") != "NAME|SYNOPSIS|DESCRIPTION|SEE ALSO" {
		t.Fatalf("sections %q", titles)
	}
	name, synopsis, desc, see := m.Sections[0].HTML, m.Sections[1].HTML, m.Sections[2].HTML, m.Sections[3].HTML
	if name != "<p>sample - a page to test the clean-up</p>" {
		t.Errorf("NAME %q", name)
	}
	if synopsis != "<p><b>sample</b> [<b>-v</b>] <i>file</i></p>" {
		t.Errorf("SYNOPSIS %q", synopsis)
	}
	for _, want := range []string{
		// References: .BR's bold name and the section after it, and one in
		// plain text.
		`<a href="man:cat(1)" class="xref"><b>cat</b>(1)</a>`,
		`<a href="man:printf(3)" class="xref">printf(3)</a>`,
		// Tagged paragraphs in both of grohtml's forms, a table and two
		// paragraphs, in one list; the option's second paragraph in its
		// definition.
		`<dl><dt><b>-v</b></dt><dd><p>Say more.</p></dd><dt><b>--width</b> <i>n</i></dt><dd><p>Lay it out <i>n</i> columns wide.</p><p>A second paragraph of the same option.</p></dd></dl>`,
		`<h3>Exit status</h3><dl><dt><b>0</b></dt><dd><p>It worked.</p></dd>`,
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("DESCRIPTION has no %s:\n%s", want, desc)
		}
	}
	for _, want := range []string{
		`<a href="man:ls(1)" class="xref"><b>ls</b>(1)</a>`,
		`<a href="man:hotty-demo(1)" class="xref"><b>hotty-demo</b>(1)</a>`,
		`<a href="https://github.com/neuroplastio/hotty" target="_blank" rel="noopener noreferrer">The protocol</a>`,
	} {
		if !strings.Contains(see, want) {
			t.Errorf("SEE ALSO has no %s:\n%s", want, see)
		}
	}
	for _, s := range m.Sections {
		if strings.Contains(s.HTML, "style=") || strings.Contains(s.HTML, "<table") {
			t.Errorf("groff's layout is left in %s: %s", s.Title, s.HTML)
		}
	}
}

// With groff installed, the sample's page made now cleans up the same.
func TestCleanManLive(t *testing.T) {
	if _, err := exec.LookPath("groff"); err != nil {
		t.Skip("no groff")
	}
	out, err := exec.Command("groff", "-k", "-t", "-mandoc", "-Thtml", "-P-l", "-P-r", "testdata/sample.1").Output()
	if err != nil {
		t.Fatal(err)
	}
	m, err := CleanMan(out, "sample", "1")
	if err != nil {
		t.Fatal(err)
	}
	checkSample(t, m)
}

// grohtml puts the first tag of a list after a <br> at the end of the
// paragraph before it.
func TestCleanManTagAfterBreak(t *testing.T) {
	src := `<html><body><h2>KEYS<a name="KEYS"></a></h2>
<p style="margin-left:6%; margin-top: 1em">Keys: <b><br>
j</b>, <b>k</b></p>
<p style="margin-left:15%;">Scroll a line.</p>
<p style="margin-left:6%;"><b>q</b></p>
<p style="margin-left:15%;">Quit.</p>
<p style="margin-left:6%; margin-top: 1em">After.<br>
Broken line.</p>
<p style="margin-left:6%;">Continued.</p>
<p style="margin-left:11%; margin-top: 1em">An indented example</p>
</body></html>`
	m, err := CleanMan([]byte(src), "k", "1")
	if err != nil {
		t.Fatal(err)
	}
	want := `<p>Keys:</p><dl><dt><b>j</b>, <b>k</b></dt><dd><p>Scroll a line.</p></dd><dt><b>q</b></dt><dd><p>Quit.</p></dd></dl>` +
		`<p>After.<br/>Broken line.<br/>Continued.</p><p class="i1">An indented example</p>`
	if got := m.Sections[0].HTML; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// mandoc -Thtml's markup is semantic already: its sections, lists and
// references are kept, its permalinks and classes dropped.
func TestCleanManMandoc(t *testing.T) {
	src := `<html><body><div class="head"></div><div class="manual-text">
<section class="Sh"><h1 class="Sh" id="NAME"><a class="permalink" href="#NAME">NAME</a></h1>
<p class="Pp"><code class="Nm">sample</code> — a page</p></section>
<section class="Sh"><h1 class="Sh" id="OPTIONS"><a class="permalink" href="#OPTIONS">OPTIONS</a></h1>
<dl class="Bl-tag"><dt id="v"><a class="permalink" href="#v"><code class="Fl">-v</code></a></dt><dd>Say more; see <a class="Xr" href="ls.1.html">ls(1)</a>.</dd></dl>
<div class="Bd-indent"><pre>sample -v</pre></div></section></div></body></html>`
	m, err := CleanMan([]byte(src), "sample", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sections) != 2 || m.Sections[1].Title != "OPTIONS" {
		t.Fatalf("sections %+v", m.Sections)
	}
	want := `<dl><dt><b>-v</b></dt><dd>Say more; see <a href="man:ls(1)" class="xref">ls(1)</a>.</dd></dl><div class="i1"><pre>sample -v</pre></div>`
	if got := m.Sections[1].HTML; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if m.Sections[0].HTML != "<p><b>sample</b> — a page</p>" {
		t.Errorf("NAME %q", m.Sections[0].HTML)
	}
}

func TestManFile(t *testing.T) {
	src, _ := os.ReadFile("testdata/sample.groff.html")
	m, err := CleanMan(src, "sample", "1")
	if err != nil {
		t.Fatal(err)
	}
	file := m.HTML("groff 1.24.1")
	if Generator([]byte(file)) != "groff 1.24.1" {
		t.Errorf("generator %q", Generator([]byte(file)))
	}
	back, err := ReadMan([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if back.Title() != "sample(1)" || len(back.Sections) != len(m.Sections) {
		t.Fatalf("read back %s with %d sections", back.Title(), len(back.Sections))
	}
	if again := back.HTML("groff 1.24.1"); again != file {
		t.Errorf("a page read back is written differently:\n%s\n---\n%s", file, again)
	}
	// Shown as an HTML file, a page is inert: its references are text.
	for _, b := range HTML([]byte(file), Options{}).Blocks {
		checkInert(t, b.HTML)
	}
}

func TestManRef(t *testing.T) {
	if ManRef("ls", "1") != "man:ls(1)" {
		t.Error(ManRef("ls", "1"))
	}
	if n, s, ok := ParseManRef("man:hotty-demo(1)"); !ok || n != "hotty-demo" || s != "1" {
		t.Errorf("%q %q %v", n, s, ok)
	}
	if _, _, ok := ParseManRef("https://example.com"); ok {
		t.Error("a web link read as a reference")
	}
	got := linkRefs("<p>see ls(1), printf(3p), f(x) and (1) and a.b(8).</p>")
	for _, want := range []string{`>ls(1)</a>`, `>printf(3p)</a>`, `>a.b(8)</a>`, ` f(x) and (1) `} {
		if !strings.Contains(got, want) {
			t.Errorf("%s has no %s", got, want)
		}
	}
}

func TestManBlocks(t *testing.T) {
	var dl strings.Builder
	dl.WriteString("<dl>")
	for range 60 {
		dl.WriteString("<dt><b>-x</b></dt><dd><p>An option with a line of text about it.</p></dd>")
	}
	dl.WriteString("</dl>")
	m := &ManPage{Name: "x", Section: "1", Sections: []ManSection{
		{Title: "NAME", HTML: "<p>x - y</p>"},
		{Title: "OPTIONS", HTML: "<p>Intro.</p>" + dl.String()},
	}}
	blocks := m.Blocks(Options{Cols: 80}, 30)
	if len(blocks) < 4 || blocks[0].Section != 0 || !blocks[0].First || blocks[1].Section != 1 || !blocks[1].First || blocks[2].First {
		t.Fatalf("blocks %+v", blocks)
	}
	if !strings.HasPrefix(blocks[1].Text, "OPTIONS\nIntro.\n-x\nAn option") {
		t.Errorf("the block's text %q", blocks[1].Text)
	}
	if !strings.HasPrefix(blocks[1].HTML, "<h2>OPTIONS</h2><p>Intro.</p><dl><dt>") {
		t.Errorf("the section's first block %q", blocks[1].HTML[:60])
	}
	for i, b := range blocks[1:] {
		if strings.Count(b.HTML, "<dl>") != strings.Count(b.HTML, "</dl>") {
			t.Errorf("block %d cuts a list: %s", i+1, b.HTML)
		}
		if strings.Contains(b.HTML, "<dt><b>-x</b></dt></dl>") {
			t.Errorf("block %d cuts an entry from its definition", i+1)
		}
	}
	paras := m.Text()
	if len(paras) < 3 || paras[0].Kind != "h2" || paras[0].Text != "NAME" {
		t.Errorf("text %+v", paras[:2])
	}
}

// A page read from a file is anyone's markup: its blocks are inert, but
// for the man: links a viewer hears.
func TestManBlocksAreInert(t *testing.T) {
	evil := `<p>x<script>alert(1)</script><img src=x onerror=y><a href="javascript:z" id=q>l</a>` +
		`<button id=b>b</button> see <a href="man:ls(1)" class="xref" id="r" onclick="z()">ls(1)</a></p>`
	m, err := ReadMan([]byte(`<article class="man"><section><h2>NAME</h2>` + evil + `</section></article>`))
	if err != nil {
		t.Fatal(err)
	}
	blocks := m.Blocks(Options{}, 0)
	if len(blocks) != 1 {
		t.Fatalf("%d blocks", len(blocks))
	}
	b := blocks[0]
	for _, bad := range []string{"<script", "alert", "onerror", "javascript", `id="`, "onclick"} {
		if strings.Contains(b.HTML, bad) {
			t.Errorf("the block has %q: %s", bad, b.HTML)
		}
	}
	if !strings.Contains(b.HTML, `<a class="xref" href="man:ls(1)">ls(1)</a>`) || !strings.Contains(b.HTML, `<button disabled="">`) {
		t.Errorf("the block %s", b.HTML)
	}
	if strings.Contains(b.Text, "alert") {
		t.Errorf("the block's text %q", b.Text)
	}
}

// mandoc's lists, tables, subsections and indented displays come out as
// their plain HTML, its classes and styles left behind.
func TestCleanManMandocBlocks(t *testing.T) {
	src := `<html><body><div class="manual-text">
<section class="Sh"><h1 class="Sh">DESCRIPTION</h1>
loose text
<h2 class="Ss">Lists</h2>
<ul class="Bl-bullet"><li>one</li><li><p class="Pp">two</p><p class="Pp">more</p></li></ul>
<ol class="Bl-enum"><li>first</li></ol>
<table class="tbl"><tbody><tr><th>key</th><th>value</th></tr><tr><td colspan="2" style="x">both</td></tr></tbody></table>
<div class="Bd-indent"><div class="Bd-indent">deep</div></div>
<hr><img src="x.png"><script>alert(1)</script>
<div class="Nd"><span>inline only</span></div>
<div class="wrap"><p>in a div</p><pre>code</pre></div>
<blockquote>quoted</blockquote>
</section></div></body></html>`
	m, err := CleanMan([]byte(src), "x", "1")
	if err != nil {
		t.Fatal(err)
	}
	want := `<p>loose text</p><h3>Lists</h3><ul><li>one</li><li><p>two</p><p>more</p></li></ul><ol><li>first</li></ol>` +
		`<table><tbody><tr><th>key</th><th>value</th></tr><tr><td colspan="2">both</td></tr></tbody></table>` +
		`<div class="i1"><div class="i1"><p>deep</p></div></div><p>inline only</p><p>in a div</p><pre>code</pre><blockquote>quoted</blockquote>`
	if len(m.Sections) != 1 || m.Sections[0].HTML != want {
		t.Errorf("got\n%+v\nwant\n%s", m.Sections, want)
	}
}

// A link on a line of its own in groff's HTML is a hyperlink; an anchor
// goes.
func TestCleanManGroffLink(t *testing.T) {
	src := `<html><body><h2>SEE ALSO</h2><a name="x"></a><a href="https://example.com/x">home</a></body></html>`
	m, err := CleanMan([]byte(src), "x", "1")
	if err != nil {
		t.Fatal(err)
	}
	want := `<p><a href="https://example.com/x" target="_blank" rel="noopener noreferrer">home</a></p>`
	if len(m.Sections) != 1 || m.Sections[0].HTML != want {
		t.Errorf("got %+v, want %s", m.Sections, want)
	}
	if _, err := CleanMan([]byte("<html><frameset></frameset></html>"), "x", "1"); err == nil {
		t.Error("a page with no body, and no error")
	}
	if g := Generator([]byte(`<p>no meta`)); g != "" {
		t.Errorf("Generator of a page without one: %q", g)
	}
}
