package hottyvt

import (
	"flag"
	"fmt"
	"go/format"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "write draw_gen.go from the tables in draw_test.go")

// boxes are the box-drawing characters that are drawn, by their arms: the
// weight of the line from the cell's centre to its left, right, top and
// bottom edges, as "lrud", 0 for none, 1 light, 2 heavy, 3 double; "a" at
// the end rounds the corner (╭). A tee or a cross whose arms differ in
// weight on one axis draws that axis at the heavier.
var boxes = map[rune]string{
	'─': "1100", '━': "2200", '│': "0011", '┃': "0022",
	'┌': "0101", '┍': "0201", '┎': "0102", '┏': "0202",
	'┐': "1001", '┑': "2001", '┒': "1002", '┓': "2002",
	'└': "0110", '┕': "0210", '┖': "0120", '┗': "0220",
	'┘': "1010", '┙': "2010", '┚': "1020", '┛': "2020",
	'├': "0111", '┝': "0211", '┞': "0121", '┟': "0112", '┠': "0122", '┡': "0221", '┢': "0212", '┣': "0222",
	'┤': "1011", '┥': "2011", '┦': "1021", '┧': "1012", '┨': "1022", '┩': "2021", '┪': "2012", '┫': "2022",
	'┬': "1101", '┭': "2101", '┮': "1201", '┯': "2201", '┰': "1102", '┱': "2102", '┲': "1202", '┳': "2202",
	'┴': "1110", '┵': "2110", '┶': "1210", '┷': "2210", '┸': "1120", '┹': "2120", '┺': "1220", '┻': "2220",
	'┼': "1111", '┽': "2111", '┾': "1211", '┿': "2211", '╀': "1121", '╁': "1112", '╂': "1122",
	'╃': "2121", '╄': "1221", '╅': "2112", '╆': "1212", '╇': "2221", '╈': "2212", '╉': "2122", '╊': "1222", '╋': "2222",
	'═': "3300", '║': "0033",
	'╒': "0301", '╓': "0103", '╔': "0303", '╕': "3001", '╖': "1003", '╗': "3003",
	'╘': "0310", '╙': "0130", '╚': "0330", '╛': "3010", '╜': "1030", '╝': "3030",
	'╞': "0311", '╟': "0133", '╠': "0333", '╡': "3011", '╢': "1033", '╣': "3033",
	'╤': "3301", '╥': "1103", '╦': "3303", '╧': "3310", '╨': "1130", '╩': "3330",
	'╪': "3311", '╫': "1133", '╬': "3333",
	'╭': "0101a", '╮': "1001a", '╯': "1010a", '╰': "0110a",
	'╴': "1000", '╵': "0010", '╶': "0100", '╷': "0001",
	'╸': "2000", '╹': "0020", '╺': "0200", '╻': "0002",
	'╼': "1200", '╽': "0012", '╾': "2100", '╿': "0021",
}

// The quadrants of a cell, for the block elements that are made of them.
const (
	ul = 1 << iota
	ur
	ll
	lr
)

// blocks are the block elements, by their background: a part of the cell,
// a shade, or quadrants (an int, of ul, ur, ll, lr).
var blocks = map[rune]any{
	'▀': "linear-gradient(currentColor 50%, transparent 0)",
	'▁': lower(1), '▂': lower(2), '▃': lower(3), '▄': lower(4), '▅': lower(5), '▆': lower(6), '▇': lower(7),
	'█': "currentColor",
	'▉': left(7), '▊': left(6), '▋': left(5), '▌': left(4), '▍': left(3), '▎': left(2), '▏': left(1),
	'▐': "linear-gradient(to right, transparent 50%, currentColor 0)",
	'░': shade(25), '▒': shade(50), '▓': shade(75),
	'▔': "linear-gradient(currentColor 12.5%, transparent 0)",
	'▕': "linear-gradient(to right, transparent 87.5%, currentColor 0)",
	'▖': ll, '▗': lr, '▘': ul, '▙': ul | ll | lr, '▚': ul | lr, '▛': ul | ur | ll, '▜': ul | ur | lr,
	'▝': ur, '▞': ur | ll, '▟': ur | ll | lr,
}

// tiling are the characters that draw alike in every cell of a run, which
// is then one element.
const tiling = "─━═▀▁▂▃▄▅▆▇█░▒▓▔"

func lower(eighths int) string {
	return "linear-gradient(transparent " + pct(8-eighths) + ", currentColor 0)"
}

func left(eighths int) string {
	return "linear-gradient(to right, currentColor " + pct(eighths) + ", transparent 0)"
}

