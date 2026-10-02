// Package hottyterm is HOTTY for a program that is not a full-screen Bubble
// Tea program: a command that prints documents and exits, one that asks a
// question, a chart that streams. A Term is the process's terminal: it finds
// out whether the terminal is a HOTTY host, names surfaces so that two runs
// never share one, writes commands, and reads what comes back.
//
// The order of use:
//
//	t, err := hottyterm.Open("mytool") // no terminal: write plain data instead
//	defer t.Close()
//	if !t.Detect(ctx) {           // not a host: draw in cells
//		...
//	}
//	t.LineStart(ctx)              // surfaces are placed at the cursor
//	t.Print("card", html, hotty.Placement{Cols: 40})
//	replies, err := t.Fence(ctx)  // the terminal took it all; errors?
//
// # Input
//
// Everything the terminal sends is read once, by the Term: keys, the mouse,
// pastes, and HOTTY replies and events. What a call waits for goes to that
// call: Detect's answer, a Fence's DA1, a Request's numbered reply. The
// rest is delivered by Events, in order, from whenever it arrived. So a
// program may Request while another goroutine reads Events.
//
// # Leaving the terminal clean
//
// A command that exits hands the terminal to whatever runs next, usually a
// shell, which reads what the terminal sends as typing. So before it
// exits, a program reads every reply its commands caused (Fence), and
// leaves no surface that still reports events: it sends documents with
// hotty.Detached (Print does), or detaches them (DetachAll).
package hottyterm

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/internal/detect"
)

// Size is the terminal's size in cells.
type Size struct{ Cols, Rows int }

// Known is what a terminal is known to be without asking it, such as a
// terminal emulated in a page that always has the HOTTY addon.
type Known struct {
	Native bool
	Caps   hotty.Caps
}

// Message is a HOTTY message from the terminal, a reply or an event
// (SPEC §3.6, §9), as Events delivers it.
type Message struct{ hotty.Message }

// Event is what Events delivers: a Message, or one of ultraviolet's input
// events (uv.KeyPressEvent, uv.MouseClickEvent, uv.PasteEvent, …).
type Event = uv.Event

// Errors a Term returns.
var (
	// ErrNoTerminal is Open's error when the process has no terminal.
	ErrNoTerminal = errors.New("term: no terminal")
	// ErrNoAnswer is the error of a call that waited for the terminal in
	// vain: it did not answer in time, or its input ended.
	ErrNoAnswer = errors.New("term: the terminal did not answer")
)

// How long a call waits for the terminal, unless its context ends first.
const (
	// ReplyTimeout bounds Request: a terminal that is not a host never
	// replies.
	ReplyTimeout = 3 * time.Second
	// FenceTimeout bounds Fence and LineStart: every terminal answers them
	// at once, or never.
	FenceTimeout = time.Second
)

// Term is one process's terminal. Its methods are safe for concurrent use.
type Term struct {
	// In is what the terminal sends: keys, replies and events, as bytes.
	// The Term reads it once something asks (Detect, Events, …). Give it
	// to a Bubble Tea program instead (tea.WithInput), and use none of
	// those.
	In io.Reader
	// Out goes to the terminal. Write whole commands in one call (Send).
	Out io.Writer
	// Name prefixes every surface this process makes (Surface): the
	// program's name and a number unique on this terminal, such as its pid.
	Name string
	// TermType is $TERM, for decoding keys.
	TermType string

	size     func() (cols, rows int)
	known    *Known
	file     *os.File // natively, /dev/tty
	raw      func() (restore func(), err error)
	cancelIn func() // natively, ends a read in progress on In
	close    func() error

	mu       sync.Mutex
	surfaces []string
	sizes    chan Size // Sizes, made on first use
	closed   bool
	detected bool
	native   bool
	caps     hotty.Caps

	// The input: a goroutine reads In and routes each event to the first
	// tap that takes it, else to queue, for Events.
	started bool
	stop    context.CancelFunc
	taps    []*tap
	queue   []Event
	queued  chan struct{} // a token when queue grows
	ended   chan struct{} // closed when the input ends
	events  <-chan Event  // what Events returned
	nextN   int
}

