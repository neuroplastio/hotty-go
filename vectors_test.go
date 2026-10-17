package hotty

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The conformance vectors' SDK sections (SDK.md §5, conformance/README.md):
// what this package builds, encodes, decodes, scans and detects.

// features is what this package implements of what a vector may require.
// A vector that requires anything else is skipped: what is missing here is
// SDK.md's list of this package's gaps.
var features = map[string]bool{
	"options.unordered":   true,
	"decode.abort-count":  true,
	"decode.max":          true,
	"decode.unterminated": true,
	"scanner.da1":         true,
	"graphemes":           true,
}

func applies(t *testing.T, requires []string) {
	t.Helper()
	for _, r := range requires {
		if !features[r] {
			t.Skip("requires " + r)
		}
	}
}

// sdkVectors reads the vectors' SDK sections.
func sdkVectors(t *testing.T) (v struct {
	Build  []buildVector  `json:"build"`
	Encode []encodeVector `json:"encode"`
	Decode []decodeVector `json:"decode"`
	Scan   []scanVector   `json:"scan"`
	Detect []detectVector `json:"detect"`
	Keys   []keysVector   `json:"keys"`
	Keymap []keymapVector `json:"keymap"`
}) {
	t.Helper()
	data, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

type buildVector struct {
	Name     string         `json:"name"`
	Requires []string       `json:"requires"`
	Build    string         `json:"build"`
	Args     map[string]any `json:"args"`
	Options  map[string]any `json:"options"`
	Out      []segment      `json:"out"`
}

// segment is a part of a builder's output: bytes that are not hotty's, or
// one command, decoded.
type segment struct {
	Raw *string `json:"raw,omitempty"`
	Cmd *struct {
		Control    map[string]string `json:"control"`
		Payload    string            `json:"payload"`
		PayloadB64 string            `json:"payload_b64,omitempty"`
	} `json:"cmd,omitempty"`
}

func TestBuildVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Build {
		t.Run(v.Name, func(t *testing.T) {
			applies(t, v.Requires)
			for _, s := range v.Out {
				if s.Cmd != nil && s.Cmd.Control == nil {
					s.Cmd.Control = map[string]string{}
				}
				if s.Cmd != nil && s.Cmd.PayloadB64 != "" {
					b, err := base64.StdEncoding.DecodeString(s.Cmd.PayloadB64)
					if err != nil {
						t.Fatal(err)
					}
					s.Cmd.Payload, s.Cmd.PayloadB64 = string(b), ""
				}
			}
			// The options have no order: each order must build the same.
			for _, order := range [][]string{{"n", "q"}, {"q", "n"}} {
				got := segments(t, build(t, v.Build, v.Args, v.Options, order))
				if !reflect.DeepEqual(got, v.Out) {
					g, _ := json.Marshal(got)
					w, _ := json.Marshal(v.Out)
					t.Errorf("options in the order %v:\ngot  %s\nwant %s", order, g, w)
				}
			}
		})
	}
}

