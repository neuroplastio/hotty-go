// Markdown shows a Markdown file among a command's output, its images and
// tables included, and leaves it in the scrollback.
//
//	markdown README.md
//	gh release view --json body -q .body | markdown
//
// The SDK has no Markdown in it: the program renders with a Markdown
// library (goldmark) and sends the HTML, the way it would any other. What
// is HOTTY's is around that:
//
//   - A surface has at most 1000 rows (SPEC §5.2), and one that fits the
//     screen can be seen whole: the document goes out in pages of about a
//     screen each, split between top-level blocks.
//   - An image is a resource (§7.1), sent before the page that shows it as
//     cid:<id>. Only images next to the file are read: a document must not
//     send /etc/passwd or ../.ssh/id_ed25519 to the terminal.
//   - A link with target="_blank" is a hyperlink the terminal opens itself
//     (§9); the pages are sent detached, so nothing in them reports to
//     whatever reads the terminal after the program exits (§5.5).
//   - 1rlh is one row (§8), so CSS can size an image in rows.
//
// Anywhere else, a pipe or a terminal that is not a HOTTY host, the
// Markdown itself is printed: it is written to be read as text.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyterm"
)

// imageRows is the most rows an image takes: style keeps it to that.
const imageRows = 12

// style is the pages' stylesheet. The host's own gives the terminal's font,
// colours and row height (SPEC §8).
var style = fmt.Sprintf(`<style>
img { max-width: 100%%; max-height: %drlh; }
pre { white-space: pre-wrap; }
table { border-collapse: collapse; }
th, td { padding: 0 1ch; }
</style>`, imageRows)

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
	asText := func() int {
		_, _ = e.stdout.Write(src)
		return 0
	}
	if !e.tty {
		return asText()
	}
	t, err := e.open()
	if err != nil {
		return asText() // a terminal we cannot talk to
	}
	defer t.Close()
	if !t.Detect(ctx) {
		return asText()
	}
	if err := show(ctx, t, src, images(files, dir)); err != nil {
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

// image is an image a document shows, read from its file.
type image struct {
	mime string
	data []byte
}

// images reads the images a document refers to, by names relative to dir:
// only below it, and only images.
func images(files fs.FS, dir string) func(string) (image, error) {
	return func(name string) (image, error) {
		name = path.Clean(name)
		if !fs.ValidPath(name) {
			return image{}, fs.ErrPermission
		}
		b, err := fs.ReadFile(files, path.Join(dir, name))
		if err != nil {
			return image{}, err
		}
		mime := http.DetectContentType(b)
		if path.Ext(name) == ".svg" && bytes.Contains(b[:min(len(b), 1024)], []byte("<svg")) {
			mime = "image/svg+xml"
		}
		if !strings.HasPrefix(mime, "image/") {
			return image{}, errors.New(name + " is not an image")
		}
		return image{mime, b}, nil
	}
}

// show sends the document a page a surface, under the command line, and
// waits for the host to take them.
func show(ctx context.Context, t *hottyterm.Term, src []byte, read func(string) (image, error)) error {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM)) // raw HTML is left out
	doc := md.Parser().Parse(text.NewReader(src))
	res := prepare(doc, src, t, read)

	size := t.Size()
	if err := t.LineStart(ctx); err != nil {
		return err
	}
	sent := map[string]bool{}
	for i, page := range pages(doc, src, size.Cols, min(max(10, size.Rows-2), hotty.MaxSize)) {
		var cmds []string
		var body bytes.Buffer
		for _, n := range page {
			// The page's images first; the same image twice is one resource.
			_ = ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
				if img, ok := n.(*ast.Image); ok && entering {
					id := strings.TrimPrefix(string(img.Destination), "cid:")
					if r, ok := res[id]; ok && !sent[id] {
						cmds = append(cmds, hotty.Res(id, r.mime, r.data, hotty.Q(hotty.ReplyOnError)))
						sent[id] = true
					}
				}
				return ast.WalkContinue, nil
			})
			if err := md.Renderer().Render(&body, src, n); err != nil {
				return err
			}
		}
		name := t.Surface(fmt.Sprintf("page%d", i))
		// Rows 0: the host makes the placement as tall as the page is.
		cmds = append(cmds, hotty.Doc(name, style+body.String(), hotty.Detached()), hotty.Place(name, hotty.Placement{Cols: size.Cols}))
		if err := t.Send(cmds...); err != nil {
			return err
		}
	}
	replies, err := t.Fence(ctx)
	for _, r := range replies {
		err = errors.Join(err, hotty.Err(r))
	}
	return err
}

// prepare makes the document's links hyperlinks, and its images resources:
// an image read becomes cid:<id>, one that is not its description, in
// brackets. It returns the resources by id.
func prepare(doc ast.Node, src []byte, t *hottyterm.Term, read func(string) (image, error)) map[string]image {
	res := map[string]image{}
	var unread []*ast.Image
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Link, *ast.AutoLink:
			n.SetAttributeString("target", "_blank")
			n.SetAttributeString("rel", "noopener noreferrer")
		case *ast.Image:
			u, err := url.Parse(string(n.Destination))
			if err != nil || u.Scheme != "" || u.Host != "" {
				unread = append(unread, n) // a host fetches nothing (SPEC §12)
				break
			}
			img, err := read(u.Path)
			if err != nil {
				unread = append(unread, n)
				break
			}
			sum := sha256.Sum256(img.data)
			id := t.Surface("img-" + hex.EncodeToString(sum[:8]))
			res[id] = img
			n.Destination = []byte("cid:" + id)
		}
		return ast.WalkContinue, nil
	})
	for _, n := range unread {
		n.Parent().ReplaceChild(n.Parent(), n, ast.NewString([]byte("["+alt(n, src)+"]")))
	}
	return res
}

// alt is an image's description, its text.
func alt(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := n.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// pages groups the document's top-level blocks into pages of about rows
// rows. A block taller than that is a page of its own.
func pages(doc ast.Node, src []byte, cols, rows int) [][]ast.Node {
	var all [][]ast.Node
	var cur []ast.Node
	n := 0
	for b := doc.FirstChild(); b != nil; b = b.NextSibling() {
		r := estimate(b, src, cols)
		if len(cur) > 0 && n+r > rows {
			all, cur, n = append(all, cur), nil, 0
		}
		cur, n = append(cur, b), n+r
	}
	if len(cur) > 0 {
		all = append(all, cur)
	}
	return all
}

// estimate is about the rows a top-level block takes: the lines of its
// source, wrapped at cols, an image each up to imageRows, and a row of
// margin. Only where a page ends depends on it: the host lays the page out.
func estimate(b ast.Node, src []byte, cols int) int {
	start, stop, imgs := len(src), 0, 0
	_ = ast.Walk(b, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			start, stop = min(start, n.Segment.Start), max(stop, n.Segment.Stop)
		case *ast.Image:
			imgs++
		}
		if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
			lines := n.Lines()
			start, stop = min(start, lines.At(0).Start), max(stop, lines.At(lines.Len()-1).Stop)
		}
		return ast.WalkContinue, nil
	})
	rows := 1 + imgs*imageRows
	if start < stop {
		for _, line := range strings.Split(string(src[start:stop]), "\n") {
			rows += 1 + utf8.RuneCountInString(line)/max(1, cols)
		}
	}
	return rows
}
