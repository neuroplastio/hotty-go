package doc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// checkInert fails if markup has anything that would report a user's
// action, or a link that is not a hyperlink to the web.
func checkInert(t *testing.T, markup string) {
	t.Helper()
	root, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			name := strings.ToLower(n.Data)
			if dropped[name] {
				t.Errorf("a <%s> is left", name)
			}
			if name == "form" {
				t.Errorf("a form is left: a submit is reported whatever the ids")
			}
			for _, a := range n.Attr {
				switch {
				case a.Key == "id" && reporting[name]:
					t.Errorf("<%s id=%q> would report", name, a.Val)
				case a.Key == "data-on":
					t.Errorf("<%s data-on> would report", name)
				case strings.HasPrefix(a.Key, "on"):
					t.Errorf("<%s %s> is an event attribute", name, a.Key)
				}
			}
			if name == "a" && n.Namespace == "" {
				if !Web(attr(n, "href")) || attr(n, "target") != "_blank" {
					t.Errorf("a link that is not a hyperlink: %q target=%q", attr(n, "href"), attr(n, "target"))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
}

func TestInert(t *testing.T) {
	for _, tc := range []struct{ in, want, not string }{
		{`<script>alert(1)</script><p>x</p>`, `<p>x</p>`, `script`},
		{`<a href="https://example.com/a">web</a>`, `<a href="https://example.com/a" target="_blank" rel="noopener noreferrer">web</a>`, ``},
		{`<a href="notes.md">a file</a>`, `<span>a file</span>`, `href`},
		{`<a href="javascript:alert(1)">js</a>`, `<span>js</span>`, `javascript`},
		{`<a href="#top" id="t">top</a>`, `<span>top</span>`, `id=`},
		{`<button id="go" onclick="x()">Go</button>`, `<button>Go</button>`, `onclick`},
		{`<details><summary id="s">more</summary>x</details>`, `<summary>more</summary>`, `id=`},
		{`<div id="card" data-on="click">x</div>`, `<div id="card">x</div>`, `data-on`},
		{`<form action="/x"><input id="n" name="n" autofocus></form>`, `<div><input name="n"/></div>`, `form`},
		{`<img src="javascript:x" alt="a">`, `<img alt="a"/>`, `javascript`},
		{`<iframe src="https://example.com"></iframe><p>after</p>`, `<p>after</p>`, `iframe`},
		{`<svg><a href="https://example.com"><text>t</text></a><script>x</script></svg>`, `target="_blank"`, `script`},
	} {
		got := Inert(tc.in)
		if !strings.Contains(got, tc.want) {
			t.Errorf("Inert(%q) = %q, want it to contain %q", tc.in, got, tc.want)
		}
		if tc.not != "" && strings.Contains(got, tc.not) {
			t.Errorf("Inert(%q) = %q, still has %q", tc.in, got, tc.not)
		}
		checkInert(t, got)
	}
}

func TestPages(t *testing.T) {
	blocks := []Block{{Rows: 100}, {Rows: 150}, {Rows: 80}, {Rows: 400}, {Rows: 10}, {Rows: 10}}
	pages := Pages(blocks, 250)
	var sizes []int
	for _, p := range pages {
		sizes = append(sizes, Rows(p))
	}
	if fmt.Sprint(sizes) != "[250 80 400 20]" {
		t.Errorf("pages of %v rows, want [250 80 400 20]: a block too big is a page of its own", sizes)
	}
}

func TestPageRowsFitTheScreen(t *testing.T) {
	o := Options{Screen: 40}
	if got := o.PageRows(); got != 37 {
		t.Errorf("PageRows on a 40-row screen = %d", got)
	}
	o = Options{}
	if got := o.PageRows(); got != Target {
		t.Errorf("PageRows with no screen = %d, want %d", got, Target)
	}
}

// sample is Markdown with every construct the spec asks for.
const sample = "# Title\n\n" +
	"A paragraph with **bold**, `code`, ~~gone~~, a [web link](https://example.com), " +
	"a [link to a file](other.md) and https://auto.example.org.\n\n" +
	"| name | n |\n| --- | ---: |\n| a | 1 |\n| b | 22 |\n\n" +
	"```go\nfunc main() { return }\n```\n\n" +
	"```sh\necho <hi>\n```\n\n" +
	"- [x] done\n- [ ] open\n\n" +
	"![a picture](pic.png) and ![missing](nope.png) and ![remote](https://example.com/r.png)\n\n" +
	"> [!WARNING]\n> Careful.\n\n" +
	"> A plain quote.\n\n" +
	"<button id=\"b\" onclick=\"x()\">raw</button><script>alert(1)</script>\n\n---\n"

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestMarkdown(t *testing.T) {
	pic := pngBytes(90, 36)
	o := Options{
		Cols: 80, ResPrefix: "t-1",
		Highlight: func(lang, code string) (string, bool) {
			if lang != "go" {
				return "", false
			}
			return `<span class="kw">` + html.EscapeString(code) + `</span>`, true
		},
		ReadFile: func(name string) ([]byte, error) {
			if name == "pic.png" {
				return pic, nil
			}
			return nil, fmt.Errorf("no such file")
		},
	}
	d := Markdown([]byte(sample), o)
	var all strings.Builder
	var res []Resource
	for _, b := range d.Blocks {
		all.WriteString(b.HTML)
		res = append(res, b.Res...)
		if b.Rows < 1 {
			t.Errorf("a block of %d rows: %q", b.Rows, b.HTML)
		}
	}
	got := all.String()
	for _, want := range []string{
		"<h1>Title</h1>",
		"<strong>bold</strong>", "<code>code</code>", "<del>gone</del>",
		`<a href="https://example.com" target="_blank" rel="noopener noreferrer">web link</a>`,
		"link to a file", // as text
		`<a href="https://auto.example.org" target="_blank"`,
		"<table>", `<th style="text-align:right">n</th>`,
		`<pre class="code" data-lang="go"><code><span class="kw">func main() { return }</span></code></pre>`,
		`<pre class="code" data-lang="sh"><code>echo &lt;hi&gt;</code></pre>`,
		`<li class="task done"><span class="box">✓</span>done</li>`, `<li class="task"><span class="box">☐</span>open</li>`,
		`<img src="cid:t-1-img1" alt="a picture"/>`,
		`<span class="alt">[missing]</span>`,
		`<a class="alt" href="https://example.com/r.png" target="_blank" rel="noopener noreferrer">[remote]</a>`,
		`<blockquote class="warning">`, `<span class="callout">WARNING</span>`,
		"<blockquote>", "A plain quote.",
		"<button>raw</button>", "<hr/>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the document has no %s", want)
		}
	}
	if strings.Contains(got, "other.md") || strings.Contains(got, "[!WARNING]") {
		t.Errorf("a relative link or a callout marker is left: %s", got)
	}
	checkInert(t, got)
	if len(res) != 1 || res[0].ID != "t-1-img1" || res[0].Type != "image/png" || !bytes.Equal(res[0].Data, pic) {
		t.Errorf("resources %+v, want the one image", res)
	}
}