func pct(eighths int) string { return strconv.FormatFloat(float64(eighths)*12.5, 'f', -1, 64) + "%" }

func shade(p int) string {
	return "color-mix(in srgb, currentColor " + strconv.Itoa(p) + "%, transparent)"
}

func quadrants(q int) string {
	var layers []string
	for _, c := range []struct {
		bit int
		at  string
	}{{ul, "0 0"}, {ur, "100% 0"}, {ll, "0 100%"}, {lr, "100% 100%"}} {
		if q&c.bit != 0 {
			layers = append(layers, "var(--vt-q) "+c.at+"/50% 50% no-repeat")
		}
	}
	return strings.Join(layers, ",")
}

// border is a line of the weight: --vt-b1 light (--vt-l wide), --vt-b2
// heavy (twice it), --vt-b3 double (three times: two light lines, a light
// line apart). vt-k defines them.
func border(weight byte) string { return "var(--vt-b" + string(weight) + ")" }

// centre is where a line of the weight starts, for it to be centred in the
// cell: half its width before the middle (--vt-c1, 2, 3), and the middle
// for no line.
func centre(weight byte) string {
	if weight == '0' {
		return "50%"
	}
	return "var(--vt-c" + string(weight) + ")"
}

// boxRules draw a box-drawing character. Most are only their arms'
// weights (--vt-al, ar, au, ad), which the rules for every vt-k draw: a
// line across for the arms left and right, a line down for those up and
// down, each from its edges to the other line's near side, so they join. A
// rounded corner and a double one are a box of their own, two of its sides
// the lines, which meet as a corner does; a line whose halves differ in
// weight is a half each.
func boxRules(sel, arms string) []string {
	l, r, u, d := arms[0], arms[1], arms[2], arms[3]
	h := max(l, r)
	v := max(u, d)
	rule := func(pseudo string, props ...string) string {
		return sel + "::" + pseudo + "{" + strings.Join(props, ";") + "}"
	}
	arc := len(arms) > 4 && arms[4] == 'a'
	corner := (l == '0') != (r == '0') && (u == '0') != (d == '0')
	switch {
	case corner && (arc || h == '3' || v == '3'):
		var props []string
		if r != '0' {
			props = append(props, "left:"+centre(v), "right:0", "border-left:"+border(v))
		} else {
			props = append(props, "left:0", "right:"+centre(v), "border-right:"+border(v))
		}
		if d != '0' {
			props = append(props, "top:"+centre(h), "bottom:0", "border-top:"+border(h))
		} else {
			props = append(props, "top:0", "bottom:"+centre(h), "border-bottom:"+border(h))
		}
		if arc {
			corner := map[[2]bool]string{{true, true}: "top-left", {true, false}: "top-right", {false, false}: "bottom-right", {false, true}: "bottom-left"}[[2]bool{d != '0', r != '0'}]
			props = append(props, "border-"+corner+"-radius:var(--vt-r)")
		}
		return []string{rule("before", props...)}
	case v == '0' && l != r && l != '0' && r != '0':
		return []string{
			rule("before", "left:0", "right:50%", "top:"+centre(l), "bottom:auto", "border-top:"+border(l)),
			rule("after", "left:50%", "right:0", "top:"+centre(r), "bottom:auto", "border-top:"+border(r)),
		}
	case h == '0' && u != d && u != '0' && d != '0':
		return []string{
			rule("before", "top:0", "bottom:50%", "left:"+centre(u), "right:auto", "border-left:"+border(u)),
			rule("after", "top:50%", "bottom:0", "left:"+centre(d), "right:auto", "border-left:"+border(d)),
		}
	}
	var props []string
	for i, arm := range []string{"al", "ar", "au", "ad"} {
		if arms[i] != '0' {
			props = append(props, "--vt-"+arm+":"+string(arms[i]))
		}
	}
	if h == '3' {
		props = append(props, "--vt-hs:double")
	}
	if v == '3' {
		props = append(props, "--vt-vs:double")
	}
	return []string{sel + "{" + strings.Join(props, ";") + "}"}
}

