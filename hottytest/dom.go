package hottytest

import (
	"slices"
	"strings"
	"sync"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyedit"
)

// Surface is one surface the program made: its document, as the program's
// commands left it, and its placement.
type Surface struct {
	mu       *sync.Mutex // the host's: its accessors are safe while the program writes
	name     string
	doc      *html.Node
	detached bool
	placed   bool
	place    hotty.Placement
	fitRows  int     // with place.Fit: the rows the program last heard (SPEC §5.2)
	heard    *string // with place.Hover: the element it last heard the pointer is over; nil: out (SPEC §9.4)
	col, row int     // where the placement's top-left cell is on the screen
	alt      bool    // placed on the alternate screen
	created  int     // the order of creation, for stacking

	// The controls' state, which the user changes and the program's
	// attributes set (SPEC §6.2): a control's value, a box's checked.
	values  map[*html.Node]string
	checked map[*html.Node]bool
	edited  map[*html.Node]bool             // text controls edited since their last commit
	fields  map[*html.Node]*hottyedit.Field // text fields' carets, once used
	focused *html.Node
	keyb    bool // the surface has the keyboard
}

func newSurface(mu *sync.Mutex, name, markup string, created int) *Surface {
	s := &Surface{mu: mu, name: name, created: created}
	s.setDoc(markup)
	return s
}

func (s *Surface) setDoc(markup string) {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		doc, _ = html.Parse(strings.NewReader(""))
	}
	s.doc = doc
	s.values = map[*html.Node]string{}
	s.checked = map[*html.Node]bool{}
	s.edited = map[*html.Node]bool{}
	s.fields = map[*html.Node]*hottyedit.Field{}
	s.focused, s.keyb = nil, false
}

// dirty are the text controls edited since their last commit.
func (s *Surface) dirty() map[*html.Node]bool { return s.edited }

// Name is the surface's name.
func (s *Surface) Name() string { return s.name }

// Detached reports whether the surface is detached (SPEC §5.5).
func (s *Surface) Detached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detached
}

// Placed reports whether the surface is placed: on screen, not hidden.
func (s *Surface) Placed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.placed
}

// Placement is the surface's last placement, as the program sent it; Rows
// is the rows the host chose when the program asked for auto.
func (s *Surface) Placement() hotty.Placement {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.place
}

// At is the cell of the placement's top-left corner: the cursor's, when
// the program placed it. row is a line of Screen, the scrollback's
// included, of the screen it was placed on.
func (s *Surface) At() (col, row int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.col, s.row
}

// HTML is the document as it is now.
func (s *Surface) HTML() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	_ = html.Render(&b, s.doc)
	return b.String()
}

// Text is the text of the document's body as a reader sees it, roughly:
// block elements apart, runs of whitespace one space, styles and scripts
// left out.
func (s *Surface) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	body := find(s.doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
	if body == nil {
		return ""
	}
	var b strings.Builder
	var visit func(n *html.Node)
	visit = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			b.WriteString(n.Data)
			return
		case n.Type == html.ElementNode && (n.DataAtom == atom.Style || n.DataAtom == atom.Script || n.DataAtom == atom.Template):
			return
		}
		apart := n.Type == html.ElementNode && blocks[n.DataAtom]
		if apart {
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
		if apart {
			b.WriteByte(' ')
		}
	}
	visit(body)
	return strings.Join(strings.Fields(b.String()), " ")
}

// blocks are the elements a reader sees apart from their neighbours.
var blocks = map[atom.Atom]bool{
	atom.Address: true, atom.Article: true, atom.Aside: true, atom.Blockquote: true, atom.Br: true,
	atom.Button: true, atom.Caption: true, atom.Dd: true, atom.Details: true, atom.Dialog: true,
	atom.Div: true, atom.Dl: true, atom.Dt: true, atom.Fieldset: true, atom.Figcaption: true,
	atom.Figure: true, atom.Footer: true, atom.Form: true, atom.H1: true, atom.H2: true, atom.H3: true,
	atom.H4: true, atom.H5: true, atom.H6: true, atom.Header: true, atom.Hr: true, atom.Legend: true,
	atom.Li: true, atom.Main: true, atom.Nav: true, atom.Ol: true, atom.Option: true, atom.P: true,
	atom.Pre: true, atom.Section: true, atom.Summary: true, atom.Table: true, atom.Tbody: true,
	atom.Td: true, atom.Tfoot: true, atom.Th: true, atom.Thead: true, atom.Tr: true, atom.Ul: true,
}

// Element is an element as a host inspects it (SPEC §16): its tag, its
// attributes with the program's values, its text, and its child elements.
type Element struct {
	Tag      string
	Attrs    map[string]string
	Text     string
	Children []Child
	node     *html.Node
}

// Child is a child element: its tag, its id ("" when it has none, and
// HasID false), and its text.
type Child struct {
	Tag   string
	ID    string
	HasID bool
	Text  string
}

