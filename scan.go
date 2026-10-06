package hotty

import (
	"bytes"
	"strconv"
)

// SegmentKind is what a Segment holds.
type SegmentKind uint8

// The kinds of Segment.
const (
	// SegmentPass: bytes that are not HOTTY's, unchanged: keys, mouse
	// reports, text, other escape sequences, other OSCs.
	SegmentPass SegmentKind = iota
	// SegmentOSC: one complete HOTTY sequence, from ESC ] 7279 ; to its
	// terminator (ST or BEL), ready for a Decoder.
	SegmentOSC
	// SegmentDA1: an answer to Primary Device Attributes, CSI ? <digits
	// and ;> c, from a Scanner made to look for them (Scanner.DA1).
	SegmentDA1
)

// String names the kind: "pass", "osc" or "da1".
func (k SegmentKind) String() string {
	switch k {
	case SegmentPass:
		return "pass"
	case SegmentOSC:
		return "osc"
	case SegmentDA1:
		return "da1"
	}
	return "SegmentKind(" + strconv.Itoa(int(k)) + ")"
}

// Segment is a piece of a scanned stream.
type Segment struct {
	Kind SegmentKind
	// Data is the segment's bytes. It may share memory with the bytes
	// given to Feed, so it is good until the caller changes those: a
	// caller that reuses its read buffer copies what it keeps.
	Data []byte
}

// scanMax is the longest HOTTY sequence a Scanner takes, its terminator
// not counted (SDK.md §3.7): no host sends one longer, since a chunk is at
// most Chunk bytes of base64.
const scanMax = 64 << 10

const da1Prefix = "\x1b[?"

type scanMode uint8

const (
	scanGround  scanMode = iota // between segments
	scanOSC                     // in a HOTTY sequence
	scanDiscard                 // in one too long, dropped up to its terminator
)

// Scanner cuts HOTTY sequences out of a byte stream, however the reads
// split it (SDK.md §3.7): a terminal's input, read raw, or a program's
// output, for a relay. Everything else goes out as it came, in order, in
// the Feed that brought it; the Scanner holds only what may still become a
// segment of its own, the start of ESC ] 7279 ; (or of a DA1 answer, with
// DA1) and a HOTTY sequence in progress. So the segments do not depend on
// how the stream was split, and a key is never kept waiting.
//
// An ESC inside a HOTTY sequence that does not begin its ST ends the
// sequence unfinished: it is dropped as malformed, and the ESC begins what
// comes next. A sequence longer than 64 KiB is dropped up to its
// terminator.
//
// Its zero value is ready to use. It is not safe for concurrent use.
type Scanner struct {
	// DA1 makes the Scanner cut out answers to Primary Device Attributes
	// as SegmentDA1, for a program that reads them itself (SDK.md §4.1).
	// Without it they pass.
	DA1 bool
	// Invalid counts the HOTTY sequences dropped so far.
	Invalid int

	mode   scanMode
	held   []byte // in scanGround: the start of a segment, from an earlier Feed
	seq    []byte // in scanOSC: the sequence so far, when not in the bytes fed
	inFeed bool   // in scanOSC: the sequence began in the bytes fed, at start
	start  int
	n      int  // in scanOSC: the sequence's length so far
	esc    bool // in scanOSC or scanDiscard: the last byte was ESC
}

// Feed takes the next bytes of the stream and returns its segments, in
// order. Adjacent SegmentPass segments are one.
func (s *Scanner) Feed(p []byte) []Segment {
	var out []Segment
	i := 0
	for i < len(p) {
		switch {
		case s.mode == scanGround && len(s.held) > 0:
			out = s.heldByte(out, p[i])
			i++
		case s.mode == scanGround:
			out, i = s.ground(out, p, i)
		case s.mode == scanOSC:
			out, i = s.osc(out, p, i)
		default:
			i = s.discard(p, i)
		}
	}
	if s.mode == scanOSC && s.inFeed {
		// The sequence goes on in the next Feed: keep what came of it.
		s.seq, s.inFeed = append([]byte(nil), p[s.start:]...), false
	}
	return out
}

// ground passes bytes up to the next segment, or the end of p.
func (s *Scanner) ground(out []Segment, p []byte, i int) ([]Segment, int) {
	run := i // the bytes passing, from run
	for {
		j := bytes.IndexByte(p[i:], 0x1b)
		if j < 0 {
			return push(out, SegmentPass, p[run:]), len(p)
		}
		k := i + j
		kind, end, partial := s.match(p[k:])
		switch {
		case partial:
			out = push(out, SegmentPass, p[run:k])
			s.held = append([]byte(nil), p[k:]...)
			return out, len(p)
		case kind == SegmentOSC:
			out = push(out, SegmentPass, p[run:k])
			s.mode, s.inFeed, s.start, s.n, s.esc = scanOSC, true, k, len(prefix), false
			return out, k + end
		case kind == SegmentDA1:
			out = push(out, SegmentPass, p[run:k])
			out = push(out, SegmentDA1, p[k:k+end])
			i, run = k+end, k+end
		default:
			i = k + 1 // an ESC that begins nothing of ours passes
		}
	}
}

// match tells what b, which begins with ESC, begins: a HOTTY sequence or
// a DA1 answer, ending at end; perhaps one, if the bytes that would tell
// have not come yet (partial); or neither (SegmentPass).
func (s *Scanner) match(b []byte) (kind SegmentKind, end int, partial bool) {
	if len(b) >= len(prefix) {
		if string(b[:len(prefix)]) == prefix {
			return SegmentOSC, len(prefix), false
		}
	} else if string(b) == prefix[:len(b)] {
		return SegmentPass, 0, true
	}
	if !s.DA1 {
		return SegmentPass, 0, false
	}
	if len(b) < len(da1Prefix) {
		return SegmentPass, 0, string(b) == da1Prefix[:len(b)]
	}
	if string(b[:len(da1Prefix)]) != da1Prefix {
		return SegmentPass, 0, false
	}
	for j := len(da1Prefix); j < len(b); j++ {
		switch c := b[j]; {
		case c == 'c':
			return SegmentDA1, j + 1, false
		case c != ';' && (c < '0' || c > '9'):
			return SegmentPass, 0, false
		}
	}
	return SegmentPass, 0, true
}