// build calls the builder a vector names, with its arguments, and its
// options n and q in the order given.
func build(t *testing.T, name string, args, options map[string]any, order []string) string {
	t.Helper()
	str := func(k string) string { s, _ := args[k].(string); return s }
	num := func(k string) int { f, _ := args[k].(float64); return int(f) }
	var opts []ReplyOption
	for _, k := range order {
		v, ok := options[k].(float64)
		switch {
		case !ok:
		case k == "n":
			opts = append(opts, N(int(v)))
		case k == "q":
			opts = append(opts, Q(Quiet(v)))
		}
	}
	switch name {
	case "query":
		if l, _ := options["late"].(bool); l {
			return Query(num("n"), Late())
		}
		return Query(num("n"))
	case "withdraw_late":
		return WithdrawLate()
	case "doc":
		var docOpts []DocOption
		for _, o := range opts {
			docOpts = append(docOpts, o)
		}
		if d, _ := options["detached"].(bool); d {
			docOpts = append(docOpts, Detached())
		}
		if s, ok := options["scroll"].(float64); ok {
			docOpts = append(docOpts, Scroll(Axes(s)))
		}
		return Doc(str("surface"), str("html"), docOpts...)
	case "place":
		return Place(str("surface"), placement(args["placement"]), opts...)
	case "place_at":
		return PlaceAt(str("surface"), num("x"), num("y"), placement(args["placement"]), opts...)
	case "hide":
		return Hide(str("surface"), opts...)
	case "delta":
		return Delta(str("surface"), opsOf(args["ops"]), opts...)
	case "set_text":
		return SetText(str("surface"), str("target"), str("text"), opts...)
	case "set_var":
		return SetVar(str("surface"), str("target"), str("name"), str("value"), opts...)
	case "set_attr":
		return SetAttr(str("surface"), str("target"), str("name"), str("value"), opts...)
	case "remove_attr":
		return RemoveAttr(str("surface"), str("target"), str("name"), opts...)
	case "morph_to":
		return MorphTo(str("surface"), str("target"), str("html"), opts...)
	case "reveal":
		return Reveal(str("surface"), str("target"), str("where"), opts...)
	case "res":
		return Res(str("id"), str("mime"), []byte(str("data")), opts...)
	case "del_res":
		return DelRes(str("id"), opts...)
	case "del":
		return Del(str("surface"), opts...)
	case "del_all":
		return DelAll(opts...)
	case "detach":
		return Detach(str("surface"), opts...)
	case "focus":
		return Focus(str("surface"), str("target"), opts...)
	case "blur":
		return Blur(str("surface"), opts...)
	case "edit":
		names := []string{}
		for _, n := range asSlice(args["names"]) {
			names = append(names, n.(string))
		}
		return Edit(names, opts...)
	case "menu":
		m, ok := args["menu"].(map[string]any)
		if !ok {
			return Menu(nil, opts...)
		}
		t := MenuTree{Items: menuItems(m["items"])}
		t.Label, _ = m["label"].(string)
		return Menu(&t, opts...)
	case "menu_show":
		var keys []string
		for _, k := range asSlice(args["keys"]) {
			keys = append(keys, k.(string))
		}
		return MenuShow(keys, opts...)
	case "menu_hide":
		return MenuHide(opts...)
	case "touch":
		var areas []TouchArea
		for _, a := range asSlice(args["areas"]) {
			a := asMap(a)
			n := func(k string) int { f, _ := a[k].(float64); return int(f) }
			drag, _ := a["drag"].(string)
			areas = append(areas, TouchArea{C: n("c"), R: n("r"), W: n("w"), H: n("h"), Drag: drag})
		}
		return Touch(areas, opts...)
	case "sync":
		var cmds []string
		for _, c := range args["commands"].([]any) {
			c := c.(map[string]any)
			a, _ := c["args"].(map[string]any)
			o, _ := c["options"].(map[string]any)
			cmds = append(cmds, build(t, c["build"].(string), a, o, order))
		}
		return Sync(cmds...)
	}
	t.Fatalf("no builder %q", name)
	return ""
}

// menuItems reads a menu vector's items.
func menuItems(v any) []MenuItem {
	items := []MenuItem{}
	for _, i := range asSlice(v) {
		i := i.(map[string]any)
		var it MenuItem
		it.Key, _ = i["key"].(string)
		it.Label, _ = i["label"].(string)
		it.Name, _ = i["name"].(string)
		it.Enabled, _ = i["enabled"].(bool)
		if sub := menuItems(i["items"]); len(sub) > 0 {
			it.Items = sub
		}
		items = append(items, it)
	}
	return items
}

// opsOf reads a delta vector's ops: an op has a value only if the vector
// gives one, even an empty one.
func opsOf(v any) []Op {
	var ops []Op
	for _, o := range asSlice(v) {
		m := asMap(o)
		op := Op{}
		op.Op, _ = m["op"].(string)
		op.Target, _ = m["target"].(string)
		op.Key, _ = m["key"].(string)
		if s, ok := m["value"].(string); ok {
			op.Value = &s
		}
		ops = append(ops, op)
	}
	return ops
}

