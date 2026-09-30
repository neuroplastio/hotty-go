package doc

import (
	"bytes"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ManPage is a manual page as clean, semantic HTML: a section per
// heading, definition lists for tagged paragraphs (options), and references
// to other pages as man: links. It is what a viewer shows, and what the
// web shell's /usr/share/man/html holds, ready made.
type ManPage struct {
	Name, Section string // "ls", "1"
	Sections      []ManSection
}

// ManSection is one section of a page: NAME, SYNOPSIS, OPTIONS, ….
type ManSection struct {
	Title string
	// HTML is its content, its heading left out: paragraphs, <dl>, <pre>,
	// <table> and <h3> subsections.
	HTML string
}

// Title is the page as man writes it: "ls(1)".
func (m *ManPage) Title() string { return m.Name + "(" + m.Section + ")" }

// ManRef is a reference's link: "man:ls(1)".
func ManRef(name, section string) string { return "man:" + name + "(" + section + ")" }

// ParseManRef reads a man: link back.
func ParseManRef(href string) (name, section string, ok bool) {
	m := manLink.FindStringSubmatch(href)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

var manLink = regexp.MustCompile(`^man:([^()\s]+)\(([^()\s]+)\)$`)

// crossRef finds references such as ls(1) or printf(3p) in text.
var crossRef = regexp.MustCompile(`(^|[^\w.:+@-])([A-Za-z_][\w.:+@-]*[\w+])\(([1-9][a-z0-9]{0,6}|n|l)\)`)

// refName is a word that may be a page's name.
var refName = regexp.MustCompile(`^[A-Za-z_][\w.:+@-]*$`)

// refSection is a section in parentheses, at the start of text.
var refSection = regexp.MustCompile(`^\(([1-9][a-z0-9]{0,6}|n|l)\)`)

// CleanMan turns HTML from groff -mandoc -Thtml or mandoc -Thtml into a
// clean page: groff's inline styles dropped, its percent indents read as
// tagged paragraphs or indented ones, and references linked.
func CleanMan(src []byte, name, section string) (*ManPage, error) {
	root, err := xhtml.Parse(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	page := &ManPage{Name: name, Section: section}
	body := find(root, func(n *xhtml.Node) bool { return n.DataAtom == atom.Body })
	if body == nil {
		return nil, fmt.Errorf("no body")
	}
	if find(body, func(n *xhtml.Node) bool { return n.DataAtom == atom.Section && hasClass(n, "Sh") }) != nil {
		cleanMandoc(page, body)
	} else {
		cleanGroff(page, body)
	}
	for i := range page.Sections {
		page.Sections[i].HTML = linkRefs(page.Sections[i].HTML)
	}
	return page, nil
}

// --- groff ------------------------------------------------------------------------

// item is one of groff's top-level blocks in a section.
type item struct {
	kind   byte // 'p' a paragraph, 't' a tagged paragraph (from a table), 'h' a subsection, 'o' anything else
	indent float64
	gap    bool // margin-top: a blank line before it
	node   *xhtml.Node
	dt, dd *xhtml.Node // 't'
}

var marginLeft = regexp.MustCompile(`margin-left:\s*([\d.]+)%`)

func cleanGroff(page *ManPage, body *xhtml.Node) {
	var title string
	var items []item
	started := false
	base := math.Inf(1)
	flush := func() {
		if started {
			page.Sections = append(page.Sections, ManSection{Title: title, HTML: groffSection(items, base)})
		}
		items = nil
	}
	// The smallest indent is the page's left margin.
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if c.DataAtom == atom.P {
			if m := marginLeft.FindStringSubmatch(attr(c, "style")); m != nil {
				f, _ := strconv.ParseFloat(m[1], 64)
				base = math.Min(base, f)
			}
		}
	}
	if math.IsInf(base, 1) {
		base = 0
	}
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.TextNode {
			if strings.TrimSpace(c.Data) != "" {
				items = append(items, item{kind: 'p', indent: base, node: &xhtml.Node{Type: xhtml.ElementNode, Data: "p", DataAtom: atom.P, FirstChild: nil}})
				items[len(items)-1].node.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: c.Data})
			}
			continue
		}
		if c.Type != xhtml.ElementNode {
			continue
		}
		switch c.DataAtom {
		case atom.H1, atom.Hr:
		case atom.H2:
			flush()
			title, started = strings.Join(strings.Fields(text(c)), " "), true
		case atom.H3, atom.H4:
			items = append(items, item{kind: 'h', node: c})
		case atom.P:
			it := item{kind: 'p', indent: base, node: c}
			style := attr(c, "style")
			if m := marginLeft.FindStringSubmatch(style); m != nil {
				it.indent, _ = strconv.ParseFloat(m[1], 64)
			}
			it.gap = strings.Contains(style, "margin-top")
			items = append(items, it)
		case atom.Table:
			if rows, ok := taggedRows(c); ok {
				items = append(items, rows...)
			} else {
				items = append(items, item{kind: 'o', node: c})
			}
		case atom.A:
			if attr(c, "href") == "" {
				continue // an anchor
			}
			items = append(items, item{kind: 'p', indent: base, node: wrapP(c)})
		default:
			items = append(items, item{kind: 'o', node: c})
		}
	}
	flush()
}

