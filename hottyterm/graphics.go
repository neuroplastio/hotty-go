package hottyterm

import (
	"context"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/neuroplastio/hotty-go/internal/detect"
)

// graphicsQuery asks whether a terminal shows kitty graphics: a query for a
// 1×1 RGB image with id 31, which a terminal that has them answers with
// ESC _Gi=31;OK ESC \ and draws nothing, then DA1, which every terminal
// answers after it.
const graphicsQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\" + "\x1b[c"

// KittyGraphics asks the terminal whether it shows kitty graphics: pixels
// placed on the cells, which is what a HOTTY polyfill (hotty-blitz's
// `hotty run`) draws surfaces with on a terminal that is not a host. Ask
// after Detect, and only when Detect is false: a host needs no pixels of
// its own. It asks each time it is called. A terminal made with Known is
// not asked (false), nor is one that answers neither the query nor DA1
// within 1.5 s.
func (t *Term) KittyGraphics(ctx context.Context) bool {
	if t.known != nil {
		return false
	}
	restore, err := t.Raw()
	if err != nil {
		return false
	}
	defer restore()
	answer := make(chan bool, 1)
	da1 := make(chan struct{}, 8)
	remove := t.listen(func(ev Event) bool {
		switch ev := ev.(type) {
		case uv.KittyGraphicsEvent:
			if ev.Options.ID != 31 {
				return false
			}
			select {
			case answer <- string(ev.Payload) == "OK":
			default:
			}
			return true
		case uv.PrimaryDeviceAttributesEvent:
			select {
			case da1 <- struct{}{}:
			default:
			}
			return true
		}
		return false
	})
	defer remove()
	if err := t.Send(graphicsQuery); err != nil {
		return false
	}
	deadline := time.NewTimer(detect.Timeout)
	defer deadline.Stop()
	// A DA1 before any answer ends the wait after a moment: it is this
	// query's, or one an earlier question left behind, with this one's
	// answer right behind it. After the answer, this query's DA1 is taken
	// too, so that it is not left for whoever reads next.
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
		case <-t.ended:
			return ok
		case ok = <-answer:
			answered = true
			fence = time.After(detect.AfterDA1)
		case <-da1:
			if answered {
				return ok
			}
			select {
			case ok = <-answer: // taken before this DA1
				return ok
			default:
			}
			if fence == nil {
				fence = time.After(detect.AfterDA1)
			}
		}
	}
}
