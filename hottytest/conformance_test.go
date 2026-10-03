package hottytest

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/neuroplastio/hotty-go"
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
			if rep, ok := m.Reply(); ok {
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
		c = c.With("a", a)
	}
	for _, k := range keys {
		c = c.With(k, m[k])
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
			Name  string       `json:"name"`
			Steps []vectorStep `json:"steps"`
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
			for i, st := range vec.Steps {
				if st.Inspect != nil {
					checkInspect(t, i, h, st)
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
					if err := json.Unmarshal(st.Reply, &want); err != nil {
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
							got = r.Message.Get(k)
						}
						if got != val {
							t.Errorf("step %d: %s=%q, want %q (%v)", i, k, got, val, r.Message.Control)
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