// generate is draw_gen.go.
func generate() []byte {
	var runes []rune
	for r := range boxes {
		runes = append(runes, r)
	}
	for r := range blocks {
		runes = append(runes, r)
	}
	slices.Sort(runes)
	// A cell's element, its arms (none, until a character's rule says),
	// their lines (boxRules), the widths of the lines a character's own
	// rules draw and where they start (border, centre), and a rounded
	// corner's radius: half a cell's width; a quadrant's colour (--vt-q).
	css := []string{
		".vt { --vt-l: max(1px, 0.07em); }",
		".vt-k { display: inline-block; position: relative; vertical-align: top; overflow: hidden; letter-spacing: 0;",
		"  width: calc(var(--vt-n, 1) * var(--vt-scale) * var(--hotty-cell-w)); height: calc(var(--vt-scale) * var(--hotty-cell-h));",
		"  -webkit-text-fill-color: transparent;",
		"  --vt-al: 0; --vt-ar: 0; --vt-au: 0; --vt-ad: 0;",
		"  --vt-h: max(var(--vt-al), var(--vt-ar)); --vt-v: max(var(--vt-au), var(--vt-ad));",
		"  --vt-b1: var(--vt-l) solid; --vt-b2: calc(2 * var(--vt-l)) solid; --vt-b3: calc(3 * var(--vt-l)) double;",
		"  --vt-c1: calc(50% - var(--vt-l) / 2); --vt-c2: calc(50% - var(--vt-l)); --vt-c3: calc(50% - 1.5 * var(--vt-l));",
		"  --vt-r: calc(var(--vt-scale) * var(--hotty-cell-w) / 2); --vt-q: linear-gradient(currentColor, currentColor); }",
		`.vt-k::before, .vt-k::after { content: ""; position: absolute; box-sizing: border-box; border: 0 solid; }`,
		".vt-k::before { left: calc((1 - min(var(--vt-al), 1)) * (50% - var(--vt-v) * var(--vt-l) / 2));",
		"  right: calc((1 - min(var(--vt-ar), 1)) * (50% - var(--vt-v) * var(--vt-l) / 2));",
		"  top: calc(50% - var(--vt-h) * var(--vt-l) / 2); border-top: calc(var(--vt-h) * var(--vt-l)) var(--vt-hs, solid); }",
		".vt-k::after { top: calc((1 - min(var(--vt-au), 1)) * (50% - var(--vt-h) * var(--vt-l) / 2));",
		"  bottom: calc((1 - min(var(--vt-ad), 1)) * (50% - var(--vt-h) * var(--vt-l) / 2));",
		"  left: calc(50% - var(--vt-v) * var(--vt-l) / 2); border-left: calc(var(--vt-v) * var(--vt-l)) var(--vt-vs, solid); }",
	}
	for _, r := range runes {
		sel := ".vt-k" + strconv.FormatInt(int64(r), 16)
		if arms, ok := boxes[r]; ok {
			css = append(css, boxRules(sel, arms)...)
			continue
		}
		bg := blocks[r]
		if q, ok := bg.(int); ok {
			bg = quadrants(q)
		}
		css = append(css, sel+"{background:"+bg.(string)+"}")
	}
	var b strings.Builder
	b.WriteString("// Code generated by go test -run TestDrawGenerated -update; DO NOT EDIT.\n\npackage hottyvt\n\n")
	b.WriteString("// drawCSS draws the drawn characters (draw.go).\nconst drawCSS = `" + strings.Join(css, "\n") + "\n`\n\n")
	b.WriteString("// drawnRunes are the characters drawn; tilingRunes, those of them that draw\n// alike in every cell.\n")
	b.WriteString("const (\n\tdrawnRunes  = " + strconv.Quote(string(runes)) + "\n\ttilingRunes = " + strconv.Quote(tiling) + "\n)\n")
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		panic(fmt.Sprint(err, "\n", b.String()))
	}
	return out
}

// draw_gen.go is what the tables make.
func TestDrawGenerated(t *testing.T) {
	want := generate()
	if *update {
		if err := os.WriteFile("draw_gen.go", want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile("draw_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("draw_gen.go is not what the tables in draw_test.go make: go test -run TestDrawGenerated -update")
	}
}

// Every character the tables draw is in range, and the ones that tile are
// drawn.
func TestDrawTables(t *testing.T) {
	for r := range boxes {
		if r < 0x2500 || r > 0x257f {
			t.Errorf("%U is no box-drawing character", r)
		}
	}
	for r := range blocks {
		if r < 0x2580 || r > 0x259f {
			t.Errorf("%U is no block element", r)
		}
	}
	for _, r := range tiling {
		_, box := boxes[r]
		_, block := blocks[r]
		if !box && !block {
			t.Errorf("%c tiles, but is not drawn", r)
		}
	}
	// The ones left to the font: dashes and diagonals.
	for _, r := range "┄┅┆┇┈┉┊┋╌╍╎╏╱╲╳" {
		if _, k := drawKind(string(r)); k != 0 {
			t.Errorf("%c is drawn; the font sets it", r)
		}
	}
	if len(boxes) != 128-15 || len(blocks) != 32 {
		t.Errorf("%d box-drawing characters and %d block elements drawn", len(boxes), len(blocks))
	}
}