func placement(v any) Placement {
	m, _ := v.(map[string]any)
	num := func(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }
	flag := func(k string) Flag {
		switch b, ok := m[k].(bool); {
		case !ok:
			return 0
		case b:
			return On
		}
		return Off
	}
	keep, _ := m["keep_cursor"].(bool)
	p := Placement{
		Cols: num(m, "cols"), Rows: num(m, "rows"), Z: num(m, "z"),
		Press: flag("press"), Fit: flag("fit"), Hover: flag("hover"), KeepCursor: keep,
	}
	if w, ok := m["window"].(map[string]any); ok {
		p.Window = Window{X: num(w, "x"), Y: num(w, "y"), W: num(w, "w"), H: num(w, "h")}
	}
	return p
}

// segments splits output into the bytes that are not hotty's, adjacent ones
// together, and the commands, decoded.
func segments(t *testing.T, out string) []segment {
	t.Helper()
	var segs []segment
	raw := func(s string) {
		if s == "" {
			return
		}
		if n := len(segs); n > 0 && segs[n-1].Raw != nil {
			*segs[n-1].Raw += s
			return
		}
		segs = append(segs, segment{Raw: &s})
	}
	var d Decoder
	for {
		i := strings.Index(out, prefix)
		if i < 0 {
			raw(out)
			return segs
		}
		raw(out[:i])
		out = out[i:]
		end := strings.Index(out, st)
		if end < 0 {
			t.Fatalf("unterminated sequence %q", out)
		}
		m, r := d.Feed(out[:end+len(st)])
		out = out[end+len(st):]
		switch r {
		case Complete:
			s := segment{Cmd: &struct {
				Control    map[string]string `json:"control"`
				Payload    string            `json:"payload"`
				PayloadB64 string            `json:"payload_b64,omitempty"`
			}{ctlMap(m.Control), string(m.Payload), ""}}
			segs = append(segs, s)
		case Partial:
		default:
			t.Fatalf("a builder's output does not decode: %v", r)
		}
	}
}

type encodeVector struct {
	Name       string     `json:"name"`
	Requires   []string   `json:"requires"`
	Control    [][]string `json:"control"`
	Payload    *string    `json:"payload"`
	PayloadB64 *string    `json:"payload_b64"`
	Bytes      *string    `json:"bytes"`
	Chunks     []struct {
		Control string `json:"control"`
		Len     int    `json:"len"`
	} `json:"chunks"`
}

func TestEncodeVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Encode {
		t.Run(v.Name, func(t *testing.T) {
			applies(t, v.Requires)
			var ctl Control
			var keys []string
			for _, kv := range v.Control {
				ctl = append(ctl, KV{kv[0], kv[1]})
				if kv[0] != "m" && kv[0] != "o" { // Encode's to set
					keys = append(keys, kv[0])
				}
			}
			payload := []byte(deref(v.Payload))
			if v.PayloadB64 != nil {
				var err error
				if payload, err = base64.StdEncoding.DecodeString(*v.PayloadB64); err != nil {
					t.Fatal(err)
				}
			}
			out := Encode(ctl, payload)
			if v.Bytes != nil && out != *v.Bytes {
				t.Errorf("got  %q\nwant %q", out, *v.Bytes)
			}
			seqs := split(out)
			if v.Chunks != nil {
				if len(seqs) != len(v.Chunks) {
					t.Fatalf("%d sequences, want %d", len(seqs), len(v.Chunks))
				}
				for i, c := range v.Chunks {
					control, data, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(seqs[i], prefix), st), ";")
					if control != c.Control || len(data) != c.Len {
						t.Errorf("chunk %d: %s with %d bytes, want %s with %d", i, control, len(data), c.Control, c.Len)
					}
				}
			}
			// It decodes back to the payload, with the keys in order.
			msgs, invalid := decodeAll(out)
			if len(msgs) != 1 || invalid != 0 {
				t.Fatalf("%d messages and %d invalid, want 1 and 0", len(msgs), invalid)
			}
			if string(msgs[0].Payload) != string(payload) {
				t.Errorf("payload does not round-trip")
			}
			first, _, _ := strings.Cut(strings.TrimPrefix(seqs[0], prefix), ";")
			var order []string
			for kv := range strings.SplitSeq(strings.TrimSuffix(first, st), ":") {
				if k, _, _ := strings.Cut(kv, "="); k != "m" && k != "o" {
					order = append(order, k)
				}
			}
			if !slices.Equal(order, keys) {
				t.Errorf("keys %v, want %v", order, keys)
			}
			var decoded []string
			for _, kv := range msgs[0].Control {
				decoded = append(decoded, kv.K)
			}
			if !slices.Equal(decoded, keys) {
				t.Errorf("decoded keys %v, want %v", decoded, keys)
			}
		})
	}
}

