package hottytea

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestWatcherFindsErasesAndScrolls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		want   bool
	}{
		{"erase display", []string{"\x1b[?2026h\x1b[H\x1b[2J hello"}, true},
		{"erase split between writes", []string{"\x1b[H\x1b", "[", "2", "J"}, true},
		{"region scroll", []string{"\x1b[10;36r\x1b[10;1H\x1b[2T\x1b[1;37r"}, true},
		{"reverse index", []string{"\x1b[5;1H\x1bM"}, true},
		{"erase below only", []string{"\x1b[J\x1b[0J\x1b[K\x1b[5X"}, false},
		{"private modes", []string{"\x1b[?2026h\x1b[?1049h\x1b[?25l\x1b[>4;2m\x1b[=1;1u"}, false},
		{"cursor and colours", []string{"\x1b7\x1b[2;1H\x1b[38;2;1;2;3mabc\x1b[m\x1b8"}, false},
		{"a HOTTY payload", []string{"\x1b]7279;a=doc:s=x;MltTTUJb", "WzJKG1syVA==\x1b\\"}, false},
		{"a title with an ESC-like body", []string{"\x1b]2;[2J\x07text"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan tea.Msg, 4)
			w := &watcher{send: func(m tea.Msg) { got <- m }}
			for _, s := range tc.writes {
				w.scan([]byte(s))
			}
			select {
			case <-got:
				if !tc.want {
					t.Fatal("reported an erase")
				}
			case <-time.After(50 * time.Millisecond):
				if tc.want {
					t.Fatal("missed an erase")
				}
			}
		})
	}
}

func TestWatcherReportsOnceUntilHandled(t *testing.T) {
	got := make(chan tea.Msg, 4)
	w := &watcher{send: func(m tea.Msg) { got <- m }}
	w.scan([]byte("\x1b[2J"))
	w.scan([]byte("\x1b[2J"))
	<-got
	select {
	case <-got:
		t.Fatal("a second report before the first was handled")
	case <-time.After(30 * time.Millisecond):
	}
	w.handled()
	w.scan([]byte("\x1b[2J"))
	select {
	case <-got:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("no report after handled")
	}
}
