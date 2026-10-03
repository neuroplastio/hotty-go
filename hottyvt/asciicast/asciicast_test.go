package asciicast

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

const v3 = `{"version": 3, "term": {"cols": 54, "rows": 11, "type": "xterm-256color"}, "timestamp": 1800000000, "title": "make test", "idle_time_limit": 1.5}
# a comment
[0.0, "o", "$ "]
[0.25, "o", "make test\r\n"]

[0.5, "m", "Tests run"]
[3.0, "r", "80x24"]
[0.125, "x", "0"]
`

const v2 = `{"version": 2, "width": 80, "height": 24, "timestamp": 1504467315, "env": {"TERM": "xterm", "SHELL": "/bin/zsh"}}
[0.0, "o", "hello"]
[1.5, "i", "q"]
[1.25, "o", "late"]
`

func TestDecodeVersion3AddsUpTheIntervals(t *testing.T) {
	c, err := Decode(strings.NewReader(v3))
	if err != nil {
		t.Fatal(err)
	}
	want := Header{Version: 3, Cols: 54, Rows: 11, Term: "xterm-256color", Title: "make test",
		Timestamp: time.Unix(1800000000, 0), IdleTimeLimit: 1500 * time.Millisecond}
	if c.Header != want {
		t.Errorf("header %+v, want %+v", c.Header, want)
	}
	ms := time.Millisecond
	events := []Event{
		{0, Output, "$ "}, {250 * ms, Output, "make test\r\n"}, {750 * ms, Marker, "Tests run"},
		{3750 * ms, Resize, "80x24"}, {3875 * ms, Exit, "0"},
	}
	if len(c.Events) != len(events) {
		t.Fatalf("events %+v, want %+v", c.Events, events)
	}
	for i := range events {
		if c.Events[i] != events[i] {
			t.Errorf("event %d is %+v, want %+v", i, c.Events[i], events[i])
		}
	}
	if d := c.Duration(); d != 3875*ms {
		t.Errorf("Duration %v", d)
	}
}

// Version 2 writes times from the start; one that goes back is taken as
// the time before it, so the events stay in order.
func TestDecodeVersion2TakesTimesFromTheStart(t *testing.T) {
	c, err := Decode(strings.NewReader(v2))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 2 || c.Cols != 80 || c.Rows != 24 || c.Term != "xterm" || c.IdleTimeLimit != 0 || !c.Timestamp.Equal(time.Unix(1504467315, 0)) {
		t.Errorf("header %+v", c.Header)
	}
	var got []time.Duration
	for _, ev := range c.Events {
		got = append(got, ev.Time)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 1500*time.Millisecond || got[2] != 1500*time.Millisecond {
		t.Errorf("times %v", got)
	}
}

func TestDecodeRefuses(t *testing.T) {
	for _, tc := range []struct{ name, in, err string }{
		{"empty", "", "no header"},
		{"only comments", "# nothing\n\n", "no header"},
		{"not JSON", "version 3\n", "line 1: header"},
		{"version 1", `{"version": 1, "width": 80, "height": 24}` + "\n", "version 1"},
		{"no size", `{"version": 3, "term": {}}` + "\n", "no terminal size"},
		{"v2 header of the wrong shape", `{"version": 2, "width": "80"}` + "\n", "line 1: header"},
		{"v3 header of the wrong shape", `{"version": 3, "term": []}` + "\n", "line 1: header"},
		{"an event that is no array", `{"version": 3, "term": {"cols": 1, "rows": 1}}` + "\n{}\n", "line 2: an event"},
		{"an event too short", `{"version": 3, "term": {"cols": 1, "rows": 1}}` + "\n" + `[0.1, "o"]` + "\n", "line 2: an event"},
		{"a time that is no number", `{"version": 3, "term": {"cols": 1, "rows": 1}}` + "\n" + `["0.1", "o", "x"]` + "\n", "line 2: an event"},
		{"a time before the start", `{"version": 3, "term": {"cols": 1, "rows": 1}}` + "\n" + `[-1, "o", "x"]` + "\n", "line 2: an event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("error %v, want one saying %q", err, tc.err)
			}
		})
	}
}

func TestDecodeReportsAReadError(t *testing.T) {
	broken := errors.New("broken")
	if _, err := Decode(iotest.ErrReader(broken)); !errors.Is(err, broken) {
		t.Errorf("error %v, want %v", err, broken)
	}
	// A read that fails inside an event's line: the part read is no event.
	r := io.MultiReader(strings.NewReader(`{"version": 3, "term": {"cols": 1, "rows": 1}}`+"\n"+`[0.1, "o", "x"]`), iotest.ErrReader(broken))
	if _, err := Decode(r); !errors.Is(err, broken) {
		t.Errorf("error %v after the header, want %v", err, broken)
	}
}

// The last line needs no newline.
func TestDecodeTakesALastLineWithoutANewline(t *testing.T) {
	c, err := Decode(strings.NewReader(`{"version": 3, "term": {"cols": 2, "rows": 1}}` + "\n" + `[0.5, "o", "x"]`))
	if err != nil || len(c.Events) != 1 || c.Events[0].Data != "x" {
		t.Errorf("Decode: %+v, %v", c, err)
	}
}

func TestSize(t *testing.T) {
	for _, tc := range []struct {
		ev         Event
		cols, rows int
		ok         bool
	}{
		{Event{Code: Resize, Data: "80x24"}, 80, 24, true},
		{Event{Code: Output, Data: "80x24"}, 0, 0, false},
		{Event{Code: Resize, Data: "80"}, 0, 0, false},
		{Event{Code: Resize, Data: "ax24"}, 0, 0, false},
		{Event{Code: Resize, Data: "0x24"}, 0, 0, false},
	} {
		cols, rows, ok := tc.ev.Size()
		if cols != tc.cols || rows != tc.rows || ok != tc.ok {
			t.Errorf("%+v.Size() = %d, %d, %v", tc.ev, cols, rows, ok)
		}
	}
}

func TestCapIdle(t *testing.T) {
	s := time.Second
	c := &Cast{Events: []Event{{Time: 0}, {Time: 1 * s}, {Time: 10 * s}, {Time: 11 * s}, {Time: 20 * s}}}
	capped := c.CapIdle(2 * s)
	var got []time.Duration
	for _, ev := range capped.Events {
		got = append(got, ev.Time)
	}
	want := []time.Duration{0, 1 * s, 3 * s, 4 * s, 6 * s}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CapIdle(2s) times %v, want %v", got, want)
		}
	}
	if c.Events[2].Time != 10*s {
		t.Error("CapIdle changed the recording it was called on")
	}
	if d := c.CapIdle(0).Duration(); d != 20*s {
		t.Errorf("CapIdle(0) plays %v, want the whole 20s", d)
	}
	if d := (&Cast{}).Duration(); d != 0 {
		t.Errorf("an empty recording plays %v", d)
	}
}
