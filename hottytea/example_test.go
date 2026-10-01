package hottytea_test

import (
	"fmt"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottytea"
	"github.com/neuroplastio/hotty-go/hottytest"
)

// counter is a full-screen program with one surface: a count, and a button
// that adds one. In a terminal that is not a HOTTY host, it draws the
// count in cells, and the + key adds one.
type counter struct {
	s     *hottytea.Session
	n     int
	frame string
}

func (m *counter) Init() tea.Cmd { return m.s.Detect() }

func (m *counter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg, cmd := m.s.Update(msg) // the Session's first
	switch msg := msg.(type) {
	case hottytea.ReadyMsg:
		fmt.Println("ready:", msg.Mode)
	case hottytea.EventMsg:
		if msg.Kind == hotty.EventClick && msg.Target == "plus" {
			m.add()
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "+":
			m.add()
		case "q":
			return m, tea.Sequence(m.s.Close(), tea.Quit)
		}
	}
	return m, tea.Batch(cmd, m.draw())
}

// add counts one, and patches the surface: a few bytes, not a document.
func (m *counter) add() {
	m.n++
	m.s.Send(hotty.SetText("counter", "count", strconv.Itoa(m.n)))
}

// draw lays the frame out: the cells that View returns, and the surfaces
// that go in them. It returns the commands to send.
func (m *counter) draw() tea.Cmd {
	if m.s.Mode != hottytea.Native {
		m.frame = fmt.Sprintf("Counter\n%d\n(+) add  (q)uit", m.n)
		return nil
	}
	m.frame = "Counter\n\n(q)uit"
	m.s.Layout([]hottytea.Surface{{
		Name: "counter",
		Rect: hottytea.Rect{X: 0, Y: 1, W: 24, H: 1},
		Doc: func() string {
			return `<b id="count">` + strconv.Itoa(m.n) + `</b> <button id="plus">+</button>`
		},
	}})
	return m.s.Flush()
}

func (m *counter) View() tea.View {
	v := tea.NewView(m.frame)
	v.AltScreen = true
	return v
}

// A full-screen Bubble Tea program with a surface. Here it runs on a host
// from hottytest, and a goroutine plays the user; on a real terminal it is
//
//	p := tea.NewProgram(m, tea.WithOutput(m.s.WatchFile(os.Stdout)))
//	m.s.Attach(p.Send)
func Example() {
	h := hottytest.New(nil)
	m := &counter{s: hottytea.New()}
	p := tea.NewProgram(m, tea.WithInput(h), tea.WithOutput(m.s.Watch(h)),
		tea.WithWindowSize(40, 10), tea.WithoutSignalHandler(), tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	m.s.Attach(p.Send)

	go func() {
		// The user clicks + twice, once it is on the screen, and quits.
		for h.Click("counter", "plus") != nil {
			time.Sleep(time.Millisecond)
		}
		_ = h.Click("counter", "plus")
		h.Type("q")
	}()
	if _, err := p.Run(); err != nil {
		fmt.Println(err)
	}

	// What the program sent the host, placements aside: one document, two
	// patches, and the delete on its way out.
	for _, c := range h.Commands() {
		switch c.Get("a") {
		case "doc":
			fmt.Println("doc", c.Get("s"))
		case "patch":
			fmt.Println("patch", c.Get("s"), "#"+c.Get("t"), string(c.Payload))
		case "del":
			fmt.Println("del", c.Get("s"))
		}
	}
	// Output:
	// ready: native
	// doc counter
	// patch counter #count 1
	// patch counter #count 2
	// del counter
}
