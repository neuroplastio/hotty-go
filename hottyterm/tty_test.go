//go:build linux

package hottyterm_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/neuroplastio/hotty-go"
	"github.com/neuroplastio/hotty-go/hottyterm"
	"github.com/neuroplastio/hotty-go/hottytest"
)

// childArgs run one test of this binary again, in a child process, whose
// coverage counts with the parent's (go test merges -test.gocoverdir).
func childArgs(test string) []string {
	args := []string{"-test.run=^" + test + "$"}
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+f.Value.String())
	}
	return args
}

// openPTY opens a pseudo-terminal pair: the terminal's side (master) and
// the program's (slave).
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no ptmx:", err)
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skip("unlockpt:", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skip("ptsname:", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("open slave:", err)
	}
	return master, slave
}

// A tool run natively: Open finds its terminal through /dev/tty, whatever
// its streams are, detects the host, prints a card and fences the replies,
// and leaves the terminal as it found it. The test runs itself again as
// the tool, with a pseudo-terminal as its controlling terminal and
// hottytest at the other end.
func TestOpenNative(t *testing.T) {
	if os.Getenv("HOTTY_TERM_CHILD") == "1" {
		nativeTool()
		return
	}
	master, slave := openPTY(t)
	defer master.Close()
	h := hottytest.New(t)

	cmd := exec.Command(os.Args[0], childArgs("TestOpenNative")...)
	cmd.Env = append(os.Environ(), "HOTTY_TERM_CHILD=1", "TERM=xterm-256color")
	cmd.Stdin = slave
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr // pipes: the tool's output is not its terminal
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	// The terminal: what the tool writes goes to the host, and what the
	// host answers comes back.
	go func() { _, _ = io.Copy(h, master) }()
	go func() { _, _ = io.Copy(master, h) }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil || !strings.Contains(stdout.String(), "child ok") {
			t.Fatalf("the tool: %v\n%s%s", err, stdout.String(), stderr.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the tool did not finish")
	}
	name := fmt.Sprintf("native-%d-card", cmd.Process.Pid)
	s := h.Surface(name)
	if s == nil || !s.Detached() || s.TextOf("msg") != "native" {
		t.Fatalf("surfaces %v, want %s", h.Surfaces(), name)
	}
}

// nativeTool is the tool TestOpenNative runs.
func nativeTool() {
	fail := func(format string, args ...any) {
		fmt.Printf("child: "+format+"\n", args...)
		os.Exit(1)
	}
	tm, err := hottyterm.Open("native")
	if err != nil {
		fail("Open: %v", err)
	}
	if tm.File() == nil {
		fail("no File")
	}
	before, _ := unix.IoctlGetTermios(int(tm.File().Fd()), unix.TCGETS)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !tm.Detect(ctx) || tm.Caps().Host != "hottytest" {
		fail("Detect: not native")
	}
	if s := tm.Size(); s.Cols <= 0 || s.Rows <= 0 {
		fail("Size %v", s)
	}
	restore, err := tm.Raw()
	if err != nil {
		fail("Raw: %v", err)
	}
	inner, _ := tm.Raw() // already raw: the outer restore puts it back
	inner()
	if err := tm.LineStart(ctx); err != nil {
		fail("LineStart: %v", err)
	}
	if _, err := tm.Print("card", `<p id=msg>native</p>`, hotty.Placement{Cols: 20}); err != nil {
		fail("Print: %v", err)
	}
	if replies, err := tm.Fence(ctx); err != nil || len(replies) != 0 {
		fail("Fence: %v %v", replies, err)
	}
	restore()
	after, _ := unix.IoctlGetTermios(int(tm.File().Fd()), unix.TCGETS)
	if before == nil || after == nil || before.Lflag != after.Lflag {
		fail("the terminal's modes changed: %v → %v", before, after)
	}
	if err := tm.Close(); err != nil {
		fail("Close: %v", err)
	}
	if err := tm.Close(); err != nil {
		fail("Close again: %v", err)
	}
	fmt.Println("child ok")
}

func TestOpenWithoutATerminal(t *testing.T) {
	if os.Getenv("HOTTY_TERM_CHILD") == "2" {
		_, err := hottyterm.Open("x")
		if errors.Is(err, hottyterm.ErrNoTerminal) {
			fmt.Println("child ok")
		} else {
			fmt.Println("child:", err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], childArgs("TestOpenWithoutATerminal")...)
	cmd.Env = append(os.Environ(), "HOTTY_TERM_CHILD=2")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // no controlling terminal
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "child ok") {
		t.Errorf("Open without a terminal: %v\n%s", err, out)
	}
}
