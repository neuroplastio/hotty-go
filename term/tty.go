//go:build !js

package term

import (
	"fmt"
	"os"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	xterm "github.com/charmbracelet/x/term"

	"github.com/neuroplastio/hotty-go"
)

// Open opens the process's terminal, /dev/tty, whatever its standard streams
// are: `x=$(mytool ask)` draws on the terminal and prints to the pipe. name
// and the pid name the process's surfaces (Surface). ErrNoTerminal when
// there is no terminal.
func Open(name string) (*Term, error) {
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
		Name:     hotty.SurfaceName(fmt.Sprintf("%s-%d", name, os.Getpid())),
		TermType: os.Getenv("TERM"),
		size: func() Size {
			w, h, err := xterm.GetSize(fd)
			if err != nil {
				return Size{}
			}
			return Size{w, h}
		},
	}
	// Raw mode is the Term's, whoever asks: the first call sets it, and
	// its restore puts the terminal back; calls while it is raw do
	// nothing.
	var mu sync.Mutex
	var rawState *xterm.State
	t.raw = func() (func(), error) {
		mu.Lock()
		defer mu.Unlock()
		if rawState != nil {
			return func() {}, nil
		}
		st, err := xterm.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		rawState = st
		return func() {
			mu.Lock()
			defer mu.Unlock()
			if rawState == st {
				_ = xterm.Restore(fd, st)
				rawState = nil
			}
		}, nil
	}
	t.close = func() error {
		cr.Cancel()
		mu.Lock()
		if rawState != nil {
			_ = xterm.Restore(fd, rawState)
			rawState = nil
		}
		mu.Unlock()
		return f.Close()
	}
	return t, nil
}
