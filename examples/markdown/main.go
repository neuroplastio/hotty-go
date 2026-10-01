// Markdown shows a Markdown file among a command's output, its images and
// tables included, and leaves it in the scrollback.
//
//	markdown README.md
//	gh release view --json body -q .body | markdown
//
// hottydoc.Markdown converts the file into blocks; hottydoc.Pages groups them into
// surfaces that each fit the screen, so a host can show each whole. A
// page's images go first, as resources (hotty.Res), and the page is sent
// detached: its links are hyperlinks the terminal opens itself, and
// nothing in it reports to the shell (SPEC §5.5, §7). On a terminal that
// is not a HOTTY host the text is printed with bold headings and bullets,
// and into a pipe as plain text.
//
// Images are read next to the file (the current directory for stdin), and
// never from above it.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottydoc"
	"github.com/neuroplastio/hotty-go/hottyterm"
)

// env is the process's streams, files and terminal: main fills it from
// the OS, a test from hottytest.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	files          fs.FS                           // the file system, from its root or the current directory
	tty            bool                            // stdout is the terminal
	open           func() (*hottyterm.Term, error) // the terminal, whatever the streams
}

func main() {
	e := env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, files: os.DirFS("."),
		tty: hottyterm.IsTerminal(os.Stdout), open: func() (*hottyterm.Term, error) { return hottyterm.Open("markdown") }}
	os.Exit(run(context.Background(), os.Args[1:], e))
}

func run(ctx context.Context, args []string, e env) int {
	if len(args) > 1 || (len(args) == 1 && strings.HasPrefix(args[0], "-") && args[0] != "-") {
		fmt.Fprintln(e.stderr, "usage: markdown [FILE]")
		return 2
	}
	src, files, dir, err := read(args, e)
	if err != nil {
		fmt.Fprintln(e.stderr, "markdown:", err)
		return 1
	}
	o := hottydoc.Options{ReadFile: images(files, dir)}

	// Into a pipe: the text, without escape codes.
	if !e.tty {
		printText(e.stdout, hottydoc.Markdown(src, o), false)
		return 0
	}
	t, err := e.open()
	if err != nil {
		printText(e.stdout, hottydoc.Markdown(src, o), true) // a terminal we cannot talk to
		return 0
	}
	defer t.Close()
	if !t.Detect(ctx) {
		printText(e.stdout, hottydoc.Markdown(src, o), true)
		return 0
	}
	if err := show(ctx, t, src, o); err != nil {
		fmt.Fprintln(e.stderr, "markdown:", err)
		return 1
	}
	return 0
}

// read is the Markdown: the file named, or stdin. Its images are in dir,
// in files.
func read(args []string, e env) (src []byte, files fs.FS, dir string, err error) {
	if len(args) == 0 || args[0] == "-" {
		src, err = io.ReadAll(e.stdin)
		return src, e.files, ".", err
	}
	files, name := e.files, filepath.ToSlash(filepath.Clean(args[0]))
	if !fs.ValidPath(name) {
		// Above the current directory, or absolute: the file system from
		// the file's own directory.
		abs, err := filepath.Abs(args[0])
		if err != nil {
			return nil, nil, "", err
		}
		files, name = os.DirFS(filepath.Dir(abs)), filepath.Base(abs)
	}
	src, err = fs.ReadFile(files, name)
	return src, files, path.Dir(name), err
}

// images reads the files a document refers to, by names relative to dir:
// only below it, and only images, so that a document cannot send what is
// not its own, /etc/passwd or ../.ssh/id_ed25519, to the terminal.
func images(files fs.FS, dir string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		name = path.Clean(name)
		if !fs.ValidPath(name) {
			return nil, fs.ErrPermission
		}
		b, err := fs.ReadFile(files, path.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if !hottydoc.IsImage(b) && !hottydoc.IsSVG(b) {
			return nil, errors.New(name + " is not an image")
		}
		return b, nil
	}
}

// show sends the document a page a surface, under the command line, and
// waits for the host to take them.
func show(ctx context.Context, t *hottyterm.Term, src []byte, o hottydoc.Options) error {
	caps := t.Caps()
	o.Cols, o.Screen = t.Size().Cols, t.Size().Rows
	o.CellW, o.CellH = caps.CellCSS()
	o.ResPrefix = t.Surface("res")
	d := hottydoc.Markdown(src, o)
	if err := t.LineStart(ctx); err != nil {
		return err
	}
	sent := map[string]bool{}
	for i, page := range hottydoc.Pages(d.Blocks, o.PageRows()) {
		var cmds []string
		for _, r := range hottydoc.Resources(page) {
			if !sent[r.ID] { // the same image twice is one resource
				cmds = append(cmds, hotty.Res(r.ID, r.Type, r.Data, hotty.Q(hotty.ReplyOnError)))
				sent[r.ID] = true
			}
		}
		name := t.Surface(fmt.Sprintf("page%d", i))
		cols, rows := d.Cols, d.Rows // 0 and 0: the width, and the rows the content needs
		if cols == 0 {
			cols = o.Cols
		}
		cmds = append(cmds, hotty.DocDetached(name, d.Page(page)), hotty.Place(name, hotty.Placement{Cols: cols, Rows: rows}))
		if err := t.Send(cmds...); err != nil {
			return err
		}
	}
	if d.Rest != "" {
		_ = t.Send(d.Rest + "\r\n")
	}
	replies, err := t.Fence(ctx)
	for _, r := range replies {
		err = errors.Join(err, r.Err())
	}
	return err
}

// printText prints the document's text: a paragraph a line, a blank line
// between blocks; with style, its headings bold.
func printText(w io.Writer, d *hottydoc.Doc, style bool) {
	bold := func(s string) string {
		if style {
			return "\x1b[1m" + s + "\x1b[m"
		}
		return s
	}
	prev := ""
	for _, p := range hottydoc.Paragraphs([]byte(d.Body(d.Blocks))) {
		// Items of a list, rows of a table and a term's definitions go
		// together; any other paragraph after a blank line.
		together := (p.Kind == prev && (p.Kind == "li" || p.Kind == "td")) || (prev == "dt" && p.Kind == "dd")
		if prev != "" && !together {
			fmt.Fprintln(w)
		}
		switch p.Kind {
		case "h1", "h2", "h3", "h4", "h5", "h6", "dt":
			fmt.Fprintln(w, bold(p.Text))
		case "li":
			fmt.Fprintln(w, "  • "+p.Text)
		case "pre":
			fmt.Fprintln(w, "    "+strings.ReplaceAll(strings.TrimRight(p.Text, "\n"), "\n", "\n    "))
		case "dd":
			fmt.Fprintln(w, "    "+p.Text)
		default:
			fmt.Fprintln(w, p.Text)
		}
		prev = p.Kind
	}
	if d.Rest != "" {
		fmt.Fprintln(w, d.Rest)
	}
}
