package hottytest

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyedit"
)

// This file keeps what a=field changes (SPEC §6.4): each control's rev, a
// text field's selection in bytes of its value, and the command itself.

// formControl reports whether a=field takes an element: an input of any
// type but hidden, button, submit, reset, image and file, a textarea, a
// select (SPEC §6.4).
func formControl(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Textarea, atom.Select:
		return true
	case atom.Input:
		t, _ := attr(n, "type")
		switch strings.ToLower(t) {
		case "hidden", "button", "submit", "reset", "image", "file":
			return false
		}
		return true
	}
	return false
}

// plainField reports whether a control is a text field that is not an
// editing host, as a=field's text, from, to and sel are for (SPEC §6.4).
func plainField(n *html.Node) bool {
	return textField(n) && !editingHost(n)
}

// group is a radio button's group: the radio buttons of its name in its
// form, or in no form; one without a name is a group of its own.
func (s *Surface) group(n *html.Node) []*html.Node {
	name, _ := attr(n, "name")
	if name == "" {
		return []*html.Node{n}
	}
	owner := formOf(n)
	var out []*html.Node
	walk(s.doc, func(m *html.Node) {
		if radio(m) && formOf(m) == owner {
			if mn, _ := attr(m, "name"); mn == name {
				out = append(out, m)
			}
		}
	})
	return out
}

func radio(n *html.Node) bool {
	t, _ := attr(n, "type")
	return box(n) && strings.EqualFold(t, "radio")
}

func formOf(n *html.Node) *html.Node {
	return closest(n, func(p *html.Node) bool { return p.DataAtom == atom.Form })
}

// rev is a control's revision (SPEC §6.4): a radio button's is the highest
// of its group's.
func (s *Surface) rev(n *html.Node) int {
	if !radio(n) {
		return s.revs[n]
	}
	r := 0
	for _, m := range s.group(n) {
		r = max(r, s.revs[m])
	}
	return r
}

// bump counts a change the user made to a control, and returns its new
// rev: a radio button's makes its whole group's one more.
func (s *Surface) bump(n *html.Node) int {
	r := s.rev(n) + 1
	s.revs[n] = r
	return r
}

// check checks a box as the user's or the program's check does: a radio
// button unchecks the others of its group.
func (s *Surface) check(n *html.Node, on bool) {
	if on && radio(n) {
		for _, m := range s.group(n) {
			s.checked[m] = false
		}
	}
	s.checked[n] = on
}

// field is a text field's caret and selection, kept while it is not
// focused (SPEC §10.2): the caret at the end of its value until it moves.
func (s *Surface) field(n *html.Node) *hottyedit.Field {
	v := s.valueOf(n)
	f := s.fields[n]
	if f == nil {
		t, _ := attr(n, "type")
		f = &hottyedit.Field{Caret: len(bounds(v)) - 1, Multiline: n.DataAtom == atom.Textarea || editingHost(n), Password: strings.EqualFold(t, "password")}
		s.fields[n] = f
	}
	f.Value = v
	return f
}

// sel is a text field's selection, its anchor then its caret, in bytes of
// its value (SPEC §6.4).
func (s *Surface) sel(n *html.Node) [2]int {
	f := s.field(n)
	b := bounds(f.Value)
	at := func(c int) int { return b[min(max(c, 0), len(b)-1)] }
	anchor, _ := f.Anchor()
	return [2]int{at(anchor), at(f.Caret)}
}

// setSel sets a text field's selection from positions in bytes: an end
// inside a character moves to its start.
func (s *Surface) setSel(n *html.Node, anchor, caret int) {
	f := s.field(n)
	b := bounds(f.Value)
	f.Select(char(b, anchor), char(b, caret))
}

// bounds are where a value's characters (grapheme clusters) start, in
// bytes, and its end.
func bounds(v string) []int {
	b := []int{0}
	for g := graphemes.FromString(v); g.Next(); {
		b = append(b, g.End())
	}
	return b
}

