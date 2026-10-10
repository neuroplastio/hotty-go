package hottytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/neuroplastio/hotty-go"
	"github.com/tinylib/msgp/msgp"
)

// drain takes what the host has sent the program, unread.
func (h *Host) drain() string {
	h.in.mu.Lock()
	defer h.in.mu.Unlock()
	s := string(h.in.buf)
	h.in.buf = nil
	return s
}

// replies decodes what the host has sent the program since the last drain.
func (h *Host) sentReplies() []hotty.Reply {
	var d hotty.Decoder
	var out []hotty.Reply
	for _, seq := range oscs(h.drain()) {
		if m, r := d.Feed(seq); r == hotty.Complete {
			if rep, ok := hotty.ReplyOf(m); ok {
				out = append(out, rep)
			}
		}
	}
	return out
}

// oscs cuts a stream into its OSC sequences.
func oscs(stream string) []string {
	var out []string
	for i := 0; i < len(stream); i++ {
		if stream[i] != 0x1b || i+1 >= len(stream) || stream[i+1] != ']' {
			continue
		}
		for j := i + 2; j < len(stream); j++ {
			if stream[j] == 0x07 {
				out = append(out, stream[i:j+1])
				i = j
				break
			}
			if stream[j] == 0x1b && j+1 < len(stream) && stream[j+1] == '\\' {
				out = append(out, stream[i:j+2])
				i = j + 1
				break
			}
		}
	}
	return out
}

type vectorStep struct {
	Send    map[string]string `json:"send"`
	Payload *string           `json:"payload"`
	Reply   json.RawMessage   `json:"reply"`
	Inspect []string          `json:"inspect"`
	Expect  json.RawMessage   `json:"expect"`
	Pointer string            `json:"pointer"`
	Key     *string           `json:"key"`
	Keys    []string          `json:"keys"`
	Term    *bool             `json:"terminal"`
	Events  []vectorEvent     `json:"events"`
	HasEvs  bool              `json:"-"`
}

type vectorEvent struct {
	E      string          `json:"e"`
	S      string          `json:"s"`
	T      string          `json:"t"`
	Detail json.RawMessage `json:"detail"`
}

// keyName is a key step's key as SPEC §10.4 names it: the modifiers held,
// in its order, before the key; Shift left out before a character.
func keyName(st vectorStep) string {
	held := map[string]bool{}
	for _, k := range st.Keys {
		held[k] = true
	}
	name := *st.Key
	if name == " " {
		name = "Space"
	}
	var b strings.Builder
	for _, m := range []struct{ held, name string }{{"ctrl", "Control"}, {"alt", "Alt"}, {"meta", "Meta"}, {"shift", "Shift"}} {
		if held[m.held] && (m.held != "shift" || utf8.RuneCountInString(*st.Key) != 1) {
			b.WriteString(m.name + "+")
		}
	}
	return b.String() + name
}

// sentEvents decodes the events the host has sent the program since the
// last drain.
func (h *Host) sentEvents() []hotty.Event {
	var d hotty.Decoder
	var out []hotty.Event
	for _, seq := range oscs(h.drain()) {
		if m, r := d.Feed(seq); r == hotty.Complete {
			if e, ok := hotty.EventOf(m); ok {
				out = append(out, e)
			}
		}
	}
	return out
}

// checkKey presses a key step's key, and checks where it went and the
// events it made.
func checkKey(t *testing.T, i int, h *Host, st vectorStep) {
	t.Helper()
	h.drain()
	name := keyName(st)
	used := h.Key(name)
	if st.Term != nil && used == *st.Term {
		t.Errorf("step %d (%s): reached the program %v, want %v", i, name, !used, *st.Term)
	}
	if !st.HasEvs {
		return
	}
	got := h.sentEvents()
	if len(got) != len(st.Events) {
		t.Errorf("step %d (%s): %d events %v, want %d", i, name, len(got), got, len(st.Events))
		return
	}
	for j, w := range st.Events {
		g := got[j]
		if g.Kind != w.E || g.Surface != w.S || g.Target != w.T {
			t.Errorf("step %d (%s): event %d is %s s=%s t=%s, want %s s=%s t=%s", i, name, j, g.Kind, g.Surface, g.Target, w.E, w.S, w.T)
			continue
		}
		if w.Detail == nil {
			continue
		}
		// Each value of its type (conformance/README.md, Numbers).
		gd, wd := fromMsgpack(g.Detail), fromJSON(w.Detail)
		// This host lays nothing out, so it knows no element's area.
		if m, ok := wd.(map[string]any); ok {
			delete(m, "area")
			if len(m) == 0 && gd == nil {
				continue
			}
		}
		if !reflect.DeepEqual(gd, wd) {
			t.Errorf("step %d (%s): event %d detail %#v, want %s", i, name, j, gd, w.Detail)
		}
	}
}