func TestMarkdownLongCodeIsCut(t *testing.T) {
	var src strings.Builder
	src.WriteString("```\n")
	for i := range 100 {
		fmt.Fprintf(&src, "line %d\n", i)
	}
	src.WriteString("```\n")
	o := Options{Cols: 80, Screen: 30}
	d := Markdown([]byte(src.String()), o)
	if len(d.Blocks) < 4 {
		t.Fatalf("100 lines on a 30-row screen made %d blocks", len(d.Blocks))
	}
	for i, b := range d.Blocks {
		if b.Rows > o.PageRows() {
			t.Errorf("block %d: %d rows, more than a page of %d", i, b.Rows, o.PageRows())
		}
	}
	if !strings.Contains(d.Blocks[0].HTML, `class="code before"`) || !strings.Contains(d.Blocks[1].HTML, `class="code after before"`) {
		t.Errorf("the parts do not join: %q, %q", d.Blocks[0].HTML[:40], d.Blocks[1].HTML[:40])
	}
	for _, p := range Pages(d.Blocks, o.PageRows()) {
		if Rows(p) > o.PageRows() {
			t.Errorf("a page of %d rows", Rows(p))
		}
	}
}

func TestHTML(t *testing.T) {
	src := `<!doctype html><html><head><style>h1 { color: gold; }</style><script>alert(1)</script>
<link rel="stylesheet" href="site.css"></head>
<body class="wide" style="margin: 0 2em"><h1>Page</h1><p>Text with <a href="/x">a link</a> and <a href="https://example.com">a web one</a>.</p>
<img src="pic.png" alt="pic"><form><button id="b">B</button></form></body></html>`
	o := Options{Cols: 80, ResPrefix: "h", ReadFile: func(name string) ([]byte, error) {
		switch name {
		case "site.css":
			return []byte("p { color: red; }"), nil
		case "pic.png":
			return pngBytes(10, 10), nil
		}
		return nil, fmt.Errorf("no")
	}}
	d := HTML([]byte(src), o)
	if !strings.Contains(d.CSS, "h1 { color: gold; }") || !strings.Contains(d.CSS, "p { color: red; }") || !strings.Contains(d.CSS, "main.page {margin: 0 2em}") {
		t.Errorf("the document's CSS is lost: %q", d.CSS)
	}
	if d.Class != "page wide" {
		t.Errorf("class %q", d.Class)
	}
	var all strings.Builder
	var res int
	for _, b := range d.Blocks {
		all.WriteString(b.HTML)
		res += len(b.Res)
	}
	got := all.String()
	if strings.Contains(got, "script") || !strings.Contains(got, `<img src="cid:h-img1" alt="pic"/>`) || res != 1 {
		t.Errorf("blocks %q, %d resources", got, res)
	}
	checkInert(t, got)
	if body := d.Body(d.Blocks); !strings.HasPrefix(body, `<main class="page wide">`) {
		t.Errorf("body %q", body[:30])
	}
}

