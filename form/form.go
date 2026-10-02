// Package form is a form for a HOTTY surface: a spec, the document it makes
// (real HTML controls, with ids a program can focus and change with
// deltas), and what a submit event brings back, read into typed answers.
//
// The order of use:
//
//	spec, err := form.Parse(jsonBytes)      // or build a Spec in Go
//	doc := spec.HTML()                      // the body; form.CSS styles it
//	rows := spec.Rows()                     // the surface's fixed height, in cells
//	... hotty.Doc, hotty.Place, hotty.Focus(surface, spec.First()) ...
//	answers, problems := spec.Read(ev.Fields())   // on a submit event
//	if len(problems) > 0 { t.Send(spec.Show(surface, problems)...) }
//
// A form never needs a round trip while the user fills it in: typing, focus
// and checking happen in the terminal. A program that submits the form itself
// (a key the controls do not use, such as Ctrl-D) keeps a State from the
// change events and reads that instead.
//
// It does no I/O. HTML is a fragment for a document that has the host
// stylesheet (SPEC §8) and CSS: put both in the page a program sends with
// hotty.Doc.
package form

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The field types.
const (
	Text     = "text"
	Password = "password"
	Email    = "email"
	Number   = "number"
	Date     = "date" // YYYY-MM-DD
	Textarea = "textarea"
	Select   = "select" // one of Options, as a row of choices
	Radio    = "radio"  // one of Options, one a line
	Checkbox = "checkbox"
)

var types = []string{Text, Password, Email, Number, Date, Textarea, Select, Radio, Checkbox}

// Spec is a form: a title, its fields in order, and the submit button's
// label.
type Spec struct {
	// Title heads the form; none when empty.
	Title string `json:"title,omitempty"`
	// Submit is the submit button's label: "OK" when empty.
	Submit string `json:"submit,omitempty"`
	// Fields are the questions, in order.
	Fields []Field `json:"fields"`
}

// Field is one question.
type Field struct {
	// Name is the key of its answer.
	Name string `json:"name"`
	// Label is what it asks; the name when empty.
	Label string `json:"label,omitempty"`
	// Type is one of the types above; text when empty.
	Type string `json:"type,omitempty"`
	// Placeholder is shown in an empty text field.
	Placeholder string `json:"placeholder,omitempty"`
	// Options are a select's or a radio's choices, each once.
	Options []string `json:"options,omitempty"`
	// Default is the value it starts with: a string, a number for a number
	// field, a boolean for a checkbox.
	Default any `json:"default,omitempty"`
	// Required fields must be filled before the form submits.
	Required bool `json:"required,omitempty"`
	// Bare leaves out the row above a single-line field, where its label
	// and its problems go: a prompt that asks without a label.
	Bare bool `json:"-"`
}

// Parse reads a spec from JSON and checks it.
func Parse(b []byte) (*Spec, error) {
	var s Spec
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err := d.Decode(&s); err != nil {
		return nil, fmt.Errorf("form: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("form: more after the spec")
	}
	for i := range s.Fields {
		if n, ok := s.Fields[i].Default.(json.Number); ok {
			f, err := n.Float64()
			if err != nil {
				return nil, fmt.Errorf("form: field %d: %v", i+1, err)
			}
			s.Fields[i].Default = f
		}
	}
	if err := s.Check(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Check reports what is wrong with a spec: no fields, a field without a
// name, two with one name, an unknown type, a choice without options or
// with one twice, a default of the wrong kind.
func (s *Spec) Check() error {
	if len(s.Fields) == 0 {
		return errors.New("form: no fields")
	}
	seen := map[string]bool{}
	for i, f := range s.Fields {
		where := fmt.Sprintf("form: field %d", i+1)
		if f.Name != "" {
			where = fmt.Sprintf("form: field %q", f.Name)
		}
		switch {
		case f.Name == "":
			return fmt.Errorf("%s: no name", where)
		case seen[f.Name]:
			return fmt.Errorf("%s: the name is taken", where)
		case !known(f.kind()):
			return fmt.Errorf("%s: unknown type %q (one of %s)", where, f.Type, strings.Join(types, ", "))
		case (f.kind() == Select || f.kind() == Radio) && len(f.Options) == 0:
			return fmt.Errorf("%s: a %s needs options", where, f.kind())
		}
		for j, o := range f.Options {
			if indexOf(f.Options[:j], o) >= 0 {
				return fmt.Errorf("%s: the option %q is there twice", where, o)
			}
		}
		seen[f.Name] = true
		switch d := f.Default.(type) {
		case nil:
		case bool:
			if f.kind() != Checkbox {
				return fmt.Errorf("%s: a %s's default is not true or false", where, f.kind())
			}
		case string:
			if f.kind() == Checkbox && d != "true" && d != "false" {
				return fmt.Errorf("%s: a checkbox's default is true or false", where)
			}
			if (f.kind() == Select || f.kind() == Radio) && d != "" && indexOf(f.Options, d) < 0 {
				return fmt.Errorf("%s: the default %q is not an option", where, d)
			}
		case float64, int:
			if f.kind() != Number {
				return fmt.Errorf("%s: a %s's default is not a number", where, f.kind())
			}
		default:
			return fmt.Errorf("%s: a default is a string, a number or true or false", where)
		}
	}
	return nil
}

func known(t string) bool { return indexOf(types, t) >= 0 }

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// kind is the field's type, text when empty.
func (f Field) kind() string {
	if f.Type == "" {
		return Text
	}
	return f.Type
}

// Title is the field's label, or its name.
func (f Field) Title() string {
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

// Kind is the field's type, text when empty.
func (f Field) Kind() string { return f.kind() }

// Start is the field's default as the form carries it: "" for none, "true"
// for a checked checkbox, a number in its shortest form.
func (f Field) Start() string {
	switch d := f.Default.(type) {
	case nil:
		return ""
	case bool:
		if d {
			return "true"
		}
		return ""
	case string:
		if f.kind() == Checkbox && d == "false" {
			return ""
		}
		return d
	case float64:
		return strconv.FormatFloat(d, 'f', -1, 64)
	case int:
		return fmt.Sprint(d)
	}
	return ""
}

// SubmitLabel is the submit button's label.
func (s *Spec) SubmitLabel() string {
	if s.Submit != "" {
		return s.Submit
	}
	return "OK"
}
