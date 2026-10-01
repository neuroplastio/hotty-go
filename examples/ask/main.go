// Ask asks for what a deploy needs with a form, and prints the answers as
// JSON for the script that ran it.
//
// It shows a surface the program reads events from: the form is the
// program's while it asks, takes the keyboard, reports a submit, and shows
// what is wrong without a round trip per key; then a detached summary
// replaces it, so nothing is left that reports to the shell (SPEC §5.5,
// §10). Where the terminal is not a HOTTY host it asks a question a line.
// Either way only the answers go to stdout:
//
//	answers=$(ask) && echo "$answers" | jq .env
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"os/signal"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/form"
	"github.com/neuroplastio/hotty-go/hottyterm"
)

// deploy is the form. form.Parse reads the same from JSON.
var deploy = &form.Spec{
	Title:  "Deploy",
	Submit: "Deploy",
	Fields: []form.Field{
		{Name: "service", Label: "Service", Placeholder: "api", Required: true},
		{Name: "env", Label: "Environment", Type: form.Select, Options: []string{"staging", "production"}, Default: "staging"},
		{Name: "replicas", Label: "Replicas", Type: form.Number, Default: 2.0},
		{Name: "notify", Label: "Tell the team", Type: form.Checkbox, Default: true},
	},
}

// Exit statuses.
const (
	ok        = 0
	noTTY     = 2
	cancelled = 130
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	open := func() (*hottyterm.Term, error) { return hottyterm.Open("ask") }
	os.Exit(run(ctx, os.Stdout, os.Stderr, open))
}

func run(ctx context.Context, stdout, stderr io.Writer, open func() (*hottyterm.Term, error)) int {
	// The questions go to the terminal whatever stdout is: a script reads
	// stdout, the person reads the terminal.
	t, err := open()
	if err != nil {
		fmt.Fprintln(stderr, "ask: no terminal to ask on")
		return noTTY
	}
	defer t.Close()
	native := t.Detect(ctx)
	restore, err := t.Raw()
	if err != nil {
		fmt.Fprintln(stderr, "ask:", err)
		return 1
	}
	in := &input{evs: t.Events(ctx), done: ctx.Done()}
	var answers *form.Answers
	if native {
		answers = askSurface(ctx, t, in)
	} else {
		answers = askCells(t, in)
	}
	restore() // before printing: raw mode does not return the carriage
	if answers == nil {
		return cancelled
	}
	b, _ := json.Marshal(answers)
	fmt.Fprintln(stdout, string(b))
	return ok
}

// input is the terminal's events, read one at a time.
type input struct {
	evs  <-chan hottyterm.Event
	done <-chan struct{}
}

// next is the next event; false when the input ends or the run is
// cancelled.
func (in *input) next() (hottyterm.Event, bool) {
	select {
	case ev, ok := <-in.evs:
		return ev, ok
	case <-in.done:
		return nil, false
	}
}

// quits reports the keys that cancel: Escape and Ctrl-C. In raw mode
// Ctrl-C is a key, not a signal.
func quits(ev hottyterm.Event) bool {
	k, ok := ev.(uv.KeyPressEvent)
	return ok && (k.String() == "esc" || k.String() == "ctrl+c")
}

