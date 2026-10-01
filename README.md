# hotty-go

hotty-go is the Go SDK for [HOTTY](https://github.com/neuroplastio/hotty),
HTML Over The TTY: a terminal program shows small HTML documents, surfaces,
on rectangles of cells, and hears what the user does in them. The SDK
encodes the protocol, finds out whether the terminal is a HOTTY host, keeps
surfaces on screen, and tests programs against a host that runs in the test.
The reference is also on
[pkg.go.dev](https://pkg.go.dev/github.com/neuroplastio/hotty-go).

## Install

```
go get github.com/neuroplastio/hotty-go
```

## Packages

<!-- docgen:packages -->
| package | what it is |
| --- | --- |
| [`hotty`](docs/api/hotty.md) | Package hotty speaks HOTTY, HTML Over The TTY (https://github.com/neuroplastio/hotty), from a Go program. |
| [`blocks`](docs/api/blocks.md) | Package blocks draws bars in terminal cells with the block elements (U+2581–U+2588): a column's height in eighths of a row, as a bar chart or a histogram in text has it. |
| [`braille`](docs/api/braille.md) | Package braille draws in terminal cells with the Unicode braille patterns (U+2800–U+28FF): each cell is two dots across and four down, so a line chart in text has four times the rows and twice the columns of its cells. |
| [`chart`](docs/api/chart.md) | Package chart draws line charts for HOTTY surfaces, as inline SVG that a program patches as values arrive, and in cells for a terminal that is not a host (Spark). |
| [`doc`](docs/api/doc.md) | Package doc turns files into HOTTY documents: Markdown, HTML, CSV and TSV, JSON, images and plain text become blocks of HTML that a program places as surfaces (SPEC §5), with the resources they refer to (§7.1). |
| [`form`](docs/api/form.md) | Package form is a form for a HOTTY surface: a spec, the document it makes (real HTML controls, with ids a program can focus and patch), and what a submit event brings back, read into typed answers. |
| [`hottytea`](docs/api/hottytea.md) | Package hottytea is HOTTY for a full-screen Bubble Tea program: it finds out whether the terminal is a HOTTY host, keeps the program's surfaces on screen as its frame changes, and turns what the host sends into messages. |
| [`hottytest`](docs/api/hottytest.md) | Package hottytest is a HOTTY host that runs inside a test. |
| [`series`](docs/api/series.md) | Package series reads numbers from a stream of text, as plotting tools such as youplot and asciigraph do: lines of numbers separated by spaces, tabs or commas, a column a series, and a first line with no numbers naming them. |
| [`term`](docs/api/term.md) | Package term is HOTTY for a program that is not a full-screen Bubble Tea program: a command that prints documents and exits, one that asks a question, a chart that streams. |
<!-- /docgen:packages -->

## Examples

Each example is a program for one use case. It works in a HOTTY host, in
any other terminal, and into a pipe.

<!-- docgen:examples -->
| example | use case | what it shows |
| --- | --- | --- |
| [hello](examples/hello) | Hello prints a line of HTML in the terminal, and a line of text where the terminal cannot show HTML. | It is the smallest HOTTY program: open the terminal, ask whether it is a host, print a document at the cursor, and read the host's replies before exiting, so that none is left for the shell. |
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

- `make check` is the gate: formatting, vet, the tests with the race
  detector, coverage, and the documentation being current.
- `make cover` runs the tests with coverage, prints a table by package, and
  fails when a public package is under `COVER_MIN` (85%).
- `make docs` writes the documentation.
- `make vectors` copies the conformance vectors from a checkout of
  [neuroplastio/hotty](https://github.com/neuroplastio/hotty)
  (`HOTTY_DIR`, by default `../../hotty/main`).

Licensed under Apache-2.0.
