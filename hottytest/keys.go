package hottytest

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
)

// Key types a key as the user does, and does with it what SPEC §10.2 has a
// host do while a surface has the keyboard. The key is a W3C UI Events key
// value after its modifiers, joined by "+": "a", "A", " ", "Enter", "Tab",
// "Shift+Tab", "Backspace", "ArrowDown", "Escape", "Control+s".
//
// Tab and Shift+Tab move focus among the surface's focusable elements in
// tree order, those with a negative tabindex and those in an inert subtree
// left out; past the last, or before the first, the surface loses the
// keyboard (blur). The focused element takes the keys of its row of the
// table, unmodified or with Shift only:
//   - a button, a link, a summary: Space and Enter click it;
//   - a checkbox or a radio button: Space checks it, Enter submits its
//     form;
//   - a text-like input: characters and Space type at the end, Backspace
//     takes the last character back (input, with data-on~=input), Enter
//     commits it and submits its form; Delete, the arrows across, Home
//     and End move a caret this host does not keep;
//   - a textarea: the same, with Enter typing a new line, and the arrows
//     up and down and the page keys used;
//   - a select: the arrows, Home and End pick an option (change), a
//     character the next option it starts; Space, Enter and the page keys
//     are used.
//
// Every other key reaches the program, as typed (Type) when it has a
// terminal encoding here: characters, Enter, Tab, Escape, Backspace, the
// arrows and the editing keys, and Control or Alt with a character. So
// does every key while no surface has the keyboard. used reports whether
// a surface used the key.
func (h *Host) Key(key string) (used bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.keyboard; s != nil && !s.detached && h.useKey(s, key) {
		return true
	}
	if seq := keySequence(key); seq != "" {
		h.in.write(seq)
	}
	return false
}

func (h *Host) useKey(s *Surface, key string) bool {
	mods, name := splitKey(key)
	if slices.ContainsFunc(mods, func(m string) bool { return m != "Shift" }) {
		return false
	}
	if name == "Tab" {
		h.tab(s, len(mods) > 0)
		return true
	}
	el := s.focused
	if el == nil {
		return false
	}
	char := utf8.RuneCountInString(name) == 1
	switch {
	case el.DataAtom == atom.Textarea || textInput(el):
		multi := el.DataAtom == atom.Textarea
		switch {
		case char:
			h.typeInto(s, el, s.valueOf(el)+name)
		case name == "Backspace":
			if v := []rune(s.valueOf(el)); len(v) > 0 {
				h.typeInto(s, el, string(v[:len(v)-1]))
			}
		case name == "Enter" && multi:
			h.typeInto(s, el, s.valueOf(el)+"\n")
		case name == "Enter":
			if form := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form }); form != nil {
				h.commit(s)
				h.submit(s, form, nil)
			}
		case slices.Contains([]string{"Delete", "ArrowLeft", "ArrowRight", "Home", "End"}, name):
		case multi && slices.Contains([]string{"ArrowUp", "ArrowDown", "PageUp", "PageDown"}, name):
		default:
			return false
		}
		return true
	case el.DataAtom == atom.Select:
		return h.selectKey(s, el, name, char)
	case box(el):
		switch name {
		case " ":
			h.toggle(s, el)
		case "Enter":
			if form := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form }); form != nil {
				h.submit(s, form, nil)
			}
		default:
			return false
		}
		return true
	case reportsClick(el) && (el.DataAtom != atom.A || !hyperlink(el)):
		if name != " " && name != "Enter" {
			return false
		}
		_ = h.click(s, el, false)
		return true
	}
	return false
}

// tab moves focus to the next focusable element, or the previous one.
func (h *Host) tab(s *Surface, back bool) {
	var order []*html.Node
	walk(s.doc, func(n *html.Node) {
		if !focusable(n) || closest(n, func(p *html.Node) bool { _, ok := attr(p, "inert"); return ok }) != nil {
			return
		}
		if t, ok := attr(n, "tabindex"); ok {
			if v, err := strconv.Atoi(t); err == nil && v < 0 {
				return
			}
		}
		order = append(order, n)
	})
	i := slices.Index(order, s.focused)
	switch {
	case i < 0 && back:
		i = len(order) - 1
	case i < 0:
		i = 0
	case back:
		i--
	default:
		i++
	}
	if i < 0 || i >= len(order) {
		h.blur(s)
		return
	}
	h.takeKeyboard(s, order[i], true)
}

// typeInto sets a text control's value as typing does.
func (h *Host) typeInto(s *Surface, el *html.Node, v string) {
	s.values[el] = v
	s.dirty()[el] = true
	if on, _ := attr(el, "data-on"); slices.Contains(strings.Fields(on), "input") {
		if id, ok := attr(el, "id"); ok {
			h.event(s, hotty.EventInput, id, map[string]string{"value": v})
		}
	}
}

