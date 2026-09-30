package doc

import (
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Inert makes a fragment of HTML safe to leave in scrollback after its
// program exits (docs/toolkit.md, Surfaces):
//   - nothing that runs or loads: <script>, <iframe>, <object>, <embed>,
//     <base>, <meta>, <link>, event attributes, javascript: URLs;
//   - nothing that reports: no id on a link, button, summary or form
//     control, no data-on, and no forms (a submit is reported whatever
//     the ids);
//   - links only as hyperlinks: an absolute http(s) link gets
//     target="_blank", so the terminal opens it and never reports it
//     (SPEC §9); any other link becomes its text.
//
// A host removes scripts itself (SPEC §12); this does not rely on it.
func Inert(fragment string) string {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), ctx)
	if err != nil {
		return html.EscapeString(fragment)
	}
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == html.ElementNode && dropped[strings.ToLower(n.Data)] || n.Type == html.CommentNode {
			continue
		}
		if n.Type == html.ElementNode {
			inertElement(n)
		}
		inert(n)
		_ = html.Render(&b, n)
	}
	return b.String()
}

// dropped are the elements Inert removes with their content.
var dropped = map[string]bool{
	"script": true, "noscript": true, "iframe": true, "frame": true, "frameset": true, "object": true,
	"embed": true, "applet": true, "base": true, "meta": true, "link": true, "template": true, "portal": true,
}

// reporting are the elements whose clicks or changes a host reports when
// they have an id (SPEC §9).
var reporting = map[string]bool{
	"a": true, "button": true, "summary": true, "input": true, "select": true, "textarea": true,
	"option": true, "area": true, "label": true,
}

func inert(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode:
			n.RemoveChild(c)
		case c.Type == html.ElementNode && dropped[strings.ToLower(c.Data)]:
			n.RemoveChild(c)
		case c.Type == html.ElementNode:
			inertElement(c)
			inert(c)
		}
		c = next
	}
}

func inertElement(n *html.Node) {
	name := strings.ToLower(n.Data)
	keep := n.Attr[:0]
	var href string
	for _, a := range n.Attr {
		k := strings.ToLower(a.Key)
		switch {
		case strings.HasPrefix(k, "on"), k == "data-on", k == "autofocus", k == "action", k == "formaction",
			k == "target", k == "ping", k == "download", k == "rel", k == "form":
			continue
		case k == "id" && reporting[name]:
			continue
		case k == "href" || (a.Namespace == "xlink" && k == "href"):
			href = strings.TrimSpace(a.Val)
			continue
		case urlAttr[k] && scriptURL(a.Val):
			continue
		}
		keep = append(keep, a)
	}
	n.Attr = keep
	switch name {
	case "a":
		if Web(href) {
			n.Attr = append(n.Attr, html.Attribute{Key: "href", Val: href},
				html.Attribute{Key: "target", Val: "_blank"}, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
		} else if n.Namespace == "" {
			n.Data, n.DataAtom = "span", atom.Span
		}
	case "form":
		n.Data, n.DataAtom = "div", atom.Div
	}
}

var urlAttr = map[string]bool{"src": true, "srcset": true, "poster": true, "background": true, "cite": true, "data": true, "formaction": true}

func scriptURL(v string) bool {
	v = strings.ToLower(strings.Join(strings.Fields(v), ""))
	return strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "vbscript:")
}

// Web reports whether a link goes to the web: an absolute http or https
// URL, the only kind a document left behind keeps as a link.
func Web(href string) bool {
	h := strings.ToLower(strings.TrimSpace(href))
	return strings.HasPrefix(h, "https://") || strings.HasPrefix(h, "http://")
}

// local reports whether a reference names a file next to the document:
// not a URL of any scheme, not a fragment.
func local(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "//") {
		return false
	}
	if i := strings.IndexAny(ref, ":/?#"); i > 0 && ref[i] == ':' {
		return false // a scheme: http:, data:, cid:, mailto:
	}
	return true
}

// image reads a local image a document refers to and names it as a
// resource: its cid: URL, and the resource. ok is false when it cannot be
// read or is not an image.
func (o *Options) image(ref string) (src string, res Resource, ok bool) {
	if o.ReadFile == nil || !local(ref) {
		return "", Resource{}, false
	}
	name := ref
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	data, err := o.ReadFile(name)
	if err != nil {
		return "", Resource{}, false
	}
	info, ok := ImageInfo(data, name)
	if !ok {
		return "", Resource{}, false
	}
	res = Resource{ID: o.resID("img"), Type: info.Type, Data: data}
	return "cid:" + res.ID, res, true
}