func wrapP(n *xhtml.Node) *xhtml.Node {
	p := &xhtml.Node{Type: xhtml.ElementNode, Data: "p", DataAtom: atom.P}
	c := &xhtml.Node{Type: n.Type, Data: n.Data, DataAtom: n.DataAtom, Attr: n.Attr}
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		c.AppendChild(clone(k))
	}
	p.AppendChild(c)
	return p
}

// taggedRows reads groff's table for tagged paragraphs (.TP, .IP with a
// short tag): per row an empty cell, the tag, an empty cell, the text, and
// perhaps an empty cell for the rest of the line.
func taggedRows(t *xhtml.Node) ([]item, bool) {
	var out []item
	var walk func(n *xhtml.Node) bool
	walk = func(n *xhtml.Node) bool {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch c.DataAtom {
			case atom.Tbody, atom.Thead:
				if !walk(c) {
					return false
				}
			case atom.Tr:
				var tds []*xhtml.Node
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					if d.DataAtom == atom.Td {
						tds = append(tds, d)
					}
				}
				if len(tds) < 4 || len(tds) > 5 || !blank(tds[0]) || !blank(tds[2]) || (len(tds) == 5 && !blank(tds[4])) {
					return false
				}
				w, _ := strconv.ParseFloat(strings.TrimSuffix(attr(tds[0], "width"), "%"), 64)
				gap := false
				if p := find(tds[1], func(n *xhtml.Node) bool { return n.DataAtom == atom.P }); p != nil {
					gap = strings.Contains(attr(p, "style"), "margin-top")
				}
				out = append(out, item{kind: 't', indent: w, gap: gap, dt: tds[1], dd: tds[3]})
			}
		}
		return true
	}
	if !walk(t) || len(out) == 0 {
		return nil, false
	}
	return out, true
}

func blank(n *xhtml.Node) bool { return strings.TrimSpace(text(n)) == "" }

// indentClass names how far in a paragraph is from where it should be.
func indentClass(by float64) string {
	switch {
	case by > 22:
		return "i3"
	case by > 12:
		return "i2"
	case by > 2:
		return "i1"
	}
	return ""
}

