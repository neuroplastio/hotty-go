package doc

import (
	"bytes"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	gtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Markdown is a Markdown file (GitHub's flavour: tables, task lists,
// strikethrough, autolinks) as a document, a block per top-level block.
//
//   - Fenced code is a monospace block, highlighted by Options.Highlight
//     where it knows the language.
//   - A local image is read through ReadFile and sent as a resource; one
//     that cannot be read is its alt text. An image on the web is a
//     hyperlink to it: a surface fetches nothing (SPEC §7.2).
//   - Links to the web are hyperlinks; any other link is its text.
//   - Raw HTML is kept, made inert (Inert).
//   - A quote that starts with GitHub's [!NOTE] or [!WARNING] is a callout.
func Markdown(src []byte, o Options) *Doc {
	r := &mdRenderer{o: &o}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(ghtml.WithUnsafe(),
			renderer.WithNodeRenderers(util.Prioritized(r, 100))),
	)
	root := md.Parser().Parse(gtext.NewReader(src))
	prepare(root, src)
	d := &Doc{}
	cols := o.cols()
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		var b bytes.Buffer
		r.res = nil
		if err := md.Renderer().Render(&b, src, n); err != nil {
			continue
		}
		d.Blocks = append(d.Blocks, Block{HTML: Inert(b.String()), Rows: mdRows(n, src, cols) + 1, Res: r.res})
	}
	return d
}

var calloutMark = regexp.MustCompile(`^\[!([A-Za-z]+)\]\s*$`)

// prepare marks what the renderer draws differently: task list items, and
// callouts (their marker taken out of the text).
func prepare(root ast.Node, src []byte) {
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.ListItem:
			if c := n.FirstChild(); c != nil {
				if box, ok := c.FirstChild().(*east.TaskCheckBox); ok {
					class := "task"
					if box.IsChecked {
						class += " done"
					}
					n.SetAttributeString("class", []byte(class))
				}
			}
		case *ast.Blockquote:
			p, ok := n.FirstChild().(*ast.Paragraph)
			if !ok {
				break
			}
			var lead strings.Builder
			var nodes []ast.Node
			for c := p.FirstChild(); c != nil; c = c.NextSibling() {
				t, ok := c.(*ast.Text)
				if !ok {
					break
				}
				lead.Write(t.Segment.Value(src))
				nodes = append(nodes, c)
				if t.SoftLineBreak() || t.HardLineBreak() {
					break
				}
			}
			if m := calloutMark.FindStringSubmatch(lead.String()); m != nil {
				for _, c := range nodes {
					p.RemoveChild(p, c)
				}
				n.SetAttributeString("class", []byte(strings.ToLower(m[1])))
				n.SetAttributeString("data-callout", []byte(strings.ToUpper(m[1])))
			}
		}
		return ast.WalkContinue, nil
	})
}

// mdRenderer draws the nodes the documents draw their own way; goldmark's
// HTML renderer draws the rest.
type mdRenderer struct {
	o   *Options
	res []Resource // the block's resources, as it is rendered
}

func (r *mdRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, r.link)
	reg.Register(ast.KindAutoLink, r.autoLink)
	reg.Register(ast.KindImage, r.image)
	reg.Register(ast.KindFencedCodeBlock, r.code)
	reg.Register(ast.KindCodeBlock, r.code)
	reg.Register(east.KindTaskCheckBox, r.task)
	reg.Register(ast.KindBlockquote, r.quote)
}