// Element finds the element with an id. ok is false when there is none.
func (s *Surface) Element(id string) (e Element, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.byID(id)
	if n == nil {
		return Element{}, false
	}
	e = Element{Tag: n.Data, Attrs: map[string]string{}, Text: textOf(n), node: n}
	for _, a := range n.Attr {
		e.Attrs[a.Key] = a.Val
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			cid, has := attr(c, "id")
			e.Children = append(e.Children, Child{Tag: c.Data, ID: cid, HasID: has, Text: textOf(c)})
		}
	}
	return e, true
}

// TextOf is the text of the element with an id, "" when there is none.
func (s *Surface) TextOf(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.byID(id); n != nil {
		return textOf(n)
	}
	return ""
}

// Attr is an attribute of the element with an id.
func (s *Surface) Attr(id, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.byID(id)
	if n == nil {
		return "", false
	}
	return attr(n, name)
}

// Var is the custom property --name set on the element with an id, by the
// document's style attribute or a var delta.
func (s *Surface) Var(id, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.byID(id)
	if n == nil {
		return "", false
	}
	style, ok := attr(n, "style")
	if !ok {
		return "", false
	}
	return styleProp(style, varName(name))
}

// Value is a control's current value: what the program set, or the user
// typed (Fill). For a checkbox or radio button, "true" or "false".
func (s *Surface) Value(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.byID(id)
	if n == nil {
		return "", false
	}
	if box(n) {
		if s.isChecked(n) {
			return "true", true
		}
		return "false", true
	}
	return s.valueOf(n), true
}

// Focused is the id of the element that has focus, while the surface has
// the keyboard; "" otherwise.
func (s *Surface) Focused() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.keyb || s.focused == nil {
		return ""
	}
	id, _ := attr(s.focused, "id")
	return id
}

func (s *Surface) byID(id string) *html.Node {
	if id == "" {
		return nil
	}
	return find(s.doc, func(n *html.Node) bool {
		v, ok := attr(n, "id")
		return n.Type == html.ElementNode && ok && v == id
	})
}

// --- controls -----------------------------------------------------------

func box(n *html.Node) bool {
	if n.DataAtom != atom.Input {
		return false
	}
	t, _ := attr(n, "type")
	t = strings.ToLower(t)
	return t == "checkbox" || t == "radio"
}

func control(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Input, atom.Textarea, atom.Select:
		return true
	}
	return false
}

func (s *Surface) valueOf(n *html.Node) string {
	if v, ok := s.values[n]; ok {
		return v
	}
	switch n.DataAtom {
	case atom.Textarea:
		return textOf(n)
	case atom.Select:
		var first, chosen string
		var found bool
		walk(n, func(o *html.Node) {
			if o.DataAtom != atom.Option {
				return
			}
			v, ok := attr(o, "value")
			if !ok {
				v = textOf(o)
			}
			if !found && first == "" {
				first = v
			}
			if _, sel := attr(o, "selected"); sel && !found {
				chosen, found = v, true
			}
		})
		if found {
			return chosen
		}
		return first
	}
	if box(n) {
		if v, ok := attr(n, "value"); ok {
			return v
		}
		return "on"
	}
	v, _ := attr(n, "value")
	return v
}

func (s *Surface) isChecked(n *html.Node) bool {
	if c, ok := s.checked[n]; ok {
		return c
	}
	_, ok := attr(n, "checked")
	return ok
}

// attrChanged keeps a control's state with the program's attribute, unless
// the user is editing it (SPEC §6.2, controls whose values the program
// sets).
func (s *Surface) attrChanged(n *html.Node, key string) {
	if n == s.focused && s.keyb {
		return
	}
	switch {
	case key == "value" && control(n) && !box(n):
		delete(s.values, n)
		delete(s.edited, n)
		delete(s.fields, n)
	case key == "checked" && box(n):
		delete(s.checked, n)
	case key == "selected" && n.DataAtom == atom.Option:
		for p := n.Parent; p != nil; p = p.Parent {
			if p.DataAtom == atom.Select {
				delete(s.values, p)
				break
			}
		}
	}
}

// --- helpers --------------------------------------------------------------

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func delAttr(n *html.Node, key string) {
	n.Attr = slices.DeleteFunc(n.Attr, func(a html.Attribute) bool { return a.Namespace == "" && a.Key == key })
}

func textOf(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	})
	return b.String()
}

func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}

func find(n *html.Node, f func(*html.Node) bool) *html.Node {
	if f(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if m := find(c, f); m != nil {
			return m
		}
	}
	return nil
}

func varName(k string) string {
	if strings.HasPrefix(k, "--") {
		return k
	}
	return "--" + k
}

// styleProp reads a property from a style attribute.
func styleProp(style, name string) (string, bool) {
	for decl := range strings.SplitSeq(style, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// setStyleProp sets a property in a style attribute, keeping the others.
func setStyleProp(style, name, value string) string {
	var out []string
	found := false
	for decl := range strings.SplitSeq(style, ";") {
		k, _, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == name {
			out = append(out, name+": "+value)
			found = true
			continue
		}
		out = append(out, strings.TrimSpace(decl))
	}
	if !found {
		out = append(out, name+": "+value)
	}
	return strings.Join(out, "; ")
}