// groffSection makes a section's clean content from groff's blocks.
func groffSection(items []item, base float64) string {
	var out []*xhtml.Node
	var dl, dd *xhtml.Node
	var tagIndent, ddIndent float64
	var last *xhtml.Node // the last plain paragraph, which a line broken with .br continues
	var lastIndent float64
	newDL := func() {
		if dl == nil {
			dl = el("dl")
			out = append(out, dl)
		}
		last = nil
	}
	for i := 0; i < len(items); i++ {
		it := items[i]
		switch it.kind {
		case 't':
			newDL()
			dt := el("dt")
			inline(dt, it.dt)
			dd = el("dd")
			blockish := el("p")
			inline(blockish, it.dd)
			dd.AppendChild(blockish)
			dl.AppendChild(dt)
			dl.AppendChild(dd)
			tagIndent, ddIndent = it.indent, -1
		case 'p':
			// Text inside the definition that came before.
			if dl != nil && dd != nil && it.indent > tagIndent+1 {
				p := el("p")
				if ddIndent >= 0 {
					if c := indentClass(it.indent - ddIndent); c != "" {
						setAttr(p, "class", c)
					}
				}
				inline(p, it.node)
				dd.AppendChild(p)
				continue
			}
			// A tag: a paragraph followed by a deeper one that has no
			// blank line of its own (.TP). grohtml puts the first tag of a
			// list at the end of the paragraph before it, after a <br>.
			if i+1 < len(items) && items[i+1].kind == 'p' && items[i+1].indent > it.indent+1 && !items[i+1].gap {
				tag := it.node
				if before, after, ok := splitAtLastBR(it.node); ok && strings.TrimSpace(text(before)) != "" && strings.TrimSpace(text(after)) != "" {
					dl, dd = nil, nil
					p := el("p")
					if c := indentClass(it.indent - base); c != "" {
						setAttr(p, "class", c)
					}
					inline(p, before)
					out = append(out, p)
					tag = after
				}
				newDL()
				dt := el("dt")
				inline(dt, tag)
				dd = el("dd")
				p := el("p")
				inline(p, items[i+1].node)
				dd.AppendChild(p)
				dl.AppendChild(dt)
				dl.AppendChild(dd)
				tagIndent, ddIndent = it.indent, items[i+1].indent
				i++
				continue
			}
			dl, dd = nil, nil
			if last != nil && !it.gap && math.Abs(it.indent-lastIndent) < 1 {
				last.AppendChild(el("br"))
				inline(last, it.node)
				continue
			}
			p := el("p")
			if c := indentClass(it.indent - base); c != "" {
				setAttr(p, "class", c)
			}
			inline(p, it.node)
			out = append(out, p)
			last, lastIndent = p, it.indent
		case 'h':
			dl, dd, last = nil, nil, nil
			h := el("h3")
			h.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: strings.Join(strings.Fields(text(it.node)), " ")})
			out = append(out, h)
		default:
			dl, dd, last = nil, nil, nil
			out = append(out, cleanBlock(it.node)...)
		}
	}
	for _, n := range out {
		tidy(n)
	}
	return render(out)
}

// tidy collapses runs of white space to one space outside <pre>, trims it
// at the edges of blocks, and drops inline elements left empty.
func tidy(n *xhtml.Node) {
	if n.DataAtom == atom.Pre {
		return
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case xhtml.TextNode:
			c.Data = spaces.ReplaceAllString(c.Data, " ")
		case xhtml.ElementNode:
			tidy(c)
			switch c.DataAtom {
			case atom.B, atom.I, atom.U, atom.Code, atom.Sup, atom.Sub, atom.Small:
				if c.FirstChild == nil {
					n.RemoveChild(c)
				}
			}
		}
		c = next
	}
	switch n.DataAtom {
	case atom.P, atom.Dt, atom.Dd, atom.Li, atom.Td, atom.Th, atom.H3:
		trimEdge(n, true)
		trimEdge(n, false)
	}
}

var spaces = regexp.MustCompile(`\s+`)

// trimEdge trims the white space at the start (or end) of a block's text,
// and drops text left empty there.
func trimEdge(n *xhtml.Node, start bool) {
	for {
		c := n.LastChild
		if start {
			c = n.FirstChild
		}
		switch {
		case c == nil:
			return
		case c.Type == xhtml.TextNode:
			if start {
				c.Data = strings.TrimLeft(c.Data, " ")
			} else {
				c.Data = strings.TrimRight(c.Data, " ")
			}
			if c.Data != "" {
				return
			}
			n.RemoveChild(c)
		case c.Type == xhtml.ElementNode && c.DataAtom != atom.Br:
			trimEdge(c, start)
			if c.FirstChild != nil {
				return
			}
			n.RemoveChild(c)
		default:
			return
		}
	}
}

