package main

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Module is the module the documentation is about.
type Module struct {
	Root string // its directory
	Path string // its module path
	// Packages are its public packages: the root's first, then by import
	// path.
	Packages []*Package
	// Examples are the programs in examples/, by directory.
	Examples []*Program
	// names maps a package name to its import path, for doc links to
	// packages a comment's package does not import.
	names map[string]string
}

// Package is one public package, read.
type Package struct {
	Rel        string // its directory, slash-separated, "" for the root
	ImportPath string
	Doc        *doc.Package
	Fset       *token.FileSet
	// imports maps the names this package's files import to their paths.
	imports map[string]string
}

// Page is the page of the package in docs/api, relative to the root.
func (p *Package) Page() string { return "docs/api/" + pageName(p.Rel, p.Doc.Name) }

// Short is the package as the module's own tables name it: the root
// package's name, or its directory.
func (p *Package) Short() string {
	if p.Rel == "" {
		return p.Doc.Name
	}
	return p.Rel
}

// pageName is a package's page in docs/api: the root package's name, or
// its directory with slashes made dashes.
func pageName(rel, name string) string {
	if rel == "" {
		return name + ".md"
	}
	return strings.ReplaceAll(rel, "/", "-") + ".md"
}

// Program is one example program, examples/<dir>/main.go.
type Program struct {
	Dir string // relative to the root, slash-separated
	Doc string // its package comment
}

// buildContext is the one platform every page is written for, so that the
// output does not depend on where it runs.
func buildContext() build.Context {
	c := build.Default
	c.GOOS, c.GOARCH, c.CgoEnabled = "linux", "amd64", false
	c.BuildTags = nil
	return c
}

// skipDir reports the directories that hold no public package: internal
// ones, test data, examples, and hidden or underscored ones.
func skipDir(rel, name string) bool {
	if rel == "." {
		return false
	}
	return name == "internal" || name == "testdata" || name == "examples" || name == "vendor" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Load reads the module at root: its path, its public packages, those of
// the modules nested in it under its path, and its examples.
func Load(root string) (*Module, error) {
	modPath, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	m := &Module{Root: root, Path: modPath, names: map[string]string{}}
	ctx := buildContext()
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if skipDir(rel, d.Name()) {
			return filepath.SkipDir
		}
		if rel != "." {
			// A module in a directory is the repository's, and documented
			// with it, when its path is the root's and the directory: the
			// packages with dependencies of their own (term, hottytea, …).
			// Any other is someone else's.
			if nested, err := modulePath(filepath.Join(p, "go.mod")); err == nil &&
				nested != modPath+"/"+filepath.ToSlash(rel) {
				return filepath.SkipDir
			}
		}
		pkg, err := loadPackage(&ctx, root, filepath.ToSlash(rel), modPath)
		if err != nil {
			return err
		}
		if pkg != nil {
			m.Packages = append(m.Packages, pkg)
			m.names[pkg.Doc.Name] = pkg.ImportPath
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(m.Packages, func(i, j int) bool {
		a, b := m.Packages[i], m.Packages[j]
		if (a.Rel == "") != (b.Rel == "") {
			return a.Rel == ""
		}
		return a.ImportPath < b.ImportPath
	})
	m.Examples, err = loadExamples(root)
	return m, err
}

// loadPackage reads the package in a directory, with its test files for
// their examples. It returns nil for a directory with no Go package, or a
// command.
func loadPackage(ctx *build.Context, root, rel, modPath string) (*Package, error) {
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if rel == "." {
		rel = ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	name := ""
	imports := map[string]string{}
	for _, e := range entries {
		fn := e.Name()
		if e.IsDir() || !strings.HasSuffix(fn, ".go") {
			continue
		}
		if ok, err := ctx.MatchFile(dir, fn); err != nil || !ok {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, fn), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(fn, "_test.go") {
			if name != "" && f.Name.Name != name {
				return nil, fmt.Errorf("%s: packages %s and %s", dir, name, f.Name.Name)
			}
			name = f.Name.Name
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				n := assumedName(p)
				if imp.Name != nil {
					n = imp.Name.Name
				}
				imports[n] = p
			}
		}
		files = append(files, f)
	}
	if name == "" || name == "main" {
		return nil, nil
	}
	importPath := modPath
	if rel != "" {
		importPath = path.Join(modPath, rel)
	}
	dp, err := doc.NewFromFiles(fset, files, importPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return &Package{Rel: rel, ImportPath: importPath, Doc: dp, Fset: fset, imports: imports}, nil
}

// assumedName is the name a package of an import path is usually given:
// its last element, without a "go-" prefix or a "-go" suffix, and not a
// major version.
func assumedName(importPath string) string {
	base := path.Base(importPath)
	if len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		base = path.Base(path.Dir(importPath))
	}
	base = strings.TrimPrefix(base, "go-")
	base = strings.TrimSuffix(base, "-go")
	if i := strings.IndexAny(base, ".-"); i >= 0 {
		base = base[:i]
	}
	return base
}

// modulePath reads the module line of a go.mod.
func modulePath(gomod string) (string, error) {
	f, err := os.Open(gomod)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(s.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", errors.New(gomod + ": no module line")
}

// loadExamples reads the package comment of every examples/<dir>/main.go.
func loadExamples(root string) ([]*Program, error) {
	entries, err := os.ReadDir(filepath.Join(root, "examples"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Program
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		file := filepath.Join(root, "examples", e.Name(), "main.go")
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments|parser.PackageClauseOnly)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		text := ""
		if f.Doc != nil {
			text = f.Doc.Text()
		}
		out = append(out, &Program{Dir: "examples/" + e.Name(), Doc: text})
	}
	return out, nil
}