// tap takes the events a call waits for. take runs on the reading
// goroutine with the Term's lock held: it must not block.
type tap struct{ take func(Event) bool }

// New makes a terminal from its two streams. name prefixes the process's
// surfaces (Surface) and is made a valid surface name. size reports the
// terminal's size in cells, as hottytest.Host.TermSize does; nil, or a
// size of 0, is 80×24. known, when not nil, is what Detect answers without
// asking.
func New(in io.Reader, out io.Writer, name string, size func() (cols, rows int), known *Known) *Term {
	return &Term{In: in, Out: out, Name: hotty.SurfaceName(name), TermType: "xterm-256color", size: size, known: known}
}

// Surface is a surface name of this process's own: Name, a dash, and name,
// made a valid surface name. Surfaces left in scrollback by an earlier run
// keep theirs. Every name it returns is remembered (Surfaces).
func (t *Term) Surface(name string) string {
	s := hotty.SurfaceName(t.Name + "-" + name)
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, have := range t.surfaces {
		if have == s {
			return s
		}
	}
	t.surfaces = append(t.surfaces, s)
	return s
}

// Surfaces are the names Surface has returned: what this process may have
// left on the terminal.
func (t *Term) Surfaces() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.surfaces...)
}

// File is the terminal's own file natively (/dev/tty, what Open opened),
// and nil for a Term made with New. A full-screen Bubble Tea program takes
// it as both its input and its output (tea.WithInput, tea.WithOutput), so
// that Bubble Tea finds a terminal on both sides: it sets raw mode and
// learns the size itself, and writes CR LF where raw mode no longer maps LF.
// The file belongs to the Term: Close closes it.
func (t *Term) File() *os.File { return t.file }

// Size is the terminal's size in cells, or 80×24 when it cannot be told.
func (t *Term) Size() Size {
	if t.size != nil {
		if c, r := t.size(); c > 0 && r > 0 {
			return Size{c, r}
		}
	}
	return Size{80, 24}
}

// Send writes commands to the terminal in one write, so nothing the program
// prints lands between them.
func (t *Term) Send(cmds ...string) error {
	_, err := io.WriteString(t.Out, strings.Join(cmds, ""))
	return err
}

// Raw puts a native terminal into raw mode: no echo, no line editing, keys
// as they are typed. Calls that wait for the terminal do this for
// themselves; a program that reads keys calls it once and restores on exit.
// In raw mode "\n" does not return the carriage: write "\r\n". For a Term
// made with New it does nothing.
func (t *Term) Raw() (restore func(), err error) {
	if t.raw == nil {
		return func() {}, nil
	}
	return t.raw()
}

// Close restores the terminal and stops reading it; Events' channel closes.
// Natively it also closes /dev/tty. It is safe to call more than once.
func (t *Term) Close() error {
	t.mu.Lock()
	stop, cancelIn, closeFn := t.stop, t.cancelIn, t.close
	t.stop, t.cancelIn, t.close = nil, nil, nil
	started, ended := t.started, t.ended
	if !t.closed && t.sizes != nil {
		close(t.sizes)
	}
	t.closed = true
	t.mu.Unlock()
	if stop != nil {
		stop()
	}
	// A read in progress ends before the file under it closes.
	if cancelIn != nil {
		cancelIn()
		if started {
			select {
			case <-ended:
			case <-time.After(time.Second):
			}
		}
	}
	if closeFn != nil {
		return closeFn()
	}
	return nil
}

// OnClose adds fn to what Close does, after what it did already: a host
// that runs programs in-process (a shell in a page) releases the program's
// hold on the input there, so that a read left blocked on In ends.
func (t *Term) OnClose(fn func() error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev := t.close
	t.close = func() error {
		var err error
		if prev != nil {
			err = prev()
		}
		if e := fn(); err == nil {
			err = e
		}
		return err
	}
}