// splitAtLastBR splits a paragraph at its last <br>, the elements around
// it cut in two, and reports whether it had one.
func splitAtLastBR(p *xhtml.Node) (before, after *xhtml.Node, ok bool) {
	var br *xhtml.Node
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Br {
				br = c
			}
			walk(c)
		}
	}
	walk(p)
	if br == nil {
		return nil, nil, false
	}
	before, after = clone(p), shallow(p)
	// Find the <br> in the copy, by its path from p.
	var path []int
	for n := br; n != p; n = n.Parent {
		i := 0
		for s := n.PrevSibling; s != nil; s = s.PrevSibling {
			i++
		}
		path = append([]int{i}, path...)
	}
	// Walk down the copy: at each level, what follows the path moves to a
	// shallow copy of that level in after.
	src, dst := before, after
	for depth, i := range path {
		c := src.FirstChild
		for range i {
			c = c.NextSibling
		}
		if depth == len(path)-1 {
			// c is the <br>: everything after it moves, and it goes.
			for s := c.NextSibling; s != nil; {
				next := s.NextSibling
				src.RemoveChild(s)
				dst.AppendChild(s)
				s = next
			}
			src.RemoveChild(c)
			break
		}
		mid := shallow(c)
		dst.AppendChild(mid)
		for s := c.NextSibling; s != nil; {
			next := s.NextSibling
			src.RemoveChild(s)
			dst.AppendChild(s)
			s = next
		}
		src, dst = c, mid
	}
	return before, after, true
}

// --- mandoc -----------------------------------------------------------------------

func cleanMandoc(page *ManPage, body *xhtml.Node) {
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Section && hasClass(c, "Sh") {
				var title string
				var out []*xhtml.Node
				for k := c.FirstChild; k != nil; k = k.NextSibling {
					if k.DataAtom == atom.H1 && title == "" {
						title = strings.Join(strings.Fields(text(k)), " ")
						continue
					}
					out = append(out, cleanBlock(k)...)
				}
				for _, n := range out {
					tidy(n)
				}
				page.Sections = append(page.Sections, ManSection{Title: title, HTML: render(out)})
				continue
			}
			walk(c)
		}
	}
	walk(body)
}

// --- the clean-up of markup --------------------------------------------------------

// cleanBlock copies block-level markup without its styles: paragraphs,
// lists, definition lists, tables, <pre>, subsection headings.
func cleanBlock(n *xhtml.Node) []*xhtml.Node {
	switch n.Type {
	case xhtml.TextNode:
		if strings.TrimSpace(n.Data) == "" {
			return nil
		}
		p := el("p")
		p.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: n.Data})
		return []*xhtml.Node{p}
	case xhtml.ElementNode:
	default:
		return nil
	}
	tag := ""
	switch n.DataAtom {
	case atom.P, atom.Dl, atom.Dt, atom.Dd, atom.Ul, atom.Ol, atom.Li, atom.Pre, atom.Table, atom.Thead,
		atom.Tbody, atom.Tr, atom.Td, atom.Th, atom.Blockquote:
		tag = n.Data
	case atom.H2, atom.H3, atom.H4:
		tag = "h3"
	case atom.Div:
		if hasClass(n, "Bd-indent") || hasClass(n, "Bl-indent") || hasClass(n, "Rs") {
			d := el("div")
			setAttr(d, "class", "i1")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				for _, k := range cleanBlock(c) {
					d.AppendChild(k)
				}
			}
			return []*xhtml.Node{d}
		}
	case atom.Hr, atom.Script, atom.Style, atom.Img:
		return nil
	}
	if tag == "" {
		// Anything else: its content, as blocks if it has any, else as a
		// paragraph.
		var out []*xhtml.Node
		hasBlock := false
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode && (blockish[c.DataAtom] || c.DataAtom == atom.Dl) {
				hasBlock = true
			}
		}
		if !hasBlock {
			if strings.TrimSpace(text(n)) == "" {
				return nil
			}
			p := el("p")
			inline(p, n)
			return []*xhtml.Node{p}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			out = append(out, cleanBlock(c)...)
		}
		return out
	}
	out := el(tag)
	for _, a := range n.Attr {
		if a.Key == "colspan" || a.Key == "rowspan" {
			out.Attr = append(out.Attr, xhtml.Attribute{Key: a.Key, Val: a.Val})
		}
	}
	switch tag {
	case "p", "dt", "h3", "pre", "th":
		inline(out, n)
	case "td", "dd", "li", "blockquote":
		hasBlock := false
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode && (blockish[c.DataAtom] || c.DataAtom == atom.Dl) {
				hasBlock = true
			}
		}
		if hasBlock {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				for _, k := range cleanBlock(c) {
					out.AppendChild(k)
				}
			}
		} else {
			inline(out, n)
		}
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.TextNode && strings.TrimSpace(c.Data) == "" {
				continue
			}
			for _, k := range cleanBlock(c) {
				out.AppendChild(k)
			}
		}
	}
	return []*xhtml.Node{out}
}

