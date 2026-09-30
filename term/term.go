// Package term is the terminal of one run of a tool that is not a
// full-screen Bubble Tea program: a command that prints and exits (showhot),
// a prompt (askhot), a chart that streams (plothot). It finds out whether the
// terminal is a HOTTY host, names surfaces so that two runs never share one,
// and turns what the terminal sends into events.
//
// A Term is the same object natively, where it is /dev/tty (Open), and in the
// browser, where the web shell makes one per process on its xterm.js stream
// (New). A tool never needs to know which.
//
// The order of use:
//
//	t, err := p.OpenTerm()      // no terminal: draw nothing, or plain text
//	defer t.Close()
//	native := t.Detect(ctx)     // ask once; false means draw in cells
//	evs := t.Events(ctx)        // then keys and HOTTY messages, as events
//
// Detect reads the terminal's answer from the same input the events come
// from; whatever else arrives while it waits is kept and delivered first.
package term

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-demo/sdk/hotty"
)

// Size is the terminal's size in cells.
type Size struct{ Cols, Rows int }

// Known is what a terminal is known to be without asking: the web shell's
// terminal is always the page's xterm.js with the HOTTY addon.
type Known struct {
	Native bool
	Caps   hotty.Caps
}

// Message is a HOTTY message from the terminal: a reply or an event
// (SPEC §3.6, §9), delivered by Events.
type Message struct{ hotty.Message }

// Event is what Events delivers: a Message, or one of ultraviolet's input
// events (uv.KeyPressEvent, uv.MouseClickEvent, uv.PasteEvent, …).
type Event = uv.Event

// Term is one process's terminal.
type Term struct {
	// In is what the terminal sends: keys, replies and events, as bytes.
	// Read it through Detect and Events, or give it whole to a Bubble Tea
	// program (tea.WithInput) and use neither.
	In io.Reader
	// Out goes to the terminal. Write whole commands in one call (Send).
	Out io.Writer
	// Name prefixes every surface this process makes (Surface): the tool's
	// name and a number unique on this terminal, the pid natively and the
	// shell's process number in the browser.
	Name string
	// TermType is $TERM, for decoding keys.
	TermType string

	size  func() Size
	known *Known
	raw   func() (restore func(), err error)
	close func() error

	mu       sync.Mutex
	surfaces []string
	started  bool
	evc      chan Event
	backlog  []Event
	dec      hotty.Decoder
	detected bool
	native   bool
	caps     hotty.Caps
	cancel   context.CancelFunc
}

// New makes a terminal from its two streams: what the web shell gives each
// process. size reports the terminal's size; known, when not nil, is what
// Detect answers without asking.
func New(in io.Reader, out io.Writer, name string, size func() Size, known *Known) *Term {
	return &Term{In: in, Out: out, Name: name, TermType: "xterm-256color", size: size, known: known}
}

// ErrNoTerminal is Open's error when the process has no terminal.
var ErrNoTerminal = errors.New("no terminal")

// Surface is a surface name that is this process's own: Name, a dash, and
// name. Surfaces left in scrollback by an earlier run keep theirs. Every
// name it returns is remembered (Surfaces).
func (t *Term) Surface(name string) string {
	s := t.Name + "-" + name
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
// left on the terminal. The web shell deletes them when their scrollback is
// long gone; natively they stay until the host drops them.
func (t *Term) Surfaces() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.surfaces...)
}