// Sizes delivers the terminal's size each time Resized reports it, from
// the first call on, for a Bubble Tea program on a Term made with New,
// which needs a tea.WindowSizeMsg to redraw:
//
//	go func() {
//		for s := range t.Sizes() {
//			prog.Send(tea.WindowSizeMsg{Width: s.Cols, Height: s.Rows})
//		}
//	}()
//
// Only the latest size waits to be taken. The channel closes on Close.
// Natively nothing reports sizes: Bubble Tea hears SIGWINCH itself.
func (t *Term) Sizes() <-chan Size {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sizes == nil {
		t.sizes = make(chan Size, 1)
		if t.closed {
			close(t.sizes)
		}
	}
	return t.sizes
}

// Resized tells the Term its terminal is now s; Sizes delivers it. It never
// blocks. Size reports the new size either way, from the size function the
// Term was made with.
func (t *Term) Resized(s Size) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sizes == nil || t.closed {
		return
	}
	select {
	case <-t.sizes: // an older size nobody took
	default:
	}
	t.sizes <- s
}

// Native reports what Detect found: true for a HOTTY host.
func (t *Term) Native() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.native
}

// Caps is what the host said about itself, when Native.
func (t *Term) Caps() hotty.Caps {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.caps
}

// Detect asks the terminal whether it is a HOTTY host (SPEC §4), once, and
// reports the answer; later calls return the first answer. A terminal that
// answers neither the query nor DA1 within 1.5 s, or before ctx ends, is
// not a host.
//
// A host's reply comes before its answer to DA1, and Detect waits for that
// too, so that nothing is left for whoever reads the terminal next.
func (t *Term) Detect(ctx context.Context) bool {
	t.mu.Lock()
	if t.detected {
		defer t.mu.Unlock()
		return t.native
	}
	if t.known != nil {
		t.detected, t.native, t.caps = true, t.known.Native, t.known.Caps
		t.mu.Unlock()
		return t.native
	}
	t.mu.Unlock()

	restore, err := t.Raw()
	if err != nil {
		return t.setDetected(false, hotty.Caps{})
	}
	defer restore()

	var native bool
	var caps hotty.Caps
	replied := make(chan struct{})
	da1 := make(chan struct{}, 8)
	remove := t.listen(func(ev Event) bool {
		switch ev := ev.(type) {
		case Message:
			if r, ok := ev.Reply(); ok && !native {
				if c, ok := detect.Answer(r, detect.N); ok {
					native, caps = true, c
					close(replied)
					return true
				}
			}
		case uv.PrimaryDeviceAttributesEvent:
			select {
			case da1 <- struct{}{}:
			default:
			}
			return true
		}
		return false
	})
	if t.Send(hotty.Query(detect.N)) != nil {
		remove()
		return t.setDetected(false, hotty.Caps{})
	}

	deadline := time.NewTimer(detect.Timeout)
	defer deadline.Stop()
	// Until the host replies, a DA1 ends the wait after a moment. After
	// the reply, the DA1 behind it ends the wait at once.
	var fence <-chan time.Time
wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case <-deadline.C:
			break wait
		case <-fence:
			break wait
		case <-t.ended:
			break wait
		case <-replied:
			replied = nil
			fence = time.After(detect.AfterReply)
		case <-da1:
			select {
			case <-replied: // taken before this DA1: it is the one behind it
				break wait
			default:
			}
			if replied == nil {
				break wait
			}
			if fence == nil {
				fence = time.After(detect.AfterDA1)
			}
		}
	}
	remove()
	t.mu.Lock()
	n, c := native, caps
	t.mu.Unlock()
	return t.setDetected(n, c)
}

func (t *Term) setDetected(native bool, caps hotty.Caps) bool {
	t.mu.Lock()
	t.detected, t.native, t.caps = true, native, caps
	t.mu.Unlock()
	return native
}

