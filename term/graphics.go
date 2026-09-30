package term

import (
	"context"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// graphicsQuery asks whether a terminal shows kitty graphics: a query for a
// 1×1 RGB image with id 31, which a terminal that has them answers with
// ESC _Gi=31;OK ESC \ and draws nothing, then DA1, which every terminal
// answers after it.
const graphicsQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\" + "\x1b[c"

// KittyGraphics asks the terminal whether it shows kitty graphics: pixels
// placed on the cells, what hotty-blitz's polyfill (`hotty run`) draws
// surfaces with on a terminal that is not a HOTTY host. It asks each time it
// is called; ask after Detect, and only when Detect is false, since a HOTTY
// host needs no pixels of its own. A terminal made with Known is not asked
// (false), nor is one that answers neither the query nor DA1 within 1.5 s.
//
// As with Detect, whatever else arrives while it waits is kept for Events.
func (t *Term) KittyGraphics(ctx context.Context) bool {
	if t.known != nil {
		return false
	}
	restore, err := t.Raw()
	if err != nil {
		return false
	}
	defer restore()
	evc := t.stream(ctx)
	if err := t.Send(graphicsQuery); err != nil {
		return false
	}
	deadline := time.NewTimer(detectTimeout)
	defer deadline.Stop()
	// A DA1 before any answer ends the wait after a moment: it is this
	// query's, or one an earlier question left behind, with this one's answer
	// right behind it. After the answer, this query's DA1 is taken too, so
	// that it is not left for whoever reads next.
	var fence <-chan time.Time
	answered, ok := false, false
	for {
		select {
		case <-ctx.Done():
			return ok
		case <-deadline.C:
			return ok
		case <-fence:
			return ok
		case ev, open := <-evc:
			if !open {
				return ok
			}
			switch ev := ev.(type) {
			case uv.KittyGraphicsEvent:
				if ev.Options.ID != 31 {
					t.keep(ev)
					continue
				}
				answered, ok = true, string(ev.Payload) == "OK"
				fence = time.After(afterDA1)
			case uv.PrimaryDeviceAttributesEvent:
				if answered {
					return ok
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
