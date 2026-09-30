// Package proc is what a tool is given when it runs, the same in a real
// shell and in the web shell: its arguments, its three streams, its
// environment, its files and its terminal. A tool is a function from a Proc
// to an exit status, registered by name; hotty-demo is one binary that runs
// whichever tool it is called as (cmd/hotty-demo), and the web shell runs
// them as processes of its own (internal/shell).
//
// A tool never touches os.Args, os.Stdin, os.Stdout, os.Getenv or the file
// system directly: everything goes through its Proc, so that the browser,
// which has none of these, can give it the same things.
package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/neuroplastio/hotty-demo/sdk/term"
)

// Exit statuses, as shells know them.
const (
	OK          = 0
	Failure     = 1
	Usage       = 2   // bad arguments
	Interrupted = 130 // Ctrl-C, or ctx done
)

// Proc is one run of a tool.
type Proc struct {
	// Args are the arguments; Args[0] is the name the tool was called by,
	// which may be one of its aliases ("man").
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Env is the environment, KEY=value.
	Env []string
	// Dir is the working directory, absolute.
	Dir string
	// FS is the file system from its root: a path "/a/b" is "a/b" in FS.
	// Use Open and ReadFile, which resolve paths against Dir.
	FS fs.FS
	// Ctx is done when the run should stop: Ctrl-C in the web shell, a
	// signal natively. A tool returns Interrupted soon after.
	Ctx context.Context

	// StdinTTY and StdoutTTY report whether a stream is the terminal, not
	// a pipe or a file. A tool that prints surfaces does so only when
	// StdoutTTY; otherwise it writes plain data, as `ls | cat` expects.
	StdinTTY, StdoutTTY bool

	// OpenTerm opens the process's terminal, even when its streams are
	// pipes. It returns term.ErrNoTerminal when there is none. The tool
	// closes the terminal it opened.
	OpenTerm func() (*term.Term, error)

	// Exec runs another program found on PATH, with this Proc's streams,
	// and returns its status: how `man`, called when surfaces cannot help,
	// hands over to the real man. It skips hotty-demo's own links. nil where
	// there are no other programs (the browser).
	Exec func(name string, args ...string) int
}

// Getenv is the value of an environment variable, or "".
func (p *Proc) Getenv(key string) string {
	for i := len(p.Env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(p.Env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Abs is name as an absolute, clean path, resolved against Dir.
func (p *Proc) Abs(name string) string {
	if path.IsAbs(name) {
		return path.Clean(name)
	}
	return path.Join(p.Dir, name)
}

// Open opens a file by the name a user typed.
func (p *Proc) Open(name string) (fs.File, error) {
	return p.FS.Open(fsPath(p.Abs(name)))
}

// ReadFile reads a file by the name a user typed.
func (p *Proc) ReadFile(name string) ([]byte, error) {
	return fs.ReadFile(p.FS, fsPath(p.Abs(name)))
}

// ReadDir lists a directory by the name a user typed.
func (p *Proc) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(p.FS, fsPath(p.Abs(name)))
}

func fsPath(abs string) string {
	if abs == "/" {
		return "."
	}
	return strings.TrimPrefix(abs, "/")
}

// Errorf writes "name: message" to Stderr, as NEIO-4 has errors, and returns
// Failure.
func (p *Proc) Errorf(format string, args ...any) int {
	name := "tool"
	if len(p.Args) > 0 {
		name = path.Base(p.Args[0])
	}
	_, _ = io.WriteString(p.Stderr, name+": "+fmt.Sprintf(format, args...)+"\n")
	return Failure
}

// Interrupted reports whether the run was asked to stop.
func (p *Proc) Interrupted() bool {
	return p.Ctx != nil && errors.Is(p.Ctx.Err(), context.Canceled)
}

// Tool is a program hotty-demo carries.
type Tool struct {
	// Name is what it is called: showhot, askhot, plothot.
	Name string
	// Aliases are other names it answers to when called by them: showhot
	// is man in `hotty-demo shell`.
	Aliases []string
	// Summary is one line for `help` and `hotty-demo tools`.
	Summary string
	// Run runs it and returns its exit status.
	Run func(p *Proc) int
}

var (
	mu    sync.Mutex
	tools = map[string]Tool{}
	names = map[string]string{} // name or alias -> name
)

// Register adds a tool; call it from the tool package's init. A second tool
// with a taken name or alias panics.
func Register(t Tool) {
	mu.Lock()
	defer mu.Unlock()
	for _, n := range append([]string{t.Name}, t.Aliases...) {
		if _, taken := names[n]; taken {
			panic("proc: " + n + " is registered twice")
		}
		names[n] = t.Name
	}
	tools[t.Name] = t
}

// Lookup finds a tool by its name or an alias.
func Lookup(name string) (Tool, bool) {
	mu.Lock()
	defer mu.Unlock()
	t, ok := tools[names[name]]
	return t, ok
}

// All is every tool, by name.
func All() []Tool {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