func TestHTMLLongContainerIsSplit(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<body><div class="wrap" id="w">`)
	for i := range 400 {
		fmt.Fprintf(&b, "<p>paragraph %d</p>", i)
	}
	b.WriteString(`</div></body>`)
	d := HTML([]byte(b.String()), Options{Cols: 80})
	if len(d.Blocks) < 400 {
		t.Fatalf("%d blocks: the container was not split", len(d.Blocks))
	}
	if !strings.HasPrefix(d.Blocks[1].HTML, `<div class="wrap">`) {
		t.Errorf("a part is not in a copy of its container, without the id: %q", d.Blocks[1].HTML)
	}
}

func TestParagraphs(t *testing.T) {
	src := `<html><head><title>T</title><style>p{}</style></head><body><h1>Head</h1><p>One
  two</p><ul><li>a</li><li>b</li></ul><pre>  code
 here</pre><table><tr><td>x</td><td>1</td></tr></table></body></html>`
	var got []string
	for _, p := range Paragraphs([]byte(src)) {
		got = append(got, p.Kind+":"+p.Text)
	}
	want := "[h1:Head p:One two li:a li:b pre:  code\n here td:x  1]"
	if fmt.Sprint(got) != want {
		t.Errorf("paragraphs %q, want %q", fmt.Sprint(got), want)
	}
}

func TestTable(t *testing.T) {
	var src strings.Builder
	src.WriteString("host,p50 ms,share,note\n")
	for i := range 1003 {
		fmt.Fprintf(&src, "h%d,%d.5,%d%%,\"a, b\"\n", i, i, i%100)
	}
	tb, err := ReadTable([]byte(src.String()), ',')
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Rows) != MaxRecords || tb.More != 3 {
		t.Errorf("%d rows and %d more, want %d and 3", len(tb.Rows), tb.More, MaxRecords)
	}
	if fmt.Sprint(tb.Numeric) != "[false true true false]" {
		t.Errorf("numeric columns %v", tb.Numeric)
	}
	if tb.Cell(0, 3) != "a, b" {
		t.Errorf("a quoted field %q", tb.Cell(0, 3))
	}
	o := Options{Cols: 100, Screen: 50}
	d := tb.Doc(o)
	if d.Rest != "3 more rows" {
		t.Errorf("rest %q", d.Rest)
	}
	for i, b := range d.Blocks {
		if b.Rows > o.PageRows() {
			t.Errorf("block %d has %d rows, a page is %d", i, b.Rows, o.PageRows())
		}
		if !strings.Contains(b.HTML, "<thead>") {
			t.Errorf("block %d has no header", i)
		}
	}
	first := d.Blocks[0].HTML
	for _, want := range []string{`<th class="num">p50 ms</th>`, `<td class="num">0.5</td>`, `<tr class="shade">`, `<td>a, b</td>`} {
		if !strings.Contains(first, want) {
			t.Errorf("the table has no %s", want)
		}
	}
	if last := d.Blocks[len(d.Blocks)-1].HTML; !strings.Contains(last, `<p class="more">… 3 more rows</p>`) {
		t.Errorf("the last block does not say what is left out")
	}

	tsv, err := ReadTable([]byte("a\tb\n\"x\t1\n"), '\t')
	if err != nil || tsv.Cell(0, 0) != `"x` || tsv.Cell(0, 1) != "1" {
		t.Errorf("TSV: %+v, %v", tsv, err)
	}
	if MoreRows(1234567) != "1,234,567 more rows" || MoreRows(1) != "1 more row" {
		t.Errorf("MoreRows: %q, %q", MoreRows(1234567), MoreRows(1))
	}
}

