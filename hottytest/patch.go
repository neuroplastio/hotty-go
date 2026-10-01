package hottytest

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
)

// patch applies a patch to the surface's document (SPEC §6), and returns
// the error code and detail when it cannot.
func (s *Surface) patch(op hotty.Op, target, key string, payload string) (code, detail string) {
	if op == hotty.OpMorph && target == "" {
		return s.morphByIDs(payload)
	}
	t := s.byID(target)
	if t == nil {
		return hotty.ENOTARGET, target
	}
	switch op {
	case hotty.OpMorph:
		nodes := s.fragment(payload, t.Parent)
		if len(elements(nodes)) != 1 || len(nodes) != 1 {
			s.replaceWith(t, nodes)
			return "", ""
		}
		s.morph(t, nodes[0])
	case hotty.OpInner:
		s.morphChildren(t, s.fragment(payload, t))
	case hotty.OpReplace:
		s.replaceWith(t, s.fragment(payload, t.Parent))
	case hotty.OpAppend, hotty.OpPrepend:
		nodes := s.fragment(payload, t)
		first := t.FirstChild
		for _, n := range nodes {
			if id, ok := attr(n, "id"); ok && n.Type == html.ElementNode {
				if old := childByID(t, id); old != nil {
					s.morph(old, n)
					continue
				}
			}
			if op == hotty.OpAppend || first == nil {
				t.AppendChild(n)
			} else {
				t.InsertBefore(n, first)
			}
		}
	case hotty.OpBefore, hotty.OpAfter:
		if t.Parent == nil {
			return hotty.EINVAL, "no parent"
		}
		ref := t
		if op == hotty.OpAfter {
			ref = t.NextSibling
		}
		for _, n := range s.fragment(payload, t.Parent) {
			t.Parent.InsertBefore(n, ref)
		}
	case hotty.OpRemove:
		s.drop(t)
		if t.Parent != nil {
			t.Parent.RemoveChild(t)
		}
	case hotty.OpAttr:
		setAttr(t, key, payload)
		s.attrChanged(t, key)
	case hotty.OpUnattr:
		delAttr(t, key)
		s.attrChanged(t, key)
	case hotty.OpText:
		for c := t.FirstChild; c != nil; {
			next := c.NextSibling
			s.drop(c)
			t.RemoveChild(c)
			c = next
		}
		t.AppendChild(&html.Node{Type: html.TextNode, Data: payload})
		if t.DataAtom == atom.Textarea {
			s.attrChanged(t, "value")
		}
	case hotty.OpVar:
		style, _ := attr(t, "style")
		setAttr(t, "style", setStyleProp(style, varName(key), payload))
	default:
		return hotty.EINVAL, "unknown op " + string(op)
	}
	return "", ""
}

// morphByIDs morphs each top-level element of the payload into the
// document's element with its id, and reports the ids it could not find
// (SPEC §6.2, morph without t).
func (s *Surface) morphByIDs(payload string) (code, detail string) {
	body := find(s.doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
	var missing []string
	for _, n := range elements(s.fragment(payload, body)) {
		id, ok := attr(n, "id")
		if !ok {
			continue
		}
		old := s.byID(id)
		if old == nil {
			missing = append(missing, id)
			continue
		}
		s.morph(old, n)
	}
	if len(missing) > 0 {
		return hotty.ENOTARGET, strings.Join(missing, ",")
	}
	return "", ""
}

// fragment parses a payload in the context of the element it will be a
// child of, so table rows and list items parse as they would in place.
func (s *Surface) fragment(payload string, context *html.Node) []*html.Node {
	if context == nil || context.Type != html.ElementNode {
		context = &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	}
	nodes, err := html.ParseFragment(strings.NewReader(payload), context)
	if err != nil {
		return nil
	}
	return nodes
}

func (s *Surface) replaceWith(t *html.Node, nodes []*html.Node) {
	if t.Parent == nil {
		return
	}
	for _, n := range nodes {
		t.Parent.InsertBefore(n, t)
	}
	s.drop(t)
	t.Parent.RemoveChild(t)
}

// drop forgets the state of a subtree leaving the document.
func (s *Surface) drop(n *html.Node) {
	walk(n, func(c *html.Node) {
		delete(s.values, c)
		delete(s.checked, c)
		delete(s.edited, c)
		if c == s.focused {
			s.focused, s.keyb = nil, false
		}
	})
}

func elements(nodes []*html.Node) []*html.Node {
	var out []*html.Node
	for _, n := range nodes {
		if n.Type == html.ElementNode {
			out = append(out, n)
		}
	}
	return out
}

func childByID(parent *html.Node, id string) *html.Node {
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if v, ok := attr(c, "id"); ok && c.Type == html.ElementNode && v == id {
			return c
		}
	}
	return nil
}

