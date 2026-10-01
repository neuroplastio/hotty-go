package doc_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"log"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/doc"
)

// A program shows a Markdown file among its output: a surface for each
// page, the page's images sent first as resources. The surfaces are
// created detached, so nothing in them reaches whatever reads the terminal
// after the program exits (SPEC §5.5), and their blocks are inert.
func Example_markdown() {
	readme := []byte("# Deploys\n\nRun `deploy` with an environment, as the diagram shows.\n\n" +
		"![how a deploy goes](flow.svg)\n\n" +
		"| env | region |\n| --- | --- |\n| staging | eu-west |\n| production | us-east |\n")
	flow := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="360" height="72"></svg>`)
	o := doc.Options{
		Cols:      80, // the terminal's size
		Screen:    40,
		ResPrefix: "deploy-4121", // the program's own, so two runs never share an id
		ReadFile: func(name string) ([]byte, error) {
			if name == "flow.svg" {
				return flow, nil
			}
			return nil, fs.ErrNotExist
		},
	}
	d := doc.Markdown(readme, o)

	var term bytes.Buffer // os.Stdout, in a program
	for i, page := range doc.Pages(d.Blocks, o.PageRows()) {
		name := fmt.Sprintf("deploy-4121-readme%d", i)
		for _, r := range doc.Resources(page) {
			term.WriteString(hotty.Res(r.ID, r.Type, r.Data))
			fmt.Println("resource", r.ID, r.Type)
		}
		term.WriteString(hotty.DocDetached(name, d.Page(page)))
		term.WriteString(hotty.Place(name, hotty.Placement{Cols: o.Cols})) // rows: as the content needs
		fmt.Printf("surface %s: %d blocks, about %d rows\n", name, len(page), doc.Rows(page))
	}
	// Output:
	// resource deploy-4121-img-206171d340b210d1 image/svg+xml
	// surface deploy-4121-readme0: 4 blocks, about 16 rows
}

// A CSV file as a table: its numeric columns right-aligned, and its text
// for a terminal that is not a host.
func ExampleReadTable() {
	csv := []byte("service,p50 ms,p99 ms\napi,12,48\nauth,7,31\nsearch,25,140\n")
	t, err := doc.ReadTable(csv, ',')
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(t.Head, len(t.Rows), "rows; numeric:", t.Numeric)

	d := t.Doc(doc.Options{Cols: 80})
	_ = hotty.DocDetached("latency", d.Page(d.Blocks)) // a host draws the table
	for _, p := range doc.Paragraphs([]byte(d.Body(d.Blocks))) {
		fmt.Println(p.Text) // any other terminal, its text
	}
	// Output:
	// [service p50 ms p99 ms] 3 rows; numeric: [false true true]
	// service  p50 ms  p99 ms
	// api  12  48
	// auth  7  31
	// search  25  140
}

// A program's last surface, left behind in scrollback when it exits:
// its link opens in the browser, and its button does nothing.
func ExampleInert() {
	fmt.Println(doc.Inert(`<p id="status">Deployed. <a href="https://ci.example.com/run/42">See the run</a>` +
		`, or <button id="undo" onclick="undo()">undo</button>.</p>`))
	// Output:
	// <p id="status">Deployed. <a href="https://ci.example.com/run/42" target="_blank" rel="noopener noreferrer">See the run</a>, or <button disabled="">undo</button>.</p>
}
