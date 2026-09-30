package hotty

import (
	"encoding/json"
	"strconv"
)

// Event is what the user did in a surface (SPEC §9).
type Event struct {
	Surface string
	Kind    string // click, change, input, submit, focus, blur, resize
	Target  string // the element's id; empty for focus, blur and resize
	Detail  json.RawMessage
}

// Event returns the message as an event, if it is one.
func (m Message) Event() (Event, bool) {
	if m.Get("a") != "ev" {
		return Event{}, false
	}
	return Event{Surface: m.Get("s"), Kind: m.Get("e"), Target: m.Get("t"), Detail: m.Payload}, true
}

// Value is the "value" field of an event's detail, if it has one.
func (e Event) Value() string {
	var d struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(e.Detail, &d)
	return d.Value
}

// Fields is a submit event's detail: the form's fields by name.
func (e Event) Fields() map[string]string {
	raw := map[string]any{}
	_ = json.Unmarshal(e.Detail, &raw)
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch v := v.(type) {
		case string:
			out[k] = v
		case nil:
		default:
			b, _ := json.Marshal(v)
			out[k] = string(b)
		}
	}
	return out
}

// ReplyMsg is the host's answer to a command (SPEC §3.6).
type ReplyMsg struct {
	OK      bool
	Re      string // the action answered
	N       int    // the request number, if the command had one
	Surface string
	Code    string // on error: EINVAL, ENOENT, ENOTARGET, EQUOTA, EBUDGET
	Detail  string
	Message Message
}

// Reply returns the message as a reply, if it is one.
func (m Message) Reply() (ReplyMsg, bool) {
	a := m.Get("a")
	if a != "ok" && a != "err" {
		return ReplyMsg{}, false
	}
	n, _ := strconv.Atoi(m.Get("n"))
	r := ReplyMsg{OK: a == "ok", Re: m.Get("re"), N: n, Surface: m.Get("s"), Message: m}
	if !r.OK {
		var e struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(m.Payload, &e)
		r.Code, r.Detail = e.Code, e.Detail
	}
	return r, true
}

// Caps is what a host says about itself in its reply to a query (SPEC §4).
type Caps struct {
	V      string   `json:"v"`
	Ops    []string `json:"ops"`
	Events []string `json:"events"`
	Cell   struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	} `json:"cell"`
	Scale  float64        `json:"scale"`
	Scheme string         `json:"scheme"`
	Limits map[string]int `json:"limits"`
	Host   string         `json:"host"`
}

// Caps returns the capabilities a reply to a query carries.
func (r ReplyMsg) Caps() (Caps, bool) {
	if !r.OK || r.Re != "q" {
		return Caps{}, false
	}
	var c Caps
	if err := json.Unmarshal(r.Message.Payload, &c); err != nil {
		return Caps{}, false
	}
	return c, true
}