// fromMsgpack is a body's value: ints as int64, floats as float64; nil
// for none.
func fromMsgpack(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	v, err := msgp.NewReader(bytes.NewReader(b)).ReadIntf()
	if err != nil {
		return fmt.Sprintf("not msgpack: %x", b)
	}
	return normal(v)
}

func normal(v any) any {
	switch v := v.(type) {
	case uint64:
		return int64(v)
	case float32:
		return float64(v)
	case []any:
		for i := range v {
			v[i] = normal(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = normal(v[k])
		}
	}
	return v
}

// ofType says why v is not of type t (conformance/README.md, Send: types),
// or nothing when it is: a field t names that v lacks is not checked.
func ofType(v, t any, at string) string {
	switch t := t.(type) {
	case string:
		ok := false
		switch t {
		case "int":
			_, ok = v.(int64)
		case "float":
			_, ok = v.(float64)
		case "str":
			_, ok = v.(string)
		case "bool":
			_, ok = v.(bool)
		}
		if !ok {
			return fmt.Sprintf("%s: %T %v, want %s", at, v, v, t)
		}
	case []any:
		a, ok := v.([]any)
		if !ok {
			return fmt.Sprintf("%s: %T, want an array", at, v)
		}
		for i, x := range a {
			if why := ofType(x, t[0], fmt.Sprintf("%s[%d]", at, i)); why != "" {
				return why
			}
		}
	case map[string]any:
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: %T, want a map", at, v)
		}
		for k, x := range m {
			f, ok := t["*"]
			if !ok {
				f, ok = t[k]
			}
			if !ok {
				continue
			}
			if why := ofType(x, f, at+"."+k); why != "" {
				return why
			}
		}
	}
	return ""
}

// fromJSON is a vector's value: a number with a fraction or an exponent a
// float64, any other an int64.
func fromJSON(raw []byte) any {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil
	}
	return numbers(v)
}

func numbers(v any) any {
	switch v := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			f, _ := v.Float64()
			return f
		}
		n, _ := v.Int64()
		return n
	case []any:
		for i := range v {
			v[i] = numbers(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = numbers(v[k])
		}
	}
	return v
}

