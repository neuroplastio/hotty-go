package hottyedit

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/neuroplastio/hotty-go"
)

// The conformance vectors' edit section (SDK.md §4.6), from the copy the
// root package keeps.
const vectorsFile = "../testdata/conformance/vectors.json"

type editVector struct {
	Name     string   `json:"name"`
	Requires []string `json:"requires"`
	Field    struct {
		Value     string `json:"value"`
		Caret     int    `json:"caret"`
		Multiline bool   `json:"multiline"`
		Password  bool   `json:"password"`
		Rows      int    `json:"rows"`
	} `json:"field"`
	Steps []struct {
		Do      *string `json:"do"`
		Type    *string `json:"type"`
		Value   *string `json:"value"`
		Caret   *int    `json:"caret"`
		Changed *bool   `json:"changed"`
	} `json:"steps"`
}

// features is what this package implements of what a vector may require.
var features = map[string]bool{"graphemes": true}

func TestEditVectors(t *testing.T) {
	data, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Edit []editVector `json:"edit"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Edit) == 0 {
		t.Fatal("no edit vectors")
	}
	for _, e := range v.Edit {
		t.Run(e.Name, func(t *testing.T) {
			for _, r := range e.Requires {
				if !features[r] {
					t.Skip("requires " + r)
				}
			}
			f := Field{Value: e.Field.Value, Caret: e.Field.Caret, Multiline: e.Field.Multiline, Password: e.Field.Password, Rows: e.Field.Rows}
			for i, st := range e.Steps {
				var changed bool
				what := ""
				if st.Do != nil {
					what = *st.Do
					changed = f.Do(hotty.Action(*st.Do))
				} else {
					what = "type " + *st.Type
					changed = f.Type(*st.Type)
				}
				if st.Value != nil && f.Value != *st.Value {
					t.Errorf("step %d (%s): value %q, want %q", i, what, f.Value, *st.Value)
				}
				if st.Caret != nil && f.Caret != *st.Caret {
					t.Errorf("step %d (%s): caret %d, want %d", i, what, f.Caret, *st.Caret)
				}
				if st.Changed != nil && changed != *st.Changed {
					t.Errorf("step %d (%s): changed %v, want %v", i, what, changed, *st.Changed)
				}
			}
		})
	}
}

func TestKey(t *testing.T) {
	km := hotty.Resolve(false, hotty.TerminalKeys, "Control+s=submit")
	f := Field{Value: "foo bar"}
	f.Caret = len("foo bar")
	for _, c := range []struct {
		key     string
		action  hotty.Action
		changed bool
		value   string
		caret   int
	}{
		{"Control+w", hotty.DeleteWordBackward, true, "foo ", 4},
		{"Space", hotty.Insert, true, "foo  ", 5},
		{"Shift+b", hotty.Insert, true, "foo  B", 6},
		{"+", hotty.Insert, true, "foo  B+", 7},
		{"Control+a", hotty.LineStart, false, "foo  B+", 0},
		{"Control+x", "", false, "foo  B+", 0},
		{"Escape", "", false, "foo  B+", 0},
		{"Control+s", hotty.Submit, false, "foo  B+", 0},
		{"Control+n", "", false, "foo  B+", 0},
		{"Control+e", hotty.LineEnd, false, "foo  B+", 7},
	} {
		a, changed := f.Key(km, c.key)
		if a != c.action || changed != c.changed || f.Value != c.value || f.Caret != c.caret {
			t.Errorf("Key(%q) = %q, %v, value %q, caret %d; want %q, %v, %q, %d", c.key, a, changed, f.Value, f.Caret, c.action, c.changed, c.value, c.caret)
		}
	}
}

func TestMultilineOnlyActionsInAnInput(t *testing.T) {
	f := Field{Value: "ab", Caret: 1}
	for _, a := range []hotty.Action{hotty.LinePrevious, hotty.LineNext, hotty.PageUp, hotty.PageDown, hotty.InputStart, hotty.InputEnd, hotty.Newline} {
		if f.Do(a) || f.Value != "ab" || f.Caret != 1 {
			t.Errorf("%s in an input: value %q, caret %d", a, f.Value, f.Caret)
		}
	}
}