// HTML is an HTML file as a document: its body, split into blocks at its
// top-level elements, and its stylesheets. The document's own CSS goes
// after the shared one, so it wins; the shared defaults are written with
// :where(), which any rule of the document's beats. Local images and
// stylesheets are read through ReadFile.
func HTML(src []byte, o Options) *Doc {
	root, err := html.Parse(bytes.NewReader(src))
	d := &Doc{Class: "page"}
	if err != nil {
		d.Blocks = []Block{{HTML: "<pre>" + html.EscapeString(string(src)) + "</pre>", Rows: len(strings.Split(string(src), "\n"))}}
		return d
	}
	var css strings.Builder
	var body *html.Node
	var res []Resource
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode {
				switch c.DataAtom {
				case atom.Body:
					body = c
				case atom.Style:
					css.WriteString(text(c) + "\n")
					n.RemoveChild(c)
					c = next
					continue
				case atom.Link:
					if rel := strings.ToLower(attr(c, "rel")); strings.Contains(rel, "stylesheet") && o.ReadFile != nil && local(attr(c, "href")) {
						if b, err := o.ReadFile(attr(c, "href")); err == nil {
							css.Write(b)
							css.WriteString("\n")
						}
					}
				case atom.Img:
					if s, r, ok := o.image(attr(c, "src")); ok {
						setAttr(c, "src", s)
						delAttr(c, "srcset")
						res = append(res, r)
					}
				}
			}
			walk(c)
			c = next
		}
	}
	walk(root)
	if body == nil {
		return d
	}
	// The body's own class and style move to the element that stands for
	// it (Body).
	if c := attr(body, "class"); c != "" {
		d.Class += " " + c
	}
	if s := attr(body, "style"); s != "" {
		css.WriteString("main.page {" + s + "}\n")
	}
	d.CSS = PageCSS + css.String()
	d.Blocks = splitNodes(children(body), &o, 0)
	// Resources go with the first block that refers to them.
	for _, r := range res {
		for i := range d.Blocks {
			if strings.Contains(d.Blocks[i].HTML, "cid:"+r.ID) {
				d.Blocks[i].Res = append(d.Blocks[i].Res, r)
				break
			}
		}
	}
	return d
}

// PageCSS is the shared defaults for an HTML file's document: the look of
// the terminal (the page's font and colours come from the program's
// stylesheet) and readable links and code, all of it at no specificity, so
// the document's own rules win.
const PageCSS = `
main.page { display: block; padding: 0 var(--doc-col) var(--doc-row); }
:where(main.page) a { color: var(--doc-accent); }
:where(main.page) pre, :where(main.page) code { font-family: var(--doc-mono); }
:where(main.page) img { max-width: 100%; }
:where(main.page) table { border-collapse: collapse; }
:where(main.page) th, :where(main.page) td { padding: 0 var(--doc-col); text-align: left; }
`

// blockish are the elements that are blocks of their own in a body.
var blockish = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Ul: true, atom.Ol: true, atom.Dl: true, atom.Table: true, atom.Pre: true, atom.Blockquote: true, atom.Section: true,
	atom.Article: true, atom.Header: true, atom.Footer: true, atom.Nav: true, atom.Aside: true, atom.Main: true,
	atom.Figure: true, atom.Hr: true, atom.Form: true, atom.Details: true, atom.Fieldset: true, atom.Address: true,
	atom.Center: true, atom.Svg: true, atom.Picture: true, atom.Video: true, atom.Audio: true, atom.Canvas: true,
}

// containers may be split between their children when they are too long
// for one surface.
var containers = map[atom.Atom]bool{
	atom.Div: true, atom.Section: true, atom.Article: true, atom.Main: true, atom.Header: true, atom.Footer: true,
	atom.Aside: true, atom.Center: true, atom.Form: true,
}

// splitNodes makes blocks of a list of sibling nodes: each block-level
// element one, the text and inline elements between them one together. A
// container too long for a surface is split between its children, each part
// in a copy of it.
func splitNodes(nodes []*html.Node, o *Options, depth int) []Block {
	var out []Block
	var run []*html.Node
	flush := func() {
		if len(run) == 0 {
			return
		}
		blank := true
		for _, n := range run {
			if n.Type != html.TextNode || strings.TrimSpace(n.Data) != "" {
				blank = false
			}
		}
		if !blank {
			out = append(out, nodeBlock(run, o, "div"))
		}
		run = nil
	}
	for _, n := range nodes {
		if n.Type == html.CommentNode {
			continue
		}
		if n.Type != html.ElementNode || !blockish[n.DataAtom] {
			run = append(run, n)
			continue
		}
		flush()
		if containers[n.DataAtom] && depth < 8 && estimate(n, o.cols()) > Target {
			for _, part := range splitNodes(children(n), o, depth+1) {
				open, end := openTag(n)
				part.HTML = open + part.HTML + end
				out = append(out, part)
			}
			continue
		}
		out = append(out, nodeBlock([]*html.Node{n}, o, ""))
	}
	flush()
	return out
}

