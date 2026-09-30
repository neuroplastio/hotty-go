package form

import (
	"fmt"
	"html"
	"strings"
)

// Ids in the document. A program focuses and patches them; they are the
// same for every form, so a document is known without reading it.
const (
	// FormID is the <form>: submit events name it.
	FormID = "form"
	// SubmitID is the submit button.
	SubmitID = "submit"
)

// ControlID is the id of field i's control: its input, textarea or
// checkbox. A choice (select, radio) has one per option (OptionID); this is
// then the id of its group.
func ControlID(i int) string { return fmt.Sprintf("f%d", i) }

// OptionID is the id of option j of field i, a choice.
func OptionID(i, j int) string { return fmt.Sprintf("f%d-%d", i, j) }

// ProblemID is where field i says what is wrong with it.
func ProblemID(i int) string { return fmt.Sprintf("e%d", i) }

// FieldID is field i's box: its class is "field", or "field bad" while it
// has a problem.
func FieldID(i int) string { return fmt.Sprintf("w%d", i) }

// Heights, in half rows: the document is laid out in rows of the terminal
// (--row, a cell's height), so that Rows knows how tall it is at any width.
const (
	hPad      = 1 // the box's padding, top and bottom each
	hTitle    = 3 // the title and the gap below it
	hLabel    = 2
	hLine     = 3 // a single-line control
	hTextarea = 7 // three lines and padding
	hCheck    = 2
	hOption   = 2 // a radio option, one a row
	hGap      = 1 // between fields
	hActions  = 4 // a gap and the buttons
)

// A select is a row of choices when they are few and short enough for any
// terminal; else it is laid out as a radio, one a row.
const (
	rowMaxOptions = 4
	rowMaxChars   = 30
)

func (f Field) inRow() bool {
	if f.kind() != Select || len(f.Options) > rowMaxOptions {
		return false
	}
	n := 0
	for _, o := range f.Options {
		n += len([]rune(o)) + 4 // a radio and a gap
	}
	return n <= rowMaxChars
}

// inline reports whether the submit button sits beside the form's one
// line of input, as a prompt's does, rather than below it.
func (s *Spec) inline() bool {
	if s.Title != "" || len(s.Fields) != 1 {
		return false
	}
	switch s.Fields[0].kind() {
	case Text, Password, Email, Number, Date:
		return true
	}
	return false
}

func (f Field) bare() bool {
	switch f.kind() {
	case Text, Password, Email, Number, Date:
		return f.Bare
	}
	return false
}

func (f Field) height() int {
	if f.bare() {
		return hLine
	}
	switch f.kind() {
	case Checkbox:
		return hCheck
	case Textarea:
		return hLabel + hTextarea
	case Radio, Select:
		if f.inRow() {
			return hLabel + hLine
		}
		return hLabel + hOption*len(f.Options)
	}
	return hLabel + hLine
}

// Rows is how many rows of the terminal the form takes, whatever its width:
// place it with this many, and it fits.
func (s *Spec) Rows() int {
	h := 2 * hPad
	if s.Title != "" {
		h += hTitle
	}
	for i, f := range s.Fields {
		if i > 0 {
			h += hGap
		}
		h += f.height()
	}
	if !s.inline() {
		h += hActions
	}
	return (h + 1) / 2
}

// First is the id to focus first: the first field's control, or its chosen
// option.
func (s *Spec) First() string {
	f := s.Fields[0]
	switch f.kind() {
	case Select, Radio:
		if j := indexOf(f.Options, f.Start()); j >= 0 {
			return OptionID(0, j)
		}
		return OptionID(0, 0)
	}
	return ControlID(0)
}

