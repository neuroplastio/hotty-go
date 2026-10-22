package hottytest

import (
	"testing"

	"github.com/neuroplastio/hotty-go"
)

func ptr[T any](v T) *T { return &v }

func TestFieldRevAndSelection(t *testing.T) {
	h := shown(t, "f", `<input id=a value=héllo><p id=p>p</p><input type=checkbox id=c>`+
		`<form><input type=radio name=g id=r1><input type=radio name=g id=r2></form><input type=radio name=g id=r3>`)
	s := h.Surface("f")
	if rev, ok := s.Rev("a"); rev != 0 || !ok {
		t.Errorf("Rev(a) = %d, %v before an edit", rev, ok)
	}
	// The caret starts at the end, counted in bytes.
	if a, c, ok := s.Selection("a"); a != 6 || c != 6 || !ok {
		t.Errorf("Selection(a) = %d, %d, %v", a, c, ok)
	}
	for _, id := range []string{"p", "nope"} {
		if _, ok := s.Rev(id); ok {
			t.Errorf("Rev(%s) is a control's", id)
		}
	}
	for _, id := range []string{"c", "p", "nope"} {
		if _, _, ok := s.Selection(id); ok {
			t.Errorf("Selection(%s) is a text field's", id)
		}
	}

	// The program selects in a field that is not focused; the user's edit
	// is one more rev, and the caret goes after it.
	send(h, hotty.SetField("f", "a", hotty.FieldChange{Sel: &[2]int{1, 3}}))
	if a, c, _ := s.Selection("a"); a != 1 || c != 3 {
		t.Errorf("after sel [1,3]: %d, %d", a, c)
	}
	// a=focus gives the field its selection back.
	send(h, hotty.Focus("f", "a"))
	h.Key("e")
	if v, _ := s.Value("a"); v != "hello" {
		t.Errorf("typed over the selection: %q", v)
	}
	if rev, _ := s.Rev("a"); rev != 1 {
		t.Errorf("Rev(a) = %d after an edit", rev)
	}
	if a, c, _ := s.Selection("a"); a != 2 || c != 2 {
		t.Errorf("after typing: %d, %d", a, c)
	}

	// A radio group counts together, within its form.
	if err := h.Check("f", "r1", true); err != nil {
		t.Fatal(err)
	}
	if err := h.Check("f", "r3", true); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"r1": 1, "r2": 1, "r3": 1, "c": 0} {
		if rev, _ := s.Rev(id); rev != want {
			t.Errorf("Rev(%s) = %d, want %d", id, rev, want)
		}
	}
	if v, _ := s.Value("r1"); v != "true" {
		t.Error("checking r3 unchecked r1, of another form")
	}
	h.drain()
	send(h, hotty.SetField("f", "r2", hotty.FieldChange{Rev: 1, Checked: ptr(true)}))
	if v, _ := s.Value("r1"); v != "false" {
		t.Error("a=field checking r2 left r1 checked")
	}
	if rev, _ := s.Rev("r2"); rev != 1 {
		t.Errorf("a=field changed the group's rev: %d", rev)
	}
	expect(t, sent(h))
}

func TestFieldSanitizes(t *testing.T) {
	for _, c := range []struct{ markup, value, want string }{
		{`<input id=x>`, "a\r\nb\nc", "abc"},
		{`<textarea id=x></textarea>`, "a\r\nb\rc", "a\nb\nc"},
		{`<input id=x type=number>`, "-1.5e3", "-1.5e3"},
		{`<input id=x type=number>`, "1,5", ""},
		{`<input id=x type=email>`, " a@b.c\t", "a@b.c"},
		{`<input id=x type=color>`, "#ABCDEF", "#abcdef"},
		{`<input id=x type=color>`, "red", "#000000"},
		{`<input id=x type=date>`, "2026-10-22", "2026-10-22"},
		{`<input id=x type=date>`, "2026-13-01", ""},
		{`<input id=x type=time>`, "23:59:30.5", "23:59:30.5"},
		{`<input id=x type=time>`, "24:00", ""},
		{`<input id=x type=week>`, "2026-W43", "2026-W43"},
		{`<input id=x type=range>`, "33", "33"},
		{`<input id=x type=range>`, "150", "100"},
		{`<input id=x type=range>`, "high", "50"},
		{`<input id=x type=range min=0 max=10 step=4>`, "10", "8"},
		{`<input id=x type=range min=0 max=10 step=3>`, "5", "6"},
		{`<input id=x type=range min=1 max=2 step=any>`, "1.25", "1.25"},
		{`<input id=x type=range min=5 max=1 step=0>`, "9", "5"},
		{`<input id=x type=range min=x max=10 step=-2>`, "-3", "0"},
		{`<input id=x type=frob>`, "a\nb", "ab"},
	} {
		h := shown(t, "f", c.markup)
		send(h, hotty.SetField("f", "x", hotty.FieldChange{Value: ptr(c.value)}))
		if got, _ := h.Surface("f").Value("x"); got != c.want {
			t.Errorf("%s value %q: %q, want %q", c.markup, c.value, got, c.want)
		}
		expect(t, sent(h))
	}
}

func TestFieldTakesControlsOnly(t *testing.T) {
	h := shown(t, "f", `<input type=hidden id=h><input type=submit id=b><input id=a value=ab>`, Lenient())
	send(h, hotty.SetField("f", "h", hotty.FieldChange{Value: ptr("x")}, hotty.N(1)),
		hotty.SetField("f", "b", hotty.FieldChange{Value: ptr("x")}, hotty.N(2)),
		hotty.SetField("f", "a", hotty.FieldChange{Text: ptr("x"), From: ptr(0), To: ptr(3)}, hotty.N(3)),
		hotty.SetField("f", "a", hotty.FieldChange{Rev: 1, Text: ptr("x")}, hotty.N(4)),
		hotty.SetField("f", "a", hotty.FieldChange{Text: ptr("x")}, hotty.N(5)))
	expect(t, sent(h), "err re=field s=f EINVAL", "err re=field s=f EINVAL", "err re=field s=f EINVAL",
		"err re=field s=f ESTALE", "ok re=field s=f")
	if v, _ := h.Surface("f").Value("a"); v != "abx" {
		t.Errorf("text with no from or to replaced the selection, the caret at the end: %q", v)
	}
}