// char is the character a position in bytes falls in, or the end.
func char(b []int, p int) int {
	i := 0
	for i+1 < len(b) && b[i+1] <= p {
		i++
	}
	return i
}

// position reports whether p is a position in v: not past its end, and
// between code points.
func position(v string, p int) bool {
	return p >= 0 && p <= len(v) && (p == len(v) || utf8.RuneStart(v[p]))
}

// listening is whether a text field reports its edits (data-on~=input) and
// its selection (data-on~=caret).
func listening(n *html.Node) (input, caret bool) {
	return listens("input")(n), listens("caret")(n) && plainField(n)
}

// edited reports the user's edit of a control's value, which makes its rev
// one more (SPEC §6.4): input, with data-on~=input, and with data-on~=caret
// the selection after it; caret instead in a text field with data-on~=caret
// alone (SPEC §9).
func (h *Host) edited(s *Surface, el *html.Node) {
	rev := s.bump(el)
	s.dirty()[el] = true
	id, ok := attr(el, "id")
	if !ok {
		return
	}
	input, caret := listening(el)
	switch {
	case input && caret:
		sel := s.sel(el)
		h.event(s, hotty.EventInput, id, map[string]any{"value": s.valueOf(el), "rev": rev, "sel": []int{sel[0], sel[1]}})
	case input:
		h.event(s, hotty.EventInput, id, map[string]any{"value": s.valueOf(el), "rev": rev})
	case caret:
		h.caret(s, el, id)
	}
}

// moved reports the user's move of a text field's selection, with
// data-on~=caret, when it is not where it was (SPEC §9).
func (h *Host) moved(s *Surface, el *html.Node, was [2]int) {
	if _, caret := listening(el); !caret || s.sel(el) == was {
		return
	}
	if id, ok := attr(el, "id"); ok {
		h.caret(s, el, id)
	}
}

func (h *Host) caret(s *Surface, el *html.Node, id string) {
	sel := s.sel(el)
	h.event(s, hotty.EventCaret, id, map[string]any{"sel": []int{sel[0], sel[1]}, "rev": s.rev(el)})
}

// setField is a=field (SPEC §6.4): checked whole, in the order EDETACHED,
// ENOTARGET, EINVAL, ESTALE, before it changes anything; then it changes
// the control as the user would, and sends nothing.
func (h *Host) setField(s *Surface, m hotty.Message) (code, detail string) {
	if s.detached {
		return hotty.EDETACHED, s.name
	}
	t := hotty.Get(m.Control, "t")
	el := s.byID(t)
	if t != "" && el == nil {
		return hotty.ENOTARGET, t
	}
	c, err := hotty.FieldOf(m)
	if e := (*hotty.Error)(nil); errors.As(err, &e) {
		return e.Code, e.Detail
	}
	invalid := func(d string) (string, string) { return hotty.EINVAL, d }
	text := plainField(el)
	switch {
	case !formControl(el):
		return invalid("not a control")
	case c.Value != nil && box(el):
		return invalid("value for a box")
	case c.Checked != nil && !box(el):
		return invalid("checked for what is not a box")
	case (c.Text != nil || c.Sel != nil) && !text:
		return invalid("text or sel for what is not a text field")
	}
	v := s.valueOf(el)
	var caret int // where the caret goes without sel, in bytes of the new value
	switch {
	case c.Value != nil && el.DataAtom == atom.Select:
		if !hasOption(el, *c.Value) {
			return invalid("no option " + *c.Value)
		}
		v = *c.Value
	case c.Value != nil:
		v = sanitize(el, *c.Value)
		caret = len(v)
	case c.Text != nil:
		lo, hi := 0, 0
		if c.From != nil {
			if !position(v, *c.From) || !position(v, *c.To) {
				return invalid("from or to not a position")
			}
			lo, hi = *c.From, *c.To
		} else {
			sel := s.sel(el)
			lo, hi = min(sel[0], sel[1]), max(sel[0], sel[1])
		}
		b := bounds(v)
		lo, hi = b[char(b, lo)], b[char(b, hi)]
		in := paste(el, *c.Text)
		v = v[:lo] + in + v[hi:]
		caret = lo + len(in)
	}
	if c.Sel != nil && (!position(v, c.Sel[0]) || !position(v, c.Sel[1])) {
		return invalid("sel not a position")
	}
	if r := s.rev(el); c.Rev != r {
		return hotty.ESTALE, strconv.Itoa(r)
	}
	// No echo: no event, no rev, and nothing to commit (SPEC §6.4).
	if c.Checked != nil {
		s.check(el, *c.Checked)
	}
	if c.Value != nil || c.Text != nil {
		s.values[el] = v
	}
	if text {
		switch {
		case c.Sel != nil:
			s.setSel(el, c.Sel[0], c.Sel[1])
		case c.Value != nil || c.Text != nil:
			s.setSel(el, caret, caret)
		}
	}
	return "", ""
}