// HTML is the form, a fragment for a page that has CSS (and the host's
// stylesheet, for the row height). hint is the submit button's key hint,
// such as "⏎"; "" for none.
func (s *Spec) HTML(hint string) string {
	var b strings.Builder
	b.WriteString(`<form id="` + FormID + `" class="ask" novalidate autocomplete="off">`)
	if s.Title != "" {
		b.WriteString(`<div class="title">` + esc(s.Title) + `</div>`)
	}
	for i, f := range s.Fields {
		s.field(&b, i, f)
	}
	button := `<button type="submit" id="` + SubmitID + `" class="go">` + esc(s.SubmitLabel()) + Kbd(hint) + `</button>`
	if s.inline() {
		// The button goes into the field's row, beside its input.
		out := b.String()
		i := strings.LastIndex(out, `</div></div>`)
		return out[:i] + button + out[i:] + `</form>`
	}
	b.WriteString(`<div class="actions">` + button + `</div></form>`)
	return b.String()
}

// Kbd is a key hint, for a button: dim, after its label.
func Kbd(key string) string {
	if key == "" {
		return ""
	}
	return `<kbd>` + esc(key) + `</kbd>`
}

func (s *Spec) field(b *strings.Builder, i int, f Field) {
	id := ControlID(i)
	gap := ""
	if i > 0 {
		gap = " gap"
	}
	fmt.Fprintf(b, `<div class="field k-%s%s" id="%s">`, f.kind(), gap, FieldID(i))
	problem := `<span class="problem" id="` + ProblemID(i) + `"></span>`
	if f.kind() == Checkbox {
		checked := ""
		if f.Start() == "true" {
			checked = " checked"
		}
		fmt.Fprintf(b, `<label class="check"><input type="checkbox" id="%s" name="%s" value="true"%s><span>%s</span></label>%s</div>`,
			id, esc(f.Name), checked, esc(f.Title()), problem)
		return
	}
	forID := id
	if f.kind() == Select || f.kind() == Radio {
		forID = ""
	}
	if !f.bare() {
		b.WriteString(`<div class="head">`)
		if forID != "" {
			fmt.Fprintf(b, `<label class="label" for="%s">%s</label>`, forID, esc(f.Title()))
		} else {
			fmt.Fprintf(b, `<span class="label">%s</span>`, esc(f.Title()))
		}
		b.WriteString(problem + `</div>`)
	}
	ph := f.Placeholder
	if ph == "" && f.kind() == Date {
		ph = "YYYY-MM-DD"
	}
	attrs := fmt.Sprintf(`id="%s" name="%s"`, id, esc(f.Name))
	if ph != "" {
		attrs += ` placeholder="` + esc(ph) + `"`
	}
	switch f.kind() {
	case Textarea:
		fmt.Fprintf(b, `<textarea %s rows="3">%s</textarea>`, attrs, esc(f.Start()))
	case Select, Radio:
		class := "opts"
		if f.inRow() {
			class = "opts row"
		}
		fmt.Fprintf(b, `<div class="%s" id="%s">`, class, id)
		for j, o := range f.Options {
			checked := ""
			if o == f.Start() {
				checked = " checked"
			}
			fmt.Fprintf(b, `<label class="opt"><input type="radio" id="%s" name="%s" value="%s"%s><span>%s</span></label>`,
				OptionID(i, j), esc(f.Name), esc(o), checked, esc(o))
		}
		b.WriteString(`</div>`)
	default:
		// Text inputs carry no type, but a password: every host then has a
		// plain text field, and Enter submits in all of them (a host may
		// not submit on Enter from a form with more than one typed field).
		// The kind is a hint for on-screen keyboards, and Read checks it.
		typ := ""
		switch f.kind() {
		case Password:
			typ = ` type="password"`
		case Email:
			typ = ` inputmode="email"`
		case Number:
			typ = ` inputmode="decimal"`
		case Date:
			typ = ` inputmode="numeric"`
		}
		value := ""
		if v := f.Start(); v != "" {
			value = ` value="` + esc(v) + `"`
		}
		fmt.Fprintf(b, `<div class="line"><input%s %s%s spellcheck="false"></div>`, typ, attrs, value)
	}
	b.WriteString(`</div>`)
}

func esc(s string) string { return html.EscapeString(s) }