type decodeVector struct {
	Name     string   `json:"name"`
	Requires []string `json:"requires"`
	Seqs     []string `json:"seqs"`
	Max      int      `json:"max"`
	Results  []string `json:"results"`
	Invalid  int      `json:"invalid"`
	Messages []struct {
		Control map[string]string `json:"control"`
		Reply   map[string]any    `json:"reply"`
		Event   map[string]any    `json:"event"`
		Caps    *map[string]any   `json:"caps"`
		Changed map[string]any    `json:"changed"`
	} `json:"messages"`
}

func TestDecodeVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Decode {
		t.Run(v.Name, func(t *testing.T) {
			applies(t, v.Requires)
			d := Decoder{Max: v.Max}
			var results []string
			var msgs []Message
			for _, seq := range v.Seqs {
				m, r := d.Feed(seq)
				results = append(results, strings.ReplaceAll(r.String(), " ", "_"))
				if r == Complete {
					msgs = append(msgs, m)
				}
			}
			if !slices.Equal(results, v.Results) {
				t.Errorf("results %v, want %v", results, v.Results)
			}
			if d.Invalid != v.Invalid {
				t.Errorf("%d invalid, want %d", d.Invalid, v.Invalid)
			}
			if len(msgs) != len(v.Messages) {
				t.Fatalf("%d messages, want %d", len(msgs), len(v.Messages))
			}
			for i, want := range v.Messages {
				m := msgs[i]
				if !reflect.DeepEqual(ctlMap(m.Control), want.Control) {
					t.Errorf("message %d: control %v, want %v", i, m.Control, want.Control)
				}
				if want.Reply != nil {
					r, ok := ReplyOf(m)
					if !ok {
						t.Fatalf("message %d is not a reply", i)
					}
					check(t, fmt.Sprintf("message %d: reply", i), replyView(r), want.Reply)
				}
				if want.Event != nil {
					e, ok := EventOf(m)
					if !ok {
						t.Fatalf("message %d is not an event", i)
					}
					check(t, fmt.Sprintf("message %d: event", i), eventView(e), want.Event)
					if want.Changed != nil {
						if e.Changed == nil {
							t.Fatalf("message %d: no Changed", i)
						}
						var fields []string
						for _, f := range rawFields(e.Changed.Raw) {
							fields = append(fields, f.key)
						}
						var wantFields []string
						for _, f := range asSlice(want.Changed["fields"]) {
							wantFields = append(wantFields, f.(string))
						}
						if !slices.Equal(fields, wantFields) {
							t.Errorf("message %d: changed fields %v, want %v", i, fields, wantFields)
						}
						w := maps.Clone(want.Changed)
						delete(w, "fields")
						check(t, fmt.Sprintf("message %d: changed", i), capsView(*e.Changed, w), w)
					}
				}
				switch r, _ := ReplyOf(m); {
				case want.Caps == nil:
				case *want.Caps == nil: // null: none
					if r.Caps != nil {
						t.Errorf("message %d: caps %+v, want none", i, r.Caps)
					}
				case r.Caps == nil:
					t.Fatalf("message %d carries no capabilities", i)
				default:
					check(t, fmt.Sprintf("message %d: caps", i), capsView(*r.Caps, *want.Caps), *want.Caps)
				}
			}
		})
	}
}