// Request sends one command and waits for its reply: build makes the
// command with the option it is given, which numbers it and asks for the
// reply (hotty.N). It returns the reply, or ErrNoAnswer if none comes
// within ReplyTimeout; an error reply is returned as a reply, not an error.
//
//	r, err := t.Request(ctx, func(o hotty.ReplyOption) string {
//		return hotty.Place(name, hotty.Placement{Cols: 40}, o)
//	})
//	// r.Rows: the rows the host chose
func (t *Term) Request(ctx context.Context, build func(hotty.ReplyOption) string) (hotty.Reply, error) {
	restore, err := t.Raw()
	if err != nil {
		return hotty.Reply{}, err
	}
	defer restore()
	t.mu.Lock()
	t.nextN++
	n := detect.N + t.nextN // never the query's
	t.mu.Unlock()
	got := make(chan hotty.Reply, 1)
	remove := t.listen(func(ev Event) bool {
		if m, ok := ev.(Message); ok {
			if r, ok := m.Reply(); ok && r.N == n {
				got <- r
				return true
			}
		}
		return false
	})
	defer remove()
	if err := t.Send(build(hotty.N(n))); err != nil {
		return hotty.Reply{}, err
	}
	timeout := time.NewTimer(ReplyTimeout)
	defer timeout.Stop()
	select {
	case r := <-got:
		return r, nil
	case <-ctx.Done():
		return hotty.Reply{}, ctx.Err()
	case <-timeout.C:
	case <-t.ended:
	}
	return hotty.Reply{}, ErrNoAnswer
}

// Fence waits until the terminal has taken everything written before it,
// and returns the replies to it that nothing has read: the errors of
// commands sent with hotty.ReplyOnError, mostly. It asks for Primary Device
// Attributes, which every terminal answers in order with what it was sent.
// Replies to numbered commands go to their Request, or to Events, and so do
// the replies Events delivered before the fence.
//
// Call it before exiting, after the last command that may be answered, so
// that no reply is left for the shell to read as typing.
func (t *Term) Fence(ctx context.Context) ([]hotty.Reply, error) {
	restore, err := t.Raw()
	if err != nil {
		return nil, err
	}
	defer restore()
	var replies []hotty.Reply
	done := make(chan struct{})
	finished := false
	// Replies that came before the fence, and that Events has not
	// delivered, are the fence's too.
	claim := func(queue []Event) []Event {
		kept := queue[:0]
		for _, ev := range queue {
			if m, ok := ev.(Message); ok {
				if r, ok := m.Reply(); ok && r.N == 0 {
					replies = append(replies, r)
					continue
				}
			}
			kept = append(kept, ev)
		}
		return kept
	}
	remove := t.listenClaiming(claim, func(ev Event) bool {
		if finished {
			return false
		}
		switch ev := ev.(type) {
		case Message:
			if r, ok := ev.Reply(); ok && r.N == 0 {
				replies = append(replies, r)
				return true
			}
		case uv.PrimaryDeviceAttributesEvent:
			finished = true
			close(done)
			return true
		}
		return false
	})
	if err := t.Send("\x1b[c"); err != nil {
		remove()
		return nil, err
	}
	timeout := time.NewTimer(FenceTimeout)
	defer timeout.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		err = ctx.Err()
	case <-timeout.C:
		err = ErrNoAnswer
	case <-t.ended:
		err = ErrNoAnswer
	}
	remove()
	t.mu.Lock()
	defer t.mu.Unlock()
	return replies, err
}

