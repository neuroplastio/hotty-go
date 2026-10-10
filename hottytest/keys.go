package hottytest

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyedit"
)

// Key types a key as the user does, and does with it what SPEC §10.2 has a
// host do while a surface has the keyboard. The key is named as SPEC §10.4
// names it: a W3C UI Events key value after its modifiers, joined by "+":
// "a", "A", "Space", "Enter", "Tab", "Shift+Tab", "Backspace", "ArrowDown",
// "Escape", "Control+s".
//
// Tab and Shift+Tab move focus among the surface's focusable elements in
// tree order, those with a negative tabindex and those in an inert subtree
// left out; past the last, or before the first, the surface loses the
// keyboard (blur). A text field (a text-like input, a textarea) does what
// its keymap says (hotty.Resolve, with the data-keys of the elements from
// the root to it): it edits its value at a caret and a selection this host
// keeps, as hottyedit.Field does, with an input event for each edit
// (data-on~=input); a move whose key has Shift selects
// (hotty.Keymap.Selects), select-all selects the value, characters type in
// place of the selection, and submit commits the value and submits its
// form.
//
// Any other element has a keymap too, read the same way but with no
// default keymap (hotty.Keymap.Program): a key it binds to program reaches
// the program, and its other bindings do nothing. This host does not
// scroll, so a key bound to a scroll action goes on as if the keymap did
// not bind it (SPEC §10.2). The element takes the other keys of its row of
// the table, unmodified or with Shift only:
//   - a button, a link, a summary: Space and Enter click it;
//   - a checkbox or a radio button: Space checks it, Enter submits its
//     form;
//   - a date or time input: the keys a text input's default keymap binds,
//     and characters; the arrows up and down and the page keys are used;
//   - a select: the arrows, Home and End pick an option, the page keys
//     as far as Home and End, a character the next option its label
//     starts, past disabled ones; a pick sends input (with data-on~=input)
//     and change at once. Space and Enter do nothing: this host shows no
//     list.
//
// With no element focused there is no keymap: the data-keys of the
// document are not read, and every key but Tab is the program's.
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
	shiftOnly := !slices.ContainsFunc(mods, func(m string) bool { return m != "Shift" })
	if name == "Tab" && shiftOnly {
		h.tab(s, len(mods) > 0)
		return true
	}
	el := s.focused
	if el == nil {
		// No element focused, no keymap (SPEC §10.2).
		return false
	}
	if textField(el) {
		return h.fieldKey(s, el, key)
	}
	// Any other element's keymap gives keys to the program, and does
	// nothing else.
	if hotty.ParseKeymap(strings.Join(keymaps(el), " ")).Program(key) {
		return false
	}
	if !shiftOnly {
		return false
	}
	char := utf8.RuneCountInString(name) == 1
	switch {
	case textInput(el):
		// A date or time input.
		switch name {
		case "ArrowUp", "ArrowDown", "PageUp", "PageDown":
			return true
		}
		return h.fieldKey(s, el, key)
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

// fieldKey gives a key to a text field, which does what its keymap says
// (SPEC §10.2). A date or time input has the default keymap only.
func (h *Host) fieldKey(s *Surface, el *html.Node, key string) bool {
	multi := el.DataAtom == atom.Textarea || editingHost(el)
	var values []string
	if textField(el) {
		values = keymaps(el)
	}
	f := s.fields[el]
	if f == nil {
		v := s.valueOf(el)
		t, _ := attr(el, "type")
		f = &hottyedit.Field{Value: v, Caret: len(v), Multiline: multi, Password: strings.EqualFold(t, "password")}
		s.fields[el] = f
	}
	f.Value = s.valueOf(el)
	a, changed := f.Key(hotty.Resolve(multi, values...), key)
	switch {
	case a == "":
		return false
	case a == hotty.Submit:
		if form := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form }); form != nil {
			h.commit(s)
			h.submit(s, form, nil)
		}
	case changed:
		h.typeInto(s, el, f.Value)
	}
	return true
}

// keymaps are the data-keys values of the elements from the root down to
// el, el's last: its keymap, as SPEC §10.2 reads it.
func keymaps(el *html.Node) []string {
	var values []string
	for n := el; n != nil; n = n.Parent {
		if v, ok := attr(n, "data-keys"); ok {
			values = append(values, v)
		}
	}
	slices.Reverse(values)
	return values
}

// textField reports whether an element edits text with a keymap (SPEC
// §10.2): a text-like input, a textarea, an editing host.
func textField(n *html.Node) bool {
	if n.DataAtom == atom.Textarea || editingHost(n) {
		return true
	}
	if !textInput(n) {
		return false
	}
	t, _ := attr(n, "type")
	switch strings.ToLower(t) {
	case "date", "time", "datetime-local", "month", "week":
		return false
	}
	return true
}

// editingHost reports whether an element is contenteditable.
func editingHost(n *html.Node) bool {
	v, ok := attr(n, "contenteditable")
	return ok && !strings.EqualFold(v, "false")
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
		label, ok := attr(o, "label")
		if !ok {
			label = textOf(o)
		}
		vals, labels = append(vals, v), append(labels, label)
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
	case name == "Home" || name == "PageUp":
		pick = 0
	case name == "End" || name == "PageDown":
		pick = len(vals) - 1
	case name == " " || name == "Enter":
	default:
		return false
	}
	if pick >= 0 && pick < len(vals) && pick != i {
		h.pick(s, el, vals[pick])
	}
	return true
}

// pick picks a select's option by its value: input, with data-on~=input,
// and change come at once, as a browser's select sends them (SPEC §10.2).
func (h *Host) pick(s *Surface, el *html.Node, v string) {
	s.values[el] = v
	id, ok := attr(el, "id")
	if !ok {
		return
	}
	if listens("input")(el) {
		h.event(s, hotty.EventInput, id, map[string]string{"value": v})
	}
	h.event(s, hotty.EventChange, id, map[string]string{"value": v})
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
