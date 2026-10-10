package hottytea_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottytea"
	"github.com/neuroplastio/hotty-go/hottytest"
)

// cardApp is a small full-screen program: a header in cells, and a card
// with a button below it, as a surface on a host and as cells elsewhere.
type cardApp struct {
	s      *hottytea.Session
	y      int // the card's row
	status string
	clears int            // times the screen was cleared, shown in the header
	seen   chan<- tea.Msg // what the Session told it, for the test
	frame  string
}

const cardName = "app-card"

func (m *cardApp) Init() tea.Cmd { return m.s.Detect() }

func (m *cardApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg, cmd := m.s.Update(msg)
	switch msg := msg.(type) {
	case hottytea.ReadyMsg, hottytea.ErrorMsg, hottytea.RelayoutMsg:
		m.tell(msg)
	case hottytea.EventMsg:
		m.tell(msg)
		if msg.Kind == hotty.EventClick && msg.Target == "go" {
			m.status = "clicked"
			m.s.Send(hotty.SetText(cardName, "status", m.status))
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q":
			return m, tea.Sequence(m.s.Close(), tea.Quit)
		case "c":
			// Bubble Tea redraws a cleared screen with the next frame
			// that differs.
			m.clears++
			return m, tea.Batch(cmd, tea.ClearScreen, m.draw())
		case "d":
			// Behind the Session's back, the surface goes; then it moves,
			// and its placement finds it gone.
			m.s.Send(hotty.Del(cardName))
			m.y++
		}
	}
	return m, tea.Batch(cmd, m.draw())
}

func (m *cardApp) tell(msg tea.Msg) {
	select {
	case m.seen <- msg:
	default:
	}
}

// draw lays the frame out: the cells for View, and the surfaces in them.
func (m *cardApp) draw() tea.Cmd {
	lines := []string{"cards" + strings.Repeat("*", m.clears)}
	if m.s.Mode == hottytea.Native {
		m.s.Layout([]hottytea.Surface{{Name: cardName, Rect: hottytea.Rect{X: 2, Y: m.y, W: 20, H: 3}, Press: true,
			Doc: func() string {
				return `<p id="status">` + m.status + `</p><button id="go">Go</button>`
			}}})
	} else {
		lines = append(lines, "  ["+m.status+"] (g)o")
	}
	m.frame = strings.Join(lines, "\n")
	return m.s.Flush()
}

func (m *cardApp) View() tea.View {
	v := tea.NewView(m.frame)
	v.AltScreen = true
	return v
}

// run starts the app on a terminal, and returns what its Session told it
// and a function that waits for it to end. setup, if any, sets the
// Session up before it runs.
func run(t *testing.T, h *hottytest.Host, setup ...func(*hottytea.Session)) (seen <-chan tea.Msg, wait func()) {
	t.Helper()
	ch := make(chan tea.Msg, 64)
	app := &cardApp{s: hottytea.New(), y: 1, status: "ready", seen: ch}
	for _, f := range setup {
		f(app.s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	p := tea.NewProgram(app, tea.WithContext(ctx), tea.WithInput(h), tea.WithOutput(app.s.Watch(h)),
		tea.WithoutSignalHandler(), tea.WithWindowSize(40, 10), tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	app.s.Attach(p.Send)
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	return ch, func() {
		defer cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("the program: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the program did not end")
		}
	}
}

// next waits for the Session's next message of type M.
func next[M tea.Msg](t *testing.T, seen <-chan tea.Msg) M {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-seen:
			if m, ok := msg.(M); ok {
				return m
			}
		case <-deadline:
			var zero M
			t.Fatalf("no %T", zero)
			return zero
		}
	}
}

// eventually polls cond until it holds, or fails.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// command is the last HOTTY command the program sent with these keys.
// The test reads the host's copies of the commands while the program runs:
// a surface's own state is the host's to change then.
func command(h *hottytest.Host, keys ...string) (hotty.Message, bool) {
	cmds := h.Commands()
	for i := len(cmds) - 1; i >= 0; i-- {
		m := cmds[i]
		match := true
		for j := 0; j+1 < len(keys); j += 2 {
			if hotty.Get(m.Control, keys[j]) != keys[j+1] {
				match = false
				break
			}
		}
		if match {
			return m, true
		}
	}
	return hotty.Message{}, false
}

func count(h *hottytest.Host, keys ...string) int {
	n := 0
	for _, m := range h.Commands() {
		match := true
		for j := 0; j+1 < len(keys); j += 2 {
			match = match && hotty.Get(m.Control, keys[j]) == keys[j+1]
		}
		if match {
			n++
		}
	}
	return n
}

func drain(seen <-chan tea.Msg) {
	for {
		select {
		case <-seen:
		default:
			return
		}
	}
}

