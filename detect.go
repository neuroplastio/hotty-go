package hotty

import (
	"strconv"
	"time"
)

// DetectState is what a Detector has found.
type DetectState uint8

// The states of a Detector.
const (
	// Detecting: nothing is known yet.
	Detecting DetectState = iota
	// Native: the terminal is a HOTTY host.
	Native
	// Text: it is not; the program draws in cells.
	Text
)

// String names the state: "detecting", "native" or "text".
func (s DetectState) String() string {
	switch s {
	case Detecting:
		return "detecting"
	case Native:
		return "native"
	case Text:
		return "text"
	}
	return "DetectState(" + strconv.Itoa(int(s)) + ")"
}

// The Detector's times (SPEC §4): how long to wait for any answer at all;
// after a DA1 answer that came before any reply, for a reply that may still
// be on its way, since the DA1 may answer a question asked before the
// query; and after the host's reply, for the DA1 answer behind it, which
// would otherwise be left for whoever reads the terminal next.
const (
	DetectTimeout    = 1500 * time.Millisecond
	DetectAfterDA1   = 150 * time.Millisecond
	DetectAfterReply = 300 * time.Millisecond
)

// Detector decides whether the terminal is a HOTTY host (SDK.md §3.8). It
// is a state machine with the time passed in, so it behaves the same
// however the program reads the terminal: the program sends what Start
// returns, gives it each DA1 answer and each reply it reads, calls Tick at
// Deadline, and End when the input ends or it gives up.
//
// Every DA1 answer from the start until Done is detection's, and the
// program swallows it: it answers the query's fence, or a question asked
// before, and is no key. A reply that answers the query is detection's
// whenever it comes.
//
// With Late, the query asks for a late answer (SPEC §4): the first reply
// that answers it once the State is Text makes the State Native, with the
// host's capabilities, Decided and Done as it was. The program then starts
// using HOTTY.
//
// Decided and Done differ only for a host: it is known to be one when its
// reply arrives, and detection is over when the DA1 answer behind it does.
// A program that must not wait acts when Decided; one that hands the
// terminal on, when Done.
//
// Its zero value is ready to use. It is not safe for concurrent use.
type Detector struct {
	// N numbers the query: a reply that does not echo it answers someone
	// else's. 0 is 1.
	N int
	// Late asks for a late answer (Late, SPEC §4).
	Late bool

	// State is what the Detector has found, and Caps the host's
	// capabilities, when Native.
	State DetectState
	Caps  Caps
	// Decided is whether State is final; Done, whether detection is over.
	Decided, Done bool
	// Deadline is when to call Tick next; zero before Start and once Done.
	Deadline time.Time

	timeout, grace, after time.Time
}

func (d *Detector) n() int {
	if d.N == 0 {
		return 1
	}
	return d.N
}

// Start starts detection at now, and returns the query to send.
func (d *Detector) Start(now time.Time) string {
	d.timeout = now.Add(DetectTimeout)
	d.update()
	if d.Late {
		return Query(d.n(), Late())
	}
	return Query(d.n())
}

// DA1 takes a DA1 answer that arrived at now, and reports whether it was
// detection's.
func (d *Detector) DA1(now time.Time) bool {
	d.fire(now)
	defer d.update()
	if d.Done {
		return false
	}
	if d.State != Detecting {
		d.Done = true // the DA1 behind the host's reply
	} else if d.grace.IsZero() {
		d.grace = now.Add(DetectAfterDA1)
	}
	return true
}

// Reply takes a reply that arrived at now, and reports whether it answers
// the query: a=ok, re=q, and the query's n. One that comes after the
// Detector decided changes nothing, unless the query asked for a late
// answer (Late): then the first one that comes once the State is Text
// makes it Native.
func (d *Detector) Reply(r Reply, now time.Time) bool {
	d.fire(now)
	defer d.update()
	if !r.OK || r.Re != "q" || r.N != d.n() {
		return false
	}
	if d.State == Detecting {
		d.State, d.Decided = Native, true
		d.Caps, _ = r.Caps()
		d.after = now.Add(DetectAfterReply)
		if !d.timeout.IsZero() && d.timeout.Before(d.after) {
			d.after = d.timeout
		}
	} else if d.State == Text && d.Late {
		d.State = Native
		d.Caps, _ = r.Caps()
	}
	return true
}

// Tick tells the Detector the time is now.
func (d *Detector) Tick(now time.Time) {
	d.fire(now)
	d.update()
}

// End ends detection at now: the input ended, or the program gave up. A
// terminal that has not answered is not a host.
func (d *Detector) End(now time.Time) {
	d.fire(now)
	if !d.Decided {
		d.State, d.Decided = Text, true
	}
	d.Done = true
	d.update()
}

// fire makes happen what was due at or before now.
func (d *Detector) fire(now time.Time) {
	if d.Done || d.timeout.IsZero() {
		return
	}
	if d.State == Detecting {
		if !now.Before(d.timeout) || !d.grace.IsZero() && !now.Before(d.grace) {
			d.State, d.Decided, d.Done = Text, true, true
		}
	} else if !now.Before(d.after) {
		d.Done = true
	}
}

func (d *Detector) update() {
	switch {
	case d.Done || d.timeout.IsZero():
		d.Deadline = time.Time{}
	case d.State != Detecting:
		d.Deadline = d.after
	case !d.grace.IsZero() && d.grace.Before(d.timeout):
		d.Deadline = d.grace
	default:
		d.Deadline = d.timeout
	}
}