func TestColumnWidths(t *testing.T) {
	tb := &Table{Widths: []int{5, 60, 20}}
	w := tb.ColumnWidths(50, 2)
	total := 0
	for _, n := range w {
		total += n
	}
	if total != 50 || w[0] != 7 {
		t.Errorf("widths %v (total %d) for 50 columns: the widest give way", w, total)
	}
}

func TestJSON(t *testing.T) {
	src := `{"zeta": 1, "alpha": [true, null, "s<b>"], "empty": {}, "n": 1.50}`
	d, err := JSON([]byte(src), Options{Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, b := range d.Blocks {
		all.WriteString(b.HTML)
	}
	got := all.String()
	for _, want := range []string{
		`<span class="k">&#34;zeta&#34;</span>`, `<span class="n">1</span>`, `<span class="n">1.50</span>`,
		`<details open><summary><span class="k">&#34;alpha&#34;</span>`, `<span class="l">true</span>`, `<span class="l">null</span>`,
		`<span class="s">&#34;s&lt;b&gt;&#34;</span>`, `<span class="p">{}</span>`,
	} {
		if !strings.Contains(got, strings.ReplaceAll(want, "&#34;", "&#34;")) && !strings.Contains(got, strings.ReplaceAll(want, "&#34;", "\"")) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if strings.Index(got, "zeta") > strings.Index(got, "alpha") {
		t.Error("the keys are not in the file's order")
	}
	// A top-level object is split between its members: an opening, four
	// members, a closing.
	if len(d.Blocks) != 6 {
		t.Errorf("%d blocks, want 6", len(d.Blocks))
	}

	_, err = JSON([]byte("{\n  \"a\": 1,\n  \"b\": }\n"), Options{})
	if err == nil || !strings.Contains(err.Error(), "line 3, column") {
		t.Errorf("error %v, want where it is", err)
	}
	_, err = JSON([]byte(`{"a": [1, 2`), Options{})
	if err == nil || !strings.Contains(err.Error(), "unexpected end") {
		t.Errorf("error %v", err)
	}
	vals, err := ParseJSON([]byte("{\"a\":1}\n{\"a\":2}\n"))
	if err != nil || len(vals) != 2 {
		t.Errorf("JSON Lines: %d values, %v", len(vals), err)
	}
}

func TestImageInfo(t *testing.T) {
	var g, j bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 7, 3), []color.Color{color.Black}), nil)
	_ = jpeg.Encode(&j, image.NewRGBA(image.Rect(0, 0, 12, 5)), nil)
	vp8l := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f"), make([]byte, 16)...)
	binary.LittleEndian.PutUint32(vp8l[21:], uint32(99)|uint32(49)<<14) // 100×50
	for _, tc := range []struct {
		name string
		data []byte
		want Info
	}{
		{"a.png", pngBytes(30, 20), Info{"image/png", 30, 20}},
		{"a.gif", g.Bytes(), Info{"image/gif", 7, 3}},
		{"a.jpg", j.Bytes(), Info{"image/jpeg", 12, 5}},
		{"a.webp", vp8l, Info{"image/webp", 100, 50}},
		{"a.svg", []byte(`<?xml version="1.0"?><!-- c --><svg xmlns="http://www.w3.org/2000/svg" width="640" height="260"></svg>`), Info{"image/svg+xml", 640, 260}},
		{"b.svg", []byte(`<svg viewBox="0 0 400 100" width="200"></svg>`), Info{"image/svg+xml", 200, 50}},
		{"c.svg", []byte(`<svg></svg>`), Info{"image/svg+xml", 300, 150}},
	} {
		got, ok := ImageInfo(tc.data, tc.name)
		if !ok || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", tc.name, got, ok, tc.want)
		}
		if !IsImage(tc.data) {
			t.Errorf("%s is not sniffed as an image", tc.name)
		}
	}
	if IsImage([]byte("hello")) || IsImage([]byte("<html>")) {
		t.Error("text sniffed as an image")
	}
}

