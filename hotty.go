// Package hotty speaks HOTTY, HTML Over The TTY
// (https://github.com/neuroplastio/hotty), from a Go program.
//
// A HOTTY host is a terminal that shows surfaces: small HTML documents
// placed on rectangles of cells. A program sends documents, placements and
// deltas as escape sequences in its ordinary output, and hears replies and
// the user's events on its input.
//
// This package is the wire, and does no I/O:
//
//   - The command functions (Doc, Place, Delta, …) return escape sequences
//     as strings. Write them to the terminal like any other output; in a
//     Bubble Tea program, through tea.Raw, so they stay in order with its
//     frames.
//   - A Decoder turns the OSC sequences the program reads back into
//     Messages: replies (ReplyOf) and events (EventOf), their msgpack
//     bodies read by the types in wire.go.
//
// The packages beside it do the I/O: term for a command that prints and
// exits or asks a question, hottytea for a full-screen Bubble Tea program,
// and hottytest for testing either against a host that runs in the test.
//
// # Replies
//
// Every command asks for the replies that are usually wanted: Doc and Place
// are answered on error (EQUOTA, ENOENT), everything else never. The
// options change that: Q sets the quiet level, and N numbers a command and
// asks for its reply. Replies arrive on the program's input, mixed with
// keys. A program that exits leaves unread replies to whatever reads the
// terminal next, a shell for instance, which reads them as typing.
//
// # Names and ids
//
// A surface name is 1 to 64 of A–Z, a–z, 0–9, '_' and '-' (SurfaceName
// makes one). Other control values, such as element ids, may be any
// printable ASCII but ':', ';' and '='. Encode replaces each character
// outside those with '_', as the specification has hosts do (SPEC §3.2),
// so a value can never end a command early or add a key to it.
package hotty

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
)

const (
	// Number is HOTTY's OSC number: the ASCII codes of H and O.
	Number = "7279"
	// Chunk is the most payload bytes one OSC carries (SPEC §3.4).
	Chunk = 4096
	// MaxSize is the most columns or rows a surface has (SPEC §5.2).
	MaxSize = 1000
	// MaxName is the longest a surface name is (SPEC §3.5).
	MaxName = 64
	// Version is the protocol version this package speaks: what its query
	// lists, and what a host's capabilities must name (SPEC §4).
	Version = "0.2"

	prefix = "\x1b]" + Number + ";"
	st     = "\x1b\\"
	// Payloads shorter than this are not worth compressing.
	compressFrom = 256
)

// Quiet says which replies the host sends for a command (SPEC §3.5).
type Quiet int

// The quiet levels.
const (
	ReplyAlways  Quiet = 0 // reply whatever the outcome
	ReplyOnError Quiet = 1 // reply only on error
	NoReply      Quiet = 2 // never reply
)

// KV is one control key and its value.
type KV struct{ K, V string }

// Control is a command's or a message's keys, in the order they are sent.
type Control []KV

// Get returns a control's value for a key, "" when it lacks the key.
func Get(c Control, k string) string {
	v, _ := Lookup(c, k)
	return v
}

// Lookup returns a control's value for a key, and whether it has the key.
func Lookup(c Control, k string) (string, bool) {
	for _, kv := range c {
		if kv.K == k {
			return kv.V, true
		}
	}
	return "", false
}

// Has reports whether a control has a key.
func Has(c Control, k string) bool {
	_, ok := Lookup(c, k)
	return ok
}

// With returns a copy of a control with a key set: in place if the
// control has it, at the end if not (SDK.md §3.2).
func With(c Control, k, v string) Control {
	out := slices.Clone(c)
	set(&out, k, v)
	return out
}

// Without returns a copy of a control without the keys given: what a
// relay forwards with the keys it owns taken off.
func Without(c Control, keys ...string) Control {
	out := make(Control, 0, len(c))
	for _, kv := range c {
		if !slices.Contains(keys, kv.K) {
			out = append(out, kv)
		}
	}
	return out
}

// set changes a key's value, or adds the key.
func set(c *Control, k, v string) {
	for i := range *c {
		if (*c)[i].K == k {
			(*c)[i].V = v
			return
		}
	}
	*c = append(*c, KV{k, v})
}

// ReplyOption sets the reply a command asks for (SPEC §3.5): every command
// takes one, Doc as a DocOption. N and Q make them, in either order.
type ReplyOption struct {
	n, q       int
	setN, setQ bool
}

// Q sets the command's quiet level. It wins over the level N implies,
// whichever comes first.
func Q(q Quiet) ReplyOption { return ReplyOption{q: int(q), setQ: true} }

