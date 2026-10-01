// Command docgen writes hotty-go's API reference from the code: a page per
// public package in docs/api, an index of them, and the tables of packages
// and examples in README.md, between its docgen markers.
//
// It is what `make docs` and `go generate ./...` run, from the module's
// root:
//
//	go run ./internal/docgen          # write what changed
//	go run ./internal/docgen -check   # write nothing; exit 1 if anything would change
//
// Everything it writes comes from the doc comments, the Example functions
// and examples/*/main.go, so the reference is never edited by hand. Its
// tests fail when what is on disk is not what it would write.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "the module's root `directory`")
	check := flag.Bool("check", false, "write nothing; list the files that would change, and exit 1 if any would")
	flag.Parse()

	m, err := Load(*root)
	if err != nil {
		fail(err)
	}
	files, err := m.Generate()
	if err != nil {
		fail(err)
	}
	changes, err := Changes(*root, files)
	if err != nil {
		fail(err)
	}
	if *check {
		if len(changes) > 0 {
			fmt.Fprintln(os.Stderr, "docgen: the documentation is not current; run make docs:")
			for _, c := range changes {
				fmt.Fprintln(os.Stderr, "\t"+c.String())
			}
			os.Exit(1)
		}
		return
	}
	if err := Write(*root, files, changes); err != nil {
		fail(err)
	}
	for _, c := range changes {
		fmt.Println(c.String())
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "docgen:", err)
	os.Exit(1)
}
