package hottyvt

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// A scope rewrites what a program sent one of its surfaces, its markup and
// its styles, to live in the screen's document beside the rows and the
// other surfaces: element ids prefixed with the surface's (ids), so the
// screen's surface takes deltas to them and two surfaces may use one id;
// resources named with the screen's prefix (res), so they do not meet the
// resources of the program showing the screen; and every selector scoped
// to the surface's element (root), with what a document says of its root
// (html, body, :root) said of that element.
//
// A stylesheet sent as a resource is shared by every surface that links
// it: its scope has no ids, and its root is any surface's element.
type scope struct {
	ids  string   // "vt-d1-": an element's id is ids+id; "" in a shared stylesheet
	res  string   // "vt-": a resource's id is res+id
	root string   // the selector of the document's root: "#vt-d1", or "#vt :where(.vt-d)"
	base *url.URL // the document's base URL, for its hyperlinks (SPEC §7.3)
}

// --- markup -----------------------------------------------------------------

// Attributes whose value is an element's id, and those whose value is a
// list of them.
var (
	idRef = map[string]bool{"for": true, "form": true, "list": true, "popovertarget": true,
		"commandfor": true, "anchor": true, "aria-activedescendant": true}
	idRefs = map[string]bool{"headers": true, "aria-labelledby": true, "aria-describedby": true,
		"aria-controls": true, "aria-owns": true, "aria-details": true, "aria-flowto": true,
		"aria-errormessage": true}
)

// element rewrites an element and what is in it, in place.
func (sc *scope) element(n *html.Node) {
	if n.Type != html.ElementNode {
		return
	}
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if v, ok := sc.attr(n, a.Namespace, a.Key, a.Val); ok {
			a.Val = v
			kept = append(kept, a)
		}
	}
	n.Attr = kept
	if n.DataAtom == atom.Style || n.Data == "style" {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				c.Data = sc.sheet(c.Data)
			}
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sc.element(c)
	}
}

// attr is an attribute's value as the screen's document has it, and false
// for one it leaves out: data-on and autofocus, since a surface played in a
// screen reports nothing and takes no keyboard (as a detached one, SPEC
// §5.5), and a link's href unless the link is a hyperlink, which belongs to
// the terminal (SPEC §9). Any other link would report its clicks to the
// program showing the screen, as its own.
func (sc *scope) attr(n *html.Node, ns, key, val string) (string, bool) {
	switch {
	case key == "data-on" || key == "autofocus":
		return "", false
	case key == "id":
		return sc.ids + val, true
	case idRef[key]:
		return sc.ids + strings.TrimSpace(val), true
	case idRefs[key]:
		f := strings.Fields(val)
		for i := range f {
			f[i] = sc.ids + f[i]
		}
		return strings.Join(f, " "), true
	case key == "style":
		return sc.values(val), true
	case key == "href" && link(n):
		return sc.hyperlink(n, val)
	case key == "href" || key == "src" || key == "poster" || key == "data":
		return sc.url(strings.TrimSpace(val)), true
	case key == "srcset":
		return sc.srcset(val), true
	case ns == "" && strings.Contains(strings.ToLower(val), "url("):
		return sc.values(val), true // SVG's fill="url(#g)" and its kind
	}
	return val, true
}

// link reports whether an element is a link: an HTML a or area, or an SVG a.
func link(n *html.Node) bool {
	return n.DataAtom == atom.A || n.DataAtom == atom.Area || n.Data == "a"
}