func (r *mdRenderer) link(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	dest := string(n.Destination)
	if !Web(dest) {
		return ast.WalkContinue, nil // its text
	}
	if entering {
		_, _ = w.WriteString(`<a href="` + html.EscapeString(dest) + `" target="_blank" rel="noopener noreferrer"`)
		if len(n.Title) > 0 {
			_, _ = w.WriteString(` title="` + html.EscapeString(string(n.Title)) + `"`)
		}
		_, _ = w.WriteString(">")
	} else {
		_, _ = w.WriteString("</a>")
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) autoLink(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.AutoLink)
	u := string(n.URL(src))
	label := html.EscapeString(string(n.Label(src)))
	if n.AutoLinkType == ast.AutoLinkURL && !Web(u) && strings.HasPrefix(strings.ToLower(u), "www.") {
		u = "https://" + u
	}
	if n.AutoLinkType == ast.AutoLinkURL && Web(u) {
		_, _ = w.WriteString(`<a href="` + html.EscapeString(u) + `" target="_blank" rel="noopener noreferrer">` + label + `</a>`)
	} else {
		_, _ = w.WriteString(label)
	}
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) image(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	dest := string(n.Destination)
	alt := html.EscapeString(plain(n, src))
	switch {
	case strings.HasPrefix(strings.ToLower(dest), "data:image/"):
		_, _ = w.WriteString(`<img src="` + html.EscapeString(dest) + `" alt="` + alt + `">`)
	case Web(dest):
		label := alt
		if label == "" {
			label = "image"
		}
		_, _ = w.WriteString(`<a class="alt" href="` + html.EscapeString(dest) + `" target="_blank" rel="noopener noreferrer">[` + label + `]</a>`)
	default:
		if s, res, ok := r.o.image(dest); ok {
			r.res = append(r.res, res)
			_, _ = w.WriteString(`<img src="` + s + `" alt="` + alt + `">`)
		} else if alt != "" {
			_, _ = w.WriteString(`<span class="alt">[` + alt + `]</span>`)
		}
	}
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) code(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lang := ""
	if f, ok := node.(*ast.FencedCodeBlock); ok {
		lang = string(f.Language(src))
	}
	code := strings.TrimRight(lines(node, src), "\n")
	_, _ = w.WriteString(`<pre class="code"`)
	if lang != "" {
		_, _ = w.WriteString(` data-lang="` + html.EscapeString(lang) + `"`)
	}
	_, _ = w.WriteString("><code>")
	if h, ok := r.highlight(lang, code); ok {
		_, _ = w.WriteString(h)
	} else {
		_, _ = w.WriteString(html.EscapeString(code))
	}
	_, _ = w.WriteString("</code></pre>")
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) highlight(lang, code string) (string, bool) {
	if r.o.Highlight == nil || lang == "" {
		return "", false
	}
	return r.o.Highlight(lang, code)
}

func (r *mdRenderer) task(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if node.(*east.TaskCheckBox).IsChecked {
			_, _ = w.WriteString(`<span class="box">✓</span>`)
		} else {
			_, _ = w.WriteString(`<span class="box">☐</span>`)
		}
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) quote(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</blockquote>\n")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString("<blockquote")
	if c, ok := node.AttributeString("class"); ok {
		_, _ = w.WriteString(` class="` + html.EscapeString(string(c.([]byte))) + `"`)
	}
	_, _ = w.WriteString(">\n")
	if t, ok := node.AttributeString("data-callout"); ok {
		_, _ = w.WriteString(`<span class="callout">` + html.EscapeString(string(t.([]byte))) + `</span>`)
	}
	return ast.WalkContinue, nil
}

// lines is a block's source lines.
func lines(n ast.Node, src []byte) string {
	var b strings.Builder
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		b.Write(seg.Value(src))
	}
	return b.String()
}

// plain is a node's text without markup.
func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Segment.Value(src))
			if c.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(c.Value)
		case *ast.AutoLink:
			b.Write(c.Label(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// mdRows estimates the rows a Markdown block takes cols cells wide.
func mdRows(n ast.Node, src []byte, cols int) int {
	switch n := n.(type) {
	case *ast.Heading:
		if n.Level <= 2 {
			return 2 + textRows(len(plain(n, src)), cols/2) - 1
		}
		return textRows(len(plain(n, src)), cols)
	case *ast.Paragraph, *ast.TextBlock:
		rows := textRows(len([]rune(plain(n, src))), cols)
		_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
			if _, ok := c.(*ast.Image); ok && entering {
				rows += MaxImageRows
			}
			return ast.WalkContinue, nil
		})
		return rows
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return monoRows(strings.Split(strings.TrimRight(lines(n, src), "\n"), "\n"), max(10, cols-4)) + 1
	case *ast.HTMLBlock:
		raw := lines(n, src)
		return strings.Count(raw, "\n") + textRows(len(raw)/2, cols)
	case *ast.ThematicBreak:
		return 1
	case *east.Table:
		rows := 0
		for r := n.FirstChild(); r != nil; r = r.NextSibling() {
			cells := max(1, r.ChildCount())
			h := 1
			for c := r.FirstChild(); c != nil; c = c.NextSibling() {
				h = max(h, textRows(len(plain(c, src)), max(4, cols/cells-2)))
			}
			rows += h
		}
		return rows
	}
	rows := 0
	inner := max(10, cols-3)
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		rows += mdRows(c, src, inner)
		if _, ok := n.(*ast.Blockquote); ok && c.NextSibling() != nil {
			rows++
		}
	}
	return max(1, rows)
}
