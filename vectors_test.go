package hotty

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The conformance vectors' SDK sections (SDK.md §5, conformance/README.md):
// what this package builds, encodes and decodes. The scan and detect
// sections are for a Scanner and a Detector, which it does not have yet.

// features is what this package implements of what a vector may require.
// A vector that requires anything else is skipped: what is missing here is
// SDK.md's list of this package's gaps.
var features = map[string]bool{
	"place.hover":         true,
	"event.hover":         true,
	"decode.unterminated": true,
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

// segment is a part of a builder's output: bytes that are not HOTTY's, or
// one command, decoded.
type segment struct {
	Raw *string `json:"raw,omitempty"`
	Cmd *struct {
		Control map[string]string `json:"control"`
		Payload string            `json:"payload"`
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
		return Query(num("n"))
	case "doc":
		var docOpts []DocOption
		for _, o := range opts {
			docOpts = append(docOpts, o)
		}
		if d, _ := options["detached"].(bool); d {
			docOpts = append(docOpts, Detached())
		}
		return Doc(str("surface"), str("html"), docOpts...)
	case "place":
		return Place(str("surface"), placement(args["placement"]), opts...)
	case "place_at":
		return PlaceAt(str("surface"), num("x"), num("y"), placement(args["placement"]), opts...)
	case "hide":
		return Hide(str("surface"), opts...)
	case "delta":
		return Delta(str("surface"), Op(str("op")), str("target"), str("key"), []byte(str("payload")), opts...)
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

func placement(v any) Placement {
	m, _ := v.(map[string]any)
	num := func(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }
	flag := func(k string) bool { b, _ := m[k].(bool); return b }
	p := Placement{
		Cols: num(m, "cols"), Rows: num(m, "rows"), Z: num(m, "z"),
		Press: flag("press"), Fit: flag("fit"), Hover: flag("hover"), KeepCursor: flag("keep_cursor"),
	}
	if w, ok := m["window"].(map[string]any); ok {
		p.Window = Window{X: num(w, "x"), Y: num(w, "y"), W: num(w, "w"), H: num(w, "h")}
	}
	return p
}

// segments splits output into the bytes that are not HOTTY's, adjacent ones
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
				Control map[string]string `json:"control"`
				Payload string            `json:"payload"`
			}{m.Control, string(m.Payload)}}
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
				ctl = ctl.With(kv[0], kv[1])
				keys = append(keys, kv[0])
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
		})
	}
}

type decodeVector struct {
	Name     string   `json:"name"`
	Requires []string `json:"requires"`
	Seqs     []string `json:"seqs"`
	Results  []string `json:"results"`
	Invalid  int      `json:"invalid"`
	Messages []struct {
		Control map[string]string `json:"control"`
		Reply   map[string]any    `json:"reply"`
		Event   map[string]any    `json:"event"`
		Caps    map[string]any    `json:"caps"`
	} `json:"messages"`
}

func TestDecodeVectors(t *testing.T) {
	for _, v := range sdkVectors(t).Decode {
		t.Run(v.Name, func(t *testing.T) {
			applies(t, v.Requires)
			var d Decoder
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
				if !reflect.DeepEqual(m.Control, want.Control) {
					t.Errorf("message %d: control %v, want %v", i, m.Control, want.Control)
				}
				if want.Reply != nil {
					r, ok := m.Reply()
					if !ok {
						t.Fatalf("message %d is not a reply", i)
					}
					check(t, fmt.Sprintf("message %d: reply", i), replyView(r), want.Reply)
				}
				if want.Event != nil {
					e, ok := m.Event()
					if !ok {
						t.Fatalf("message %d is not an event", i)
					}
					check(t, fmt.Sprintf("message %d: event", i), eventView(e), want.Event)
				}
				if want.Caps != nil {
					r, _ := m.Reply()
					c, ok := r.Caps()
					if !ok {
						t.Fatalf("message %d carries no capabilities", i)
					}
					check(t, fmt.Sprintf("message %d: caps", i), capsView(c, want.Caps), want.Caps)
				}
			}
		})
	}
}

func replyView(r Reply) map[string]any {
	return map[string]any{
		"ok": r.OK, "re": r.Re, "n": r.N, "surface": r.Surface,
		"cols": r.Cols, "rows": r.Rows, "code": r.Code, "detail": r.Detail,
	}
}

func eventView(e Event) map[string]any {
	view := map[string]any{
		"surface": e.Surface, "kind": e.Kind, "target": e.Target,
		"value": e.Value(), "fields": e.Fields(),
	}
	if c, ok := e.Checked(); ok {
		view["checked"] = c
	}
	if href, url, ok := e.Link(); ok {
		view["link"] = map[string]any{"href": href, "url": url}
	}
	if w, h, ok := e.Size(); ok {
		view["size"] = map[string]any{"w": w, "h": h}
	}
	if r, ok := e.FitRows(); ok {
		view["fit_rows"] = r
	}
	if d, ok := e.Drag(); ok {
		view["drag"] = map[string]any{"c": d.Col, "r": d.Row, "keys": d.Keys}
	}
	if h, ok := e.Hover(); ok {
		view["hover"] = map[string]any{"c": h.Col, "r": h.Row, "out": h.Out}
	}
	return view
}

func capsView(c Caps, want map[string]any) map[string]any {
	w, h := c.CellCSS()
	view := map[string]any{
		"v": c.V, "ops": c.Ops, "events": c.Events,
		"cell":  map[string]any{"w": c.Cell.W, "h": c.Cell.H},
		"scale": c.Scale, "scheme": c.Scheme, "limits": c.Limits, "net": c.Net,
		"host": c.Host, "drags": c.Drags(), "hovers": c.Hovers(), "light": c.Light(),
		"cell_css": map[string]any{"w": w, "h": h},
	}
	supports, sends := map[string]any{}, map[string]any{}
	for op := range asMap(want["supports"]) {
		supports[op] = c.Supports(Op(op))
	}
	for kind := range asMap(want["sends"]) {
		sends[kind] = c.Sends(kind)
	}
	view["supports"], view["sends"] = supports, sends
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