// LineStart moves the cursor to the start of a line, where a surface
// placed at the cursor belongs: if it is not at one already, it writes
// CR LF. It asks the terminal where the cursor is (CPR); a terminal that
// does not say within FenceTimeout is taken to be at a line's start.
func (t *Term) LineStart(ctx context.Context) error {
	restore, err := t.Raw()
	if err != nil {
		return err
	}
	defer restore()
	col := make(chan int, 1)
	remove := t.listen(func(ev Event) bool {
		x := -1
		switch ev := ev.(type) {
		case uv.CursorPositionEvent:
			x = ev.X
		case uv.KeyPressEvent:
			// An answer on the first row, "\x1b[1;9R", is also F3 with
			// modifiers, and when it arrives alone that is all the reader
			// reports: its modifiers are the column, less one.
			if ev.Code == uv.KeyF3 && ev.Text == "" {
				x = int(ev.Mod)
			}
		}
		if x < 0 {
			return false
		}
		select {
		case col <- x:
		default:
		}
		return true
	})
	defer remove()
	if err := t.Send("\x1b[6n"); err != nil {
		return err
	}
	timeout := time.NewTimer(FenceTimeout)
	defer timeout.Stop()
	select {
	case x := <-col:
		if x > 0 {
			return t.Send("\r\n")
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-timeout.C:
	case <-t.ended:
	}
	return nil
}

// Print shows a document at the cursor, to stay in the scrollback among the
// program's output: the surface named name (made this process's own with
// Surface) gets html, detached (hotty.Detached), and is placed with p.
// The host moves the cursor below it, unless p.KeepCursor. It returns the
// surface's full name, for patches.
//
// Errors the host replies come back on the input: read them with Fence.
func (t *Term) Print(name, html string, p hotty.Placement) (string, error) {
	s := t.Surface(name)
	return s, t.Send(hotty.Doc(s, html, hotty.Detached()), hotty.Place(s, p))
}

// DetachAll detaches every surface Surface has named (hotty.Detach), so
// that none reports to whatever reads the terminal after this process.
// Detaching a detached or deleted surface does nothing.
func (t *Term) DetachAll() error {
	var cmds []string
	for _, s := range t.Surfaces() {
		cmds = append(cmds, hotty.Detach(s))
	}
	if len(cmds) == 0 {
		return nil
	}
	return t.Send(cmds...)
}

// Events delivers what the terminal sends that no call waits for, from
// when it arrived on: keys, the mouse, pastes, and HOTTY replies and
// events as Message. The channel closes when the input ends, the Term is
// closed, or ctx is done. Later calls return the same channel.
func (t *Term) Events(ctx context.Context) <-chan Event {
	t.start()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.events != nil {
		return t.events
	}
	out := make(chan Event, 64)
	t.events = out
	go func() {
		defer close(out)
		for {
			t.mu.Lock()
			if len(t.queue) > 0 {
				ev := t.queue[0]
				t.queue = t.queue[1:]
				t.mu.Unlock()
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
				continue
			}
			t.mu.Unlock()
			select {
			case <-t.queued:
			case <-t.ended:
				t.mu.Lock()
				empty := len(t.queue) == 0
				t.mu.Unlock()
				if empty {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// listen starts reading the input if nothing has yet, and adds a tap; the
// function it returns removes it.
func (t *Term) listen(take func(Event) bool) (remove func()) {
	return t.listenClaiming(nil, take)
}

// listenClaiming is listen that first lets claim take what it wants from
// the events queued for Events, with no event routed in between.
func (t *Term) listenClaiming(claim func([]Event) []Event, take func(Event) bool) (remove func()) {
	t.start()
	tp := &tap{take: take}
	t.mu.Lock()
	if claim != nil {
		t.queue = claim(t.queue)
	}
	t.taps = append(t.taps, tp)
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, have := range t.taps {
			if have == tp {
				t.taps = append(t.taps[:i], t.taps[i+1:]...)
				return
			}
		}
	}
}

// start reads In from now until Close, decoding HOTTY's messages, which may
// arrive in chunks, and routing every event.
func (t *Term) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return
	}
	t.started = true
	t.queued = make(chan struct{}, 1)
	t.ended = make(chan struct{})
	if t.closed {
		close(t.ended)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.stop = cancel
	raw := make(chan uv.Event, 64)
	r := uv.NewTerminalReader(t.In, t.TermType)
	go func() {
		_ = r.StreamEvents(ctx, raw)
		close(raw)
	}()
	go func() {
		var dec hotty.Decoder
		for ev := range raw {
			if osc, ok := ev.(uv.UnknownOscEvent); ok {
				m, res := dec.Feed(string(osc))
				switch res {
				case hotty.Partial, hotty.Invalid:
					continue
				case hotty.Complete:
					ev = Message{m}
				}
			}
			t.route(ev)
		}
		close(t.ended)
	}()
}

// route gives an event to the first tap that takes it, or queues it for
// Events. Ultraviolet reports an input it cannot tell apart as several
// events at once (a cursor position on the first row reads as a modified
// F3 too): when a tap takes one of them, the others are its misreadings.
func (t *Term) route(ev Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	evs := []Event{ev}
	if multi, ok := ev.(uv.MultiEvent); ok {
		evs = multi
	}
	for _, e := range evs {
		for _, tp := range t.taps {
			if tp.take(e) {
				return
			}
		}
	}
	t.queue = append(t.queue, evs...)
	select {
	case t.queued <- struct{}{}:
	default:
	}
}
