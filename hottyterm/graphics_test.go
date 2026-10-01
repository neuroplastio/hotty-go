package hottyterm

import (
	"context"
	"io"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// readGraphicsQuery waits for the graphics query, so the answer comes after
// it, and checks it byte for byte.
func (f *fake) readGraphicsQuery(t *testing.T) {
	t.Helper()
	buf := make([]byte, 4096)
	n, err := f.out.Read(buf)
	if err != nil || string(buf[:n]) != "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c" {
		t.Errorf("graphics query: %q, %v", buf[:n], err)
	}
}

func TestKittyGraphics(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		want         bool
	}{
		{"answered", "\x1b_Gi=31;OK\x1b\\\x1b[?62;22c", true},
		{"refused", "\x1b_Gi=31;ENOTSUPPORTED:no direct transmission\x1b\\\x1b[?62;22c", false},
		{"not answered", "\x1b[?62;22c", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(nil)
			go func() {
				f.readGraphicsQuery(t)
				_, _ = io.WriteString(f.answer, "x"+tc.answer)
			}()
			start := time.Now()
			if got := f.t.KittyGraphics(context.Background()); got != tc.want {
				t.Fatalf("KittyGraphics = %v, want %v", got, tc.want)
			}
			if took := time.Since(start); took > time.Second {
				t.Errorf("took %v; DA1 should end it", took)
			}
			// The key typed before the answer is kept, and the DA1 that
			// followed the answer is not left behind.
			go func() { _, _ = io.WriteString(f.answer, "y") }()
			evs := f.t.Events(context.Background())
			for _, want := range []string{"x", "y"} {
				ev := <-evs
				if k, ok := ev.(uv.KeyPressEvent); !ok || k.String() != want {
					t.Errorf("event %#v, want the key %s", ev, want)
				}
			}
		})
	}
}

func TestKittyGraphicsAfterDetect(t *testing.T) {
	// A host answered Detect, whose DA1 is still on its way when the
	// graphics query goes out: the stale DA1 does not end the wait.
	f := newFake(nil)
	go func() {
		f.readQuery(t)
		_, _ = io.WriteString(f.answer, capsReply())
		f.readGraphicsQuery(t)
		_, _ = io.WriteString(f.answer, "\x1b[?62;22c"+"\x1b_Gi=31;OK\x1b\\\x1b[?62;22c")
	}()
	if !f.t.Detect(context.Background()) {
		t.Fatal("a host")
	}
	if !f.t.KittyGraphics(context.Background()) {
		t.Fatal("the answer after a stale DA1 counts")
	}
}

func TestKittyGraphicsSilent(t *testing.T) {
	f := newFake(nil)
	go f.readGraphicsQuery(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if f.t.KittyGraphics(ctx) {
		t.Fatal("a terminal that says nothing shows no graphics")
	}
}

func TestKittyGraphicsKnown(t *testing.T) {
	f := newFake(&Known{Native: true})
	if f.t.KittyGraphics(context.Background()) {
		t.Fatal("a known terminal is not asked")
	}
}
