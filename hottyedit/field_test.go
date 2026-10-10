package hottyedit

import (
	"encoding/json"
	"fmt"
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
		Anchor    *int   `json:"anchor"`
		Multiline bool   `json:"multiline"`
		Password  bool   `json:"password"`
		Rows      int    `json:"rows"`
	} `json:"field"`
	Steps []struct {
		Do      *string `json:"do"`
		Extend  *string `json:"extend"`
		Type    *string `json:"type"`
		Select  []int   `json:"select"`
		Value   *string `json:"value"`
		Caret   *int    `json:"caret"`
		Anchor  *int    `json:"anchor"`
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
			if e.Field.Anchor != nil {
				f.Select(*e.Field.Anchor, e.Field.Caret)
			}
			for i, st := range e.Steps {
				var changed bool
				what := ""
				switch {
				case st.Do != nil:
					what = *st.Do
					changed = f.Do(hotty.Action(*st.Do))
				case st.Extend != nil:
					what = "extend " + *st.Extend
					changed = f.Extend(hotty.Action(*st.Extend))
				case st.Select != nil:
					what = fmt.Sprintf("select %d, %d", st.Select[0], st.Select[1])
					f.Select(st.Select[0], st.Select[1])
				default:
					what = "type " + *st.Type
					changed = f.Type(*st.Type)
				}
				if st.Value != nil && f.Value != *st.Value {
					t.Errorf("step %d (%s): value %q, want %q", i, what, f.Value, *st.Value)
				}
				if st.Caret != nil && f.Caret != *st.Caret {
					t.Errorf("step %d (%s): caret %d, want %d", i, what, f.Caret, *st.Caret)
				}
				// With nothing selected, the anchor is the caret.
				if a, _ := f.Anchor(); st.Anchor != nil && a != *st.Anchor {
					t.Errorf("step %d (%s): anchor %d, want %d", i, what, a, *st.Anchor)
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
		anchor  int // the caret when nothing is selected
	}{
		{"Control+w", hotty.DeleteWordBackward, true, "foo ", 4, 4},
		{"Space", hotty.Insert, true, "foo  ", 5, 5},
		{"Shift+b", hotty.Insert, true, "foo  B", 6, 6},
		{"+", hotty.Insert, true, "foo  B+", 7, 7},
		{"Home", hotty.LineStart, false, "foo  B+", 0, 0},
		{"Control+x", "", false, "foo  B+", 0, 0},
		{"Escape", "", false, "foo  B+", 0, 0},
		{"Control+s", hotty.Submit, false, "foo  B+", 0, 0},
		{"Control+n", "", false, "foo  B+", 0, 0},
		{"Control+e", hotty.LineEnd, false, "foo  B+", 7, 7},
		// Shift selects as it moves; submit keeps the selection, and
		// typing replaces it.
		{"Shift+ArrowLeft", hotty.CharBackward, false, "foo  B+", 6, 7},
		{"Control+Shift+ArrowLeft", hotty.WordBackward, false, "foo  B+", 5, 7},
		{"Control+s", hotty.Submit, false, "foo  B+", 5, 7},
		{"Shift+ArrowUp", "", false, "foo  B+", 5, 7},
		{"x", hotty.Insert, true, "foo  x", 6, 6},
		{"Control+a", hotty.SelectAll, false, "foo  x", 6, 0},
		{"Backspace", hotty.DeleteCharBackward, true, "", 0, 0},
	} {
		a, changed := f.Key(km, c.key)
		anchor, _ := f.Anchor()
		if a != c.action || changed != c.changed || f.Value != c.value || f.Caret != c.caret || anchor != c.anchor {
			t.Errorf("Key(%q) = %q, %v, value %q, caret %d, anchor %d; want %q, %v, %q, %d, %d",
				c.key, a, changed, f.Value, f.Caret, anchor, c.action, c.changed, c.value, c.caret, c.anchor)
		}
	}
}

// A row move or newline does nothing in an input, with Shift or without:
// the selection stays.
func TestMultilineOnlyActionsInAnInput(t *testing.T) {
	f := Field{Value: "abc"}
	f.Select(2, 1)
	for _, a := range []hotty.Action{hotty.LinePrevious, hotty.LineNext, hotty.PageUp, hotty.PageDown, hotty.Newline} {
		if f.Do(a) || f.Extend(a) || f.Value != "abc" || f.Caret != 1 {
			t.Errorf("%s in an input: value %q, caret %d", a, f.Value, f.Caret)
		}
		if start, end := f.Selection(); start != 1 || end != 2 {
			t.Errorf("%s in an input: selection %d, %d, want 1, 2", a, start, end)
		}
	}
}

// A Field literal selects nothing; Select selects either way, and the
// selection stays within the value.
func TestSelect(t *testing.T) {
	f := Field{Value: "foo bar", Caret: 3}
	if a, ok := f.Anchor(); ok || a != 3 {
		t.Errorf("a Field literal: Anchor() = %d, %v, want 3, false", a, ok)
	}
	if start, end := f.Selection(); start != 3 || end != 3 {
		t.Errorf("a Field literal: Selection() = %d, %d, want 3, 3", start, end)
	}
	f.Select(6, 2)
	if a, ok := f.Anchor(); !ok || a != 6 || f.Caret != 2 {
		t.Errorf("Select(6, 2): Anchor() = %d, %v, caret %d", a, ok, f.Caret)
	}
	if start, end := f.Selection(); start != 2 || end != 6 {
		t.Errorf("Select(6, 2): Selection() = %d, %d, want 2, 6", start, end)
	}
	f.Value = "foo"
	if start, end := f.Selection(); start != 2 || end != 3 {
		t.Errorf("a shorter value: Selection() = %d, %d, want 2, 3", start, end)
	}
	f.Select(-1, 2)
	if start, end := f.Selection(); start != 0 || end != 2 {
		t.Errorf("Select(-1, 2): Selection() = %d, %d, want 0, 2", start, end)
	}
	f.Select(1, 1)
	if a, ok := f.Anchor(); ok || a != 1 || f.Caret != 1 {
		t.Errorf("Select(1, 1): Anchor() = %d, %v, caret %d", a, ok, f.Caret)
	}
}