func TestAProgramOnAHost(t *testing.T) {
	h := hottytest.New(t)
	seen, wait := run(t, h)
	if r := next[hottytea.ReadyMsg](t, seen); r.Mode != hottytea.Native || r.Caps.Host != "hottytest" {
		t.Fatalf("ready: %+v", r)
	}
	eventually(t, "the card is placed", func() bool { _, ok := command(h, "a", "place", "s", cardName); return ok })

	doc, _ := command(h, "a", "doc", "s", cardName)
	if !strings.Contains(string(doc.Payload), `<p id="status">ready</p>`) {
		t.Errorf("document: %s", doc.Payload)
	}
	// At column 2, row 1, keeping the cursor, asking for presses.
	place, _ := command(h, "a", "place", "s", cardName)
	if m := place; hotty.Get(m.Control, "c") != "20" || hotty.Get(m.Control, "r") != "3" || hotty.Get(m.Control, "p") != "1" || hotty.Get(m.Control, "C") != "1" {
		t.Errorf("placement: %v", m.Control)
	}
	if !strings.Contains(h.Output(), "\x1b7\x1b[2;3H\x1b]7279;a=place:s="+cardName) {
		t.Errorf("not placed at 2,1")
	}

	if err := h.Click(cardName, "go"); err != nil {
		t.Fatal(err)
	}
	// The user clicks Go: a press first (the card asked for them), the
	// keyboard (a button takes focus), then the click; the app changes its
	// status with a delta.
	var kinds []string
	for _, want := range []string{hotty.EventPress, hotty.EventFocus, hotty.EventClick} {
		ev := next[hottytea.EventMsg](t, seen)
		kinds = append(kinds, ev.Kind)
		if ev.Kind != want || ev.Surface != cardName || want != hotty.EventFocus && ev.Target != "go" {
			t.Errorf("events %v, want press, focus, click on #go: %+v", kinds, ev.Event)
		}
	}
	eventually(t, "the status is changed by a delta", func() bool {
		m, ok := command(h, "a", "delta", "s", cardName, "op", "text", "t", "status")
		return ok && string(m.Payload) == "clicked"
	})

	// The renderer erases the screen: the card is placed again, and its
	// document is not sent again.
	drain(seen)
	before := count(h, "a", "place", "s", cardName)
	h.Type("c")
	next[hottytea.RelayoutMsg](t, seen)
	eventually(t, "the card is placed again", func() bool { return count(h, "a", "place", "s", cardName) > before })

	// The host loses the document: the placement says so, and the
	// document goes again.
	drain(seen)
	h.Type("d")
	next[hottytea.RelayoutMsg](t, seen)
	eventually(t, "the document again", func() bool { return count(h, "a", "doc", "s", cardName) == 2 })
	eventually(t, "the card is back", func() bool { return strings.Contains(h.Output(), "\x1b7\x1b[3;3H\x1b]7279;a=place:s="+cardName) })

	h.Type("q")
	wait()
	if h.Surface(cardName) != nil {
		t.Error("the card outlived the program")
	}
	if n := count(h, "a", "doc"); n != 2 {
		t.Errorf("%d documents sent, want 2 (the first, and after it was lost)", n)
	}
}

// In a terminal that is not a host, the program draws in cells and sends
// nothing but the query: the host fails the test if it does.
func TestAProgramInText(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	seen, wait := run(t, h)
	if r := next[hottytea.ReadyMsg](t, seen); r.Mode != hottytea.Text {
		t.Fatalf("ready: %+v", r)
	}
	eventually(t, "the card in cells", func() bool { return strings.Contains(h.Screen(), "[ready] (g)o") })
	h.Type("q")
	wait()
	if n := len(h.Commands()); n != 1 {
		t.Errorf("%d HOTTY commands, want the query alone", n)
	}
}

// With Late, a program that started on a terminal that was not a host yet
// (a multiplexer's pane with no terminal attached) moves to surfaces when
// it becomes one: ReadyMsg again, then RelayoutMsg, and its card goes out
// (SPEC §4).
func TestAProgramWithALateHost(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	seen, wait := run(t, h, func(s *hottytea.Session) { s.Late = true })
	if r := next[hottytea.ReadyMsg](t, seen); r.Mode != hottytea.Text {
		t.Fatalf("ready: %+v", r)
	}
	eventually(t, "the card in cells", func() bool { return strings.Contains(h.Screen(), "[ready] (g)o") })
	h.BecomeHost()
	if r := next[hottytea.ReadyMsg](t, seen); r.Mode != hottytea.Native || r.Caps.Host != "hottytest" {
		t.Fatalf("ready again: %+v", r)
	}
	next[hottytea.RelayoutMsg](t, seen)
	eventually(t, "the card on a surface", func() bool {
		s := h.Surface(cardName)
		return s != nil && s.Placed()
	})
	h.Type("q")
	wait()
	if h.Surface(cardName) != nil {
		t.Error("the card outlived the program")
	}
	if n := count(h, "a", "q"); n != 1 {
		t.Errorf("%d queries, want the first alone: answered, it is not withdrawn", n)
	}
}

// With Late, a program that quits before a late answer came withdraws its
// query, so that a terminal that becomes a host later answers nothing.
func TestAProgramWithdrawsItsLateQuery(t *testing.T) {
	h := hottytest.New(t, hottytest.Text())
	seen, wait := run(t, h, func(s *hottytea.Session) { s.Late = true })
	if r := next[hottytea.ReadyMsg](t, seen); r.Mode != hottytea.Text {
		t.Fatalf("ready: %+v", r)
	}
	h.Type("q")
	wait()
	cmds := h.Commands()
	if len(cmds) != 2 || hotty.Get(cmds[0].Control, "late") != "1" || hotty.Get(cmds[1].Control, "a") != "q" || hotty.Get(cmds[1].Control, "q") != "2" {
		t.Errorf("commands %v, want the query and its withdrawal", cmds)
	}
	h.BecomeHost()
	if len(h.Replies()) != 0 {
		t.Errorf("answered after the program: %v", h.Replies())
	}
}