func replyView(r Reply) map[string]any {
	var op any // null: the reply names no op
	if r.HasOp {
		op = r.Op
	}
	return map[string]any{
		"ok": r.OK, "re": r.Re, "n": r.N, "surface": r.Surface,
		"cols": r.Cols, "rows": r.Rows, "code": r.Code, "detail": r.Detail, "op": op,
	}
}

func eventView(e Event) map[string]any {
	view := map[string]any{
		"surface": e.Surface, "kind": e.Kind, "target": e.Target,
		"value": e.Value, "mods": e.Mods,
	}
	if e.Fields != nil {
		pairs := [][2]string{}
		for _, f := range e.Fields {
			pairs = append(pairs, [2]string{f.Name, f.Value})
		}
		view["fields"] = pairs
	}
	if e.Checked != nil {
		view["checked"] = *e.Checked
	}
	if l := e.Link; l != nil {
		view["link"] = map[string]any{"href": l.Href, "url": l.URL}
	}
	if e.FitRows > 0 {
		view["fit_rows"] = e.FitRows
	}
	if d := e.Drag; d != nil {
		drag := map[string]any{"c": d.Col, "r": d.Row}
		if d.HasX {
			drag["x"] = d.X
		}
		if d.HasY {
			drag["y"] = d.Y
		}
		if d.HasTX {
			drag["tx"] = d.TX
		}
		if d.HasTY {
			drag["ty"] = d.TY
		}
		view["drag"] = drag
	}
	if h := e.Hover; h != nil {
		view["hover"] = map[string]any{"c": h.Col, "r": h.Row, "out": h.Out}
	}
	if a := e.Area; a != nil {
		view["area"] = map[string]any{"c": a.Col, "r": a.Row, "w": a.W, "h": a.H}
	}
	if e.Count > 0 {
		view["count"] = e.Count
	}
	if c := e.Command; c != nil {
		view["command"] = map[string]any{"name": c.Name, "key": c.Key, "text": c.Text, "has_text": c.HasText}
	}
	if v := e.View; v != nil {
		areas := map[string]any{}
		for id, a := range v.Areas {
			areas[id] = map[string]any{"c": a.Col, "r": a.Row, "w": a.W, "h": a.H}
		}
		view["view"] = map[string]any{"areas": areas, "at_end": v.AtEnd}
	}
	return view
}

func capsView(c Caps, want map[string]any) map[string]any {
	view := map[string]any{
		"v": c.V, "cell_css": map[string]any{"w": c.CellCSS.W, "h": c.CellCSS.H},
		"scale": c.Scale, "scheme": c.Scheme, "mod": c.Mod, "features": c.Features,
		"limits": c.Limits, "net": c.Net, "edit": c.Edit, "host": c.Host, "version": c.Version,
		"light": Light(c),
	}
	has := map[string]any{}
	for feature := range asMap(want["has"]) {
		has[feature] = HasFeature(c, feature)
	}
	view["has"] = has
	return view
}

// check compares what this package says with what a vector wants, key by
// key, for the keys the vector lists. null is a zero value (SDK.md §5.2).
func check(t *testing.T, where string, got, want map[string]any) {
	t.Helper()
	var norm any
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &norm)
	if err := match(norm, want); err != nil {
		t.Errorf("%s%v", where, err)
	}
}

func match(got, want any) error {
	switch w := want.(type) {
	case nil:
		if !zero(got) {
			return fmt.Errorf(" = %v, want none", got)
		}
	case map[string]any:
		if got == nil && len(w) == 0 {
			return nil
		}
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf(" = %v, want %v", got, want)
		}
		for k, wv := range w {
			if err := match(g[k], wv); err != nil {
				return fmt.Errorf(".%s%v", k, err)
			}
			// A view's areas are compared whole (conformance/README.md, Decode).
			if wa, ok := wv.(map[string]any); ok && k == "areas" {
				if ga, _ := g[k].(map[string]any); len(ga) != len(wa) {
					return fmt.Errorf(".%s = %v, want %v", k, g[k], wv)
				}
			}
		}
	case []any:
		if got == nil && len(w) == 0 {
			return nil
		}
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Errorf(" = %v, want %v", got, want)
		}
		for i := range w {
			if err := match(g[i], w[i]); err != nil {
				return fmt.Errorf("[%d]%v", i, err)
			}
		}
	default:
		if got != want {
			return fmt.Errorf(" = %v, want %v", got, want)
		}
	}
	return nil
}