// N numbers the command and asks for its reply whatever the outcome
// (ReplyAlways): the reply echoes n, so the program can tell which command
// it answers. A Q given with it asks for less.
func N(n int) ReplyOption { return ReplyOption{n: n, setN: true} }

// command encodes a command with its default quiet level and the options.
func command(ctl Control, payload []byte, def Quiet, opts []ReplyOption) string {
	q, n, numbered, given := int(def), 0, false, false
	for _, o := range opts {
		if o.setN {
			n, numbered = o.n, true
		}
		if o.setQ {
			q, given = o.q, true
		}
	}
	if numbered && !given {
		q = int(ReplyAlways)
	}
	set(&ctl, "q", strconv.Itoa(q))
	if numbered {
		set(&ctl, "n", strconv.Itoa(n))
	}
	return Encode(ctl, payload)
}

// Encode returns one command: the control and the payload, compressed when
// that makes it smaller, base64-encoded, and split into chunks of at most
// Chunk bytes (SPEC §3.3, §3.4). Keys are sent as they are; values are
// cleaned (Sanitize). The keys o and m are Encode's to set: a control
// given with them goes out without them.
func Encode(ctl Control, payload []byte) string { return encode(ctl, payload, true) }

// EncodePlain is Encode without compression: what a host, or a relay
// speaking as one, writes to a program, since hosts never compress
// (SPEC §3.3).
func EncodePlain(ctl Control, payload []byte) string { return encode(ctl, payload, false) }

func encode(ctl Control, payload []byte, compress bool) string {
	body := payload
	var zipped bool
	if compress && len(payload) >= compressFrom {
		var z bytes.Buffer
		w, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
		_, _ = w.Write(payload)
		_ = w.Close()
		if z.Len() < len(payload) {
			zipped, body = true, z.Bytes()
		}
	}
	b64 := base64.StdEncoding.EncodeToString(body)

	var control strings.Builder
	var quiet string
	for _, kv := range ctl {
		if kv.K == "o" || kv.K == "m" {
			continue
		}
		if control.Len() > 0 {
			control.WriteByte(':')
		}
		control.WriteString(kv.K + "=" + Sanitize(kv.V))
		if kv.K == "q" {
			quiet = Sanitize(kv.V)
		}
	}
	if zipped {
		if control.Len() > 0 {
			control.WriteByte(':')
		}
		control.WriteString("o=z")
	}

	var b strings.Builder
	if len(b64) <= Chunk {
		b.WriteString(prefix)
		b.WriteString(control.String())
		if b64 != "" {
			b.WriteString(";" + b64)
		}
		b.WriteString(st)
		return b.String()
	}
	for i := 0; i < len(b64); i += Chunk {
		end := min(i+Chunk, len(b64))
		if i == 0 {
			b.WriteString(prefix + control.String() + ":m=1;" + b64[i:end] + st)
			continue
		}
		more := "1"
		if end == len(b64) {
			more = "0"
		}
		b.WriteString(prefix + "m=" + more)
		if quiet != "" {
			b.WriteString(":q=" + quiet)
		}
		b.WriteString(";" + b64[i:end] + st)
	}
	return b.String()
}

// valueByte reports whether a control value may hold c: printable ASCII
// but ':', ';' and '=' (SPEC §3.2).
func valueByte(c byte) bool {
	return c >= 0x20 && c <= 0x7e && c != ':' && c != ';' && c != '='
}

// Sanitize makes v a control value (SPEC §3.2): each character a value may
// not hold becomes '_', one for each character, not each byte, and one for
// each byte that is not UTF-8. Above 0x7e is not merely untidy: UTF-8 can
// hold 0x9c, which a terminal that reads C1 controls takes for ST, ending
// the sequence early.
func Sanitize(v string) string {
	clean := true
	for i := 0; i < len(v); i++ {
		if !valueByte(v[i]) {
			clean = false
			break
		}
	}
	if clean {
		return v
	}
	var b strings.Builder
	for _, r := range v {
		if r < 0x80 && valueByte(byte(r)) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// nameByte reports whether a surface name may hold c (SPEC §3.5).
func nameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// ValidName reports whether s is a surface name a host accepts: 1 to 64 of
// A–Z, a–z, 0–9, '_' and '-' (SPEC §3.5).
func ValidName(s string) bool {
	if len(s) == 0 || len(s) > MaxName {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !nameByte(s[i]) {
			return false
		}
	}
	return true
}

// SurfaceName makes s a valid surface name: each character a name may not
// hold becomes '_', and the name is cut to 64 bytes. An empty s is "_".
func SurfaceName(s string) string {
	if ValidName(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if b.Len() == MaxName {
			break
		}
		if r < 0x80 && nameByte(byte(r)) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
