package hotty

import (
	"bytes"
	"encoding/binary"
	"sync"
	"unicode/utf8"

	"github.com/tinylib/msgp/msgp"
)

// MaxDepth is how deep a body may nest, its map being the first level
// (SDK.md §3.9). A body nested deeper does not decode, so that no body can
// exhaust a stack: TinyGo's is 64 KB.
const MaxDepth = 32

// decodeBody reads a body into v, each field as its type (SDK.md §3.9),
// and reports whether it decoded: one msgpack map, nested at most MaxDepth
// deep, with nothing after it and nothing a host does not send in it
// (wellFormed), and no field v knows of another type.
func decodeBody(b []byte, v msgp.Decodable) bool {
	if !wellFormed(b) {
		return false
	}
	r := readers.Get().(*bodyReader)
	defer readers.Put(r)
	r.src.Reset(b)
	r.dec.Reset(&r.src)
	return v.DecodeMsg(r.dec) == nil
}

type bodyReader struct {
	src bytes.Reader
	dec *msgp.Reader
}

var readers = sync.Pool{New: func() any {
	r := &bodyReader{}
	r.dec = msgp.NewReader(&r.src)
	return r
}}

// encodeBody is v's msgpack: the body a host sends.
func encodeBody(v msgp.Encodable) []byte {
	w := writers.Get().(*bodyWriter)
	defer writers.Put(w)
	w.buf.Reset()
	w.enc.Reset(&w.buf)
	// A msgp.Writer's error does not stick: each call's is checked.
	if v.EncodeMsg(w.enc) != nil || w.enc.Flush() != nil {
		return nil
	}
	return bytes.Clone(w.buf.Bytes())
}

type bodyWriter struct {
	buf bytes.Buffer
	enc *msgp.Writer
}

var writers = sync.Pool{New: func() any {
	w := &bodyWriter{}
	w.enc = msgp.NewWriter(&w.buf)
	return w
}}

// maxInt is the furthest from zero an int in a body may be (SPEC §3.3), so
// that a reader whose numbers are doubles holds it exactly.
const maxInt = 1<<53 - 1

// wellFormed reports whether b is one msgpack map, nested at most MaxDepth
// deep, with nothing after it, that holds nothing SPEC §3.3 has no host
// send, anywhere in it: a nil, a map key that is not a str or is given
// twice in one map, a str that is not UTF-8, an int further than maxInt
// from zero, or a timestamp msgpack does not define (SDK.md §3.9). The
// generated codecs check the rest: they stop at the first field of another
// type, but count depth their own way, skip what they do not know unread,
// take a key given twice twice, and leave what follows the value.
func wellFormed(b []byte) bool {
	if len(b) == 0 || (b[0]&0xf0 != 0x80 && b[0] != 0xde && b[0] != 0xdf) {
		return false
	}
	// The containers open around the next value, the outermost first; the
	// first stands for the body. keys holds the keys of the maps open,
	// each map's after those of the maps around it.
	stack := make([]container, 1, MaxDepth+1)
	stack[0].left = 1
	var keys [][]byte
	i, n := uint64(0), uint64(len(b))
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.left == 0 {
			keys = keys[:top.keys]
			stack = stack[:len(stack)-1]
			continue
		}
		key := top.isMap && top.left%2 == 0
		top.left--
		if i >= n {
			return false
		}
		size, values, opens, ok := head(b[i:])
		if !ok || size > n-i {
			return false
		}
		v := b[i : i+size]
		if !allowed(v, key) || key && !newKey(top, &keys, v[strStart(v[0]):]) {
			return false
		}
		if opens {
			if len(stack) > MaxDepth {
				return false
			}
			c := b[i]
			stack = append(stack, container{left: values, isMap: c&0xf0 == 0x80 || c == 0xde || c == 0xdf, keys: len(keys)})
		}
		i += size
	}
	return i == n
}

// A container is an array or a map wellFormed is reading: the values it
// has still to give (a map's keys and their values in turn), and, for a
// map, where its keys start in the keys of the maps open, or the set of
// them once it has more than fewKeys.
type container struct {
	left  uint64
	isMap bool
	keys  int
	set   map[string]struct{}
}

// fewKeys is as many keys as a map's next key is compared with one by one,
// as a body's maps have; past them, a set holds a map's keys.
const fewKeys = 16

// newKey reports whether k is a key the map m has not given yet, and adds
// it to m's keys (SPEC §3.3: each key once).
func newKey(m *container, keys *[][]byte, k []byte) bool {
	if m.set != nil {
		if _, given := m.set[string(k)]; given {
			return false
		}
		m.set[string(k)] = struct{}{}
		return true
	}
	mine := (*keys)[m.keys:]
	for _, given := range mine {
		if bytes.Equal(given, k) {
			return false
		}
	}
	if len(mine) < fewKeys {
		*keys = append(*keys, k)
		return true
	}
	m.set = make(map[string]struct{}, 2*fewKeys)
	for _, given := range mine {
		m.set[string(given)] = struct{}{}
	}
	m.set[string(k)] = struct{}{}
	*keys = (*keys)[:m.keys]
	return true
}