// zero reports whether a JSON value is what Go's zero value marshals to.
func zero(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case float64:
		return v == 0
	case bool:
		return !v
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func asSlice(v any) []any { s, _ := v.([]any); return s }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type scanVector struct {
	Name     string              `json:"name"`
	Requires []string            `json:"requires"`
	Stream   string              `json:"stream"`
	Segments []map[string]string `json:"segments"`
	Held     string              `json:"held"`
	Flush    []map[string]string `json:"flush"`
	Invalid  int                 `json:"invalid"`
}

// TestScanVectors feeds each stream whole, cut in two at every offset (in
// a long stream, at every offset near its ends and at a stride between),
// and a byte at a time. Each piece is fed from one buffer, overwritten
// after each Feed once its segments are copied: what the Scanner keeps
// must not be the caller's bytes.
func TestScanVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Scan {
		t.Run(v.Name, func(t *testing.T) {
			applies(t, v.Requires)
			data := []byte(v.Stream)
			n := len(data)
			var offsets []int
			if n > 512 {
				for i := 1; i < 64; i++ {
					offsets = append(offsets, i)
				}
				for i := 64; i <= n-65; i += 4999 {
					offsets = append(offsets, i)
				}
				for i := n - 64; i < n; i++ {
					offsets = append(offsets, i)
				}
			} else {
				for i := 1; i < n; i++ {
					offsets = append(offsets, i)
				}
			}
			splits := [][]int{nil}
			for _, i := range offsets {
				splits = append(splits, []int{i})
			}
			every := make([]int, 0, n)
			for i := 1; i < n; i++ {
				every = append(every, i)
			}
			splits = append(splits, every)
			want, wantFlush := wantSegments(v.Segments), wantSegments(v.Flush)
			for _, cuts := range splits {
				s := Scanner{DA1: slices.Contains(v.Requires, "scanner.da1")}
				var got []segView
				buf := make([]byte, 0, n)
				prev := 0
				for _, c := range append(slices.Clone(cuts), n) {
					buf = append(buf[:0], data[prev:c]...)
					got = append(got, viewSegments(s.Feed(buf))...)
					for i := range buf {
						buf[i] = 'X'
					}
					prev = c
				}
				where := "whole"
				switch {
				case len(cuts) == 1:
					where = fmt.Sprintf("cut at %d", cuts[0])
				case len(cuts) > 1:
					where = "a byte at a time"
				}
				held := string(s.Holding())
				flushed := viewSegments(s.Flush())
				if got := mergePass(got); !slices.Equal(got, want) {
					t.Fatalf("%s: segments %q, want %q", where, got, want)
				}
				if held != v.Held {
					t.Fatalf("%s: held %q, want %q", where, held, v.Held)
				}
				if !slices.Equal(flushed, wantFlush) {
					t.Fatalf("%s: flush %q, want %q", where, flushed, wantFlush)
				}
				if s.Invalid != v.Invalid {
					t.Fatalf("%s: %d invalid, want %d", where, s.Invalid, v.Invalid)
				}
			}
		})
	}
}

// segView is a segment as a vector has it: "pass", "osc" or "da1", and its
// bytes, copied.
type segView struct{ Kind, Data string }

func viewSegments(segs []Segment) []segView {
	var out []segView
	for _, s := range segs {
		out = append(out, segView{s.Kind.String(), string(s.Data)})
	}
	return out
}

func wantSegments(list []map[string]string) []segView {
	var out []segView
	for _, m := range list {
		for k, v := range m {
			out = append(out, segView{k, v})
		}
	}
	return out
}

