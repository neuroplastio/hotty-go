package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottytest"
	"github.com/neuroplastio/hotty-go/term"
)

const guide = "# Deploys\n\n" +
	"Run `deploy` with an environment; the [runbook](https://example.com/runbook) has the rest.\n\n" +
	"![how a deploy goes](img/flow.svg)\n\n" +
	"| env | region |\n| --- | --- |\n| staging | eu-west |\n| production | us-east |\n\n" +
	"- build\n- push\n\n" +
	"```\ndeploy api production\n```\n"

const flow = `<svg xmlns="http://www.w3.org/2000/svg" width="360" height="72"><rect width="360" height="72"/></svg>`

var files = fstest.MapFS{
	"docs/guide.md":     {Data: []byte(guide)},
	"docs/img/flow.svg": {Data: []byte(flow)},
	"secret.png":        {Data: []byte("\x89PNG\r\n\x1a\nsecret")},
	"docs/notes.txt":    {Data: []byte("not an image")},
}

// on is the program's environment on h: stdout is the terminal.
func on(h *hottytest.Host) (env, *strings.Builder) {
	var errs strings.Builder
	return env{stdin: strings.NewReader(""), stdout: h, stderr: &errs, files: files, tty: true,
		open: func() (*term.Term, error) { return h.Term("markdown"), nil }}, &errs
}

// resources are the resources the host was sent.
func resources(h *hottytest.Host) []string {
	var ids []string
	for _, c := range h.Commands() {
		if c.Get("a") == "res" {
			ids = append(ids, c.Get("id"))
		}
	}
	return ids
}