// heldByte takes one byte after what an earlier Feed left held.
func (s *Scanner) heldByte(out []Segment, c byte) []Segment {
	cand := append(s.held, c)
	kind, _, partial := s.match(cand)
	switch {
	case partial:
		s.held = cand
	case kind == SegmentOSC:
		s.held = nil
		s.mode, s.seq, s.inFeed, s.n, s.esc = scanOSC, cand, false, len(prefix), false
	case kind == SegmentDA1:
		s.held = nil
		out = push(out, SegmentDA1, cand)
	default:
		// What was held passes, and c is taken afresh.
		out = push(out, SegmentPass, cand[:len(cand)-1])
		s.held = nil
		if c == 0x1b {
			s.held = []byte{c}
		} else {
			out = push(out, SegmentPass, []byte{c})
		}
	}
	return out
}

// osc takes the bytes of a HOTTY sequence, up to its end or p's.
func (s *Scanner) osc(out []Segment, p []byte, i int) ([]Segment, int) {
	if s.esc {
		s.esc = false
		if p[i] == '\\' {
			return s.end(out, p, i), i + 1
		}
		// An ESC that is not ST ends the sequence unfinished: it is
		// dropped, and the ESC begins what follows.
		s.Invalid++
		s.drop(scanGround)
		s.held = []byte{0x1b}
		return s.heldByte(out, p[i]), i + 1
	}
	j := bytes.IndexAny(p[i:], "\x07\x1b")
	run := j
	if j < 0 {
		run = len(p) - i
	}
	if room := scanMax - s.n; run > room {
		// The byte after room is one too many.
		s.Invalid++
		s.drop(scanDiscard)
		return out, i + room + 1
	}
	if !s.inFeed {
		s.seq = append(s.seq, p[i:i+run]...)
	}
	s.n += run
	i += run
	if j < 0 {
		return out, i
	}
	if p[i] == 0x07 {
		return s.end(out, p, i), i + 1
	}
	// Perhaps the terminator, which the limit does not count.
	s.esc = true
	if !s.inFeed {
		s.seq = append(s.seq, 0x1b)
	}
	return out, i + 1
}

// end ends the sequence with its last byte, p[i].
func (s *Scanner) end(out []Segment, p []byte, i int) []Segment {
	var seq []byte
	if s.inFeed {
		seq = p[s.start : i+1]
	} else {
		seq = append(s.seq, p[i])
	}
	s.drop(scanGround)
	return push(out, SegmentOSC, seq)
}

// drop forgets the sequence in progress.
func (s *Scanner) drop(mode scanMode) {
	s.mode, s.seq, s.inFeed, s.n, s.esc = mode, nil, false, 0, false
}

// discard skips the bytes of a sequence too long, up to its terminator.
func (s *Scanner) discard(p []byte, i int) int {
	if s.esc {
		s.esc = false
		s.mode = scanGround
		if p[i] != '\\' {
			s.held = []byte{0x1b} // an ESC that is not ST begins what follows, with p[i]
			return i
		}
		return i + 1
	}
	j := bytes.IndexAny(p[i:], "\x07\x1b")
	if j < 0 {
		return len(p)
	}
	if p[i+j] == 0x07 {
		s.mode = scanGround
	} else {
		s.esc = true
	}
	return i + j + 1
}

// Flush ends the stream: what is held goes out as SegmentPass, except a
// HOTTY sequence in progress, which is dropped as malformed.
//
// A program reading keys flushes a lone ESC that a read ended with, as the
// Escape key (SDK.md §4.1), at once or when no more bytes follow soon:
//
//	segs := s.Feed(buf[:n])
//	if h := s.Holding(); len(h) == 1 && !s.InSequence() {
//		segs = append(segs, s.Flush()...) // a lone ESC
//	}
func (s *Scanner) Flush() []Segment {
	var out []Segment
	switch s.mode {
	case scanOSC:
		s.Invalid++
	case scanGround:
		out = push(out, SegmentPass, s.held)
	}
	s.held = nil
	s.drop(scanGround)
	return out
}

// Holding returns the bytes the Scanner holds for the next Feed: the start
// of a segment, or a HOTTY sequence in progress. They are good until the
// next Feed or Flush.
func (s *Scanner) Holding() []byte {
	if s.mode == scanOSC {
		return s.seq
	}
	return s.held
}

// InSequence reports whether a HOTTY sequence is in progress, or one too
// long is being dropped: the next Feed goes on with it, and Flush would
// drop it. Otherwise what Holding returns is the start of a segment, which
// a program reading keys may flush after a moment, as typing (an ESC, then
// ], is Alt+]).
func (s *Scanner) InSequence() bool { return s.mode != scanGround }

// push adds a segment, joining a SegmentPass to the one before it. A
// segment's capacity ends with it, so that a caller's append to its Data
// never writes over the bytes after it.
func push(out []Segment, kind SegmentKind, b []byte) []Segment {
	if len(b) == 0 {
		return out
	}
	b = b[:len(b):len(b)]
	if n := len(out); n > 0 && kind == SegmentPass && out[n-1].Kind == SegmentPass {
		out[n-1].Data = append(out[n-1].Data, b...)
		return out
	}
	return append(out, Segment{Kind: kind, Data: b})
}