// toggle is Space on a box: a checkbox flips, a radio button is chosen.
func (h *Host) toggle(s *Surface, el *html.Node) {
	t, _ := attr(el, "type")
	on := !s.isChecked(el)
	if strings.EqualFold(t, "radio") {
		if !on {
			return
		}
		name, _ := attr(el, "name")
		scope := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form })
		if scope == nil {
			scope = s.doc
		}
		walk(scope, func(n *html.Node) {
			if nt, _ := attr(n, "type"); n != el && box(n) && strings.EqualFold(nt, "radio") {
				if nn, _ := attr(n, "name"); nn == name && name != "" {
					s.checked[n] = false
				}
			}
		})
	}
	s.checked[el] = on
	if id, ok := attr(el, "id"); ok {
		h.event(s, hotty.EventChange, id, map[string]any{"checked": on, "value": s.valueOf(el)})
	}
}

// selectKey is a key on a select.
func (h *Host) selectKey(s *Surface, el *html.Node, name string, char bool) bool {
	var vals, labels []string
	walk(el, func(o *html.Node) {
		if o.DataAtom != atom.Option {
			return
		}
		if _, dis := attr(o, "disabled"); dis {
			return
		}
		v, ok := attr(o, "value")
		if !ok {
			v = textOf(o)
		}
		vals, labels = append(vals, v), append(labels, textOf(o))
	})
	i := slices.Index(vals, s.valueOf(el))
	pick := -1
	switch {
	case char && name != " ":
		for k := 1; k <= len(vals); k++ {
			if j := (i + k) % len(vals); strings.HasPrefix(strings.ToLower(labels[j]), strings.ToLower(name)) {
				pick = j
				break
			}
		}
	case name == "ArrowDown":
		pick = min(i+1, len(vals)-1)
	case name == "ArrowUp":
		pick = max(i-1, 0)
	case name == "Home":
		pick = 0
	case name == "End":
		pick = len(vals) - 1
	case name == " " || name == "Enter" || name == "PageUp" || name == "PageDown":
	default:
		return false
	}
	if pick >= 0 && pick < len(vals) && pick != i {
		s.values[el] = vals[pick]
		if id, ok := attr(el, "id"); ok {
			h.event(s, hotty.EventChange, id, map[string]string{"value": vals[pick]})
		}
	}
	return true
}

// textInput: an input that takes text.
func textInput(n *html.Node) bool {
	if n.DataAtom != atom.Input {
		return false
	}
	t, _ := attr(n, "type")
	switch strings.ToLower(t) {
	case "", "text", "search", "email", "url", "tel", "password", "number", "date", "time", "datetime-local", "month", "week":
		return true
	}
	return false
}

// splitKey splits a key into its modifiers and its key value; " " may be
// written "Space".
func splitKey(key string) (mods []string, name string) {
	parts := strings.Split(key, "+")
	if strings.HasSuffix(key, "++") || key == "+" {
		parts = append(strings.Split(strings.TrimSuffix(key, "++"), "+"), "+")
		if key == "+" {
			parts = []string{"+"}
		}
	}
	name = parts[len(parts)-1]
	if name == "Space" {
		name = " "
	}
	return parts[:len(parts)-1], name
}

// keySequence is a key as a terminal sends it, in the legacy encoding; ""
// when it has none here.
func keySequence(key string) string {
	mods, name := splitKey(key)
	var ctrl, alt, shift bool
	for _, m := range mods {
		switch m {
		case "Control":
			ctrl = true
		case "Alt":
			alt = true
		case "Shift":
			shift = true
		default:
			return ""
		}
	}
	seq := map[string]string{"Enter": "\r", "Tab": "\t", "Escape": "\x1b", "Backspace": "\x7f",
		"ArrowUp": "\x1b[A", "ArrowDown": "\x1b[B", "ArrowRight": "\x1b[C", "ArrowLeft": "\x1b[D",
		"Home": "\x1b[H", "End": "\x1b[F", "Insert": "\x1b[2~", "Delete": "\x1b[3~", "PageUp": "\x1b[5~", "PageDown": "\x1b[6~"}[name]
	switch {
	case name == "Tab" && shift:
		seq = "\x1b[Z"
	case seq != "":
	case utf8.RuneCountInString(name) != 1:
		return ""
	case ctrl:
		c := strings.ToLower(name)[0]
		if c < '@' || c > 0x7e {
			return ""
		}
		seq = string(rune(c & 0x1f))
	default:
		seq = name
	}
	if alt {
		seq = "\x1b" + seq
	}
	return seq
}
