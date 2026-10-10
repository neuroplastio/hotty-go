package hotty

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// With sets in place or at the end, and copies: two Withs on one control
// do not share its memory (SDK.md §3.2).
func TestControlWith(t *testing.T) {
	base := make(Control, 0, 8)
	base = append(base, KV{"a", "doc"}, KV{"s", "x"}, KV{"q", "1"})
	if got := With(base, "s", "y"); !reflect.DeepEqual(got, Control{{"a", "doc"}, {"s", "y"}, {"q", "1"}}) {
		t.Errorf("With in place: %v", got)
	}
	one, two := With(base, "n", "1"), With(base, "d", "1")
	if v, _ := Lookup(one, "n"); v != "1" || Has(one, "d") || Has(two, "n") {
		t.Errorf("Withs on one base share it: %v %v", one, two)
	}
	if Has(base, "n") || len(base) != 3 {
		t.Errorf("With changed its control: %v", base)
	}
	if got := Without(base, "s"); !reflect.DeepEqual(got, Control{{"a", "doc"}, {"q", "1"}}) || !Has(base, "s") {
		t.Errorf("Without: %v, base %v", got, base)
	}
	if got := Without(base, "s", "q"); !reflect.DeepEqual(got, Control{{"a", "doc"}}) {
		t.Errorf("Without two keys: %v", got)
	}
}

// o and m are Encode's: a control given with them does not send them
// twice. EncodePlain never compresses.
func TestEncodeOwnsOAndM(t *testing.T) {
	small := Encode(Control{{"a", "res"}, {"id", "x"}, {"o", "z"}, {"m", "1"}}, []byte("hi"))
	if small != "\x1b]7279;a=res:id=x;aGk=\x1b\\" {
		t.Errorf("%q", small)
	}
	big := []byte(strings.Repeat("<li>row</li>", 500))
	m := decodeOne(t, Encode(Control{{"o", "z"}, {"a", "res"}, {"id", "x"}}, big))
	if string(m.Payload) != string(big) || !reflect.DeepEqual(m.Control, Control{{"a", "res"}, {"id", "x"}}) {
		t.Errorf("compressed with o given: %v", m.Control)
	}
	if plain := EncodePlain(Control{{"a", "ok"}}, big); strings.Contains(plain, "o=z") {
		t.Error("EncodePlain compressed")
	}
}

// A decoded message keeps its keys in order, so forwarding it sends the
// bytes that came.
func TestMessageForwards(t *testing.T) {
	in := "\x1b]7279;a=place:s=card:c=40:r=auto:C=1:q=1\x1b\\"
	m := decodeOne(t, in)
	if out := Encode(m.Control, m.Payload); out != in {
		t.Errorf("forwarded %q, came %q", out, in)
	}
	if !Has(m.Control, "C") || Has(m.Control, "x") {
		t.Error("Message.Has")
	}
}

// An event with no detail has none (SDK.md §2.2); one whose detail does
// not decode carries nothing, and keeps the bytes for a relay to pass on.
func TestEventDetailAbsent(t *testing.T) {
	ev, _ := EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "blur"}, {"t", ""}}, ""))
	if ev.Detail != nil {
		t.Errorf("detail %q", ev.Detail)
	}
	ev, _ = EventOf(host(Control{{"a", "ev"}, {"s", "f"}, {"e", "click"}, {"t", "go"}}, "{not msgpack"))
	if string(ev.Detail) != "{not msgpack" || ev.Value != "" || ev.Area != nil {
		t.Errorf("a detail that does not decode: %+v", ev)
	}
	click := Event{Surface: "card", Kind: EventClick, Target: "go", Value: "1", Detail: body(`{"value":"1"}`)}
	if got := EncodeEvent(click); got != EncodePlain(Control{{"a", "ev"}, {"s", "card"}, {"e", "click"}, {"t", "go"}}, body(`{"value":"1"}`)) {
		t.Errorf("EncodeEvent: %q", got)
	}
	if e, _ := EventOf(decodeOne(t, EncodeEvent(click))); !reflect.DeepEqual(e, click) {
		t.Errorf("round trip: %+v", e)
	}
}

