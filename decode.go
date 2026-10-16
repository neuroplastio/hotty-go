package hotty

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"strconv"
	"strings"
)

// Message is one HOTTY message: a reply or an event from the host, or a
// command from the program (what a host, a relay or a test decodes). Its
// control is in the order the keys came, so a relay that forwards it sends
// the same bytes.
type Message struct {
	Control Control
	Payload []byte
}

// Result is what a Decoder made of one sequence.
type Result int

// The results of Decoder.Feed.
const (
	// NotHotty: the sequence is not HOTTY's, and the caller handles it.
	NotHotty Result = iota
	// Partial: a chunk of a message that is not complete yet (SPEC §3.4).
	Partial
	// Complete: the message is complete.
	Complete
	// Invalid: the sequence was malformed, or aborted a chunked message,
	// and is dropped (SPEC §3.7).
	Invalid
)

// String names the result: "complete", "invalid", ….
func (r Result) String() string {
	switch r {
	case NotHotty:
		return "not hotty"
	case Partial:
		return "partial"
	case Complete:
		return "complete"
	case Invalid:
		return "invalid"
	}
	return "Result(" + strconv.Itoa(int(r)) + ")"
}

// Decoder turns OSC sequences into messages, joining chunked ones
// (SPEC §3.4). Its zero value is ready to use. It is not safe for
// concurrent use.
type Decoder struct {
	pending Control // the control of a chunked message
	body    strings.Builder
	over    bool // the pending message passed Max: its chunks are dropped
	// Max is the most bytes a message's payload may decode to, after
	// base64 and zlib; one that would decode to more is Invalid, and its
	// chunks are not kept (SPEC §3.4 lets a host limit a message). Zero is
	// DefaultMax. A host sets it to what it accepts from a program, at
	// least 1 MiB.
	Max int
	// Invalid counts the malformed messages dropped so far, aborted
	// chunked messages included.
	Invalid int
}

// DefaultMax is a Decoder's Max when it sets none: 16 MiB, enough for a
// document with its images inline, and a bound on what a peer can make a
// Decoder hold.
const DefaultMax = 16 << 20

// max is d's Max, or DefaultMax.
func (d *Decoder) max() int {
	if d.Max > 0 {
		return d.Max
	}
	return DefaultMax
}

// encodedMax is the most base64 a payload of max bytes is sent as: zlib may
// make it a little longer than it decodes to.
func encodedMax(max int) int {
	n := max + max/256 + 64
	return (n + 2) / 3 * 4
}

// Feed takes one complete OSC sequence, as a terminal-input parser
// delivers it: ESC ] … terminated by ST or BEL. When the result is
// Complete, m is the message.
//
// A malformed message (a control that does not parse, a payload that is
// not base64 or zlib) is Invalid. So is a chunked message that another
// message interrupts; the interrupting one is then decoded as usual, and
// its own result returned.
func (d *Decoder) Feed(seq string) (m Message, r Result) {
	body, ok := strings.CutPrefix(seq, prefix)
	if !ok {
		if seq == "\x1b]"+Number || strings.HasPrefix(seq, "\x1b]"+Number+"\x1b") || strings.HasPrefix(seq, "\x1b]"+Number+"\x07") {
			return Message{}, d.bad() // no control at all
		}
		return Message{}, NotHotty
	}
	switch {
	case strings.HasSuffix(body, st):
		body = body[:len(body)-len(st)]
	case strings.HasSuffix(body, "\x07"):
		body = body[:len(body)-1]
	}
	ctlPart, payload, _ := strings.Cut(body, ";")
	ctl, ok := parseControl(ctlPart)
	if !ok {
		return Message{}, d.bad()
	}
	more, hasMore := Lookup(ctl, "m")

	if d.pending != nil {
		if continuation(ctl) {
			if !d.over && d.body.Len()+len(payload) > encodedMax(d.max()) {
				// Too long: counted once, and its chunks dropped as they come.
				d.body, d.over = strings.Builder{}, true
				d.Invalid++
			}
			if !d.over {
				d.body.WriteString(payload)
			}
			if more == "1" {
				return Message{}, Partial
			}
			ctl, payload, over := d.pending, d.body.String(), d.over
			d.abort()
			if over {
				return Message{}, Invalid
			}
			return d.finish(ctl, payload)
		}
		// Another message before the last chunk aborts the pending one.
		if !d.over {
			d.Invalid++
		}
		d.abort()
	} else if continuation(ctl) {
		// A continuation with nothing to continue.
		return Message{}, d.invalid()
	}

	ctl = Without(ctl, "m")
	if hasMore && more == "1" {
		d.pending = ctl
		if len(payload) > encodedMax(d.max()) {
			d.over = true
			d.Invalid++
		} else {
			d.body.WriteString(payload)
		}
		return Message{}, Partial
	}
	return d.finish(ctl, payload)
}

func (d *Decoder) invalid() Result {
	d.Invalid++
	return Invalid
}

// bad drops a malformed message, and the chunked one it interrupts: two
// malformed messages (SPEC §3.7).
func (d *Decoder) bad() Result {
	if d.pending != nil {
		if !d.over {
			d.Invalid++
		}
		d.abort()
	}
	return d.invalid()
}

func (d *Decoder) abort() { d.pending, d.body, d.over = nil, strings.Builder{}, false }

func (d *Decoder) finish(ctl Control, payload string) (Message, Result) {
	if len(payload) > encodedMax(d.max()) {
		return Message{}, d.invalid()
	}
	o, _ := Lookup(ctl, "o")
	p, ok := decodePayload(payload, o, d.max())
	if !ok {
		return Message{}, d.invalid()
	}
	return Message{Control: Without(ctl, "o"), Payload: p}, Complete
}

// continuation reports whether a control is a further chunk's: m alone
// (SPEC §3.4).
func continuation(ctl Control) bool { return len(ctl) == 1 && ctl[0].K == "m" }

// parseControl reads key=value pairs separated by ':' (SPEC §3.2). A pair
// without '=', a key that is not a key, a value with a character values may
// not hold, or a key given twice, and the control does not parse.
func parseControl(s string) (Control, bool) {
	if s == "" {
		return nil, false
	}
	var ctl Control
	for kv := range strings.SplitSeq(s, ":") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !validKey(k) {
			return nil, false
		}
		for i := 0; i < len(v); i++ {
			if !valueByte(v[i]) {
				return nil, false
			}
		}
		if Has(ctl, k) {
			return nil, false
		}
		ctl = append(ctl, KV{k, v})
	}
	return ctl, true
}

// validKey: a letter, then letters, digits, '-' and '_'.
func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if i == 0 && !letter || !letter && !(c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// decodePayload reads base64, with or without padding and with whitespace
// in it, then zlib when o is "z" (SPEC §3.3). A payload that decodes to
// more than max bytes is refused, and inflating stops past it.
func decodePayload(b64, o string, max int) ([]byte, bool) {
	if strings.ContainsAny(b64, " \t\r\n") {
		b64 = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, b64)
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(b64, "="))
	if err != nil {
		return nil, false
	}
	if o != "z" {
		return b, len(b) <= max
	}
	r, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, false
	}
	out, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil || len(out) > max {
		return nil, false
	}
	return out, true
}
