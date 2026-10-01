// Card prints the result of a deploy as a card that stays in the
// scrollback, as a box of text where the terminal is not a HOTTY host, and
// as one line of data into a pipe.
//
// It shows the three renditions every HOTTY program has (SPEC §14): which
// one a run takes, and how each is made. The card is sent detached, links
// only as hyperlinks, so nothing in it reports to the shell after the
// program exits (SPEC §5.5).
//
//	card api v1.4.2 ok 42s
//	card api v1.4.3 failed 3s | cat
package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"os"
	"strings"
	"time"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/term"
)

// Deploy is what the card shows.
type Deploy struct {
	Service, Version string
	OK               bool
	Took             time.Duration
	Logs             string // a URL
}

// env is the process's streams and terminal: main fills it from the OS,
// a test from hottytest.
type env struct {
	stdout, stderr io.Writer
	tty            bool                       // stdout is the terminal
	open           func() (*term.Term, error) // the terminal, whatever the streams
}

func main() {
	e := env{stdout: os.Stdout, stderr: os.Stderr, tty: term.IsTerminal(os.Stdout),
		open: func() (*term.Term, error) { return term.Open("card") }}
	os.Exit(run(context.Background(), os.Args[1:], e))
}

func run(ctx context.Context, args []string, e env) int {
	d, err := parse(args)
	if err != nil {
		fmt.Fprintln(e.stderr, "card:", err)
		fmt.Fprintln(e.stderr, "usage: card SERVICE VERSION ok|failed DURATION")
		return 2
	}
	// Data: stdout is a pipe or a file. No escape codes, as `ls | cat`.
	if !e.tty {
		fmt.Fprintln(e.stdout, data(d))
		return 0
	}
	t, err := e.open()
	if err != nil {
		fmt.Fprintln(e.stdout, data(d))
		return 0
	}
	defer t.Close()
	// Cells: the terminal is not a host.
	if !t.Detect(ctx) {
		fmt.Fprint(e.stdout, cells(d))
		return 0
	}
	// Surfaces.
	_ = t.LineStart(ctx)
	if _, err := t.Print("card", page(d), hotty.Placement{Cols: min(60, t.Size().Cols), Rows: 4}); err != nil {
		fmt.Fprintln(e.stderr, "card:", err)
		return 1
	}
	if replies, err := t.Fence(ctx); err != nil || len(replies) > 0 {
		// The host refused the card: say what happened in text.
		fmt.Fprint(e.stdout, cells(d))
	}
	return 0
}

func parse(args []string) (Deploy, error) {
	if len(args) != 4 {
		return Deploy{}, fmt.Errorf("want 4 arguments, got %d", len(args))
	}
	took, err := time.ParseDuration(args[3])
	if err != nil {
		return Deploy{}, err
	}
	if args[2] != "ok" && args[2] != "failed" {
		return Deploy{}, fmt.Errorf("the status is ok or failed, not %q", args[2])
	}
	return Deploy{Service: args[0], Version: args[1], OK: args[2] == "ok", Took: took,
		Logs: "https://ci.example.com/deploys/" + args[0] + "/" + args[1]}, nil
}

func data(d Deploy) string {
	status := "failed"
	if d.OK {
		status = "ok"
	}
	return fmt.Sprintf("service=%s version=%s status=%s took=%s", d.Service, d.Version, status, d.Took)
}

// cells is the card in text: a box, and the status in colour and in a
// glyph, never colour alone.
func cells(d Deploy) string {
	mark, colour := "✓ deployed", "32"
	if !d.OK {
		mark, colour = "✗ failed", "31"
	}
	lines := []string{
		d.Service + " " + d.Version,
		"\x1b[" + colour + "m" + mark + "\x1b[m in " + d.Took.String(),
		d.Logs,
	}
	width := 0
	for _, l := range lines {
		width = max(width, visible(l))
	}
	var b strings.Builder
	b.WriteString("╭" + strings.Repeat("─", width+2) + "╮\n")
	for _, l := range lines {
		b.WriteString("│ " + l + strings.Repeat(" ", width-visible(l)) + " │\n")
	}
	b.WriteString("╰" + strings.Repeat("─", width+2) + "╯\n")
	return b.String()
}

// visible is a line's width in cells, its SGR sequences left out.
func visible(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc:
			esc = r != 'm'
		default:
			n++
		}
	}
	return n
}

// The card's document. html/template escapes what the program did not
// write. The colours come from the host stylesheet (SPEC §8), so the card
// matches the terminal's theme; the link is a hyperlink (target=_blank),
// which the terminal opens and never reports (SPEC §9).
var card = template.Must(template.New("card").Parse(`<style>
  .card { display: grid; grid-template-columns: auto 1fr auto; gap: 0 12px; align-items: center;
          height: 100vh; box-sizing: border-box; padding: 0 12px;
          border: 1px solid var(--hotty-ansi-8); border-radius: 8px; font-family: system-ui, sans-serif; }
  .mark { font-size: 26px; grid-row: span 2; }
  .ok   { color: var(--hotty-ansi-2); }
  .bad  { color: var(--hotty-ansi-1); }
  .name { font-weight: 600; }
  .dim  { color: var(--hotty-ansi-8); }
  a     { color: var(--hotty-ansi-4); grid-row: span 2; }
</style>
<div class="card">
  {{if .OK}}<span class="mark ok">✓</span>{{else}}<span class="mark bad">✗</span>{{end}}
  <span class="name">{{.Service}} {{.Version}}</span>
  <a id="logs" href="{{.Logs}}" target="_blank">logs</a>
  <span class="dim">{{if .OK}}deployed{{else}}failed{{end}} in {{.Took}}</span>
</div>`))

func page(d Deploy) string {
	var b strings.Builder
	_ = card.Execute(&b, d)
	return b.String()
}