func TestHostReplies(t *testing.T) {
	if got := ReplyOK("3", "card", "place", Control{{"c", "40"}, {"r", "2"}}, nil); got != "\x1b]7279;a=ok:n=3:s=card:re=place:c=40:r=2\x1b\\" {
		t.Errorf("ReplyOK: %q", got)
	}
	r, _ := ReplyOf(decodeOne(t, ReplyErr("", "card", "delta", ENOTARGET, "nope")))
	if r.OK || r.N != 0 || r.Surface != "card" || r.Re != "delta" || r.Code != ENOTARGET || r.Detail != "nope" {
		t.Errorf("ReplyErr: %+v", r)
	}
	// The capabilities go as they came, fields this package does not know
	// included, and uncompressed however long.
	raw := body(`{"v":"0.2","future":{"x":1},"host":"` + strings.Repeat("h", 400) + `"}`)
	enc := ReplyCaps("1", Caps{Raw: raw})
	if strings.Contains(enc, "o=z") {
		t.Error("a caps reply compressed")
	}
	r, _ = ReplyOf(decodeOne(t, enc))
	if caps := r.Caps; caps == nil || !bytes.Equal(caps.Raw, raw) || caps.V != "0.2" {
		t.Errorf("ReplyCaps: %+v", caps)
	}
	// Written, Caps has only what it has.
	r, _ = ReplyOf(decodeOne(t, ReplyCaps("2", Caps{V: "0.2", Host: "h"})))
	if fmt.Sprintf("%x", r.Message.Payload) != "82a176a3302e32a4686f7374a168" { // {"v":"0.2","host":"h"}
		t.Errorf("written: %x", r.Message.Payload)
	}
	if caps, ok := CapsOf(raw); !ok || caps.V != "0.2" || !bytes.Equal(caps.Raw, raw) {
		t.Errorf("CapsOf: %+v", caps)
	}
	if _, ok := CapsOf(body(`{"v":2}`)); ok {
		t.Error("CapsOf: a v of another type decoded")
	}
}

func TestReplyQuery(t *testing.T) {
	caps := Caps{V: Version, Host: "h"}
	for v, speaks := range map[string]bool{
		"0.2": true, "0.1,0.2": true, "99.0,0.2": true,
		"": false, "0.1": false, "0.20": false, "00.2": false, "0.2.0": false,
	} {
		q := Control{{"a", "q"}, {"n", "4"}}
		if v != "" {
			q = append(q, KV{"v", v})
		}
		if Speaks(q) != speaks {
			t.Errorf("Speaks(v=%q) = %v", v, !speaks)
		}
		r, _ := ReplyOf(decodeOne(t, ReplyQuery(q, caps)))
		switch {
		case r.N != 4 || r.Re != "q":
			t.Errorf("v=%q: %+v", v, r)
		case speaks && (!r.OK || r.Caps == nil || r.Caps.V != Version):
			t.Errorf("v=%q: %+v", v, r)
		case !speaks && (r.OK || r.Code != EVERSION || r.Detail != Version):
			t.Errorf("v=%q: %+v", v, r)
		}
	}
}

func TestMessagePlacement(t *testing.T) {
	place := func(ctl string) (Placement, error) {
		return PlacementOf(decodeOne(t, "\x1b]7279;"+ctl+"\x1b\\"))
	}
	p, err := place("a=place:s=card:c=40:r=3:z=-2:p=1:C=1")
	if want := (Placement{Cols: 40, Rows: 3, Z: -2, Press: true, KeepCursor: true}); err != nil || p != want {
		t.Errorf("%+v %v", p, err)
	}
	// Defaults: to the right edge, and to the bottom, which with auto rows
	// is the host's to find.
	if p, _ := place("a=place:s=card:c=40:r=10:y=2"); p.Window != (Window{0, 2, 40, 8}) {
		t.Errorf("window %+v", p.Window)
	}
	if p, _ := place("a=place:s=card:c=40:x=5"); p.Rows != 0 || p.Window != (Window{5, 0, 35, 0}) {
		t.Errorf("auto: %+v", p)
	}
	for ctl, detail := range map[string]string{
		"a=place:s=card":                      "missing c",
		"a=place:s=card:c=0":                  "c out of range",
		"a=place:s=card:c=40:r=1001":          "r out of range",
		"a=place:s=card:c=40:r=3:y=3":         "window out of the surface",
		"a=place:s=card:c=40:x=40":            "window out of the surface",
		"a=place:s=card:c=40:r=3:w=41":        "window out of the surface",
		"a=place:s=card:c=40:z=1001":          "z out of range",
		"a=place:s=card:c=forty":              "c out of range",
		"a=hide:s=card":                       "not a place command",
		"a=place:s=card:c=40:r=auto:h=0":      "window out of the surface",
		"a=place:s=card:c=40:r=3:x=-1:w=2":    "window out of the surface",
		"a=place:s=card:c=40:r=2:y=1:h=2:q=1": "window out of the surface",
	} {
		_, err := place(ctl)
		var e *Error
		if !errors.As(err, &e) || e.Code != EINVAL || e.Detail != detail {
			t.Errorf("%s: %v, want EINVAL (%s)", ctl, err, detail)
		}
	}
	if CursorBelow(2) != "\x1bD\x1bD\r" || CursorBelow(0) != "\r" {
		t.Error("CursorBelow")
	}
	if Sanitize("a:b;c=d\x07é") != "a_b_c_d__" {
		t.Errorf("Sanitize: %q", Sanitize("a:b;c=d\x07é"))
	}
}
