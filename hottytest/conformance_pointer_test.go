package hottytest

import (
	"encoding/json"
	"fmt"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"neuroplast.io/hotty-go"
)

// The runner supplies hit targets for the signed fixtures' literal cell
// moves. These are input fixtures for a host without layout. Selectors
// are element IDs except for the anonymous targets in the document.
type cellHits map[hotty.Cell]string

var hoverHits = cellHits{
	{Col: 3, Row: 0}: "a", {Col: 3, Row: 1}: "a", {}: "a",
	{Col: 1, Row: 0}: "a", {Col: 1, Row: 3}: "b", {Col: 3, Row: 3}: "b",
	{Col: 3, Row: 2}: "", {Col: 3, Row: 6}: "", {Col: 4, Row: 6}: "",
}
var outerHit = cellHits{{Col: 7, Row: 2}: "o"}

// Keys are the one-based indices in the signed vectors file.
var pointerHits = map[int]cellHits{
	20:  {{Col: 7, Row: 1}: "", {Col: 8, Row: 1}: "", {Col: 7, Row: -2}: "", {Col: 30, Row: 4}: ""},
	23:  {{Col: 1, Row: 0}: "drag without id", {Col: 4, Row: 0}: "n"},
	30:  {{Col: 12, Row: 0}: ""},
	60:  hoverHits,
	61:  hoverHits,
	62:  {{Col: 1, Row: 0}: "a", {Col: 1, Row: 1}: "b", {Col: 1, Row: 2}: "c", {Col: 1, Row: 9}: ""},
	69:  hoverHits,
	70:  hoverHits,
	74:  {{Col: 15, Row: 3}: ""},
	76:  outerHit,
	77:  {{Col: 15, Row: 5}: ""},
	78:  outerHit,
	83:  outerHit,
	84:  outerHit,
	85:  outerHit,
	89:  outerHit,
	98:  {{Col: 6, Row: 0}: "c", {Col: 8, Row: 2}: "c", {Col: 14, Row: 1}: "", {Col: 1, Row: 2}: "c", {Col: 5, Row: 2}: "c"},
	99:  {{Col: 4, Row: 0}: "track or card", {Col: 7, Row: 0}: "track", {Col: 1, Row: 0}: "card", {Col: 4, Row: 1}: "ada", {Col: 4, Row: 2}: "col"},
	100: {{Col: 4, Row: 1}: "v", {Col: 7, Row: 2}: "v"},
	103: {{Col: 7, Row: 0}: "o", {Col: 18, Row: 0}: "q"},
	104: {{Col: 2, Row: 0}: ".l", {Col: 11, Row: 0}: ".h", {Col: 15, Row: 0}: ".q", {Col: 1, Row: 2}: "link in w"},
	147: {{Col: 1, Row: 2}: "button in w"},
	171: {{Col: 1, Row: 0}: "row"},
	181: {{Col: 20, Row: 3}: ""},
	182: hoverHits,
}

type vectorPointer struct {
	fixture         int
	surface, target string
	cell            hotty.Cell
	named           bool
	lastRaw, count  int
	lastClick       *html.Node
	namedCells      map[string]hotty.Cell
}

func (p *vectorPointer) step(t *testing.T, i int, h *Host, st vectorStep) {
	t.Helper()
	if st.Pointer == "move" {
		p.surface = st.PointerSurface
		p.named = json.Unmarshal(st.PointerAt, &p.target) == nil
		if p.named {
			if p.namedCells == nil {
				p.namedCells = make(map[string]hotty.Cell)
			}
			// Distinct synthetic cells for named targets model movement.
			cell, ok := p.namedCells[p.target]
			if !ok {
				cell = hotty.Cell{Col: -2 * (len(p.namedCells) + 1)}
				p.namedCells[p.target] = cell
			}
			p.cell = cell
		} else {
			var raw []int
			if err := json.Unmarshal(st.PointerAt, &raw); err != nil || len(raw) != 2 {
				t.Fatalf("step %d: invalid pointer location %s", i, st.PointerAt)
			}
			p.cell = hotty.Cell{Col: raw[0], Row: raw[1]}
			var ok bool
			p.target, ok = pointerHits[p.fixture][p.cell]
			if !ok {
				t.Fatalf("step %d: fixture %d has no hit input for %v", i, p.fixture, p.cell)
			}
		}
	} else if st.Pointer == "leave" {
		p.surface = ""
		p.named = false
	}
	at := p.location(t, h)
	switch st.Pointer {
	case "move", "leave":
		start := h.pointer.start
		dc, dr := at.Col-start.Col, at.Row-start.Row
		pastSlop := at.surface != start.surface || dc >= 2 || dc <= -2 || dr >= 2 || dr <= -2
		h.pointerMove(at, pastSlop, st.Keys)
	case "down":
		h.pointerDown(at, st.Button, st.Keys)
	case "up":
		h.pointerUp(at, st.Button, p.clickCount(h, st), st.Keys)
	default:
		t.Fatalf("step %d: unknown pointer action %q", i, st.Pointer)
	}
	events := h.sentEvents()
	if st.HasEvs {
		checkEvents(t, fmt.Sprintf("step %d (%s)", i, st.Pointer), events, st.Events, p.named)
	}
}

func (p *vectorPointer) clickCount(h *Host, st vectorStep) int {
	rep := h.pointer.click
	if st.Button != hotty.ButtonPrimary || rep == nil {
		return 1
	}
	if rep != p.lastClick || st.Count <= p.lastRaw {
		p.count = 0
	}
	p.lastClick, p.lastRaw = rep, st.Count
	p.count++
	return p.count
}

func (p *vectorPointer) location(t *testing.T, h *Host) pointerLocation {
	t.Helper()
	at := pointerLocation{Cell: p.cell}
	s := h.surfaces[p.surface]
	if s == nil || !s.placed || s.detached {
		return at
	}
	if !p.named && (p.cell.Col < 0 || p.cell.Row < 0 || p.cell.Col >= s.place.Cols || p.cell.Row >= s.place.Rows) {
		return at
	}
	at.surface = s
	switch p.target {
	case "":
		at.element = find(s.doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
	case ".l", ".h", ".q":
		at.element = find(s.doc, func(n *html.Node) bool { class, _ := attr(n, "class"); return class == p.target[1:] })
	case "track or card":
		at.element = s.byID("track")
		if at.element == nil {
			at.element = s.byID("card")
		}
	case "drag without id":
		at.element = find(s.doc, listens("drag"))
	case "link in w", "button in w":
		child := atom.A
		if p.target == "button in w" {
			child = atom.Button
		}
		at.element = find(s.byID("w"), func(n *html.Node) bool { return n.DataAtom == child })
	default:
		at.element = s.byID(p.target)
	}
	// The signed fixture supplies 16 horizontal steps across an
	// eight-cell track, and a 3-by-2 grid on the drop target.
	if p.fixture == 99 {
		switch at.id() {
		case "track":
			at.step = &[2]int{2*(p.cell.Col-4) + 1, 0}
		case "col":
			at.step = &[2]int{2, 0}
		}
	}
	if at.element == nil && p.target != "" {
		t.Fatalf("fixture %d: missing pointer target %q", p.fixture, p.target)
	}
	return at
}