// vectorControl is a vector's control as the program sends it: a first, the
// rest in a stable order.
func vectorControl(m map[string]string) hotty.Control {
	keys := make([]string, 0, len(m))
	for k := range m {
		if k != "a" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var c hotty.Control
	if a, ok := m["a"]; ok {
		c = hotty.With(c, "a", a)
	}
	for _, k := range keys {
		c = hotty.With(c, k, m[k])
	}
	return c
}

// The host passes the HOTTY conformance vectors, as hotty-blitz and
// xterm-addon-hotty do.
func TestConformanceVectors(t *testing.T) {
	data, err := os.ReadFile("../testdata/conformance/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Vectors []struct {
			Name     string            `json:"name"`
			Requires json.RawMessage   `json:"requires"`
			Steps    []json.RawMessage `json:"steps"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, vec := range v.Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			h := New(t, Lenient())
			steps := make([]vectorStep, len(vec.Steps))
			// Keys are pressed only in vectors with no pointer, and that
			// require nothing: this host lays nothing out, so where a
			// pointer is says nothing here, and it has no passthrough,
			// hover or scroll.
			keys := len(vec.Requires) == 0
			for i, raw := range vec.Steps {
				if err := json.Unmarshal(raw, &steps[i]); err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &fields)
				_, steps[i].HasEvs = fields["events"]
				if steps[i].Pointer != "" {
					keys = false
				}
			}
			for i, st := range steps {
				if st.Inspect != nil {
					checkInspect(t, i, h, st)
					continue
				}
				if st.Pointer != "" {
					continue
				}
				if st.Key != nil {
					if keys {
						checkKey(t, i, h, st)
					}
					continue
				}
				payload := ""
				if st.Payload != nil {
					payload = *st.Payload
				}
				_, _ = h.Write([]byte(hotty.Encode(vectorControl(st.Send), []byte(payload))))
				replies := h.sentReplies()
				switch {
				case st.Reply == nil:
				case string(st.Reply) == "null":
					if len(replies) > 0 {
						t.Errorf("step %d: a reply where none is due: %+v", i, replies[0].Message.Control)
					}
				default:
					var want map[string]string
					var types struct {
						Types json.RawMessage `json:"types"`
						Body  json.RawMessage `json:"body"`
					}
					if err := json.Unmarshal(st.Reply, &types); err != nil {
						t.Fatal(err)
					}
					reply := st.Reply
					if types.Types != nil || types.Body != nil {
						var all map[string]json.RawMessage
						_ = json.Unmarshal(st.Reply, &all)
						delete(all, "types")
						delete(all, "body")
						reply, _ = json.Marshal(all)
					}
					if err := json.Unmarshal(reply, &want); err != nil {
						t.Fatal(err)
					}
					if len(replies) == 0 {
						t.Errorf("step %d: no reply, want %v", i, want)
						continue
					}
					r := replies[0]
					// This host lays nothing out: auto rows are an estimate
					// (AutoRows), so a reply's r to r=auto is not checked.
					if st.Send["a"] == "place" && (st.Send["r"] == "" || st.Send["r"] == "auto") {
						delete(want, "r")
					}
					for k, val := range want {
						var got string
						switch k {
						case "code":
							got = r.Code
						case "detail":
							got = r.Detail
						default:
							got = hotty.Get(r.Message.Control, k)
						}
						if got != val {
							t.Errorf("step %d: %s=%q, want %q (%v)", i, k, got, val, r.Message.Control)
						}
					}
					// The fields body names, equal as values, each of its
					// type (conformance/README.md, Send: body).
					if types.Body != nil {
						got, _ := fromMsgpack(r.Message.Payload).(map[string]any)
						want, _ := fromJSON(types.Body).(map[string]any)
						for k, w := range want {
							if g, ok := got[k]; !ok || !reflect.DeepEqual(g, w) {
								t.Errorf("step %d: body %s = %#v, want %#v", i, k, got[k], w)
							}
						}
					}
					if types.Types != nil {
						if r.Re == "q" && r.Caps == nil {
							t.Errorf("step %d: the capabilities do not decode: %x", i, r.Message.Payload)
						}
						var tt any
						_ = json.Unmarshal(types.Types, &tt)
						if why := ofType(fromMsgpack(r.Message.Payload), tt, "body"); why != "" {
							t.Errorf("step %d: %s", i, why)
						}
					}
				}
			}
		})
	}
}

func checkInspect(t *testing.T, i int, h *Host, st vectorStep) {
	t.Helper()
	s := h.Surface(st.Inspect[0])
	var e Element
	ok := false
	if s != nil {
		e, ok = s.Element(st.Inspect[1])
	}
	if string(st.Expect) == "null" {
		if ok {
			t.Errorf("step %d: #%s exists", i, st.Inspect[1])
		}
		return
	}
	if !ok {
		t.Errorf("step %d: no #%s in %s", i, st.Inspect[1], st.Inspect[0])
		return
	}
	var want struct {
		Tag      *string           `json:"tag"`
		Attrs    map[string]string `json:"attrs"`
		Text     *string           `json:"text"`
		Children *[][]*string      `json:"children"`
	}
	if err := json.Unmarshal(st.Expect, &want); err != nil {
		t.Fatal(err)
	}
	if want.Tag != nil && e.Tag != *want.Tag {
		t.Errorf("step %d: tag %q, want %q", i, e.Tag, *want.Tag)
	}
	if want.Attrs != nil && !reflect.DeepEqual(e.Attrs, want.Attrs) {
		t.Errorf("step %d: attrs %v, want %v", i, e.Attrs, want.Attrs)
	}
	if want.Text != nil && e.Text != *want.Text {
		t.Errorf("step %d: text %q, want %q", i, e.Text, *want.Text)
	}
	if want.Children != nil {
		var got [][]*string
		for _, c := range e.Children {
			tag, text := c.Tag, c.Text
			var id *string
			if c.HasID {
				cid := c.ID
				id = &cid
			}
			got = append(got, []*string{&tag, id, &text})
		}
		if len(got) != len(*want.Children) {
			t.Errorf("step %d: %d children, want %d", i, len(got), len(*want.Children))
			return
		}
		for j, w := range *want.Children {
			for k := range w {
				if (w[k] == nil) != (got[j][k] == nil) || w[k] != nil && *w[k] != *got[j][k] {
					t.Errorf("step %d: child %d: %v, want %v", i, j, deref(got[j]), deref(w))
					break
				}
			}
		}
	}
}

func deref(ss []*string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		if s != nil {
			out[i] = *s
		}
	}
	return out
}
