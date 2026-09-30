//go:build !js

package term

import (
	"fmt"
	"os"

	uv "github.com/charmbracelet/ultraviolet"
	xterm "github.com/charmbracelet/x/term"
)

// Open opens the process's terminal, /dev/tty, whatever its standard streams
// are: `x=$(askhot input)` draws on the terminal and prints to the pipe.
// tool names the process's surfaces, with its pid. ErrNoTerminal when there
// is no terminal.
func Open(tool string) (*Term, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}
	fd := f.Fd()
	if !xterm.IsTerminal(fd) {
		_ = f.Close()
		return nil, ErrNoTerminal
	}
	cr, err := uv.NewCancelReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	t := &Term{
		In:       cr,
		Out:      f,
		file:     f,
		Name:     fmt.Sprintf("%s-%d", tool, os.Getpid()),
		TermType: os.Getenv("TERM"),
		size: func() Size {
			w, h, err := xterm.GetSize(fd)
			if err != nil {
				return Size{}
			}
			return Size{w, h}
		},
	}
	var rawState *xterm.State
	t.raw = func() (func(), error) {
		if rawState != nil {
			return func() {}, nil // already raw: the outer call restores
		}
		st, err := xterm.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		rawState = st
		return func() {
			_ = xterm.Restore(fd, st)
			rawState = nil
		}, nil
	}
	t.close = func() error {
		cr.Cancel()
		if rawState != nil {
			_ = xterm.Restore(fd, rawState)
			rawState = nil
		}
		return f.Close()
	}
	return t, nil
}