// Size is the terminal's size in cells, or 80×24 when it cannot be told.
func (t *Term) Size() Size {
	if t.size != nil {
		if s := t.size(); s.Cols > 0 && s.Rows > 0 {
			return s
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

// Raw puts a native terminal into raw mode: no echo, no line editing, keys as
// they are typed. Detect does this for itself; a tool that reads keys calls
// it once and restores on exit. In raw mode "\n" does not return the
// carriage: write "\r\n". In the browser it does nothing.
func (t *Term) Raw() (restore func(), err error) {
	if t.raw == nil {
		return func() {}, nil
	}
	return t.raw()
}

// Close restores the terminal and stops reading it. Natively it also closes
// /dev/tty. It is safe to call more than once.
func (t *Term) Close() error {
	t.mu.Lock()
	cancel := t.cancel
	t.cancel = nil
	closeFn := t.close
	t.close = nil
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if closeFn != nil {
		return closeFn()
	}
	return nil
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

// How long Detect waits: for any answer at all; after the terminal's DA1
// (which every terminal sends), for a HOTTY reply that may still be on its
// way (sdk/host does the same); and after a host's reply, for the DA1 behind
// it.
const (
	detectTimeout = 1500 * time.Millisecond
	afterDA1      = 150 * time.Millisecond
	afterReply    = 300 * time.Millisecond
)

// Detect asks the terminal whether it is a HOTTY host (SPEC §4), once, and
// reports the answer; later calls return the first answer. A terminal that
// answers neither the query nor DA1 within 1.5 s is not a host.
//
// A host's reply comes before its answer to DA1, and Detect waits for that
// too, so that nothing is left for whoever reads the terminal next: once the
// terminal is out of raw mode, a late answer is echoed and read by the shell
// as typing ("62;52;c" at the prompt).
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
		t.setDetected(false, hotty.Caps{})
		return false
	}
	defer restore()
	evc := t.stream(ctx)
	if err := t.Send(hotty.Query(1)); err != nil {
		t.setDetected(false, hotty.Caps{})
		return false
	}
	deadline := time.NewTimer(detectTimeout)
	defer deadline.Stop()
	// Until the host replies, a DA1 ends the wait after a moment. After the
	// reply, the DA1 behind it ends the wait at once.
	var fence <-chan time.Time
	native, caps := false, hotty.Caps{}
	defer func() { t.setDetected(native, caps) }()
	for {
		select {
		case <-ctx.Done():
			return native
		case <-deadline.C:
			return native
		case <-fence:
			return native
		case ev, ok := <-evc:
			if !ok {
				return native
			}
			switch ev := ev.(type) {
			case Message:
				if r, ok := ev.Reply(); ok && !native {
					if c, ok := r.Caps(); ok {
						native, caps = true, c
						fence = time.After(afterReply)
						continue
					}
				}
				t.keep(ev)
			case uv.PrimaryDeviceAttributesEvent:
				if native {
					return true
				}
				if fence == nil {
					fence = time.After(afterDA1)
				}
			default:
				t.keep(ev)
			}
		}
	}
}

func (t *Term) setDetected(native bool, caps hotty.Caps) {
	t.mu.Lock()
	t.detected, t.native, t.caps = true, native, caps
	t.mu.Unlock()
}

func (t *Term) keep(ev Event) {
	t.mu.Lock()
	t.backlog = append(t.backlog, ev)
	t.mu.Unlock()
}

// Events delivers what the terminal sends from now on, starting with what
// arrived while Detect waited: keys, the mouse, pastes, and HOTTY replies
// and events as Message. The channel closes when the input ends or ctx is
// done. Call it once, after Detect.
func (t *Term) Events(ctx context.Context) <-chan Event {
	src := t.stream(ctx)
	out := make(chan Event, 64)
	go func() {
		defer close(out)
		t.mu.Lock()
		backlog := t.backlog
		t.backlog = nil
		t.mu.Unlock()
		for _, ev := range backlog {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
		for {
			select {
			case ev, ok := <-src:
				if !ok {
					return
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// stream starts decoding In, once, and returns the decoded events. HOTTY
// messages, which may arrive in chunks, are put together here.
func (t *Term) stream(ctx context.Context) <-chan Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return t.evc
	}
	t.started = true
	ctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	raw := make(chan uv.Event, 64)
	t.evc = make(chan Event, 64)
	r := uv.NewTerminalReader(t.In, t.TermType)
	go func() {
		_ = r.StreamEvents(ctx, raw)
		close(raw)
	}()
	go func() {
		defer close(t.evc)
		for ev := range raw {
			if osc, ok := ev.(uv.UnknownOscEvent); ok {
				m, complete, isHotty := t.dec.Feed(string(osc))
				if isHotty {
					if complete {
						ev = Message{m}
					} else {
						continue
					}
				}
			}
			select {
			case t.evc <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return t.evc
}
