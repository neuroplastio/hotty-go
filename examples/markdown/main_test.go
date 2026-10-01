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
	"github.com/neuroplastio/hotty-go/hottyterm"
	"github.com/neuroplastio/hotty-go/hottytest"
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
		open: func() (*hottyterm.Term, error) { return hottyterm.New(h, h, "markdown", h.TermSize, nil), nil }}, &errs
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
	if len(ids) != 1 || !strings.HasPrefix(ids[0], "markdown-img-") {
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
	// Each shows its description instead.
	if s := h.Surface("markdown-page0"); s == nil || s.Text() != "[a] [b] [c] [d]" {
		t.Errorf("the page: %v", s)
	}
}

// What the document cannot show stays out of the page: raw HTML, which
// could be a form, and a link that would run script. A web image is its
// description: a host fetches nothing (SPEC §12).
func TestLeftOut(t *testing.T) {
	h := hottytest.New(t)
	e, errs := on(h)
	e.stdin = strings.NewReader("<form><button id=b>Delete</button></form>\n\n" +
		"[run](javascript:alert(1)) ![logo](https://example.com/logo.png)\n")
	if code := run(context.Background(), nil, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	html := h.Surface("markdown-page0").HTML()
	for _, bad := range []string{"<form", "<button", "javascript:", "https://example.com/logo.png"} {
		if strings.Contains(html, bad) {
			t.Errorf("the page has %s:\n%s", bad, html)
		}
	}
	if !strings.Contains(html, "[logo]") {
		t.Errorf("no description for the image:\n%s", html)
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

// On a terminal that is not a host: the Markdown, as it is.
func TestCells(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	e, _ := on(h)
	if code := run(context.Background(), []string{"docs/guide.md"}, e); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := h.Output(); !strings.HasSuffix(got, guide) {
		t.Errorf("the output:\n%q\nwant it to end with:\n%q", got, guide)
	}
	if len(h.Surfaces()) != 0 {
		t.Errorf("surfaces: %v", h.Surfaces())
	}
}

// Into a pipe: the Markdown, from stdin.
func TestPipe(t *testing.T) {
	var out, errs strings.Builder
	e := env{stdin: strings.NewReader(guide), stdout: &out, stderr: &errs, files: files}
	if code := run(context.Background(), []string{"-"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if got := out.String(); got != guide {
		t.Errorf("stdout: %q", got)
	}
}

// A terminal that cannot be opened is written to as any other.
func TestNoTerminal(t *testing.T) {
	var out strings.Builder
	e := env{stdin: strings.NewReader("# Hi\n"), stdout: &out, stderr: &out, files: files, tty: true,
		open: func() (*hottyterm.Term, error) { return nil, hottyterm.ErrNoTerminal }}
	if code := run(context.Background(), nil, e); code != 0 || out.String() != "# Hi\n" {
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
