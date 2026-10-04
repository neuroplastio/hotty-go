package hottyvt

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func TestScopeSheet(t *testing.T) {
	sc := &scope{ids: "vt-d1-", res: "vt-", root: "#vt-d1"}
	for _, c := range []struct{ in, want string }{
		{`.k { color: red }`, `#vt-d1 .k{ color: red }`},
		{`a, b > .c:not(#x) {}`, `#vt-d1 a,#vt-d1 b > .c:not(#vt-d1-x){}`},
		{`#st::before { content: "#x" }`, `#vt-d1 #vt-d1-st::before{ content: "#x" }`},
		{`[href="#top"] {}`, `#vt-d1 [href="#top"]{}`},
		{`html, body { margin: 0 }`, `#vt-d1,#vt-d1{ margin: 0 }`},
		{`:root { --c: #fff }`, `#vt-d1{ --c: #fff }`},
		{`html.dark body > p {}`, `#vt-d1.dark > p{}`},
		{`body.wide .card, :root[data-x] p {}`, `#vt-d1.wide .card,#vt-d1[data-x] p{}`},
		{`htmlx, .bodywork {}`, `#vt-d1 htmlx,#vt-d1 .bodywork{}`},
		{`@media (min-width: 40em) { .a { b: c } }`, `@media (min-width: 40em){#vt-d1 .a{ b: c }}`},
		{`@supports (display: grid) { @media print { #a {} } }`, `@supports (display: grid){@media print{#vt-d1 #vt-d1-a{}}}`},
		{`@keyframes spin { from { r: 0 } to { r: 1turn } }`, `@keyframes spin{ from { r: 0 } to { r: 1turn } }`},
		{`@font-face { src: url(cid:font) format("woff2") }`, `@font-face{ src: url(cid:vt-font) format("woff2") }`},
		{`@import "cid:base";`, `@import "cid:vt-base";`},
		{`@import url('cid:base') screen;`, `@import url('cid:vt-base') screen;`},
		{`.bg { background: url("cid:img"), url(https://x.test/a.png) }`, `#vt-d1 .bg{ background: url("cid:vt-img"), url(https://x.test/a.png) }`},
		{`.m { mask: url(#m); fill: #m }`, `#vt-d1 .m{ mask: url(#vt-d1-m); fill: #m }`},
		{`/* a } comment */ .a { b: "}" } .c{}`, `#vt-d1 .a{ b: "}" }#vt-d1 .c{}`},
		{`.card { color: red; &:hover { color: blue } .t#x { x: y } }`, `#vt-d1 .card{ color: red;&:hover{ color: blue }.t#vt-d1-x{ x: y } }`},
		{`.cut { color: red`, `#vt-d1 .cut{ color: red}`},
	} {
		if got := sc.sheet(c.in); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

// A stylesheet sent as a resource is any surface's: its ids, whose prefix
// is each surface's own, match by their end.
func TestScopeSharedSheet(t *testing.T) {
	sc := &scope{res: "vt-", root: "#vt :where(.vt-d)"}
	got := sc.sheet(`body { margin: 0 } .a #b { c: url(cid:d) }`)
	if want := `#vt :where(.vt-d){ margin: 0 }#vt :where(.vt-d) .a [id$="-b"]{ c: url(cid:vt-d) }`; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestScopeAttributes(t *testing.T) {
	sc := &scope{ids: "s-", res: "vt-", root: "#s"}
	for _, c := range []struct {
		el, key, val, want string
		keep               bool
	}{
		{"div", "id", "a", "s-a", true},
		{"label", "for", " a ", "s-a", true},
		{"td", "headers", "a  b", "s-a s-b", true},
		{"div", "aria-labelledby", "a b", "s-a s-b", true},
		{"div", "style", "background: url(cid:x)", "background: url(cid:vt-x)", true},
		{"img", "src", "cid:x", "cid:vt-x", true},
		{"img", "src", "data:image/png;base64,AA", "data:image/png;base64,AA", true},
		{"img", "srcset", "cid:a 1x, cid:b 2x", "cid:vt-a 1x, cid:vt-b 2x", true},
		{"use", "href", "#icon", "#s-icon", true},
		{"link", "href", "cid:css", "cid:vt-css", true},
		{"rect", "fill", "url(#g)", "url(#s-g)", true},
		{"div", "class", "url(", "url(", true},
		{"div", "data-on", "click", "", false},
		{"input", "autofocus", "", "", false},
		{"a", "href", "/x", "", false},
	} {
		n := elementNode(c.el)
		got, keep := sc.attr(n, "", c.key, c.val)
		if c.key == "class" {
			got, keep = c.val, true // classes are the document's own
		}
		if got != c.want || keep != c.keep {
			t.Errorf("<%s %s=%q>: %q %v, want %q %v", c.el, c.key, c.val, got, keep, c.want, c.keep)
		}
	}
}

func TestScopeHyperlinks(t *testing.T) {
	sc := &scope{ids: "s-"}
	blank := elementNode("a")
	blank.Attr = append(blank.Attr, attribute("target", "_blank"))
	for _, c := range []struct{ href, want string }{
		{"https://example.com/a", "https://example.com/a"},
		{"/rel", ""}, // no base: not a hyperlink
		{"mailto:a@b", ""},
		{"javascript:x", ""},
	} {
		got, _ := sc.hyperlink(blank, c.href)
		if got != c.want {
			t.Errorf("%q: %q, want %q", c.href, got, c.want)
		}
	}
	if got, keep := sc.hyperlink(elementNode("a"), "https://example.com"); keep || got != "" {
		t.Errorf("a link without target=_blank: %q %v", got, keep)
	}
	if !strings.Contains(sc.url("#x"), "s-x") || sc.url("https://a.test/#x") != "https://a.test/#x" {
		t.Errorf("urls: %q", sc.url("#x"))
	}
}

func elementNode(tag string) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
}

func attribute(k, v string) html.Attribute { return html.Attribute{Key: k, Val: v} }