func hasOption(sel *html.Node, v string) bool {
	found := false
	walk(sel, func(o *html.Node) {
		if o.DataAtom == atom.Option && !found {
			ov, ok := attr(o, "value")
			if !ok {
				ov = textOf(o)
			}
			found = ov == v
		}
	})
	return found
}

// paste is text as a paste puts it in a text field: an input drops its line
// breaks, a textarea keeps them as LF.
func paste(n *html.Node, text string) string {
	if n.DataAtom == atom.Textarea {
		return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(text)
}

var (
	validNumber = regexp.MustCompile(`^-?(\d+(\.\d+)?|\.\d+)([eE][-+]?\d+)?$`)
	validDate   = map[string]*regexp.Regexp{
		"date":           regexp.MustCompile(`^\d{4,}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$`),
		"month":          regexp.MustCompile(`^\d{4,}-(0[1-9]|1[0-2])$`),
		"week":           regexp.MustCompile(`^\d{4,}-W(0[1-9]|[1-4]\d|5[0-3])$`),
		"time":           regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d(\.\d{1,3})?)?$`),
		"datetime-local": regexp.MustCompile(`^\d{4,}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])[T ]([01]\d|2[0-3]):[0-5]\d(:[0-5]\d(\.\d{1,3})?)?$`),
	}
	validColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// sanitize is a value as HTML's value sanitization leaves it for the
// control's type, as a script's value is (SPEC §6.4): an input drops line
// breaks, a range clamps and steps it, a number or a date that does not
// parse is empty, a colour that does not is black.
func sanitize(n *html.Node, v string) string {
	if n.DataAtom == atom.Textarea {
		return paste(n, v)
	}
	t, _ := attr(n, "type")
	t = strings.ToLower(t)
	switch t {
	case "number":
		if !validNumber.MatchString(v) {
			return ""
		}
		return v
	case "range":
		return rangeValue(n, v)
	case "color":
		if !validColor.MatchString(v) {
			return "#000000"
		}
		return strings.ToLower(v)
	case "email":
		return strings.Trim(paste(n, v), " \t\f")
	}
	if re := validDate[t]; re != nil {
		if !re.MatchString(v) {
			return ""
		}
		return v
	}
	return paste(n, v)
}

// rangeValue is a range's value: a number between its min and max (0 and
// 100 when not given), on its step from min (1 when not given); its middle
// when v is not a number.
func rangeValue(n *html.Node, v string) string {
	num := func(key string, def float64) float64 {
		s, _ := attr(n, key)
		if f, err := strconv.ParseFloat(s, 64); err == nil && validNumber.MatchString(s) {
			return f
		}
		return def
	}
	lo, hi := num("min", 0), num("max", 100)
	if hi < lo {
		hi = lo
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil || !validNumber.MatchString(v) {
		x = lo + (hi-lo)/2
	}
	x = min(max(x, lo), hi)
	if st, _ := attr(n, "step"); !strings.EqualFold(st, "any") {
		step := num("step", 1)
		if step <= 0 {
			step = 1
		}
		x = lo + math.Round((x-lo)/step)*step
		if x > hi {
			x -= step
		}
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}
