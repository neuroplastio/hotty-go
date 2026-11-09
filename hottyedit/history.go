package hottyedit

import "strings"

// maxHistory is how many edits a field keeps to undo.
const maxHistory = 200

// history is a field's edits to undo and to redo. A Field holds it by pointer
// to stay comparable, made when the first edit needs it: a zero Field has
// none, and a copy of a Field shares it.
type history struct {
	undo, redo []step
}

// step is a value and caret a field was at, for undo and redo.
type step struct {
	value string
	caret int
}

// The kinds of run an edit can continue.
const (
	runNone = iota
	runType
	runBackward
	runForward
)

// edit does an edit that sets the value to v and the caret to p, as the
// history keeps it: one step, unless it continues a run, an edit of kind
// run that the last one left at the caret and the value.
func (f *Field) edit(v string, p, run int) {
	if run == runNone || f.run != run || f.runValue != f.Value || f.runCaret != f.Caret {
		if f.hist == nil {
			f.hist = new(history)
		}
		h := f.hist
		h.undo = append(h.undo, step{f.Value, f.Caret})
		if len(h.undo) > maxHistory {
			h.undo = append(h.undo[:0], h.undo[len(h.undo)-maxHistory:]...)
		}
	}
	if f.hist != nil {
		f.hist.redo = nil
	}
	f.Value, f.Caret = v, p
	f.run, f.runValue, f.runCaret = run, v, p
}

// Undo takes back the last edit, or the last run of typing, and reports
// whether there was one: the value and caret are as they were before it.
func (f *Field) Undo() bool {
	if f.hist == nil {
		return false
	}
	return f.travel(&f.hist.undo, &f.hist.redo)
}

// Redo does again what Undo took back.
func (f *Field) Redo() bool {
	if f.hist == nil {
		return false
	}
	return f.travel(&f.hist.redo, &f.hist.undo)
}

// travel moves a step from one stack to the other, and the field to it.
func (f *Field) travel(from, to *[]step) bool {
	if len(*from) == 0 {
		return false
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, step{f.Value, f.Caret})
	f.Value, f.Caret = s.value, s.caret
	f.anchor, f.goal, f.run = 0, 0, runNone
	return true
}

// Paste types pasted text in place of the selection, as one edit, and
// reports whether the value changed. The text is kept whole: CRLF and CR
// become LF and a tab four spaces. A field that has no lines (Multiline
// false) takes its line breaks as spaces.
func (f *Field) Paste(text string) bool {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ").Replace(text)
	if !f.Multiline {
		text = strings.ReplaceAll(text, "\n", " ")
	}
	return f.insert(text, runNone)
}
