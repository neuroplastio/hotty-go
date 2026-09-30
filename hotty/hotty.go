// Package hotty speaks HOTTY (HTML Over The TTY, github.com/neuroplastio/hotty)
// from a Go program.
//
// It does no I/O. Commands are strings the program writes to its terminal
// output, like any escape sequence; in a Bubble Tea program, through tea.Raw,
// so they stay in order with its frames. Replies and events come back as OSC
// sequences on the program's input (SPEC §3.6, §9); Decoder turns them into
// messages.
package hotty

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// Number is HOTTY's OSC number: the ASCII codes of H and O.
	Number = "7279"
	// Chunk is the most payload bytes one OSC carries (SPEC §3.4).
	Chunk = 4096

	prefix = "\x1b]" + Number + ";"
	st     = "\x1b\\"
	// Payloads shorter than this are not worth compressing.
	compressFrom = 256
)

// Quiet says which replies the host sends (SPEC §3.5).
type Quiet int

const (
	Reply        Quiet = 0 // always reply
	ReplyOnError Quiet = 1 // reply only on error
	NoReply      Quiet = 2 // never reply
)

// KV is one control key and its value.
type KV struct{ K, V string }

// Control is a command's keys, in the order they are sent.
type Control []KV

// With returns the control with a key added.
func (c Control) With(k, v string) Control { return append(c, KV{k, v}) }

// Encode returns one command: the control and the payload, compressed when that
// makes it smaller, base64-encoded, and split into chunks of at most Chunk
// bytes (SPEC §3.3, §3.4).
func Encode(ctl Control, payload []byte) string {
	body := payload
	if len(payload) >= compressFrom {
		var z bytes.Buffer
		w, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
		_, _ = w.Write(payload)
		_ = w.Close()
		if z.Len() < len(payload) {
			ctl = ctl.With("o", "z")
			body = z.Bytes()
		}
	}
	b64 := base64.StdEncoding.EncodeToString(body)

	var quiet string
	parts := make([]string, 0, len(ctl))
	for _, kv := range ctl {
		parts = append(parts, kv.K+"="+kv.V)
		if kv.K == "q" {
			quiet = kv.V
		}
	}
	control := strings.Join(parts, ":")

	var b strings.Builder
	if len(b64) <= Chunk {
		b.WriteString(prefix + control)
		if b64 != "" {
			b.WriteString(";" + b64)
		}
		b.WriteString(st)
		return b.String()
	}
	for i := 0; i < len(b64); i += Chunk {
		end := min(i+Chunk, len(b64))
		more := "1"
		if end == len(b64) {
			more = "0"
		}
		if i == 0 {
			b.WriteString(prefix + control + ":m=1;" + b64[i:end] + st)
			continue
		}
		b.WriteString(prefix + "m=" + more)
		if quiet != "" {
			b.WriteString(":q=" + quiet)
		}
		b.WriteString(";" + b64[i:end] + st)
	}
	return b.String()
}

func q(v Quiet) string { return strconv.Itoa(int(v)) }

// --- commands (SPEC §4–§10) ---------------------------------------------------

// Query asks whether the terminal is a HOTTY host, fenced by Primary Device
// Attributes (SPEC §4): a host replies to the query before the DA1 answer
// every terminal sends.
func Query(n int) string {
	return Encode(Control{{"a", "q"}, {"n", strconv.Itoa(n)}}, nil) + "\x1b[c"
}

// Doc creates a surface, or replaces its document. Errors come back
// (EQUOTA: the terminal holds no more surfaces).
func Doc(surface, html string) string {
	return Encode(Control{{"a", "doc"}, {"s", surface}, {"q", q(ReplyOnError)}}, []byte(html))
}

// Place places a surface at the cursor, over cols columns and rows rows (0
// rows: auto). The host moves the cursor below it unless keepCursor.
func Place(surface string, cols, rows int, keepCursor bool, quiet Quiet) string {
	ctl := Control{{"a", "place"}, {"s", surface}, {"c", strconv.Itoa(cols)}}
	if rows > 0 {
		ctl = ctl.With("r", strconv.Itoa(rows))
	} else {
		ctl = ctl.With("r", "auto")
	}
	if keepCursor {
		ctl = ctl.With("C", "1")
	}
	return Encode(ctl.With("q", q(quiet)), nil)
}

// Window is the part of a surface a placement shows, in cells from its
// top-left corner (SPEC §5.2). The zero Window shows all of it.
type Window struct{ X, Y, W, H int }

// PlaceAt places a surface cols×rows cells, showing win of it with the
// window's top-left corner at cell (x, y), counted from 0, above or below
// the placements it overlaps by z (SPEC §5.2: greater above, 0 the usual),
// and leaves the cursor where it was: the way a full-screen program lays
// surfaces out without disturbing its own drawing. Errors come back
// (ENOENT: the host no longer has the document).
func PlaceAt(surface string, x, y, cols, rows int, win Window, z int) string {
	ctl := Control{{"a", "place"}, {"s", surface}, {"c", strconv.Itoa(cols)}, {"r", strconv.Itoa(rows)}}
	if win != (Window{}) && win != (Window{0, 0, cols, rows}) {
		ctl = append(ctl, KV{"x", strconv.Itoa(win.X)}, KV{"y", strconv.Itoa(win.Y)},
			KV{"w", strconv.Itoa(win.W)}, KV{"h", strconv.Itoa(win.H)})
	}
	if z != 0 {
		ctl = ctl.With("z", strconv.Itoa(z))
	}
	return "\x1b7" + fmt.Sprintf("\x1b[%d;%dH", y+1, x+1) +
		Encode(ctl.With("C", "1").With("q", q(ReplyOnError)), nil) + "\x1b8"
}

