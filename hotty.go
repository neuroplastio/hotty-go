// Package hotty speaks HOTTY, HTML Over The TTY
// (https://github.com/neuroplastio/hotty), from a Go program.
//
// A HOTTY host is a terminal that shows surfaces: small HTML documents
// placed on rectangles of cells. A program sends documents, placements and
// patches as escape sequences in its ordinary output, and hears replies and
// the user's events on its input.
//
// This package is the wire, and does no I/O:
//
//   - The command functions (Doc, Place, Patch, …) return escape sequences
//     as strings. Write them to the terminal like any other output; in a
//     Bubble Tea program, through tea.Raw, so they stay in order with its
//     frames.
//   - A Decoder turns the OSC sequences the program reads back into
//     Messages: replies (Message.Reply) and events (Message.Event).
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
	// Version is the protocol version this package implements, as the
	// capabilities report it (SPEC §4).
	Version = "0.1"

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

// Control is a command's keys, in the order they are sent.
type Control []KV

// With returns the control with a key added.
func (c Control) With(k, v string) Control { return append(c, KV{k, v}) }

// Get returns a key's value, and whether the control has the key.
func (c Control) Get(k string) (string, bool) {
	for _, kv := range c {
		if kv.K == k {
			return kv.V, true
		}
	}
	return "", false
}

// set changes a key's value, or adds the key.
func (c *Control) set(k, v string) {
	for i := range *c {
		if (*c)[i].K == k {
			(*c)[i].V = v
			return
		}
	}
	*c = append(*c, KV{k, v})
}

// ReplyOption sets the reply a command asks for (SPEC §3.5): every command
// takes one, Doc as a DocOption.
type ReplyOption func(*Control)

// Q sets the command's quiet level.
func Q(q Quiet) ReplyOption {
	return func(c *Control) { c.set("q", strconv.Itoa(int(q))) }
}

// N numbers the command and asks for its reply whatever the outcome
// (ReplyAlways): the reply echoes n, so the program can tell which command
// it answers. Q after N asks for less.
func N(n int) ReplyOption {
	return func(c *Control) {
		c.set("n", strconv.Itoa(n))
		c.set("q", strconv.Itoa(int(ReplyAlways)))
	}
}

// command encodes a command with its default quiet level and the options.
func command(ctl Control, payload []byte, def Quiet, opts []ReplyOption) string {
	ctl.set("q", strconv.Itoa(int(def)))
	for _, o := range opts {
		o(&ctl)
	}
	return Encode(ctl, payload)
}

// Encode returns one command: the control and the payload, compressed when
// that makes it smaller, base64-encoded, and split into chunks of at most
// Chunk bytes (SPEC §3.3, §3.4). Keys are sent as they are; in values, each
// character that the control's grammar does not allow becomes '_'
// (SPEC §3.2).
func Encode(ctl Control, payload []byte) string {
	body := payload
	var zipped bool
	if len(payload) >= compressFrom {
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
	for i, kv := range ctl {
		if i > 0 {
			control.WriteByte(':')
		}
		control.WriteString(kv.K + "=" + cleanValue(kv.V))
		if kv.K == "q" {
			quiet = cleanValue(kv.V)
		}
	}
	if zipped {
		control.WriteString(":o=z")
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

// cleanValue replaces each character a control value may not hold with
// '_', one for each character, not each byte.
func cleanValue(v string) string {
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