// strStart is where a str's bytes start, past its length, for the first
// byte c of a str, and where an extension's type is, for that of one.
func strStart(c byte) int {
	switch c {
	case 0xd9, 0xc7:
		return 2
	case 0xda, 0xc8:
		return 3
	case 0xdb, 0xc9:
		return 5
	}
	return 1
}

// allowed reports whether a value a body may hold starts v, which is its
// head, and for a str, bin, number or extension the whole of it: no nil, a
// key only a str, a str UTF-8, an int within maxInt of zero, and a
// timestamp of 4, 8 or 12 bytes, its nanoseconds under a second and its
// seconds within maxInt of 1970.
func allowed(v []byte, key bool) bool {
	c := v[0]
	at := strStart(c)
	str := c >= 0xa0 && c <= 0xbf || c >= 0xd9 && c <= 0xdb
	switch {
	case key && !str, c == 0xc0:
		return false
	case str:
		return utf8.Valid(v[at:])
	case c == 0xcf:
		return binary.BigEndian.Uint64(v[1:]) <= maxInt
	case c == 0xd3:
		x := int64(binary.BigEndian.Uint64(v[1:]))
		return x >= -maxInt && x <= maxInt
	case c >= 0xd4 && c <= 0xd8, c >= 0xc7 && c <= 0xc9:
		if int8(v[at]) != -1 {
			return true // an extension no one defines: skipped
		}
		return timestamp(v[at+1:])
	}
	return true
}

// timestamp reports whether d is the data of a timestamp msgpack defines.
func timestamp(d []byte) bool {
	switch len(d) {
	case 4:
		return true
	case 8: // 30 bits of nanoseconds, then 34 of seconds
		return binary.BigEndian.Uint32(d)>>2 < 1e9
	case 12: // 32 bits of nanoseconds, then 64 of seconds
		sec := int64(binary.BigEndian.Uint64(d[4:]))
		return binary.BigEndian.Uint32(d) < 1e9 && sec >= -maxInt && sec <= maxInt
	}
	return false
}

// head reads the start of the msgpack value at the start of b: the bytes
// it takes besides the values inside it, and, for an array or a map, how
// many values are inside it (a map's keys counted). ok is false for a byte
// no type starts with, or a start cut short.
func head(b []byte) (size, values uint64, container, ok bool) {
	c := b[0]
	switch {
	case c <= 0x7f, c >= 0xe0, c == 0xc0, c == 0xc2, c == 0xc3:
		return 1, 0, false, true
	case c <= 0x8f:
		return 1, 2 * uint64(c&0x0f), true, true
	case c <= 0x9f:
		return 1, uint64(c & 0x0f), true, true
	case c <= 0xbf:
		return 1 + uint64(c&0x1f), 0, false, true
	}
	// length reads the w-byte length after the first byte.
	length := func(w int) (uint64, bool) {
		if len(b) < 1+w {
			return 0, false
		}
		switch w {
		case 1:
			return uint64(b[1]), true
		case 2:
			return uint64(binary.BigEndian.Uint16(b[1:])), true
		default:
			return uint64(binary.BigEndian.Uint32(b[1:])), true
		}
	}
	switch c {
	case 0xcc, 0xd0: // uint8, int8
		return 2, 0, false, true
	case 0xcd, 0xd1: // 16 bits
		return 3, 0, false, true
	case 0xca, 0xce, 0xd2: // 32 bits
		return 5, 0, false, true
	case 0xcb, 0xcf, 0xd3: // 64 bits
		return 9, 0, false, true
	case 0xd4, 0xd5, 0xd6, 0xd7, 0xd8: // fixext 1, 2, 4, 8, 16: a type byte, then the data
		return 2 + 1<<(c-0xd4), 0, false, true
	case 0xc4, 0xd9: // bin8, str8
		l, ok := length(1)
		return 2 + l, 0, false, ok
	case 0xc5, 0xda: // bin16, str16
		l, ok := length(2)
		return 3 + l, 0, false, ok
	case 0xc6, 0xdb: // bin32, str32
		l, ok := length(4)
		return 5 + l, 0, false, ok
	case 0xc7: // ext8: a length, a type byte, the data
		l, ok := length(1)
		return 3 + l, 0, false, ok
	case 0xc8:
		l, ok := length(2)
		return 4 + l, 0, false, ok
	case 0xc9:
		l, ok := length(4)
		return 6 + l, 0, false, ok
	case 0xdc: // array16
		l, ok := length(2)
		return 3, l, true, ok
	case 0xdd:
		l, ok := length(4)
		return 5, l, true, ok
	case 0xde: // map16
		l, ok := length(2)
		return 3, 2 * l, true, ok
	case 0xdf:
		l, ok := length(4)
		return 5, 2 * l, true, ok
	}
	return 0, 0, false, false // 0xc1, which no type starts with
}