// askSurface asks with the form as a surface.
func askSurface(ctx context.Context, t *hottyterm.Term, in *input) *form.Answers {
	_ = t.LineStart(ctx)
	name := t.Surface("form")
	cols := min(64, t.Size().Cols)
	// The form is the program's: it reports, and takes the keyboard.
	_ = t.Send(
		hotty.Doc(name, "<style>"+form.CSS+"</style>"+deploy.HTML("⏎")),
		hotty.Place(name, hotty.Placement{Cols: cols, Rows: deploy.Rows()}),
		hotty.Focus(name, deploy.First()),
	)
	var answers *form.Answers
	for answers == nil {
		ev, more := in.next()
		if !more || quits(ev) {
			break
		}
		m, isMsg := ev.(hottyterm.Message)
		if !isMsg {
			continue // other keys belong to the form's controls
		}
		e, isEv := m.Event()
		if !isEv || e.Surface != name || e.Kind != hotty.EventSubmit {
			continue
		}
		a, problems := deploy.Read(e.Fields())
		if len(problems) > 0 {
			// Shown in the form, which keeps what was typed; the keyboard
			// goes to the first field that is wrong.
			_ = t.Send(deploy.Show(name, problems)...)
			continue
		}
		answers = &a
	}
	// The form gives way to what was decided, detached: replacing a
	// document keeps its placement (SPEC §5.1).
	_ = t.Send(hotty.DocDetached(name, summary(answers)))
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hottyterm.FenceTimeout)
	defer cancel()
	_, _ = t.Fence(fctx)
	return answers
}

// summary is what stays in the scrollback.
func summary(a *form.Answers) string {
	style := `<style>
  .sum { margin: 0; padding: 4px 12px; font: 13px system-ui, sans-serif; color: var(--hotty-fg); }
  .sum dt { color: var(--hotty-ansi-8); float: left; width: 14ch; }
  .sum dd { margin: 0 0 2px 14ch; }
</style>`
	if a == nil {
		return style + `<p class="sum">Deploy cancelled.</p>`
	}
	var b strings.Builder
	b.WriteString(style + `<dl class="sum">`)
	for i, n := range a.Names() {
		fmt.Fprintf(&b, "<dt>%s</dt><dd>%s</dd>", html.EscapeString(deploy.Fields[i].Title()), html.EscapeString(fmt.Sprint(a.Get(n))))
	}
	b.WriteString(`</dl>`)
	return b.String()
}

// askCells asks a question a line, on a terminal that is not a host. It
// checks each answer as the form would (Field.Check), and asks again.
func askCells(t *hottyterm.Term, in *input) *form.Answers {
	fields := map[string]string{}
	for _, f := range deploy.Fields {
		for {
			prompt := f.Title()
			switch f.Kind() {
			case form.Select, form.Radio:
				prompt += " (" + strings.Join(f.Options, ", ") + ")"
			case form.Checkbox:
				prompt += " (y/n)"
			}
			if s := f.Start(); s != "" && f.Kind() != form.Checkbox {
				prompt += " [" + s + "]"
			}
			_ = t.Send(prompt + ": ")
			line, ok := readLine(t, in)
			if !ok {
				_ = t.Send("\r\n")
				return nil
			}
			v := strings.TrimSpace(line)
			if v == "" {
				v = f.Start()
			}
			if f.Kind() == form.Checkbox {
				switch strings.ToLower(v) {
				case "y", "yes", "true":
					v = "true"
				case "", "n", "no", "false":
					v = ""
				default:
					_ = t.Send("  ! y or n\r\n")
					continue
				}
			}
			if msg := f.Check(v); msg != "" {
				_ = t.Send("  ! " + msg + "\r\n")
				continue
			}
			if v != "" {
				fields[f.Name] = v
			}
			break
		}
	}
	a, _ := deploy.Read(fields)
	return &a
}

// readLine reads a line of keys, echoing them: Backspace deletes, Enter
// ends it. false when the user cancels or the input ends.
func readLine(t *hottyterm.Term, in *input) (string, bool) {
	var line []rune
	for {
		ev, more := in.next()
		if !more || quits(ev) {
			return "", false
		}
		k, isKey := ev.(uv.KeyPressEvent)
		if !isKey {
			continue
		}
		switch {
		case k.Code == uv.KeyEnter:
			_ = t.Send("\r\n")
			return string(line), true
		case k.Code == uv.KeyBackspace:
			if len(line) > 0 {
				line = line[:len(line)-1]
				_ = t.Send("\b \b")
			}
		case k.Text != "":
			line = append(line, []rune(k.Text)...)
			_ = t.Send(k.Text)
		}
	}
}