// inline appends clean copies of src's inline content to dst: bold,
// italic, code, line breaks and web links; everything else is unwrapped.
func inline(dst, src *xhtml.Node) {
	for c := src.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.TextNode:
			dst.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: c.Data})
			continue
		case xhtml.ElementNode:
		default:
			continue
		}
		tag := ""
		switch c.DataAtom {
		case atom.B, atom.Strong:
			tag = "b"
		case atom.I, atom.Em, atom.Var, atom.Cite:
			tag = "i"
		case atom.U:
			tag = "u"
		case atom.Code, atom.Tt, atom.Kbd, atom.Samp:
			tag = "code"
			if hasClass(c, "Nm") || hasClass(c, "Fl") || hasClass(c, "Cm") || hasClass(c, "Ic") {
				tag = "b"
			}
		case atom.Sup, atom.Sub, atom.Small:
			tag = c.Data
		case atom.Br:
			dst.AppendChild(el("br"))
			continue
		case atom.A:
			if href := attr(c, "href"); Web(href) {
				a := el("a")
				a.Attr = []xhtml.Attribute{{Key: "href", Val: href}, {Key: "target", Val: "_blank"}, {Key: "rel", Val: "noopener noreferrer"}}
				inline(a, c)
				dst.AppendChild(a)
				continue
			}
		case atom.Img:
			if alt := attr(c, "alt"); alt != "" {
				dst.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: alt})
			}
			continue
		case atom.Script, atom.Style:
			continue
		}
		if tag == "" {
			inline(dst, c) // unwrapped
			continue
		}
		e := el(tag)
		inline(e, c)
		dst.AppendChild(e)
	}
}

// linkRefs makes references to other pages links: "ls(1)" in text, and
// "<b>ls</b>(1)" as man(7)'s .BR writes it. Not in <pre>, nor in a link.
func linkRefs(s string) string {
	ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(s), ctx)
	if err != nil {
		return s
	}
	for _, n := range nodes {
		ctx.AppendChild(n)
	}
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			switch {
			case c.Type == xhtml.ElementNode && (c.DataAtom == atom.Pre || c.DataAtom == atom.A):
			case c.Type == xhtml.ElementNode && (c.DataAtom == atom.B || c.DataAtom == atom.I) &&
				c.FirstChild != nil && c.FirstChild == c.LastChild && c.FirstChild.Type == xhtml.TextNode &&
				refName.MatchString(c.FirstChild.Data) && next != nil && next.Type == xhtml.TextNode &&
				refSection.MatchString(next.Data):
				sec := refSection.FindStringSubmatch(next.Data)
				a := el("a")
				a.Attr = []xhtml.Attribute{{Key: "href", Val: ManRef(c.FirstChild.Data, sec[1])}, {Key: "class", Val: "xref"}}
				n.InsertBefore(a, c)
				n.RemoveChild(c)
				a.AppendChild(c)
				a.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: sec[0]})
				next.Data = next.Data[len(sec[0]):]
			case c.Type == xhtml.TextNode:
				linkText(n, c)
			default:
				walk(c)
			}
			c = next
		}
	}
	walk(ctx)
	var b strings.Builder
	for c := ctx.FirstChild; c != nil; c = c.NextSibling {
		_ = xhtml.Render(&b, c)
	}
	return b.String()
}

// linkText splits a text node around the references in it.
func linkText(parent, t *xhtml.Node) {
	ms := crossRef.FindAllStringSubmatchIndex(t.Data, -1)
	if ms == nil {
		return
	}
	s := t.Data
	at := 0
	for _, m := range ms {
		start := m[4] // the name, after the character before it
		if start > at {
			parent.InsertBefore(&xhtml.Node{Type: xhtml.TextNode, Data: s[at:start]}, t)
		}
		name, sec := s[m[4]:m[5]], s[m[6]:m[7]]
		a := el("a")
		a.Attr = []xhtml.Attribute{{Key: "href", Val: ManRef(name, sec)}, {Key: "class", Val: "xref"}}
		a.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: s[start:m[1]]})
		parent.InsertBefore(a, t)
		at = m[1]
	}
	t.Data = s[at:]
	if t.Data == "" {
		parent.RemoveChild(t)
	}
}

