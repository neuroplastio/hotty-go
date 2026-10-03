# hotty-go

hotty-go is the Go SDK for [HOTTY](https://github.com/neuroplastio/hotty),
HTML Over The TTY. With it, a terminal program shows small HTML documents,
called surfaces, on rectangles of cells, and hears what the user does in
them. A surface can be a card left in the scrollback, a chart that streams,
a form that asks, or the panels of a full-screen Bubble Tea program.

The SDK:

- encodes the protocol;
- finds out whether the terminal is a HOTTY host;
- keeps surfaces on screen while a full-screen frame changes;
- converts Markdown, tables and images into documents;
- tests programs against a host that runs inside `go test`.

Every program has three renditions (SPEC §14): surfaces on a host, cells on
any other terminal, and plain data into a pipe. The examples show all three.

```go
t, err := hottyterm.Open("hello") // the terminal, whatever stdout is
if err != nil {
	fmt.Println("Hello.") // no terminal: a pipe, a cron job
	return
}
defer t.Close()
if !t.Detect(ctx) {
	fmt.Println("Hello.") // a terminal that is not a host
	return
}
_ = t.LineStart(ctx)
_, _ = t.Print("hello", `<p>Hello, <b>HTML</b> in the terminal.</p>`, hotty.Placement{Cols: 40, Rows: 1})
_, _ = t.Fence(ctx) // the host's replies, before the shell reads the terminal
```

Testing it needs no terminal:

```go
h := hottytest.New(t)
run(ctx, hottyterm.New(h, h, "hello", h.TermSize, nil))
h.Surface("hello-hello").Text() // "Hello, HTML in the terminal."
```

The reference is in [docs/api](docs/api) and on
[pkg.go.dev](https://pkg.go.dev/github.com/neuroplastio/hotty-go).

## Install

The SDK is a few modules, so that a program takes on only the dependencies
of what it imports:

| module | packages | needs |
| --- | --- | --- |
| `github.com/neuroplastio/hotty-go` | `hotty`, `chart`, `form`, `series`, `braille`, `blocks` | the standard library |
| `github.com/neuroplastio/hotty-go/hottyterm` | `hottyterm` | ultraviolet, for the terminal's input |
| `github.com/neuroplastio/hotty-go/hottytea` | `hottytea` | Bubble Tea |
| `github.com/neuroplastio/hotty-go/hottytest` | `hottytest` | x/net/html |
| `github.com/neuroplastio/hotty-go/hottyvt` | `hottyvt`, `asciicast` | x/vt, a terminal emulator; asciicast, the standard library |

```
go get github.com/neuroplastio/hotty-go github.com/neuroplastio/hotty-go/hottyterm
```

## Packages

<!-- docgen:packages -->
| package | what it is |
| --- | --- |
| [`hotty`](docs/api/hotty.md) | Package hotty speaks HOTTY, HTML Over The TTY (https://github.com/neuroplastio/hotty), from a Go program. |
| [`blocks`](docs/api/blocks.md) | Package blocks draws bars in terminal cells with the block elements (U+2581–U+2588): a column's height in eighths of a row, as a bar chart or a histogram in text has it. |
| [`braille`](docs/api/braille.md) | Package braille draws in terminal cells with the Unicode braille patterns (U+2800–U+28FF): each cell is two dots across and four down, so a line chart in text has four times the rows and twice the columns of its cells. |
| [`chart`](docs/api/chart.md) | Package chart draws line charts for HOTTY surfaces, as inline SVG that a program changes with deltas as values arrive, and in cells for a terminal that is not a host (Spark). |
| [`form`](docs/api/form.md) | Package form is a form for a HOTTY surface: a spec, the document it makes (real HTML controls, with ids a program can focus and change with deltas), and what a submit event brings back, read into typed answers. |
| [`hottytea`](docs/api/hottytea.md) | Package hottytea is HOTTY for a full-screen Bubble Tea program: it finds out whether the terminal is a HOTTY host, keeps the program's surfaces on screen as its frame changes, and turns what the host sends into messages. |
| [`hottyterm`](docs/api/hottyterm.md) | Package hottyterm is HOTTY for a program that is not a full-screen Bubble Tea program: a command that prints documents and exits, one that asks a question, a chart that streams. |
| [`hottytest`](docs/api/hottytest.md) | Package hottytest is a HOTTY host that runs inside a test. |
| [`hottyvt`](docs/api/hottyvt.md) | Package hottyvt shows a terminal's screen on a HOTTY surface: what a program would write to a terminal goes in, and HTML comes out, first as an element of a document and then as deltas for the rows that changed. |
| [`hottyvt/asciicast`](docs/api/hottyvt-asciicast.md) | Package asciicast reads terminal recordings in asciinema's asciicast format, versions 2 and 3: a header with the terminal's size, then what the recorded program wrote, each piece at the time it wrote it, with markers a recording may carry between them. |
| [`series`](docs/api/series.md) | Package series reads numbers from a stream of text, as plotting tools such as youplot and asciigraph do: lines of numbers separated by spaces, tabs or commas, a column a series, and a first line with no numbers naming them. |
<!-- /docgen:packages -->

## Examples

Each example is a program for one use case. It works in a HOTTY host, in
any other terminal, and into a pipe, and its tests run it in each.

The examples are a module of their own (`examples/go.mod`), built against
the SDK beside them: what they import is never the SDK's dependency. Run one
from there:

```
cd examples
go run ./dashboard https://example.com
```

<!-- docgen:examples -->
| example | use case | what it shows |
| --- | --- | --- |
| [ask](examples/ask) | Ask asks for what a deploy needs with a form, and prints the answers as JSON for the script that ran it. | It shows a surface the program reads events from: the form is the program's while it asks, takes the keyboard, reports a submit, and shows what is wrong without a round trip per key; then a detached summary replaces it, so nothing is left that reports to the shell (SPEC §5.5, §10). |
| [card](examples/card) | Card prints the result of a deploy as a card that stays in the scrollback, as a box of text where the terminal is not a HOTTY host, and as one line of data into a pipe. | It shows the three renditions every HOTTY program has (SPEC §14): which one a run takes, and how each is made. |
| [dashboard](examples/dashboard) | Dashboard watches HTTP endpoints, full screen: whether each is up, and its latency charted as the probes come back. | It is a Bubble Tea program, and each endpoint is a card on a surface that hottytea keeps in place as the frame changes. |
| [hello](examples/hello) | Hello prints a line of HTML in the terminal, and a line of text where the terminal cannot show HTML. | It is the smallest HOTTY program: open the terminal, ask whether it is a host, print a document at the cursor, and read the host's replies before exiting, so that none is left for the shell. |
| [livechart](examples/livechart) | Livechart charts the numbers a command prints, live, below the command line, and leaves the chart in the scrollback when the command ends. | series.Parser reads the numbers: a column a series, named by a first line with no numbers, or with -key the number after KEY=. |
| [markdown](examples/markdown) | Markdown shows a Markdown file among a command's output, its images and tables included, and leaves it in the scrollback. | The SDK has no Markdown in it: the program renders with a Markdown library (goldmark) and sends the HTML, the way it would any other. |
| [progress](examples/progress) | Progress shows a task's progress as a bar that moves in place, and leaves its last state in the scrollback. | It shows the cheap way to change a surface many times a second: a custom property moves the bar (hotty.SetVar) and text deltas change the labels (hotty.SetText), a few dozen bytes each, in synchronized output so the host shows them together (SPEC §6). |
| [replay](examples/replay) | Replay plays a terminal session back on a surface, at the size it was recorded and scaled to fit, and leaves its last frame in the scrollback. | It shows a terminal inside a document: hottyvt keeps a terminal emulator of the session's size, apart from the terminal the program runs in, and sends its screen as HTML, then a delta per row that changes, in synchronized output so the host shows each frame whole (SPEC §6). |
<!-- /docgen:examples -->

## Documentation

The API reference in [docs/api](docs/api) is written from the code by
`internal/docgen`: the doc comments, the Example functions, and the
examples' package comments. `make docs` writes it again, as does
`go generate ./...`. The tables above are written the same way, between
their markers; the rest of this file is not. CI fails when what is checked
in is not what `make docs` would write, and when an exported identifier has
no doc comment.

## Development

The toolchain is in `mise.toml`: run `mise install` first.

- `make check` is the gate: formatting, tidy modules, vet, staticcheck,
  the tests with the race detector, coverage, and the documentation being
  current, in every module.
- The modules build against each other as they are in the repository:
  each nested `go.mod` replaces the others with their directories. What a
  program that uses the SDK gets are the requirements between them, which
  `make pin REV=…` sets: before a release to a pushed commit, and for a
  release to its version (`make pin REV=v0.1.0`), committed, and then every
  module tagged at that commit (`v0.1.0`, `hottyterm/v0.1.0`, …).
- `make cover` runs the tests with coverage, prints a table by package, and
  fails when a public package is under `COVER_MIN` (85%).
- `make docs` writes the documentation.
- `make vectors` copies the conformance vectors from a checkout of
  [neuroplastio/hotty](https://github.com/neuroplastio/hotty)
  (`HOTTY_DIR`, by default `../../hotty/main`).

Licensed under Apache-2.0.