// CSS lays the form out in rows of the terminal (Rows counts on it) and
// gives it the look of a floating prompt: a rounded box, labels above
// fields, the accent only on focus, the submit button right-aligned with its
// key hint. It uses the page's custom properties --ink, --paper, --dim,
// --rule, --accent and --bad, with fallbacks.
const CSS = `
:root { --row: var(--hotty-cell-h, 18px); }
body { overflow: hidden; }
.ask {
  box-sizing: border-box; height: 100%; margin: 0;
  display: flex; flex-direction: column; justify-content: center;
  padding: calc(var(--row) * .5 - 1px) 14px;
  border: 1px solid var(--edge, #3a3a4c); border-radius: 10px;
  background: var(--paper, #12121a); color: var(--ink, #d7d7e0);
  line-height: var(--row);
}
.ask .title { height: var(--row); margin-bottom: calc(var(--row) * .5); font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.ask .field { display: flex; flex-direction: column; flex: none; }
.ask .field.gap { margin-top: calc(var(--row) * .5); }
.ask .head { display: flex; height: var(--row); align-items: center; gap: 8px; }
.ask .label { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.ask .problem { flex: none; color: var(--bad, #f07a7a); font-size: 12px; white-space: nowrap; }
.ask .line { display: flex; gap: 8px; height: calc(var(--row) * 1.5); align-items: stretch; }
.ask input:not([type=checkbox]):not([type=radio]), .ask textarea {
  flex: 1; min-width: 0; box-sizing: border-box; margin: 0;
  font: inherit; font-size: 13px; color: var(--ink, #d7d7e0);
  background: var(--well, #0b0b10); border: 1px solid var(--edge, #3a3a4c); border-radius: 6px;
  padding: 0 8px; outline: none; caret-color: var(--accent, #9d90ff);
}
.ask input:not([type=checkbox]):not([type=radio]) { height: calc(var(--row) * 1.5); line-height: calc(var(--row) * 1.5 - 2px); }
.ask textarea { height: calc(var(--row) * 3.5); padding: calc(var(--row) * .25 - 1px) 8px; line-height: var(--row); resize: none; }
.ask input::placeholder, .ask textarea::placeholder { color: var(--dim, #8b90a0); opacity: .7; }
.ask input:focus, .ask textarea:focus { border-color: var(--accent, #9d90ff); }
.ask .bad input, .ask .bad textarea { border-color: var(--bad, #f07a7a); }
.ask input[type=checkbox], .ask input[type=radio] {
  width: 14px; height: 14px; margin: 0; flex: none; accent-color: var(--accent, #9d90ff);
}
.ask input[type=checkbox]:focus, .ask input[type=radio]:focus { outline: 2px solid var(--accent, #9d90ff); outline-offset: 2px; }
.ask .check, .ask .opt { display: flex; align-items: center; gap: 8px; height: var(--row); white-space: nowrap; overflow: hidden; cursor: pointer; }
.ask .k-checkbox { flex-direction: row; align-items: center; gap: 8px; }
.ask .k-checkbox .check { flex: 1; min-width: 0; }
.ask .opts.row { display: flex; gap: 18px; height: calc(var(--row) * 1.5); align-items: center; }
.ask .opts.row .opt { height: auto; }
.ask .actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: calc(var(--row) * .5); flex: none; }
.ask button {
  font: inherit; font-size: 13px; color: var(--ink, #d7d7e0); background: transparent; cursor: pointer;
  height: calc(var(--row) * 1.5); box-sizing: border-box; flex: none; margin: 0;
  border: 1px solid var(--edge, #3a3a4c); border-radius: 6px; padding: 0 12px;
  display: flex; align-items: center; gap: 8px; outline: none;
}
.ask button:focus { border-color: var(--accent, #9d90ff); color: var(--accent, #9d90ff); }
.ask button:hover { border-color: var(--accent, #9d90ff); }
.ask kbd { font: inherit; font-size: 11px; color: var(--dim, #8b90a0); }
`