// Hide removes a surface's placement and keeps its document, to place it
// again without sending it (SPEC §5.4).
func Hide(surface string) string {
	return Encode(Control{{"a", "hide"}, {"s", surface}, {"q", q(NoReply)}}, nil)
}

// Op is a patch operation (SPEC §6.1).
type Op string

const (
	Morph   Op = "morph"
	Inner   Op = "inner"
	Replace Op = "replace"
	Append  Op = "append"
	Prepend Op = "prepend"
	Before  Op = "before"
	After   Op = "after"
	Remove  Op = "remove"
	Attr    Op = "attr"
	Unattr  Op = "unattr"
	Text    Op = "text"
	Var     Op = "var"
)

// Patch changes one surface's document. target is an element id ("" for a
// morph by top-level ids); key names the attribute or custom property.
func Patch(surface string, op Op, target, key string, payload []byte) string {
	ctl := Control{{"a", "patch"}, {"s", surface}, {"op", string(op)}}
	if target != "" {
		ctl = ctl.With("t", target)
	}
	if key != "" {
		ctl = ctl.With("k", key)
	}
	return Encode(ctl.With("q", q(NoReply)), payload)
}

// SetText replaces an element's children with one text node.
func SetText(surface, target, text string) string {
	return Patch(surface, Text, target, "", []byte(text))
}

// SetVar sets a custom property (--name) on an element: the cheap way to
// move a bar or a needle every frame.
func SetVar(surface, target, name, value string) string {
	return Patch(surface, Var, target, name, []byte(value))
}

// SetAttr sets an attribute on an element.
func SetAttr(surface, target, name, value string) string {
	return Patch(surface, Attr, target, name, []byte(value))
}

// MorphTo morphs an element (or, with no target, the document's elements by
// id) into html, keeping what the user is doing in what stays.
func MorphTo(surface, target, html string) string {
	return Patch(surface, Morph, target, "", []byte(html))
}

// Res stores a resource that documents refer to as cid:<id> (SPEC §7).
func Res(id, mime string, data []byte) string {
	return Encode(Control{{"a", "res"}, {"id", id}, {"type", mime}, {"q", q(NoReply)}}, data)
}

// Del deletes a surface and its placement.
func Del(surface string) string {
	return Encode(Control{{"a", "del"}, {"s", surface}, {"q", q(NoReply)}}, nil)
}

// DelAll deletes every surface.
func DelAll() string { return Encode(Control{{"a", "del"}, {"q", q(NoReply)}}, nil) }

// Focus gives a surface the keyboard, at an element if target is not empty.
func Focus(surface, target string) string {
	ctl := Control{{"a", "focus"}, {"s", surface}}
	if target != "" {
		ctl = ctl.With("t", target)
	}
	return Encode(ctl.With("q", q(NoReply)), nil)
}

// Blur takes the keyboard back from a surface.
func Blur(surface string) string {
	return Encode(Control{{"a", "blur"}, {"s", surface}, {"q", q(NoReply)}}, nil)
}

// Sync wraps commands in synchronized output (SPEC §6.3): the host shows all
// of them or none.
func Sync(cmds ...string) string {
	return "\x1b[?2026h" + strings.Join(cmds, "") + "\x1b[?2026l"
}

// --- receiving ----------------------------------------------------------------

// Message is one reply or event from the host.
type Message struct {
	Control map[string]string
	Payload []byte
}

// Get returns a control key's value.
func (m Message) Get(k string) string { return m.Control[k] }

// Decoder turns the OSC sequences a program reads into messages, joining
// chunked ones (SPEC §3.4).
type Decoder struct {
	pending *Message
	body    strings.Builder
}

// Feed takes one complete OSC sequence, as a terminal-input parser delivers
// it (ESC ] … ST or BEL). It reports whether seq was a HOTTY sequence, and
// returns a message when one is complete.
func (d *Decoder) Feed(seq string) (m Message, complete, isHotty bool) {
	body, ok := strings.CutPrefix(seq, prefix)
	if !ok {
		return Message{}, false, false
	}
	body = strings.TrimSuffix(strings.TrimSuffix(body, st), "\x07")
	ctlPart, payload, _ := strings.Cut(body, ";")
	ctl := map[string]string{}
	for _, kv := range strings.Split(ctlPart, ":") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			ctl[k] = v
		}
	}
	more := ctl["m"]
	if d.pending != nil {
		if len(ctl) > 2 || (len(ctl) == 2 && ctl["q"] == "") {
			// Another message before the last chunk aborts the pending one.
			d.pending, d.body = nil, strings.Builder{}
		} else {
			d.body.WriteString(payload)
			if more == "1" {
				return Message{}, false, true
			}
			m := *d.pending
			m.Payload = decode(d.body.String(), m.Control["o"])
			d.pending, d.body = nil, strings.Builder{}
			return m, true, true
		}
	}
	delete(ctl, "m")
	if more == "1" {
		d.pending = &Message{Control: ctl}
		d.body.WriteString(payload)
		return Message{}, false, true
	}
	return Message{Control: ctl, Payload: decode(payload, ctl["o"])}, true, true
}

func decode(b64, o string) []byte {
	b64 = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, b64)
	b, err := base64.StdEncoding.DecodeString(b64 + strings.Repeat("=", (4-len(b64)%4)%4))
	if err != nil {
		return nil
	}
	if o == "z" {
		r, err := zlib.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil
		}
		out, err := io.ReadAll(r)
		if err != nil {
			return nil
		}
		return out
	}
	return b
}