// --- the page as a file ------------------------------------------------------------

// HTML is the page as a file of its own, what `go generate` writes to
// /usr/share/man/html/<name>.<section>.html. generator names what made it,
// such as "groff 1.24.1", for a test to know it can make it again.
func (m *ManPage) HTML(generator string) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html><head><meta charset=\"utf-8\">")
	if generator != "" {
		b.WriteString(`<meta name="generator" content="` + html.EscapeString(generator) + `">`)
	}
	b.WriteString("<title>" + html.EscapeString(m.Title()) + "</title></head>\n<body>\n")
	fmt.Fprintf(&b, "<article class=\"man\" data-name=\"%s\" data-section=\"%s\">\n", html.EscapeString(m.Name), html.EscapeString(m.Section))
	for _, s := range m.Sections {
		b.WriteString("<section>\n<h2>" + html.EscapeString(s.Title) + "</h2>\n")
		b.WriteString(strings.ReplaceAll(s.HTML, "</p><", "</p>\n<"))
		b.WriteString("\n</section>\n")
	}
	b.WriteString("</article>\n</body></html>\n")
	return b.String()
}

// ReadMan reads a page that HTML wrote.
func ReadMan(src []byte) (*ManPage, error) {
	root, err := xhtml.Parse(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	art := find(root, func(n *xhtml.Node) bool { return n.DataAtom == atom.Article && hasClass(n, "man") })
	if art == nil {
		return nil, fmt.Errorf("not a manual page")
	}
	m := &ManPage{Name: attr(art, "data-name"), Section: attr(art, "data-section")}
	for s := art.FirstChild; s != nil; s = s.NextSibling {
		if s.DataAtom != atom.Section {
			continue
		}
		var sec ManSection
		var body []*xhtml.Node
		for c := s.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.H2 && sec.Title == "" {
				sec.Title = strings.TrimSpace(text(c))
				continue
			}
			if c.Type == xhtml.TextNode && strings.TrimSpace(c.Data) == "" {
				continue
			}
			body = append(body, c)
		}
		sec.HTML = render(body)
		m.Sections = append(m.Sections, sec)
	}
	return m, nil
}

// Generator is the generator a page file names, "" if none.
func Generator(src []byte) string {
	root, err := xhtml.Parse(bytes.NewReader(src))
	if err != nil {
		return ""
	}
	meta := find(root, func(n *xhtml.Node) bool { return n.DataAtom == atom.Meta && attr(n, "name") == "generator" })
	if meta == nil {
		return ""
	}
	return attr(meta, "content")
}

// --- blocks for a viewer ------------------------------------------------------------

// ManBlock is a block of a page for a viewer: part of a section, the first
// part with its heading.
type ManBlock struct {
	Block
	Section int  // the section's index
	First   bool // the section's first block, with its heading
}

// ManCSS styles a page's blocks: the sections' headings, definition lists
// for options, the synopsis in monospace, references as links.
const ManCSS = `
main.man { padding: 0 calc(2 * var(--doc-col)) var(--doc-row); }
main.man h2 { margin: 0; font: 650 12px/var(--doc-row) var(--doc-sans); letter-spacing: .1em; color: var(--doc-accent); }
main.man h3 { margin: var(--doc-row) 0 0; font: 650 14px/var(--doc-row) var(--doc-sans); color: var(--doc-ink); }
main.man > p, main.man > dl, main.man > pre, main.man > table, main.man > div, main.man > ul, main.man > ol { margin: var(--doc-row) 0 0; }
main.man > h2 + * { margin-top: calc(var(--doc-row) / 2); }
main.man > .cont { margin-top: 0; }
main.man .i1 { margin-left: calc(4 * var(--doc-col)); }
main.man .i2 { margin-left: calc(8 * var(--doc-col)); }
main.man .i3 { margin-left: calc(12 * var(--doc-col)); }
main.man dl { margin: 0; }
main.man dt { margin: calc(var(--doc-row) / 2) 0 0; }
main.man dt:first-child { margin-top: 0; }
main.man dd { margin: 0 0 0 calc(5 * var(--doc-col)); }
main.man dd > p { margin: 0; }
main.man dd > p + p { margin-top: calc(var(--doc-row) / 2); }
main.man b { font-weight: 650; }
main.man i { font-style: italic; color: #c8c3ff; }
main.man dt, main.man.synopsis p, main.man pre { font-family: var(--doc-mono); font-size: 1rem; }
main.man a.xref { color: var(--doc-accent); text-decoration: underline; text-underline-offset: 2px; cursor: pointer; }
main.man a.xref:hover { color: var(--doc-ink); background: rgba(157, 144, 255, .14); border-radius: 3px; }
main.man a.xref b, main.man a.xref i { color: inherit; }
`