// hyperlink is a link's href when the link is a hyperlink (target=_blank,
// to an http or https URL, resolved against the document's base): the URL
// resolved, as the host would open it.
func (sc *scope) hyperlink(n *html.Node, href string) (string, bool) {
	blank := false
	for _, a := range n.Attr {
		if a.Key == "target" && a.Val == "_blank" {
			blank = true
		}
	}
	if !blank {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", false
	}
	if sc.base != nil {
		u = sc.base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	return u.String(), true
}

// url is a URL as the screen's document refers to it: a resource by its
// name there, an element of the document by its id there, any other as it
// is.
func (sc *scope) url(u string) string {
	switch {
	case hasPrefixFold(u, "cid:"):
		return "cid:" + sc.res + u[len("cid:"):]
	case strings.HasPrefix(u, "#") && sc.ids != "":
		return "#" + sc.ids + u[1:]
	}
	return u
}

// srcset rewrites the URLs of a srcset: candidates separated by commas,
// each a URL and an optional descriptor.
func (sc *scope) srcset(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		f := strings.Fields(p)
		if len(f) > 0 {
			f[0] = sc.url(f[0])
			parts[i] = strings.Join(f, " ")
		}
	}
	return strings.Join(parts, ", ")
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// --- styles -----------------------------------------------------------------

// sheet rewrites a stylesheet: its rules scoped, and the URLs in it.
func (sc *scope) sheet(css string) string {
	var b strings.Builder
	sc.rules(css, &b)
	return b.String()
}

// rules rewrites a list of rules: a stylesheet's, or a conditional group's
// (@media, @supports, @container, @layer).
func (sc *scope) rules(css string, b *strings.Builder) {
	i := 0
	for i < len(css) {
		i = skipSpace(css, i)
		if i >= len(css) {
			return
		}
		if css[i] == '}' { // stray
			i++
			continue
		}
		end := scanTo(css, i, "{;")
		prelude := strings.TrimSpace(css[i:end])
		if end >= len(css) || css[end] == ';' {
			// A statement: @import, @charset, @layer a, b; or a rule cut
			// short, which a browser drops.
			if strings.HasPrefix(prelude, "@") {
				b.WriteString(sc.values(prelude) + ";")
			}
			i = end + 1
			continue
		}
		close := matching(css, end)
		body := css[end+1 : close]
		i = close + 1
		if !strings.HasPrefix(prelude, "@") {
			b.WriteString(sc.selectors(prelude) + "{" + sc.block(body) + "}")
			continue
		}
		name := strings.ToLower(atName(prelude))
		switch name {
		case "media", "supports", "container", "layer", "scope", "starting-style", "document", "-moz-document":
			b.WriteString(prelude + "{")
			sc.rules(body, b)
			b.WriteString("}")
		default: // @font-face, @keyframes, @page, @property, …: no selectors of the document's
			b.WriteString(prelude + "{" + sc.values(body) + "}")
		}
	}
}

// block rewrites a rule's block: declarations, and the rules nested in it
// (CSS nesting), whose selectors are relative to the rule's and so are
// scoped already, but whose ids are the document's.
func (sc *scope) block(css string) string {
	var b strings.Builder
	i := 0
	for i < len(css) {
		end := scanTo(css, i, "{;}")
		if end < len(css) && css[end] == '{' {
			close := matching(css, end)
			b.WriteString(sc.idsIn(strings.TrimSpace(css[i:end])) + "{" + sc.block(css[end+1:close]) + "}")
			i = close + 1
			continue
		}
		b.WriteString(sc.values(css[i:min(end, len(css))]))
		if end < len(css) {
			b.WriteByte(css[end])
		}
		i = end + 1
	}
	return b.String()
}

// values rewrites the URLs in declarations or a value: url(…), and a
// string that is a resource's URL (@import "cid:…", image-set("cid:…")).
func (sc *scope) values(css string) string {
	if !strings.Contains(css, "url(") && !strings.Contains(css, "URL(") && !containsFold(css, "cid:") {
		return css
	}
	var b strings.Builder
	for i := 0; i < len(css); {
		c := css[i]
		switch {
		case c == '"' || c == '\'':
			end := skipString(css, i)
			s := css[i+1 : max(i+1, end-1)]
			if hasPrefixFold(s, "cid:") {
				b.WriteString(string(c) + sc.url(s) + string(c))
			} else {
				b.WriteString(css[i:end])
			}
			i = end
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			i = skipComment(css, i)
		case (c == 'u' || c == 'U') && hasPrefixFold(css[i:], "url(") && !identByte(prev(css, i)):
			close := scanTo(css, i+4, ")")
			arg := strings.TrimSpace(css[i+4 : min(close, len(css))])
			q := ""
			if len(arg) >= 2 && (arg[0] == '"' || arg[0] == '\'') && arg[len(arg)-1] == arg[0] {
				q, arg = arg[:1], arg[1:len(arg)-1]
			}
			b.WriteString("url(" + q + sc.url(arg) + q + ")")
			i = close + 1
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func containsFold(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }

func prev(s string, i int) byte {
	if i == 0 {
		return ' '
	}
	return s[i-1]
}

// selectors scopes a selector list to the document's root.
func (sc *scope) selectors(list string) string {
	var out []string
	for _, sel := range splitTop(list, ',') {
		if sel = strings.TrimSpace(sel); sel != "" {
			out = append(out, sc.complex(sc.idsIn(sel)))
		}
	}
	return strings.Join(out, ",")
}

// complex scopes one complex selector: its leading compounds that name the
// document's root (html, body, :root) become the root's own selector, and
// any other selector looks inside the root.
func (sc *scope) complex(sel string) string {
	parts := compounds(sel)
	var rest []string // the root's other simple selectors (html.dark: .dark)
	k := 0
	for k < len(parts) {
		r, ok := rootish(parts[k].compound)
		if !ok {
			break
		}
		rest = append(rest, r)
		k++
	}
	if k == 0 {
		return sc.root + " " + sel
	}
	var b strings.Builder
	b.WriteString(sc.root + strings.Join(rest, ""))
	for _, p := range parts[k:] {
		b.WriteString(p.comb + p.compound)
	}
	return b.String()
}

// rootish reports whether a compound selector names the document's root:
// its type selector is html or body, or it has :root. rest is what it says
// besides.
func rootish(c string) (rest string, ok bool) {
	i := 0
	for i < len(c) && identByte(c[i]) {
		i++
	}
	tag := strings.ToLower(c[:i])
	if tag == "html" || tag == "body" {
		rest, ok = c[i:], true
	} else {
		rest = c
	}
	for {
		j := strings.Index(strings.ToLower(rest), ":root")
		if j < 0 || (j+5 < len(rest) && identByte(rest[j+5])) || (j > 0 && rest[j-1] == ':') {
			break
		}
		rest, ok = rest[:j]+rest[j+5:], true
	}
	return rest, ok
}

type part struct{ comb, compound string }

// compounds splits a complex selector into its compound selectors, each
// with the combinator before it ("" for the first, " " for a descendant).
func compounds(sel string) []part {
	var parts []part
	comb, start, i := "", 0, 0
	flush := func(end int) {
		if end > start {
			parts = append(parts, part{comb, sel[start:end]})
		}
	}
	for i < len(sel) {
		switch c := sel[i]; c {
		case '\\':
			i += 2
			continue
		case '"', '\'':
			i = skipString(sel, i)
			continue
		case '(', '[':
			i = matching(sel, i) + 1
			continue
		case ' ', '\t', '\n', '\r', '\f', '>', '+', '~':
			flush(i)
			j := i
			combs := ""
			for j < len(sel) && strings.IndexByte(" \t\n\r\f>+~", sel[j]) >= 0 {
				if sel[j] != ' ' && sel[j] != '\t' && sel[j] != '\n' && sel[j] != '\r' && sel[j] != '\f' {
					combs += string(sel[j])
				}
				j++
			}
			if combs == "" {
				comb = " "
			} else {
				comb = " " + combs + " "
			}
			if len(parts) == 0 && start == i {
				comb = "" // a combinator with nothing before it: keep it on the next
				if combs != "" {
					comb = combs + " "
				}
			}
			i, start = j, j
			continue
		}
		i++
	}
	flush(len(sel))
	return parts
}

// idsIn prefixes the id selectors in a selector with the document's (a
// shared stylesheet, whose surfaces' prefixes differ, matches the id's end).
func (sc *scope) idsIn(sel string) string {
	if !strings.Contains(sel, "#") {
		return sel
	}
	var b strings.Builder
	for i := 0; i < len(sel); {
		switch c := sel[i]; {
		case c == '\\':
			end := min(i+2, len(sel))
			b.WriteString(sel[i:end])
			i = end
		case c == '"' || c == '\'':
			end := skipString(sel, i)
			b.WriteString(sel[i:end])
			i = end
		case c == '[':
			end := matching(sel, i) + 1
			b.WriteString(sel[i:min(end, len(sel))])
			i = end
		case c == '#' && i+1 < len(sel) && (identByte(sel[i+1]) || sel[i+1] == '\\'):
			j := i + 1
			for j < len(sel) && (identByte(sel[j]) || sel[j] == '\\') {
				if sel[j] == '\\' {
					j++
				}
				j++
			}
			id := sel[i+1 : min(j, len(sel))]
			if sc.ids != "" {
				b.WriteString("#" + sc.ids + id)
			} else {
				b.WriteString(`[id$="-` + id + `"]`)
			}
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// --- scanning CSS -----------------------------------------------------------

// identByte reports whether c may be in a CSS identifier (or is not ASCII,
// which may).
func identByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c >= 0x80
}

// skipSpace skips whitespace and comments from i.
func skipSpace(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f':
			i++
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipComment(s, i)
		default:
			return i
		}
	}
	return i
}

// skipComment returns the index after the comment that starts at i.
func skipComment(s string, i int) int {
	if end := strings.Index(s[i+2:], "*/"); end >= 0 {
		return i + 2 + end + 2
	}
	return len(s)
}

// skipString returns the index after the string that starts at i.
func skipString(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case q, '\n':
			return j + 1
		}
	}
	return len(s)
}

// scanTo returns the index of the first of stops at nesting depth 0 from i,
// outside strings and comments, or len(s).
func scanTo(s string, i int, stops string) int {
	for i < len(s) {
		c := s[i]
		switch {
		case strings.IndexByte(stops, c) >= 0:
			return i
		case c == '\\':
			i += 2
		case c == '"' || c == '\'':
			i = skipString(s, i)
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipComment(s, i)
		case c == '(' || c == '[' || c == '{':
			i = matching(s, i) + 1
		default:
			i++
		}
	}
	return len(s)
}

// matching returns the index of the bracket that closes the one at i, or
// len(s) when it is not closed.
func matching(s string, i int) int {
	open := s[i]
	close := map[byte]byte{'(': ')', '[': ']', '{': '}'}[open]
	depth := 0
	for j := i; j < len(s); j++ {
		switch c := s[j]; {
		case c == '\\':
			j++
		case c == '"' || c == '\'':
			j = skipString(s, j) - 1
		case c == '/' && j+1 < len(s) && s[j+1] == '*':
			j = skipComment(s, j) - 1
		case c == open:
			depth++
		case c == close:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return len(s)
}

// splitTop splits s at sep where it is at nesting depth 0.
func splitTop(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		j := scanTo(s, i, string(sep))
		if j >= len(s) {
			break
		}
		out = append(out, s[start:j])
		start, i = j+1, j+1
	}
	return append(out, s[start:])
}

// atName is an at-rule's name: "media" for "@media screen".
func atName(prelude string) string {
	i := 1
	for i < len(prelude) && identByte(prelude[i]) {
		i++
	}
	return prelude[1:i]
}