// nodeBlock renders nodes as one inert block, in a wrapper element when
// wrap is not empty.
func nodeBlock(nodes []*html.Node, o *Options, wrap string) Block {
	var b strings.Builder
	rows := 0
	for _, n := range nodes {
		_ = html.Render(&b, n)
		rows += estimate(n, o.cols())
	}
	s := Inert(b.String())
	if wrap != "" {
		s = "<" + wrap + ">" + s + "</" + wrap + ">"
	}
	return Block{HTML: s, Rows: max(1, rows)}
}

// openTag is an element's start and end tags, without its children and
// without an id (a copy must not share it).
func openTag(n *html.Node) (string, string) {
	c := &html.Node{Type: html.ElementNode, Data: n.Data, DataAtom: n.DataAtom, Namespace: n.Namespace}
	for _, a := range n.Attr {
		if a.Key != "id" {
			c.Attr = append(c.Attr, a)
		}
	}
	var b strings.Builder
	_ = html.Render(&b, c)
	s := b.String()
	end := "</" + n.Data + ">"
	return strings.TrimSuffix(s, end), end
}

// estimate is a generous guess at the rows a node takes cols cells wide.
func estimate(n *html.Node, cols int) int {
	switch n.Type {
	case html.TextNode:
		t := strings.TrimSpace(n.Data)
		if t == "" {
			return 0
		}
		return textRows(len([]rune(t)), cols)
	case html.ElementNode:
	default:
		return 0
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Head, atom.Template:
		return 0
	case atom.Pre:
		return monoRows(strings.Split(text(n), "\n"), cols) + 2
	case atom.Br, atom.Hr, atom.Tr, atom.Li, atom.Dt:
	case atom.Img, atom.Svg, atom.Video, atom.Canvas, atom.Picture:
		if h, err := strconv.Atoi(strings.TrimSuffix(attr(n, "height"), "px")); err == nil && h > 0 {
			return h/12 + 2
		}
		return 16
	}
	rows := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		rows += estimate(c, cols)
	}
	switch {
	case n.DataAtom == atom.Br || n.DataAtom == atom.Hr:
		rows++
	case n.DataAtom == atom.Tr:
		rows = max(rows, 1) + 1
	case n.DataAtom == atom.H1 || n.DataAtom == atom.H2:
		rows += 3
	case blockish[n.DataAtom] || n.DataAtom == atom.Li || n.DataAtom == atom.Dt || n.DataAtom == atom.Dd:
		rows++
	}
	return rows
}

func children(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

// text is a node's text, as it is in the source.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func delAttr(n *html.Node, key string) {
	keep := n.Attr[:0]
	for _, a := range n.Attr {
		if !strings.EqualFold(a.Key, key) {
			keep = append(keep, a)
		}
	}
	n.Attr = keep
}

// Para is a paragraph of a document's text: what a program draws in cells
// where the terminal is not a host.
type Para struct {
	// Kind is the element it came from: "h1"…"h6", "p", "li", "pre",
	// "dt", "dd", "td" (a table's row, cells joined by two spaces).
	Kind string
	Text string
}

// Paragraphs is an HTML document's text, a paragraph per block: runs of
// whitespace are one space, except in <pre>.
func Paragraphs(src []byte) []Para {
	root, err := html.Parse(bytes.NewReader(src))
	if err != nil {
		return []Para{{Kind: "pre", Text: string(src)}}
	}
	var out []Para
	var cur strings.Builder
	kind := "p"
	flush := func() {
		t := strings.Join(strings.Fields(cur.String()), " ")
		if t != "" {
			out = append(out, Para{Kind: kind, Text: t})
		}
		cur.Reset()
		kind = "p"
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			cur.WriteString(n.Data)
			return
		case html.ElementNode:
		default:
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			return
		}
		switch n.DataAtom {
		case atom.Script, atom.Style, atom.Head, atom.Template, atom.Noscript:
			return
		case atom.Pre:
			flush()
			out = append(out, Para{Kind: "pre", Text: strings.Trim(text(n), "\n")})
			return
		case atom.Br:
			cur.WriteString(" ")
			return
		case atom.Img:
			if alt := attr(n, "alt"); alt != "" {
				cur.WriteString("[" + alt + "]")
			}
			return
		case atom.Tr:
			flush()
			var cells []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
					cells = append(cells, strings.Join(strings.Fields(text(c)), " "))
				}
			}
			if t := strings.TrimSpace(strings.Join(cells, "  ")); t != "" {
				out = append(out, Para{Kind: "td", Text: strings.Join(cells, "  ")})
			}
			return
		}
		block := blockish[n.DataAtom] || n.DataAtom == atom.Li || n.DataAtom == atom.Dt || n.DataAtom == atom.Dd ||
			n.DataAtom == atom.Body || n.DataAtom == atom.Title
		if block {
			flush()
			switch n.DataAtom {
			case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Li, atom.Dt, atom.Dd:
				kind = n.Data
			case atom.Title:
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			flush()
		}
	}
	walk(root)
	flush()
	return out
}