// ManTarget is how many rows a viewer's block aims for: small enough that
// scrolling past one costs little.
const ManTarget = 60

// Blocks splits the page into blocks of about target rows at o.Cols: a
// section each, a long one cut between its paragraphs and between the
// entries of its definition lists.
func (m *ManPage) Blocks(o Options, target int) []ManBlock {
	if target <= 0 {
		target = ManTarget
	}
	cols := o.cols() - 4
	var out []ManBlock
	for si, s := range m.Sections {
		ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
		nodes, _ := xhtml.ParseFragment(strings.NewReader(s.HTML), ctx)
		// Units: each top-level node, and each entry of a <dl>.
		type unit struct {
			html string
			rows int
			dl   bool
		}
		var units []unit
		for _, n := range nodes {
			if n.DataAtom == atom.Dl {
				var entry []*xhtml.Node
				rows := 0
				emit := func() {
					if len(entry) > 0 {
						units = append(units, unit{render(entry), rows, true})
					}
					entry, rows = nil, 0
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.DataAtom == atom.Dt && len(entry) > 0 && entry[len(entry)-1].DataAtom == atom.Dd {
						emit()
					}
					entry = append(entry, c)
					rows += estimate(c, cols-5)
				}
				emit()
				continue
			}
			units = append(units, unit{render([]*xhtml.Node{n}), estimate(n, cols) + 1, false})
		}
		heading := "<h2>" + html.EscapeString(s.Title) + "</h2>"
		var cur strings.Builder
		rows, first, inDL := 1, true, false
		cur.WriteString(heading)
		emit := func() {
			if inDL {
				cur.WriteString("</dl>")
				inDL = false
			}
			out = append(out, ManBlock{Block: Block{HTML: cur.String(), Rows: rows}, Section: si, First: first})
			cur.Reset()
			rows, first = 0, false
		}
		for _, u := range units {
			if rows+u.rows > target && (rows > 1 || !first) {
				emit()
			}
			if u.dl && !inDL {
				cur.WriteString("<dl>")
				inDL = true
			} else if !u.dl && inDL {
				cur.WriteString("</dl>")
				inDL = false
			}
			cur.WriteString(u.html)
			rows += u.rows
		}
		if cur.Len() > 0 || first {
			emit()
		}
	}
	return out
}

// Text is the page as paragraphs of text, for a terminal that is not a
// host or an output that is not the terminal.
func (m *ManPage) Text() []Para {
	var b strings.Builder
	for _, s := range m.Sections {
		b.WriteString("<h2>" + html.EscapeString(s.Title) + "</h2>" + s.HTML)
	}
	return Paragraphs([]byte("<body>" + b.String() + "</body>"))
}

// --- helpers ----------------------------------------------------------------------

func el(tag string) *xhtml.Node {
	return &xhtml.Node{Type: xhtml.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
}

func clone(n *xhtml.Node) *xhtml.Node {
	c := shallow(n)
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		c.AppendChild(clone(k))
	}
	return c
}

func shallow(n *xhtml.Node) *xhtml.Node {
	return &xhtml.Node{Type: n.Type, Data: n.Data, DataAtom: n.DataAtom, Namespace: n.Namespace, Attr: append([]xhtml.Attribute(nil), n.Attr...)}
}

func find(n *xhtml.Node, f func(*xhtml.Node) bool) *xhtml.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.ElementNode && f(c) {
			return c
		}
		if r := find(c, f); r != nil {
			return r
		}
	}
	return nil
}

func hasClass(n *xhtml.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func render(nodes []*xhtml.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		_ = xhtml.Render(&b, n)
	}
	return b.String()
}
