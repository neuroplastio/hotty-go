// Package asciicast reads terminal recordings in asciinema's asciicast
// format, versions 2 and 3: a header with the terminal's size, then what the
// recorded program wrote, each piece at the time it wrote it, with markers a
// recording may carry between them. A player writes the output to a
// hottyvt.Screen of the header's size, at those times.
//
//	c, err := asciicast.Decode(f)
//	s := hottyvt.New(c.Cols, c.Rows)
//	for _, ev := range c.CapIdle(c.IdleTimeLimit).Events {
//		// wait until ev.Time, then:
//		if ev.Code == asciicast.Output {
//			s.WriteString(ev.Data)
//		}
//	}
//
// Times are from the recording's start in both versions: version 2 writes
// them so, and version 3 writes each as the interval since the event before,
// which Decode adds up.
package asciicast

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// Cast is a recording: its header, and its events in order.
type Cast struct {
	Header
	Events []Event
}

// Header is what a recording says about itself.
type Header struct {
	// Version is the format's version: 2 or 3.
	Version int
	// Cols and Rows are the terminal's size when the recording began.
	Cols, Rows int
	// Term is the terminal's type ($TERM), "" when not recorded.
	Term string
	// Title is the recording's title, "" when it has none.
	Title string
	// Timestamp is when the recording began; zero when not recorded.
	Timestamp time.Time
	// IdleTimeLimit is the longest pause the recording asks a player to
	// show (CapIdle); 0 for no limit.
	IdleTimeLimit time.Duration
}

// Code is an event's kind.
type Code string

// The event codes.
const (
	Output Code = "o" // what the program wrote to the terminal
	Input  Code = "i" // what was typed, when the recorder kept it
	Marker Code = "m" // a marker: a chapter, a caption; Data is its label
	Resize Code = "r" // the terminal's new size, "COLSxROWS" (Size)
	Exit   Code = "x" // the program's exit status (version 3)
)

// Event is one thing that happened in the recording.
type Event struct {
	// Time is when it happened, from the recording's start.
	Time time.Duration
	// Code is its kind; a player passes over a code it does not know.
	Code Code
	// Data is what was written or typed, a marker's label, or a size.
	Data string
}

// Size is a Resize event's new size.
func (e Event) Size() (cols, rows int, ok bool) {
	if e.Code != Resize {
		return 0, 0, false
	}
	c, r, found := strings.Cut(e.Data, "x")
	if !found {
		return 0, 0, false
	}
	cols, err1 := strconv.Atoi(c)
	rows, err2 := strconv.Atoi(r)
	if err1 != nil || err2 != nil || cols < 1 || rows < 1 {
		return 0, 0, false
	}
	return cols, rows, true
}

// Duration is the time of the last event: how long the recording plays.
func (c *Cast) Duration() time.Duration {
	if len(c.Events) == 0 {
		return 0
	}
	return c.Events[len(c.Events)-1].Time
}

// CapIdle is the recording with every pause longer than limit cut to limit,
// as a player shows a recording with an idle time limit. A limit of 0 or
// less leaves it as it is. The receiver is not changed.
func (c *Cast) CapIdle(limit time.Duration) *Cast {
	out := &Cast{Header: c.Header, Events: make([]Event, len(c.Events))}
	var last, shift time.Duration
	for i, ev := range c.Events {
		if gap := ev.Time - last; limit > 0 && gap > limit {
			shift += gap - limit
		}
		last = ev.Time
		ev.Time -= shift
		out.Events[i] = ev
	}
	return out
}

// header2 and header3 are the two versions' headers as written.
type header2 struct {
	Version       int               `json:"version"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	Timestamp     int64             `json:"timestamp"`
	Title         string            `json:"title"`
	IdleTimeLimit float64           `json:"idle_time_limit"`
	Env           map[string]string `json:"env"`
}

type header3 struct {
	Version int `json:"version"`
	Term    struct {
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
		Type string `json:"type"`
	} `json:"term"`
	Timestamp     int64   `json:"timestamp"`
	Title         string  `json:"title"`
	IdleTimeLimit float64 `json:"idle_time_limit"`
}

// Decode reads a recording, version 2 or 3. An event line it cannot read is
// an error that names the line; a version 3 comment (a line starting with
// "#") and a blank line are passed over.
func Decode(r io.Reader) (*Cast, error) {
	br := bufio.NewReader(r)
	n := 0
	next := func() ([]byte, error) {
		for {
			line, err := br.ReadBytes('\n')
			if err != nil && (len(line) == 0 || !errors.Is(err, io.EOF)) {
				return nil, err // the end, or a read that failed: not a line
			}
			n++
			line = bytes.TrimSpace(line)
			if len(line) == 0 || line[0] == '#' {
				if err != nil {
					return nil, err
				}
				continue
			}
			return line, nil
		}
	}

	line, err := next()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("asciicast: no header")
		}
		return nil, fmt.Errorf("asciicast: %w", err)
	}
	var v struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(line, &v); err != nil {
		return nil, fmt.Errorf("asciicast: line %d: header: %w", n, err)
	}
	c := &Cast{}
	switch v.Version {
	case 2:
		var h header2
		if err := json.Unmarshal(line, &h); err != nil {
			return nil, fmt.Errorf("asciicast: line %d: header: %w", n, err)
		}
		c.Header = Header{Version: 2, Cols: h.Width, Rows: h.Height, Term: h.Env["TERM"], Title: h.Title,
			Timestamp: unix(h.Timestamp), IdleTimeLimit: seconds(h.IdleTimeLimit)}
	case 3:
		var h header3
		if err := json.Unmarshal(line, &h); err != nil {
			return nil, fmt.Errorf("asciicast: line %d: header: %w", n, err)
		}
		c.Header = Header{Version: 3, Cols: h.Term.Cols, Rows: h.Term.Rows, Term: h.Term.Type, Title: h.Title,
			Timestamp: unix(h.Timestamp), IdleTimeLimit: seconds(h.IdleTimeLimit)}
	default:
		return nil, fmt.Errorf("asciicast: version %d: only versions 2 and 3 are read", v.Version)
	}
	if c.Cols < 1 || c.Rows < 1 {
		return nil, fmt.Errorf("asciicast: line %d: header: no terminal size", n)
	}

	var at time.Duration
	for {
		line, err := next()
		if errors.Is(err, io.EOF) {
			return c, nil
		}
		if err != nil {
			return nil, fmt.Errorf("asciicast: %w", err)
		}
		var raw []json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil || len(raw) != 3 {
			return nil, fmt.Errorf("asciicast: line %d: an event is [time, code, data]", n)
		}
		var t float64
		var code, data string
		if json.Unmarshal(raw[0], &t) != nil || json.Unmarshal(raw[1], &code) != nil ||
			json.Unmarshal(raw[2], &data) != nil || t < 0 || math.IsInf(t, 0) {
			return nil, fmt.Errorf("asciicast: line %d: an event is [time, code, data]", n)
		}
		if c.Version == 3 {
			at += seconds(t)
		} else {
			at = max(at, seconds(t))
		}
		c.Events = append(c.Events, Event{Time: at, Code: Code(code), Data: data})
	}
}

func seconds(s float64) time.Duration { return time.Duration(math.Round(s * float64(time.Second))) }

func unix(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}
