package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/doc/comment"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// The README's generated sections: the text between a start and an end
// marker is docgen's; the rest is written by hand.
const (
	packagesStart = "<!-- docgen:packages -->"
	packagesEnd   = "<!-- /docgen:packages -->"
	examplesStart = "<!-- docgen:examples -->"
	examplesEnd   = "<!-- /docgen:examples -->"
)

// Generate is every file docgen writes, by path relative to the root:
// docs/api/*.md, and README.md with its sections filled.
func (m *Module) Generate() (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, p := range m.Packages {
		files[p.Page()] = append(m.newRenderer(p).page(), '\n')
	}
	files["docs/api/README.md"] = m.apiIndex()

	readme, err := os.ReadFile(filepath.Join(m.Root, "README.md"))
	if err != nil {
		return nil, err
	}
	readme, err = replaceSection(readme, packagesStart, packagesEnd, m.packagesTable("docs/api/"))
	if err != nil {
		return nil, fmt.Errorf("README.md: %w", err)
	}
	readme, err = replaceSection(readme, examplesStart, examplesEnd, m.examplesTable())
	if err != nil {
		return nil, fmt.Errorf("README.md: %w", err)
	}
	files["README.md"] = readme
	return files, nil
}

// synopsis is a package's first sentence as text, as go/doc has it, with
// doc links to the module's other packages resolved (go/doc knows only the
// packages a package imports).
func (m *Module) synopsis(p *Package) string {
	text := firstSentence(strings.Join(strings.Fields(p.Doc.Doc), " "))
	d := m.newRenderer(p).parser.Parse(text)
	if len(d.Content) == 0 {
		return ""
	}
	if _, ok := d.Content[0].(*comment.Paragraph); !ok {
		return ""
	}
	d.Content = d.Content[:1]
	pr := &comment.Printer{TextWidth: -1}
	return strings.TrimSpace(string(pr.Text(d)))
}

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// packagesTable is a table of the packages, linking to their pages under
// prefix.
func (m *Module) packagesTable(prefix string) string {
	var b strings.Builder
	b.WriteString("| package | what it is |\n| --- | --- |\n")
	for _, p := range m.Packages {
		fmt.Fprintf(&b, "| [`%s`](%s%s) | %s |\n", p.Short(), prefix, pageName(p.Rel, p.Doc.Name), cell(m.synopsis(p)))
	}
	return b.String()
}

func (m *Module) apiIndex() []byte {
	var b strings.Builder
	b.WriteString(notice)
	b.WriteString("\n# API reference\n\n")
	b.WriteString("A page for each package of `" + m.Path + "`, written from the code by `internal/docgen` " +
		"(`make docs`). The same documentation is on [pkg.go.dev](https://pkg.go.dev/" + m.Path + ").\n\n")
	b.WriteString(m.packagesTable(""))
	return []byte(b.String())
}

// examplesTable is a table of the example programs: the first sentence of
// each one's package comment is its use case, the next what it shows.
func (m *Module) examplesTable() string {
	if len(m.Examples) == 0 {
		return "No examples yet.\n"
	}
	var b strings.Builder
	b.WriteString("| example | use case | what it shows |\n| --- | --- | --- |\n")
	for _, e := range m.Examples {
		text := strings.Join(strings.Fields(e.Doc), " ")
		useCase := firstSentence(text)
		shows := firstSentence(strings.TrimSpace(text[len(useCase):]))
		name := strings.TrimPrefix(e.Dir, "examples/")
		fmt.Fprintf(&b, "| [%s](%s) | %s | %s |\n", name, e.Dir, cell(useCase), cell(shows))
	}
	return b.String()
}

// firstSentence is the text up to the first period followed by a space
// that does not end an initial, as go/doc finds a synopsis.
func firstSentence(s string) string {
	var ppp, pp, p rune
	for i, q := range s {
		if q == '\n' || q == '\r' || q == '\t' {
			q = ' '
		}
		if q == ' ' && (p == '.' || p == '!' || p == '?') && (!unicode.IsUpper(pp) || unicode.IsUpper(ppp)) {
			return s[:i]
		}
		ppp, pp, p = pp, p, q
	}
	return s
}

// replaceSection puts body between the start and end markers, which must
// each be there once, in that order.
func replaceSection(src []byte, start, end, body string) ([]byte, error) {
	i := bytes.Index(src, []byte(start))
	j := bytes.Index(src, []byte(end))
	switch {
	case i < 0:
		return nil, errors.New("no " + start + " marker")
	case j < 0:
		return nil, errors.New("no " + end + " marker")
	case j < i:
		return nil, errors.New(end + " comes before " + start)
	case bytes.Count(src, []byte(start)) > 1 || bytes.Count(src, []byte(end)) > 1:
		return nil, errors.New("more than one " + start + " section")
	}
	var b bytes.Buffer
	b.Write(src[:i+len(start)])
	b.WriteString("\n" + strings.TrimRight(body, "\n") + "\n")
	b.Write(src[j:])
	return b.Bytes(), nil
}

// Change is a file that differs from what docgen writes.
type Change struct {
	Path string
	Kind string // "new", "changed" or "removed"
}

func (c Change) String() string { return c.Kind + " " + c.Path }

// Changes compares files with what is on disk under root, and lists what
// writing them would change: new and changed files, and pages in docs/api
// that no package has any more.
func Changes(root string, files map[string][]byte) ([]Change, error) {
	var out []Change
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		have, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out = append(out, Change{p, "new"})
		case err != nil:
			return nil, err
		case !bytes.Equal(have, files[p]):
			out = append(out, Change{p, "changed"})
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "docs", "api"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		p := "docs/api/" + e.Name()
		if _, ok := files[p]; !ok && !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, Change{p, "removed"})
		}
	}
	return out, nil
}

// Write makes the changes on disk.
func Write(root string, files map[string][]byte, changes []Change) error {
	for _, c := range changes {
		path := filepath.Join(root, filepath.FromSlash(c.Path))
		if c.Kind == "removed" {
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[c.Path], 0o644); err != nil {
			return err
		}
	}
	return nil
}
