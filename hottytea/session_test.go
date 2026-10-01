package hottytea

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
)

// native is a Session that has found a HOTTY terminal.
func native() *Session {
	h := New()
	h.Mode = Native
	return h
}

func flushed(h *Session) string {
	s := strings.Join(h.out, "")
	h.out = h.out[:0]
	h.queued = 0
	return s
}

func TestLayoutSendsOnlyWhatChanged(t *testing.T) {
	h := native()
	docs := 0
	want := []Surface{{Name: "x", Rect: Rect{1, 2, 10, 3}, Doc: func() string { docs++; return "<p>x</p>" }}}
	h.Layout(want)
	if out := flushed(h); !strings.Contains(out, "a=doc:s=x") || !strings.Contains(out, "a=place:s=x") {
		t.Fatalf("first layout sent %q", out)
	}
	h.Layout(want)
	if out := flushed(h); out != "" {
		t.Fatalf("an unchanged layout sent %q", out)
	}
	want[0].Rect.Y = 4
	h.Layout(want)
	if out := flushed(h); strings.Contains(out, "a=doc") || !strings.Contains(out, "\x1b[5;2H") {
		t.Fatalf("a move sent %q", out)
	}
	// A change of Z alone places it again, with the z.
	want[0].Z = 1
	h.Layout(want)
	if out := flushed(h); strings.Contains(out, "a=doc") || !strings.Contains(out, ":z=1:") {
		t.Fatalf("a change of Z sent %q", out)
	}
	h.Layout(nil)
	if out := flushed(h); !strings.Contains(out, "a=del:s=x") {
		t.Fatalf("removing sent %q", out)
	}
	if docs != 1 {
		t.Fatalf("the document was built %d times", docs)
	}
}

func TestErasedScreenPlacesEverySurfaceAgain(t *testing.T) {
	h := native()
	want := []Surface{{Name: "x", Rect: Rect{0, 1, 10, 3}, Doc: func() string { return "<p>x</p>" }}}
	h.Layout(want)
	flushed(h)
	msg, _ := h.Update(erasedMsg{})
	if _, ok := msg.(RelayoutMsg); !ok {
		t.Fatalf("erasedMsg gave %T", msg)
	}
	h.Layout(want)
	if out := flushed(h); strings.Contains(out, "a=doc") || !strings.Contains(out, "a=place:s=x") {
		t.Fatalf("after an erase: %q", out)
	}
}

func TestAMissingDocumentIsSentAgain(t *testing.T) {
	h := native()
	want := []Surface{{Name: "x", Rect: Rect{0, 1, 10, 3}, Doc: func() string { return "<p>x</p>" }}}
	h.Layout(want)
	flushed(h)
	reply := hotty.Encode(hotty.Control{{K: "a", V: "err"}, {K: "s", V: "x"}, {K: "re", V: "place"}},
		[]byte(`{"code":"ENOENT","detail":"no surface x"}`))
	msg, _ := h.Update(uv.UnknownOscEvent(reply))
	if _, ok := msg.(RelayoutMsg); !ok {
		t.Fatalf("ENOENT on place gave %T", msg)
	}
	h.Layout(want)
	if out := flushed(h); !strings.Contains(out, "a=doc:s=x") || !strings.Contains(out, "a=place:s=x") {
		t.Fatalf("after ENOENT: %q", out)
	}
}

func TestAClippedSurfaceShowsTheWindowInView(t *testing.T) {
	h := native()
	region := Rect{0, 1, 80, 20}
	s := Surface{Name: "x", Rect: Rect{2, -2, 10, 8}, Clip: &region, Doc: func() string { return "<p>x</p>" }}
	h.Layout([]Surface{s})
	// Rows 3-7 of the surface are in the region, from its top row.
	if out := flushed(h); !strings.Contains(out, "\x1b[2;3H") || !strings.Contains(out, "c=10:r=8:x=0:y=3:w=10:h=5") {
		t.Fatalf("clipped at the top: %q", out)
	}
	s.Rect.Y = 17
	h.Layout([]Surface{s})
	if out := flushed(h); !strings.Contains(out, "\x1b[18;3H") || !strings.Contains(out, "c=10:r=8:x=0:y=0:w=10:h=4") {
		t.Fatalf("clipped at the bottom: %q", out)
	}
	s.Rect.Y = 5
	h.Layout([]Surface{s})
	if out := flushed(h); !strings.Contains(out, "a=place:s=x:c=10:r=8:C=1") {
		t.Fatalf("whole: %q", out)
	}
}

func TestAKeptSurfaceIsHiddenAndShownAgainWithoutItsDocument(t *testing.T) {
	h := native()
	region := Rect{0, 0, 80, 20}
	docs := 0
	s := Surface{Name: "x", Rect: Rect{0, 2, 10, 4}, Clip: &region, Keep: true, Doc: func() string { docs++; return "<p>x</p>" }}
	h.Layout([]Surface{s})
	flushed(h)
	s.Rect.Y = -10 // scrolled out
	h.Layout([]Surface{s})
	if out := flushed(h); !strings.Contains(out, "a=hide:s=x") || strings.Contains(out, "a=del") {
		t.Fatalf("scrolled out: %q", out)
	}
	h.Layout(nil) // and not wanted at all: still hidden, once
	if out := flushed(h); out != "" {
		t.Fatalf("hidden again: %q", out)
	}
	s.Rect.Y = 3
	h.Layout([]Surface{s})
	if out := flushed(h); strings.Contains(out, "a=doc") || !strings.Contains(out, "a=place:s=x") {
		t.Fatalf("back in view: %q", out)
	}
	if docs != 1 {
		t.Fatalf("the document was built %d times", docs)
	}
	h.Delete("x")
	if out := flushed(h); !strings.Contains(out, "a=del:s=x") {
		t.Fatalf("delete: %q", out)
	}
}

func TestKeptSurfacesStayWithinTheLimitOldestFirst(t *testing.T) {
	h := native()
	h.Caps.Limits = map[string]int{"surfaces": 3}
	region := Rect{0, 0, 80, 20}
	card := func(i, y int) Surface {
		return Surface{Name: fmt.Sprint("c", i), Rect: Rect{0, y, 10, 2}, Clip: &region, Keep: true,
			Doc: func() string { return "<p>c</p>" }}
	}
	// Five cards scroll through a view that holds one at a time.
	for i := range 5 {
		h.Layout([]Surface{card(i, 0)})
	}
	out := flushed(h)
	for _, gone := range []string{"a=del:s=c0", "a=del:s=c1"} {
		if !strings.Contains(out, gone) {
			t.Errorf("%s: not deleted, over the limit", gone)
		}
	}
	if strings.Contains(out, "a=del:s=c2") || len(h.hasDoc) != 3 {
		t.Errorf("kept %d surfaces: %v", len(h.hasDoc), h.hasDoc)
	}
	// A refused document teaches a lower limit, and is sent again later.
	reply := hotty.Encode(hotty.Control{{K: "a", V: "err"}, {K: "s", V: "c4"}, {K: "re", V: "doc"}},
		[]byte(`{"code":"EQUOTA","detail":"at most 2 surfaces"}`))
	if msg, _ := h.Update(uv.UnknownOscEvent(reply)); msg != (RelayoutMsg{}) {
		t.Fatalf("EQUOTA gave %T", msg)
	}
	h.Layout([]Surface{card(4, 0)})
	if out := flushed(h); !strings.Contains(out, "a=doc:s=c4") || len(h.hasDoc) > 2 {
		t.Errorf("after EQUOTA: %d surfaces, %q", len(h.hasDoc), out)
	}
}
