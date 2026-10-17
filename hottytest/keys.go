package hottytest

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/neuroplastio/myelin/myelinkey"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyedit"
)

// Key types a key as the user does, and does with it what SPEC §10.2 has a
// host do while a surface has the keyboard. The key is named as SPEC §10.4
// names it, as myelin key names do: its modifiers, joined by "+", and a
// character, a physical key or a modifier key: "a", "shift+a", "space",
// "enter", "tab", "shift+tab", "backspace", "arrow_down", "escape", "ctrl+s".
// A press has one name here, as the program would read it (SPEC §10.4): a
// binding of a physical key (ctrl+key_w) holds only where the test gives
// that name. A name that does not parse is no key; the program gets nothing
// of it.
//
// tab and shift+tab move focus from the focused element to the next or the
// previous of the surface's focusable elements in tree order, those with a
// negative tabindex and those in an inert subtree left out (an element
// focused with a negative tabindex is where they start from); past the
// last, or before the first, the surface loses the keyboard (blur). A text field (a text-like input, a textarea) does what
// its keymap says (hotty.Resolve, with the data-keys of the elements from
// the root to it): it edits its value at a caret and a selection this host
// keeps, as hottyedit.Field does, with an input event for each edit
// (data-on~=input); a move whose key has shift selects
// (hotty.Keymap.Selects), select-all selects the value, characters type in
// place of the selection, and submit commits the value and submits its
// form.
//
// Any other element has a keymap too, read the same way but with no
// default keymap (hotty.Keymap.Program): a key it binds to program reaches
// the program, and its other bindings do nothing. This host does not
// scroll, so a key bound to a scroll action goes on as if the keymap did
// not bind it (SPEC §10.2). The element takes the other keys of its row of
// the table, unmodified or with shift only:
//   - a button, a link, a summary: space and enter click it (another
//     element that reports clicks, data-on~=click, uses no keys);
//   - a checkbox or a radio button: space checks it, enter submits its
//     form;
//   - a date or time input: the keys a text input's default keymap binds,
//     and characters; the arrows up and down and the page keys are used;
//   - a select: the arrows, home and end pick an option, the page keys
//     as far as home and end, a character the next option its label
//     starts, past disabled ones; a pick sends input (with data-on~=input)
//     and change at once. space and enter do nothing: this host shows no
//     list.
//
// With no element focused there is no keymap: the data-keys of the
// document are not read, and every key but tab is the program's.
//
// Every other key reaches the program, as typed (Type) when it has a
// terminal encoding here: characters, enter, tab, escape, backspace, the
// arrows and the editing keys, and ctrl or alt with a character. So
// does every key while no surface has the keyboard. used reports whether
// a surface used the key.
func (h *Host) Key(key string) (used bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.menuKey(key)
	// After a prefix every key reaches the program, whatever is focused
	// (SPEC §10.5).
	if s := h.keyboard; s != nil && !s.detached && h.prefix != s && h.useKey(s, key) {
		return true
	}
	if seq := keySequence(key); seq != "" {
		h.in.write(seq)
	}
	return false
}

func (h *Host) useKey(s *Surface, key string) bool {
	k, err := myelinkey.Parse(key)
	if err != nil {
		return false
	}
	mods, name, kind := myelinkey.Split(k)
	shiftOnly := mods&^myelinkey.Shift == 0
	if name == "tab" && kind == myelinkey.Physical && shiftOnly {
		h.tab(s, mods != 0)
		return true
	}
	el := s.focused
	if el == nil {
		// No element focused, no keymap (SPEC §10.2).
		return false
	}
	platform := hotty.PlatformOf(h.caps)
	if textField(el) {
		// A key bound to prefix is not the field's: it and every key after
		// it reach the program (SPEC §10.2, §10.5).
		if hotty.Resolve(platform, el.DataAtom == atom.Textarea || editingHost(el), keymaps(el)...).Prefix(key) {
			h.prefix = s
			return false
		}
		return h.fieldKey(s, el, key)
	}
	// Any other element's keymap gives keys to the program, and does
	// nothing else.
	if km := hotty.ParseKeymap(platform, strings.Join(keymaps(el), " ")); km.Program(key) {
		if km.Prefix(key) {
			h.prefix = s
		}
		return false
	}
	if !shiftOnly {
		return false
	}
	// A physical key's name is its own: only a character types.
	text, types := "", false
	if kind == myelinkey.Character {
		text, types = hotty.KeyText(string(k))
	}
	switch {
	case textInput(el):
		// A date or time input.
		switch name {
		case "arrow_up", "arrow_down", "page_up", "page_down":
			return true
		}
		return h.fieldKey(s, el, key)
	case el.DataAtom == atom.Select:
		return h.selectKey(s, el, name, text, types)
	case box(el):
		switch name {
		case "space":
			h.toggle(s, el)
		case "enter":
			if form := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form }); form != nil {
				h.submit(s, form, nil)
			}
		default:
			return false
		}
		return true
	case button(el) && (el.DataAtom != atom.A || !hyperlink(el)):
		if name != "space" && name != "enter" {
			return false
		}
		bits := 0
		if mods&myelinkey.Shift != 0 {
			bits = hotty.ModShift
		}
		_ = h.click(s, el, false, 1, bits)
		return true
	}
	return false
}

