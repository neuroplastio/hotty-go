//go:build !js

package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	xterm "github.com/charmbracelet/x/term"

	"github.com/neuroplastio/hotty-demo/sdk/term"
)

// Native is the Proc of a tool run by a real shell: the process's own
// arguments (args[0] the name it was called by), streams, environment and
// files. Its Ctx ends on SIGINT or SIGTERM; stop releases the signals.
func Native(args []string) (p *Proc, stop func()) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	dir, _ := os.Getwd()
	name := filepath.Base(args[0])
	p = &Proc{
		Args:      append([]string{name}, args[1:]...),
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Env:       os.Environ(),
		Dir:       dir,
		FS:        os.DirFS("/"),
		Ctx:       ctx,
		StdinTTY:  xterm.IsTerminal(os.Stdin.Fd()),
		StdoutTTY: xterm.IsTerminal(os.Stdout.Fd()),
	}
	p.OpenTerm = func() (*term.Term, error) { return term.Open(name) }
	p.Exec = func(prog string, argv ...string) int { return execOther(p, prog, argv) }
	return p, stop
}

// execOther runs prog from PATH, skipping any entry that is hotty-demo
// itself (a link `hotty-demo shell` made), so `man` reaches the real man.
func execOther(p *Proc, prog string, argv []string) int {
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	for _, dir := range filepath.SplitList(p.Getenv("PATH")) {
		cand := filepath.Join(dir, prog)
		st, err := os.Stat(cand)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		if real, _ := filepath.EvalSymlinks(cand); real == self {
			continue
		}
		cmd := exec.CommandContext(p.Ctx, cand, argv...)
		cmd.Args[0] = prog
		cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env, cmd.Dir = p.Stdin, p.Stdout, p.Stderr, p.Env, p.Dir
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return ee.ExitCode()
			}
			return p.Errorf("%s: %v", prog, err)
		}
		return OK
	}
	return p.Errorf("%s: not found (outside hotty-demo)", strings.TrimSpace(prog))
}
