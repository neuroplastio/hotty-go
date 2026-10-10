package hotty

import (
	"bytes"
	"encoding/binary"
	"sync"

	"github.com/tinylib/msgp/msgp"
)

// MaxDepth is how deep a body may nest, its map being the first level
// (SDK.md §3.9). A body nested deeper does not decode, so that no body can
// exhaust a stack: TinyGo's is 64 KB.
const MaxDepth = 32

// decodeBody reads a body into v, each field as its type (SDK.md §3.9),
// and reports whether it decoded: one msgpack map, nested at most MaxDepth
// deep, with nothing after it, and no field v knows of another type.
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

// wellFormed reports whether b is one msgpack map, nested at most MaxDepth
// deep, with nothing after it. The generated codecs check the rest: they
// stop at the first field of another type, but count depth their own way
// and leave what follows the value unread.
func wellFormed(b []byte) bool {
	if len(b) == 0 || (b[0]&0xf0 != 0x80 && b[0] != 0xde && b[0] != 0xdf) {
		return false
	}
	// The values still to read in each container open around the next
	// one, the outermost first; the first counts the body itself.
	left := make([]uint64, 1, MaxDepth+1)
	left[0] = 1
	i, n := uint64(0), uint64(len(b))
	for len(left) > 0 {
		if left[len(left)-1] == 0 {
			left = left[:len(left)-1]
			continue
		}
		left[len(left)-1]--
		if i >= n {
			return false
		}
		size, values, container, ok := head(b[i:])
		if !ok || size > n-i {
			return false
		}
		i += size
		if container {
			if len(left) > MaxDepth {
				return false
			}
			left = append(left, values)
		}
	}
	return i == n
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