// mergePass joins adjacent pass segments, which come apart where the
// stream was cut.
func mergePass(segs []segView) []segView {
	var out []segView
	for _, s := range segs {
		if n := len(out); n > 0 && s.Kind == "pass" && out[n-1].Kind == "pass" {
			out[n-1].Data += s.Data
			continue
		}
		out = append(out, s)
	}
	return out
}

type detectVector struct {
	Name  string `json:"name"`
	N     int    `json:"n"`
	Late  bool   `json:"late"`
	Steps []struct {
		At    int64   `json:"at"`
		Start bool    `json:"start"`
		DA1   bool    `json:"da1"`
		OSC   *string `json:"osc"`
		Tick  bool    `json:"tick"`
		End   bool    `json:"end"`
		// What the Detector must then say, for the keys present.
		Took     *bool            `json:"took"`
		State    *string          `json:"state"`
		Decided  *bool            `json:"decided"`
		Done     *bool            `json:"done"`
		Deadline *json.RawMessage `json:"deadline"`
	} `json:"steps"`
	// Caps are checked when present; null means none.
	Caps json.RawMessage `json:"caps"`
}

// TestDetectVectors runs each vector's steps through one Detector, with
// each step's time in milliseconds from the Unix epoch.
func TestDetectVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Detect {
		t.Run(v.Name, func(t *testing.T) {
			d := Detector{N: v.N, Late: v.Late}
			var dec Decoder
			for i, st := range v.Steps {
				now := time.UnixMilli(st.At)
				took := false
				switch {
				case st.Start:
					want := Query(max(v.N, 1))
					if v.Late {
						want = Query(max(v.N, 1), Late())
					}
					if q := d.Start(now); q != want {
						t.Errorf("step %d: Start returned %q, want %q", i, q, want)
					}
				case st.DA1:
					took = d.DA1(now)
				case st.OSC != nil:
					m, res := dec.Feed(*st.OSC)
					if r, ok := ReplyOf(m); res == Complete && ok {
						took = d.Reply(r, now)
					}
				case st.Tick:
					d.Tick(now)
				case st.End:
					d.End(now)
				}
				at := fmt.Sprintf("step %d (at %d)", i, st.At)
				if st.Took != nil && took != *st.Took {
					t.Errorf("%s: took %v, want %v", at, took, *st.Took)
				}
				if st.State != nil && d.State.String() != *st.State {
					t.Errorf("%s: state %v, want %s", at, d.State, *st.State)
				}
				if st.Decided != nil && d.Decided != *st.Decided {
					t.Errorf("%s: decided %v, want %v", at, d.Decided, *st.Decided)
				}
				if st.Done != nil && d.Done != *st.Done {
					t.Errorf("%s: done %v, want %v", at, d.Done, *st.Done)
				}
				if st.Deadline != nil {
					want := string(*st.Deadline)
					got := "null"
					if !d.Deadline.IsZero() {
						got = strconv.FormatInt(d.Deadline.UnixMilli(), 10)
					}
					if got != want {
						t.Errorf("%s: deadline %s, want %s", at, got, want)
					}
				}
			}
			switch {
			case string(v.Caps) == "null":
				if d.Caps.V != "" || d.Caps.Raw != nil {
					t.Errorf("caps %+v, want none", d.Caps)
				}
			case v.Caps != nil:
				var want map[string]any
				if err := json.Unmarshal(v.Caps, &want); err != nil {
					t.Fatal(err)
				}
				check(t, "caps", capsView(d.Caps, want), want)
			}
		})
	}
}

type keysVector struct {
	Name     string          `json:"name"`
	Requires []string        `json:"requires"`
	Input    *string         `json:"input"`
	Keys     []*string       `json:"keys"`
	Key      string          `json:"key"`
	Mod      string          `json:"mod"`
	Canon    *string         `json:"canon"`
	Text     json.RawMessage `json:"text"`
}