// matches: two elements match when their tag and namespace are equal and
// their ids are equal or absent on either side (SPEC §6.2, step 2).
func matches(old, nu *html.Node) bool {
	if old.Type != html.ElementNode || nu.Type != html.ElementNode || old.Data != nu.Data || old.Namespace != nu.Namespace {
		return false
	}
	oid, ook := attr(old, "id")
	nid, nok := attr(nu, "id")
	return !ook || !nok || oid == nid
}

// morph morphs a live node into a parsed one (SPEC §6.2).
func (s *Surface) morph(old, nu *html.Node) {
	switch {
	case old.Type == html.TextNode && nu.Type == html.TextNode:
		old.Data = nu.Data
	case old.Type == html.CommentNode && nu.Type == html.CommentNode:
	case matches(old, nu):
		s.syncAttrs(old, nu)
		var kids []*html.Node
		for c := nu.FirstChild; c != nil; c = c.NextSibling {
			kids = append(kids, c)
		}
		s.morphChildren(old, kids)
	default:
		if old.Parent != nil {
			if nu.Parent != nil {
				nu.Parent.RemoveChild(nu)
			}
			old.Parent.InsertBefore(nu, old)
			s.drop(old)
			old.Parent.RemoveChild(old)
		}
	}
}

func (s *Surface) syncAttrs(old, nu *html.Node) {
	want := map[string]string{}
	for _, a := range nu.Attr {
		want[a.Key] = a.Val
	}
	for _, a := range append([]html.Attribute(nil), old.Attr...) {
		if _, keep := want[a.Key]; !keep {
			delAttr(old, a.Key)
			s.attrChanged(old, a.Key)
		}
	}
	for _, a := range nu.Attr {
		if v, ok := attr(old, a.Key); !ok || v != a.Val {
			setAttr(old, a.Key, a.Val)
			s.attrChanged(old, a.Key)
		}
	}
}

// sameKind: an element with the same tag and no id, a text node, or a
// comment (SPEC §6.2, step 4).
func sameKind(old, nu *html.Node) bool {
	if old.Type != nu.Type {
		return false
	}
	if old.Type != html.ElementNode {
		return true
	}
	_, oid := attr(old, "id")
	return old.Data == nu.Data && old.Namespace == nu.Namespace && !oid
}

// morphChildren morphs parent's children into kids (SPEC §6.2, step 4).
func (s *Surface) morphChildren(parent *html.Node, kids []*html.Node) {
	var olds []*html.Node
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		olds = append(olds, c)
	}
	wanted := map[string]bool{}
	for _, k := range kids {
		if id, ok := attr(k, "id"); ok && k.Type == html.ElementNode {
			wanted[id] = true
		}
	}
	taken := map[*html.Node]bool{}
	match := make([]*html.Node, len(kids))
	last := -1 // the index in olds of the last match
	for i, k := range kids {
		if id, ok := attr(k, "id"); ok && k.Type == html.ElementNode {
			for j, o := range olds {
				if oid, ook := attr(o, "id"); ook && o.Type == html.ElementNode && oid == id && !taken[o] {
					match[i], taken[o], last = o, true, j
					break
				}
			}
			continue
		}
		for j := last + 1; j < len(olds); j++ {
			o := olds[j]
			if taken[o] {
				continue
			}
			if oid, ook := attr(o, "id"); ook && o.Type == html.ElementNode && wanted[oid] {
				continue
			}
			if sameKind(o, k) {
				match[i], taken[o], last = o, true, j
				break
			}
		}
	}
	for _, o := range olds {
		if !taken[o] {
			s.drop(o)
			parent.RemoveChild(o)
		}
	}
	// Taken children are morphed and moved into the new order; new
	// children with no match are inserted.
	for _, o := range olds {
		if taken[o] {
			parent.RemoveChild(o)
		}
	}
	for i, k := range kids {
		if k.Parent != nil {
			k.Parent.RemoveChild(k)
		}
		if o := match[i]; o != nil {
			parent.AppendChild(o)
			s.morph(o, k)
			continue
		}
		parent.AppendChild(k)
	}
}
