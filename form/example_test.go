package form_test

import (
	"encoding/json"
	"fmt"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/form"
)

const deploy = `{
  "title": "Deploy",
  "submit": "Deploy",
  "fields": [
    {"name": "env", "label": "Environment", "type": "select", "options": ["staging", "production"], "default": "staging"},
    {"name": "version", "label": "Version", "placeholder": "v1.4.2", "required": true},
    {"name": "notify", "label": "Tell the team", "type": "checkbox", "default": true}
  ]
}`

// Ask for a deploy's details: a spec becomes a document of real controls,
// placed at its fixed height with the keyboard at its first field. The
// user fills it in the terminal, with no round trip, and the submit event
// brings the fields back, read into typed answers.
func Example_askForDeployDetails() {
	spec, err := form.Parse([]byte(deploy))
	if err != nil {
		panic(err)
	}
	const surface = "deploy-4121-form"
	page := "<!doctype html><style>" + form.CSS + "</style>" + spec.HTML("⏎")
	cmds := []string{
		hotty.Doc(surface, page),
		hotty.Place(surface, hotty.Placement{Cols: 60, Rows: spec.Rows()}),
		hotty.Focus(surface, spec.First()),
	}
	fmt.Println(len(cmds), "commands; the form takes", spec.Rows(), "rows; focus at", spec.First())

	// What the host sends when the user presses Enter (SPEC §9).
	ev := hotty.Event{Surface: surface, Kind: hotty.EventSubmit, Target: form.FormID,
		Detail: json.RawMessage(`{"env":"production","version":"v1.4.2","notify":"true"}`)}
	answers, problems := spec.Read(ev.Fields())
	out, _ := json.Marshal(answers)
	fmt.Println(string(out), len(problems), "problems")
	// Output:
	// 3 commands; the form takes 12 rows; focus at f0-0
	// {"env":"production","version":"v1.4.2","notify":true} 0 problems
}

// Show what keeps a form from being submitted: the problem under its
// field, the field marked, and the keyboard there. What the user typed
// stays.
func ExampleSpec_Show() {
	spec, _ := form.Parse([]byte(deploy))
	ev := hotty.Event{Kind: hotty.EventSubmit, Target: form.FormID,
		Detail: json.RawMessage(`{"env":"staging","version":"  "}`)}
	_, problems := spec.Read(ev.Fields())
	for _, p := range problems {
		fmt.Printf("%s: %s\n", spec.Fields[p.Field].Name, p.Message)
	}
	var d hotty.Decoder
	for _, cmd := range spec.Show("deploy-4121-form", problems) {
		m, _ := d.Feed(cmd)
		if m.Get("t") == form.ProblemID(1) || m.Get("a") == "focus" {
			fmt.Printf("%s %s %s %q\n", m.Get("a"), m.Get("op"), m.Get("t"), m.Payload)
		}
	}
	// Output:
	// version: required
	// delta text e1 "! required"
	// focus  f1 ""
}

// A program that submits the form itself, on a key the controls leave to
// it such as Ctrl-D, keeps the fields from the change events.
func ExampleState() {
	spec, _ := form.Parse([]byte(deploy))
	st := spec.NewState()
	st.Apply(hotty.Event{Kind: hotty.EventChange, Target: form.ControlID(1), Detail: json.RawMessage(`{"value":"v2.0.0"}`)})
	answers, _ := spec.Read(st.Fields())
	out, _ := json.Marshal(answers)
	fmt.Println(string(out))
	// Output:
	// {"env":"staging","version":"v2.0.0","notify":true}
}