// TestKeysVectors decodes each vector's input, reads its key's name, with
// a Platform's mod where it has one, or asks for the text it types.
func TestKeysVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Keys {
		t.Run(v.Name, func(t *testing.T) {
			applies(t, v.Requires)
			if v.Input != nil {
				var want []string
				for _, k := range v.Keys {
					if k == nil {
						want = append(want, "")
					} else {
						want = append(want, *k)
					}
				}
				if got := DecodeKeys([]byte(*v.Input)); !slices.Equal(got, want) {
					t.Errorf("DecodeKeys(%q) = %q, want %q", *v.Input, got, want)
				}
				return
			}
			call, got, ok, want := "ParseKey", "", false, v.Canon
			switch {
			case v.Text != nil:
				var text *string
				if err := json.Unmarshal(v.Text, &text); err != nil {
					t.Fatal(err)
				}
				call, want = "KeyText", text
				got, ok = KeyText(v.Key)
			case v.Mod != "":
				call = "ReadKey"
				got, ok = Platform{Mod: v.Mod}.ReadKey(v.Key)
			default:
				got, ok = ParseKey(v.Key)
			}
			switch {
			case want == nil && ok:
				t.Errorf("%s(%q) = %q, want none", call, v.Key, got)
			case want != nil && (!ok || got != *want):
				t.Errorf("%s(%q) = %q, %v, want %q", call, v.Key, got, ok, *want)
			}
		})
	}
}

type keymapVector struct {
	Name         string             `json:"name"`
	Requires     []string           `json:"requires"`
	Parse        *string            `json:"parse"`
	Format       string             `json:"format"`
	TerminalKeys bool               `json:"terminal_keys"`
	Multiline    bool               `json:"multiline"`
	Keys         []string           `json:"keys"`
	Lookup       map[string]*string `json:"lookup"`
	Selects      map[string]bool    `json:"selects"`
	Program      map[string]bool    `json:"program"`
	Prefix       map[string]bool    `json:"prefix"`
	Scroll       map[string]*string `json:"scroll"`
	Platform     struct {
		Mod  string              `json:"mod"`
		Edit map[string][]string `json:"edit"`
	} `json:"platform"`
}

// TestKeymapVectors formats keymaps, looks keys up in resolved ones and
// asks which select, and asks an element's keymap, with no default, which
// keys it gives the program, which start a prefix and which scroll actions
// it binds them to.
func TestKeymapVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Keymap {
		t.Run(v.Name, func(t *testing.T) {
			applies(t, v.Requires)
			if v.Program != nil || v.Prefix != nil || v.Scroll != nil {
				m := ParseKeymap(Platform(v.Platform), strings.Join(v.Keys, " "))
				for k, want := range v.Program {
					if got := m.Program(k); got != want {
						t.Errorf("Program(%q) = %v, want %v", k, got, want)
					}
				}
				for k, want := range v.Prefix {
					if got := m.Prefix(k); got != want {
						t.Errorf("Prefix(%q) = %v, want %v", k, got, want)
					}
				}
				for k, want := range v.Scroll {
					w := Action("")
					if want != nil {
						w = Action(*want)
					}
					if got := m.Scroll(k); got != w {
						t.Errorf("Scroll(%q) = %q, want %q", k, got, w)
					}
				}
				return
			}
			if v.Lookup == nil {
				value := TerminalKeys
				if v.Parse != nil {
					value = *v.Parse
				}
				if got := ParseKeymap(Platform(v.Platform), value).Format(); got != v.Format {
					t.Errorf("Format() = %q, want %q", got, v.Format)
				}
				return
			}
			values := v.Keys
			if v.TerminalKeys {
				values = append([]string{TerminalKeys}, values...)
			}
			m := Resolve(Platform(v.Platform), v.Multiline, values...)
			for k, want := range v.Lookup {
				w := Action("")
				if want != nil {
					w = Action(*want)
				}
				if got := m.Lookup(k); got != w {
					t.Errorf("Lookup(%q) = %q, want %q", k, got, w)
				}
			}
			for k, want := range v.Selects {
				if got := m.Selects(k); got != want {
					t.Errorf("Selects(%q) = %v, want %v", k, got, want)
				}
			}
		})
	}
}
