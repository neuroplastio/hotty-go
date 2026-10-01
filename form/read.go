package form

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/neuroplastio/hotty-go"
)

// Answers are a submitted form's values, by field name, in the spec's order:
// strings, numbers (json.Number, or nil when a number is left empty), and
// booleans for checkboxes.
type Answers struct {
	names  []string
	values map[string]any
}

// Get is a field's answer.
func (a Answers) Get(name string) any { return a.values[name] }

// Names are the fields, in order.
func (a Answers) Names() []string { return a.names }

// MarshalJSON writes the answers as one object, in the spec's order.
func (a Answers) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, n := range a.names {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(n)
		v, err := json.Marshal(a.values[n])
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Problem is what keeps a field from being submitted.
type Problem struct {
	Field   int    // its index in the spec
	Message string // "required", "not a number", …
}

// Read reads a submit event's fields (hotty.Event.Fields) into answers, and
// reports the fields that stop the form: required ones left empty, and
// values that are not what their type asks for.
func (s *Spec) Read(fields map[string]string) (Answers, []Problem) {
	a := Answers{values: map[string]any{}}
	var problems []Problem
	for i, f := range s.Fields {
		a.names = append(a.names, f.Name)
		v, sent := fields[f.Name]
		value, msg := f.read(v, sent)
		a.values[f.Name] = value
		if msg != "" {
			problems = append(problems, Problem{Field: i, Message: msg})
		}
	}
	return a, problems
}

// Check reports what is wrong with a value typed into field f, as Read
// would: "" when nothing is.
func (f Field) Check(v string) string {
	_, msg := f.read(v, true)
	return msg
}

func (f Field) read(v string, sent bool) (any, string) {
	empty := strings.TrimSpace(v) == ""
	switch f.kind() {
	case Checkbox:
		on := sent && v != "" && v != "false"
		if f.Required && !on {
			return on, "required"
		}
		return on, ""
	case Number:
		if empty {
			if f.Required {
				return nil, "required"
			}
			return nil, ""
		}
		n, ok := decimal(v)
		if !ok {
			return v, "not a number"
		}
		return json.Number(strconv.FormatFloat(n, 'f', -1, 64)), ""
	}
	if empty {
		if f.Required {
			return v, "required"
		}
		return v, ""
	}
	switch f.kind() {
	case Email:
		at := strings.LastIndex(v, "@")
		if at <= 0 || at == len(v)-1 || strings.ContainsAny(strings.TrimSpace(v), " \t") {
			return v, "not an email address"
		}
	case Date:
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(v)); err != nil {
			return v, "not a date (YYYY-MM-DD)"
		}
		return strings.TrimSpace(v), ""
	case Select, Radio:
		if indexOf(f.Options, v) < 0 {
			return v, "not an option"
		}
	}
	return v, ""
}

// decimal reads a number as a person types one: digits, a sign, a point,
// an exponent. Not hexadecimal, NaN or an infinity, which JSON cannot
// carry.
func decimal(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if strings.ContainsAny(v, "xXnN_") { // 0x…, NaN, Inf, infinity
		return 0, false
	}
	n, err := strconv.ParseFloat(v, 64)
	return n, err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
}

// Show returns the commands that show problems in the form placed as
// surface, clear the ones fixed since, and give the keyboard to the first
// field with a problem. The values typed stay as they are. Problems for
// fields the spec does not have are left out.
func (s *Spec) Show(surface string, problems []Problem) []string {
	msg := map[int]string{}
	first := -1
	for _, p := range problems {
		if p.Field < 0 || p.Field >= len(s.Fields) {
			continue
		}
		if _, ok := msg[p.Field]; !ok {
			msg[p.Field] = p.Message
		}
		if first < 0 || p.Field < first {
			first = p.Field
		}
	}
	var cmds []string
	for i, f := range s.Fields {
		class := "field k-" + f.kind()
		if i > 0 {
			class += " gap"
		}
		text := ""
		if m, ok := msg[i]; ok {
			class += " bad"
			text = "! " + m
		}
		cmds = append(cmds, hotty.SetText(surface, ProblemID(i), text), hotty.SetAttr(surface, FieldID(i), "class", class))
	}
	if first >= 0 {
		target := ControlID(first)
		if k := s.Fields[first].kind(); k == Select || k == Radio {
			target = OptionID(first, 0)
		}
		cmds = append(cmds, hotty.Focus(surface, target))
	}
	return cmds
}

// State is what a placed form holds, kept from its change events: what a
// submit would carry, for a program that submits the form itself. Text
// fields report a change when they lose focus, and a=blur makes the focused
// one do so (SPEC §10.1): blur the surface, wait for its blur event, then
// read Fields.
type State struct {
	spec   *Spec
	fields map[string]string
}

// NewState is the form as it starts: its defaults.
func (s *Spec) NewState() *State {
	st := &State{spec: s, fields: map[string]string{}}
	for _, f := range s.Fields {
		if v := f.Start(); v != "" {
			st.fields[f.Name] = v
		}
	}
	return st
}

// Apply takes a change event from the form's surface, and reports whether
// it changed a field.
func (st *State) Apply(e hotty.Event) bool {
	if e.Kind != hotty.EventChange {
		return false
	}
	var d struct {
		Value   *string `json:"value"`
		Checked *bool   `json:"checked"`
	}
	_ = json.Unmarshal(e.Detail, &d)
	for i, f := range st.spec.Fields {
		switch f.kind() {
		case Checkbox:
			if e.Target == ControlID(i) && d.Checked != nil {
				if *d.Checked {
					st.fields[f.Name] = "true"
				} else {
					delete(st.fields, f.Name)
				}
				return true
			}
		case Select, Radio:
			for j, o := range f.Options {
				if e.Target == OptionID(i, j) && d.Checked != nil && *d.Checked {
					st.fields[f.Name] = o
					return true
				}
			}
		default:
			if e.Target == ControlID(i) && d.Value != nil {
				st.fields[f.Name] = *d.Value
				return true
			}
		}
	}
	return false
}

// Fields is the form's fields as a submit event would carry them: unchecked
// checkboxes and unchosen choices are missing.
func (st *State) Fields() map[string]string {
	out := make(map[string]string, len(st.fields))
	for k, v := range st.fields {
		out[k] = v
	}
	return out
}