func TestImage(t *testing.T) {
	o := Options{Cols: 80, CellW: 9, CellH: 18, ResPrefix: "p"}
	// Small: its own size. Wide: the width. Tall: 30 rows.
	for _, tc := range []struct{ w, h, cols, rows int }{
		{90, 36, 10, 2},
		{1800, 180, 80, 4},
		{90, 1800, 3, 30},
	} {
		if c, r := o.Fit(tc.w, tc.h); c != tc.cols || r != tc.rows {
			t.Errorf("Fit(%d, %d) = %d×%d, want %d×%d", tc.w, tc.h, c, r, tc.cols, tc.rows)
		}
	}
	short := Options{Cols: 80, CellW: 9, CellH: 18, Screen: 20}
	if _, r := short.Fit(90, 1800); r != 16 {
		t.Errorf("on a 20-row screen an image is %d rows", r)
	}
	d, info, err := Image("dir/pic.png", pngBytes(90, 36), o)
	if err != nil || info.W != 90 || d.Cols != 10 || d.Rows != 2 {
		t.Fatalf("Image: %+v %+v %v", d, info, err)
	}
	b := d.Blocks[0]
	if b.HTML != `<img class="image" src="cid:p-img1" alt="pic.png">` || len(b.Res) != 1 || b.Res[0].Type != "image/png" {
		t.Errorf("block %+v", b)
	}
	if _, _, err := Image("x.png", []byte("not"), o); err == nil {
		t.Error("not an image, and no error")
	}
}

func TestText(t *testing.T) {
	var src strings.Builder
	src.WriteString("tab\there, esc \x1b[31m, <tag> & bell \x07\n")
	for i := range 300 {
		fmt.Fprintf(&src, "line %d\n", i)
	}
	o := Options{Cols: 80, Screen: 40}
	d := Text([]byte(src.String()), o)
	if !strings.Contains(d.Blocks[0].HTML, "tab\there, esc ␛[31m, &lt;tag&gt; &amp; bell ␇") {
		t.Errorf("first block %q", d.Blocks[0].HTML[:80])
	}
	for i, b := range d.Blocks {
		if b.Rows > o.PageRows() {
			t.Errorf("block %d: %d rows", i, b.Rows)
		}
	}
	if len(d.Blocks) < 8 {
		t.Errorf("301 lines in %d blocks", len(d.Blocks))
	}
	if Printable("ok\n") != "ok\n" || Printable("\xff") != "�" {
		t.Error("Printable")
	}
}

func TestEveryDocumentIsInert(t *testing.T) {
	evil := `<p id="p"><a href="rel.html" id="a">x</a><button id="b">b</button><span data-on="click" id="s">s</span></p>`
	docs := []*Doc{
		Markdown([]byte(evil+"\n\n[x](y.md)"), Options{}),
		HTML([]byte("<body>"+evil+"</body>"), Options{}),
		Text([]byte(evil), Options{}),
	}
	tb, _ := ReadTable([]byte("a,b\n"+`"<a href=""x"" id=""i"">",1`+"\n"), ',')
	docs = append(docs, tb.Doc(Options{}))
	j, _ := JSON([]byte(`{"<button id='x'>": "<a href='y'>"}`), Options{})
	docs = append(docs, j)
	for _, d := range docs {
		for _, p := range Pages(d.Blocks, Target) {
			checkInert(t, d.Body(p))
		}
	}
}