func TestSurface(t *testing.T) {
	h := hottytest.New(t, hottytest.Size(80, 40))
	e, errs := on(h)
	if code := run(context.Background(), []string{"docs/guide.md"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	s := h.Surface("markdown-page0")
	if s == nil || len(h.Surfaces()) != 1 {
		t.Fatalf("surfaces: %v", h.Surfaces())
	}
	if p := s.Placement(); !s.Detached() || !s.Placed() || p.Cols != 80 {
		t.Errorf("detached %v, placed %v, %+v", s.Detached(), s.Placed(), p)
	}
	// The image went first, as a resource the page refers to.
	ids := resources(h)
	if len(ids) != 1 || !strings.HasPrefix(ids[0], "markdown-res-img-") {
		t.Fatalf("resources: %v", ids)
	}
	if mime, data, _ := h.Resource(ids[0]); mime != "image/svg+xml" || string(data) != flow {
		t.Errorf("the image: %s, %q", mime, data)
	}
	html := s.HTML()
	for _, want := range []string{`src="cid:` + ids[0] + `"`, `href="https://example.com/runbook"`, `target="_blank"`, "<table", "<pre"} {
		if !strings.Contains(html, want) {
			t.Errorf("the page has no %s:\n%s", want, html)
		}
	}
	if text := s.Text(); !strings.HasPrefix(text, "Deploys Run deploy with an environment") {
		t.Errorf("the text: %q", text)
	}
}

// A document taller than the screen is pages, each a surface that fits,
// in order; an image on two pages is sent once.
func TestPages(t *testing.T) {
	var md strings.Builder
	for i := range 30 {
		fmt.Fprintf(&md, "## Step %d\n\nSome words about step %d, and why it matters.\n\n", i, i)
		if i%10 == 0 {
			md.WriteString("![flow](img/flow.svg)\n\n")
		}
	}
	h := hottytest.New(t, hottytest.Size(80, 24))
	e, errs := on(h)
	e.stdin = strings.NewReader(md.String())
	e.files = fstest.MapFS{"img/flow.svg": files["docs/img/flow.svg"]}
	if code := run(context.Background(), nil, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	pages := h.Surfaces()
	if len(pages) < 3 {
		t.Fatalf("%d pages", len(pages))
	}
	for i, s := range pages {
		if s.Name() != fmt.Sprintf("markdown-page%d", i) {
			t.Errorf("page %d is %s", i, s.Name())
		}
	}
	if !strings.HasPrefix(pages[0].Text(), "Step 0 ") || !strings.Contains(pages[len(pages)-1].Text(), "Step 29 ") {
		t.Errorf("first %q, last %q", pages[0].Text(), pages[len(pages)-1].Text())
	}
	if ids := resources(h); len(ids) != 1 {
		t.Errorf("resources: %v", ids)
	}
}

// A document reads images next to it, and nothing else.
func TestImagesStayBelow(t *testing.T) {
	h := hottytest.New(t)
	e, errs := on(h)
	e.files = fstest.MapFS{
		"docs/a.md":      {Data: []byte("![a](../secret.png) ![b](/secret.png) ![c](notes.txt) ![d](missing.png)\n")},
		"secret.png":     files["secret.png"],
		"docs/notes.txt": files["docs/notes.txt"],
	}
	if code := run(context.Background(), []string{"docs/a.md"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if ids := resources(h); len(ids) != 0 {
		t.Errorf("resources sent: %v", ids)
	}
	if h.Surface("markdown-page0") == nil {
		t.Error("no page")
	}
}

// A file outside the current directory: its images are next to it.
func TestFileElsewhere(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{"a.md": "![flow](flow.svg)\n", "flow.svg": flow} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := hottytest.New(t)
	e, errs := on(h)
	if code := run(context.Background(), []string{filepath.Join(dir, "a.md")}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if ids := resources(h); len(ids) != 1 {
		t.Errorf("resources: %v", ids)
	}
}

// When the host refuses the image, the program says so, and fails.
func TestRefused(t *testing.T) {
	caps := hottytest.DefaultCaps()
	caps.Limits = map[string]int{"resources": 16}
	h := hottytest.New(t, hottytest.Caps(caps))
	e, errs := on(h)
	if code := run(context.Background(), []string{"docs/guide.md"}, e); code != 1 || !strings.Contains(errs.String(), hotty.EQUOTA) {
		t.Errorf("exit %d: %q", code, errs.String())
	}
}

// On a terminal that is not a host: the text, headings bold, an image its
// description.
func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	e, _ := on(h)
	if code := run(context.Background(), []string{"docs/guide.md"}, e); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "Deploys\n\nRun deploy with an environment; the runbook has the rest.\n\n[how a deploy goes]\n\n" +
		"env  region\nstaging  eu-west\nproduction  us-east\n\n  • build\n  • push\n\n    deploy api production"
	if got := h.Screen(); !strings.HasSuffix(got, want) {
		t.Errorf("the screen:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(h.Output(), "\x1b[1mDeploys\x1b[m") {
		t.Error("the heading is not bold")
	}
}

// Into a pipe: the same text, without escape codes; and from stdin.
func TestPipe(t *testing.T) {
	var out, errs strings.Builder
	e := env{stdin: strings.NewReader(guide), stdout: &out, stderr: &errs, files: files}
	if code := run(context.Background(), []string{"-"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if got := out.String(); !strings.HasPrefix(got, "Deploys\n\nRun deploy") || strings.Contains(got, "\x1b") {
		t.Errorf("stdout: %q", got)
	}
}

// A terminal that cannot be opened is written to as any other.
func TestNoTerminal(t *testing.T) {
	var out strings.Builder
	e := env{stdin: strings.NewReader("# Hi\n"), stdout: &out, stderr: &out, files: files, tty: true,
		open: func() (*term.Term, error) { return nil, term.ErrNoTerminal }}
	if code := run(context.Background(), nil, e); code != 0 || out.String() != "\x1b[1mHi\x1b[m\n" {
		t.Errorf("exit %d: %q", code, out.String())
	}
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"a.md", "b.md"}, 2, "usage: markdown [FILE]"},
		{[]string{"-h"}, 2, "usage: markdown [FILE]"},
		{[]string{"nope.md"}, 1, "markdown: open nope.md: file does not exist"},
	} {
		var errs strings.Builder
		e := env{stdout: &errs, stderr: &errs, files: files}
		if code := run(context.Background(), tc.args, e); code != tc.code || !strings.Contains(errs.String(), tc.want) {
			t.Errorf("%q: exit %d, %q", tc.args, code, errs.String())
		}
	}
}
