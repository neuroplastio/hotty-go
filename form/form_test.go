package form

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/neuroplastio/hotty-go"
)

// deploy is the spec in docs/tools/askhot.md.
const deploy = `{
  "title": "Deploy",
  "submit": "Deploy",
  "fields": [
    {"name": "env", "label": "Environment", "type": "select", "options": ["staging", "production"], "default": "staging"},
    {"name": "version", "label": "Version", "type": "text", "placeholder": "v1.4.2", "required": true},
    {"name": "notify", "label": "Tell the team", "type": "checkbox", "default": true},
    {"name": "notes", "label": "Notes", "type": "textarea"}
  ]
}`

func parse(t *testing.T, s string) *Spec {
	t.Helper()
	spec, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestDocument(t *testing.T) {
	s := parse(t, deploy)
	doc := s.HTML("ctrl+d")
	for _, want := range []string{
		`<form id="form" class="ask" novalidate`,
		`<div class="title">Deploy</div>`,
		// The select is a row of radio buttons, the default checked.
		`<div class="opts row" id="f0">`,
		`<input type="radio" id="f0-0" name="env" value="staging" checked>`,
		`<input type="radio" id="f0-1" name="env" value="production">`,
		// A text field has no type, so Enter submits in every host.
		`<input id="f1" name="version" placeholder="v1.4.2" spellcheck="false">`,
		`<span class="problem" id="e1"></span>`,
		`<input type="checkbox" id="f2" name="notify" value="true" checked>`,
		`<textarea id="f3" name="notes" rows="3"></textarea>`,
		`<button type="submit" id="submit" class="go">Deploy<kbd>ctrl+d</kbd></button>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %s", want)
		}
	}
	if got := s.First(); got != "f0-0" {
		t.Errorf("First %q: want the chosen option", got)
	}
	// Half rows: padding 2, the title 3, the select 5, a gap and the text
	// field 6, a gap and the checkbox 3, a gap and the textarea 10, and
	// the buttons 4: 33, so 17 rows.
	if got := s.Rows(); got != 17 {
		t.Errorf("Rows %d, want 17", got)
	}
}

func TestPromptRows(t *testing.T) {
	line := &Spec{Fields: []Field{{Name: "value", Label: "Name"}}}
	bare := &Spec{Fields: []Field{{Name: "value", Bare: true}}}
	long := &Spec{Fields: []Field{{Name: "env", Type: Select, Options: []string{"a", "b", "c", "d", "e"}}}}
	for _, c := range []struct {
		spec *Spec
		rows int
	}{{line, 4}, {bare, 3}, {long, 9}} {
		if got := c.spec.Rows(); got != c.rows {
			t.Errorf("%+v: %d rows, want %d", c.spec.Fields[0], got, c.rows)
		}
	}
	// A prompt's button sits beside its field; the field has no label row
	// when bare.
	doc := bare.HTML("⏎")
	if !strings.Contains(doc, `<div class="line"><input id="f0" name="value" spellcheck="false"><button type="submit"`) || strings.Contains(doc, `class="head"`) {
		t.Errorf("bare prompt: %s", doc)
	}
	// Five options are too many for a row: one a line.
	if doc := long.HTML(""); !strings.Contains(doc, `<div class="opts" id="f0">`) {
		t.Errorf("a long select is not a column: %s", doc)
	}
}

func TestPassword(t *testing.T) {
	s := &Spec{Fields: []Field{{Name: "pw", Type: Password}}}
	if doc := s.HTML(""); !strings.Contains(doc, `<input type="password" id="f0" name="pw"`) {
		t.Errorf("password: %s", doc)
	}
	if !strings.Contains(CSS, `input[type=password] { color: transparent; }`) {
		t.Error("a password must not be echoed: not every host masks one")
	}
}

func TestParseErrors(t *testing.T) {
	for _, c := range []struct{ spec, err string }{
		{`{"fields": []}`, "no fields"},
		{`{"fields": [{"label": "x"}]}`, "field 1: no name"},
		{`{"fields": [{"name": "a"}, {"name": "a"}]}`, `field "a": the name is taken`},
		{`{"fields": [{"name": "a", "type": "color"}]}`, `unknown type "color"`},
		{`{"fields": [{"name": "a", "type": "radio"}]}`, "a radio needs options"},
		{`{"fields": [{"name": "a", "type": "select", "options": ["x"], "default": "y"}]}`, `the default "y" is not an option`},
		{`{"fields": [{"name": "a", "default": true}]}`, "a text's default is not true or false"},
		{`{"fields": [{"name": "a", "default": 3}]}`, "a text's default is not a number"},
		{`{"fields": [{"name": "a", "type": "checkbox", "default": "maybe"}]}`, "true or false"},
		{`{"fields": [}`, "form: invalid character"},
	} {
		_, err := Parse([]byte(c.spec))
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v, want %q", c.spec, err, c.err)
		}
	}
}

func TestRead(t *testing.T) {
	s := parse(t, deploy)
	// A submit leaves out an unchecked checkbox.
	a, problems := s.Read(map[string]string{"env": "production", "version": "v1.4.2", "notes": "first\nsecond"})
	if len(problems) > 0 {
		t.Fatalf("problems %v", problems)
	}
	b, _ := json.Marshal(a)
	if got, want := string(b), `{"env":"production","version":"v1.4.2","notify":false,"notes":"first\nsecond"}`; got != want {
		t.Errorf("answers %s, want %s", got, want)
	}
	a, _ = s.Read(map[string]string{"env": "staging", "version": "v1", "notify": "true"})
	b, _ = json.Marshal(a)
	if got, want := string(b), `{"env":"staging","version":"v1","notify":true,"notes":""}`; got != want {
		t.Errorf("answers %s, want %s", got, want)
	}

	// Required and typed fields.
	typed := &Spec{Fields: []Field{
		{Name: "n", Type: Number},
		{Name: "m", Type: Number, Required: true},
		{Name: "e", Type: Email},
		{Name: "d", Type: Date},
		{Name: "ok", Type: Checkbox, Required: true},
		{Name: "s", Type: Radio, Options: []string{"a", "b"}},
		{Name: "t", Required: true},
	}}
	a, problems = typed.Read(map[string]string{"n": "1.50", "m": "", "e": "ada", "d": "2026-13-01", "s": "c", "t": "  "})
	want := map[int]string{1: "required", 2: "not an email address", 3: "not a date (YYYY-MM-DD)", 4: "required", 5: "not an option", 6: "required"}
	if len(problems) != len(want) {
		t.Errorf("problems %v, want %v", problems, want)
	}
	for _, p := range problems {
		if want[p.Field] != p.Message {
			t.Errorf("field %d: %q, want %q", p.Field, p.Message, want[p.Field])
		}
	}
	if n := a.Get("n"); n != json.Number("1.5") {
		t.Errorf("a number is %#v, want json.Number 1.5", n)
	}
	a, problems = typed.Read(map[string]string{"n": "", "m": "-2e3", "e": "ada@example.com", "d": "2026-10-01", "ok": "true", "s": "b", "t": "x"})
	if len(problems) > 0 {
		t.Errorf("problems %v", problems)
	}
	b, _ = json.Marshal(a)
	if got, want := string(b), `{"n":null,"m":-2000,"e":"ada@example.com","d":"2026-10-01","ok":true,"s":"b","t":"x"}`; got != want {
		t.Errorf("answers %s, want %s", got, want)
	}
	if msg := (Field{Type: Number}).Check("x"); msg != "not a number" {
		t.Errorf("Check: %q", msg)
	}
}

func decode(t *testing.T, cmds []string) []hotty.Message {
	t.Helper()
	var out []hotty.Message
	var d hotty.Decoder
	for _, c := range cmds {
		m, complete, ok := d.Feed(c)
		if !ok || !complete {
			t.Fatalf("not one command: %q", c)
		}
		out = append(out, m)
	}
	return out
}

func TestShow(t *testing.T) {
	s := parse(t, deploy)
	msgs := decode(t, s.Show("form-1", []Problem{{Field: 1, Message: "required"}}))
	var texts, classes []string
	for _, m := range msgs {
		switch m.Get("op") {
		case "text":
			texts = append(texts, m.Get("t")+"="+string(m.Payload))
		case "attr":
			classes = append(classes, m.Get("t")+"="+string(m.Payload))
		}
	}
	if got, want := strings.Join(texts, ","), "e0=,e1=! required,e2=,e3="; got != want {
		t.Errorf("problem texts %s, want %s", got, want)
	}
	if got, want := strings.Join(classes, ","), "w0=field k-select,w1=field k-text gap bad,w2=field k-checkbox gap,w3=field k-textarea gap"; got != want {
		t.Errorf("classes %s, want %s", got, want)
	}
	last := msgs[len(msgs)-1]
	if last.Get("a") != "focus" || last.Get("t") != "f1" || last.Get("s") != "form-1" {
		t.Errorf("last command %v: want the keyboard at the field", last.Control)
	}
	// Nothing wrong: every problem cleared, and the keyboard left alone.
	for _, m := range decode(t, s.Show("form-1", nil)) {
		if m.Get("a") == "focus" {
			t.Error("no problems, but a focus")
		}
	}
}

func TestState(t *testing.T) {
	s := parse(t, deploy)
	st := s.NewState()
	if got := st.Fields(); got["env"] != "staging" || got["notify"] != "true" || len(got) != 2 {
		t.Errorf("defaults %v", got)
	}
	ev := func(target string, detail string) hotty.Event {
		return hotty.Event{Kind: "change", Target: target, Detail: json.RawMessage(detail)}
	}
	for _, e := range []hotty.Event{
		ev("f0-1", `{"checked":true,"value":"production"}`),
		ev("f1", `{"value":"v2"}`),
		ev("f2", `{"checked":false,"value":"true"}`),
		ev("f3", `{"value":"a\nb"}`),
	} {
		if !st.Apply(e) {
			t.Errorf("%s: not applied", e.Target)
		}
	}
	if st.Apply(ev("x9", `{"value":"?"}`)) || st.Apply(hotty.Event{Kind: "click", Target: "f1"}) {
		t.Error("applied what is not a change of a field")
	}
	a, problems := s.Read(st.Fields())
	b, _ := json.Marshal(a)
	if got, want := string(b), `{"env":"production","version":"v2","notify":false,"notes":"a\nb"}`; got != want || len(problems) > 0 {
		t.Errorf("state %s %v, want %s", got, problems, want)
	}
}