// menuKey hides the level a program shows (a=menu:show=1) when a key makes
// it stale: escape, or a key it does not list (SPEC §10.7). A key it lists
// keeps it: the program shows the next level. The key still goes where it
// would without the level.
func (h *Host) menuKey(key string) {
	if !h.shown {
		return
	}
	p := hotty.PlatformOf(h.caps)
	k, ok := p.ReadKey(key)
	if ok && k != "escape" {
		for _, it := range h.level() {
			if c, ok := p.ReadKey(it.Key); ok && c == k {
				return
			}
		}
	}
	h.shown = false
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
	a, changed := f.Key(hotty.Resolve(hotty.PlatformOf(h.caps), multi, values...), key)
	switch {
	case a == "":
		return false
	case a == hotty.Submit:
		if form := closest(el, func(n *html.Node) bool { return n.DataAtom == atom.Form }); form != nil {
			h.commit(s)
			h.submit(s, form, nil)
		}
	case a == hotty.Copy || a == hotty.Cut:
		// No copy or cut with nothing selected, nor in a password field: the
		// key is the program's (SPEC §10.2). This host keeps no clipboard, so
		// a copy does nothing and a cut only deletes.
		if start, end := f.Selection(); start == end || f.Password {
			return false
		}
		if a == hotty.Cut && f.Do(hotty.DeleteCharBackward) {
			h.typeInto(s, el, f.Value)
		}
	case a == hotty.Paste:
		// This host reads no clipboard: the key is the program's.
		return false
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

// tab moves focus to the next focusable element, or the previous one,
// from the focused element, as a browser does: one Tab skips (tabindex=-1)
// is a place to start from, between the ones before it and after it.
func (h *Host) tab(s *Surface, back bool) {
	var order []*html.Node
	// at is where the focused element is in order, or would be; -1 when
	// nothing is focused.
	at := -1
	walk(s.doc, func(n *html.Node) {
		if n == s.focused {
			at = len(order)
		}
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
	in := at >= 0 && at < len(order) && order[at] == s.focused
	i := at
	switch {
	case at < 0 && back:
		i = len(order) - 1
	case at < 0:
		i = 0
	case back:
		i--
	case in:
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
func (h *Host) selectKey(s *Surface, el *html.Node, name, text string, types bool) bool {
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
	case types:
		for k := 1; k <= len(vals); k++ {
			if j := (i + k) % len(vals); strings.HasPrefix(strings.ToLower(labels[j]), strings.ToLower(text)) {
				pick = j
				break
			}
		}
	case name == "arrow_down":
		pick = min(i+1, len(vals)-1)
	case name == "arrow_up":
		pick = max(i-1, 0)
	case name == "home" || name == "page_up":
		pick = 0
	case name == "end" || name == "page_down":
		pick = len(vals) - 1
	case name == "space" || name == "enter":
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

// keySequence is a key as a terminal sends it, in the legacy encoding; ""
// when it has none here.
func keySequence(key string) string {
	k, err := myelinkey.Parse(key)
	if err != nil {
		return ""
	}
	mods, name, kind := myelinkey.Split(k)
	if mods&myelinkey.Super != 0 || kind == myelinkey.Modifier {
		return ""
	}
	ctrl, alt, shift := mods&myelinkey.Ctrl != 0, mods&myelinkey.Alt != 0, mods&myelinkey.Shift != 0
	seq := map[string]string{"enter": "\r", "tab": "\t", "escape": "\x1b", "backspace": "\x7f", "space": " ",
		"arrow_up": "\x1b[A", "arrow_down": "\x1b[B", "arrow_right": "\x1b[C", "arrow_left": "\x1b[D",
		"home": "\x1b[H", "end": "\x1b[F", "insert": "\x1b[2~", "delete": "\x1b[3~", "page_up": "\x1b[5~", "page_down": "\x1b[6~"}[name]
	switch {
	case name == "tab" && shift:
		seq = "\x1b[Z"
	case name == "space" && ctrl:
		seq = "\x00"
	case seq != "":
	case kind != myelinkey.Character:
		return ""
	case ctrl:
		c := name[0]
		if utf8.RuneCountInString(name) != 1 || c < '@' || c > 0x7e {
			return ""
		}
		seq = string(rune(c & 0x1f))
	default:
		typed := name
		if shift {
			typed = "shift+" + name
		}
		seq, _ = hotty.KeyText(typed)
	}
	if alt {
		seq = "\x1b" + seq
	}
	return seq
}
