package hottytest

import (
	"errors"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"neuroplast.io/hotty-go"
)

// pointerGesture remembers the first button's route until it is released.
// Competing buttons cannot start another gesture until all buttons are up.
type pointerGesture struct {
	down       map[int]bool
	button     int
	start      pointerLocation
	aux, click *html.Node
	mods       int
	alt        bool
}

type pointerLocation struct {
	surface *Surface
	element *html.Node
	hotty.Cell
	step *[2]int
}

func (h *Host) pointerHeld() bool {
	return len(h.pointer.down) != 0 || h.drag != nil
}

func (h *Host) cancelPointer(s *Surface, report bool) {
	if h.pointer.start.surface == s {
		h.pointer.start = pointerLocation{}
		h.pointer.aux, h.pointer.click = nil, nil
	}
	if h.drag != nil && h.drag.s == s {
		if report {
			h.cutDrag()
		} else {
			h.drag = nil
		}
	}
}

// PointerDown presses a button on the named element at a surface cell.
// An empty id points to the body. PointerUp releases the same button,
// possibly over another element or with different modifiers.
func (h *Host) PointerDown(surface, id string, button, c, r int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, err := h.pointerTarget(surface, id, c, r)
	if err != nil {
		return err
	}
	h.pointerDown(at, button, keys)
	return nil
}

func (h *Host) pointerDown(at pointerLocation, button int, keys []string) {
	p := &h.pointer
	if h.pointerHeld() {
		if p.down == nil {
			p.down = make(map[int]bool)
			p.button = -1 // an explicit DragStart owns the pointer
		}
		p.down[button] = true
		return
	}
	*p = pointerGesture{down: map[int]bool{button: true}, button: button, start: at, mods: modBits(keys)}
	p.alt = h.alt1(keys)
	s, el := at.surface, at.element
	if p.alt {
		return
	}
	if s == nil {
		if button == hotty.ButtonPrimary && h.keyboard != nil {
			h.blur(h.keyboard)
		}
		return
	}
	if button == hotty.ButtonPrimary {
		if a := closest(el, hyperlink); a != nil {
			p.click = a
			return
		}
		if s.press {
			h.pressAt(s, el, p.mods)
		}
		p.click = closest(el, reportsClick)
		h.startDrag(s, closest(el, listens("drag")), button, at.Col, at.Row, 0, 0, p.mods)
		h.pointerFocus(s, el)
		return
	}
	if p.mods&hotty.ModShift != 0 {
		return
	}
	p.aux = h.auxTarget(el, button)
	if button == hotty.ButtonSecondary && p.aux == nil {
		h.contextMenuAt(at, p.mods)
	}
}

// PointerMove moves the pointer onto a named element at a cell. Use an
// empty surface to move onto the terminal. For an auxiliary press, the
// test supplies pastSlop once the movement exceeds the drag threshold.
func (h *Host) PointerMove(surface, id string, c, r int, pastSlop bool, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, err := h.pointerTarget(surface, id, c, r)
	if err != nil {
		return err
	}
	h.pointerMove(at, pastSlop, keys)
	return nil
}

func (h *Host) pointerMove(at pointerLocation, pastSlop bool, keys []string) {
	p := &h.pointer
	if d := h.drag; d != nil {
		h.movePointerDrag(at, d, keys)
		return
	}
	if !h.pointerHeld() {
		h.hoverAt(at.surface, at.id(), at.Col, at.Row)
		return
	}
	if p.aux != nil && pastSlop && listens("drag")(p.aux) {
		var step [2]int
		if p.start.step != nil {
			step = *p.start.step
		}
		h.startDrag(p.start.surface, p.aux, p.button, p.start.Col, p.start.Row, step[0], step[1], p.mods)
		if d := h.drag; d != nil {
			p.aux = nil
			h.movePointerDrag(at, d, keys)
		}
	}
}

func (h *Host) movePointerDrag(at pointerLocation, d *drag, keys []string) {
	x, y, tx, ty := at.dragSteps(d)
	_ = h.dragMove(dragTarget(at, d.s), at.Col, at.Row, x, y, tx, ty, keys)
}

func (at pointerLocation) dragSteps(d *drag) (x, y, tx, ty int) {
	x, y, tx, ty = d.x, d.y, d.tx, d.ty
	if at.surface == d.s && at.step != nil {
		if at.element == d.start {
			x, y = at.step[0], at.step[1]
		}
		if listens("drop")(at.element) {
			tx, ty = at.step[0], at.step[1]
		}
	}
	return
}

// PointerUp releases a button at the supplied location. Use an empty
// surface if the release is over the terminal.
func (h *Host) PointerUp(surface, id string, button, c, r int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, err := h.pointerTarget(surface, id, c, r)
	if err != nil && !errors.Is(err, ErrDetached) && !errors.Is(err, ErrNotPlaced) && !errors.Is(err, ErrNoSurface) {
		return err
	}
	h.pointerUp(at, button, 1, keys)
	return nil
}

func (h *Host) pointerUp(at pointerLocation, button, count int, keys []string) {
	p := &h.pointer
	if !p.down[button] {
		return
	}
	delete(p.down, button)
	if p.button == button {
		switch {
		case h.drag != nil:
			d := h.drag
			x, y, tx, ty := at.dragSteps(d)
			_ = h.finishDrag(dragTarget(at, d.s), at.Col, at.Row, x, y, tx, ty, count, keys)
		case at.surface == nil || at.surface != p.start.surface:
		case p.aux != nil:
			if p.aux == h.auxTarget(at.element, button) {
				h.auxClickAt(at, p.aux, button, modBits(keys))
			}
		case p.click != nil && p.click == closest(at.element, reportsClick):
			if hyperlink(p.click) {
				if u := at.surface.resolve(p.click); u != "" {
					h.opened = append(h.opened, u)
				}
			} else {
				_ = h.reportClick(at.surface, p.click, count, modBits(keys))
			}
		}
		p.button = -1
		p.aux, p.click = nil, nil
	}
	if !h.pointerHeld() {
		if !p.alt {
			h.hoverAt(at.surface, at.id(), at.Col, at.Row)
		}
		h.pointer = pointerGesture{}
	}
}

func (at pointerLocation) id() string {
	n := closest(at.element, func(n *html.Node) bool { id, _ := attr(n, "id"); return id != "" })
	if n == nil {
		return ""
	}
	id, _ := attr(n, "id")
	return id
}

func dragTarget(at pointerLocation, surface *Surface) string {
	if at.surface != surface {
		return ""
	}
	return at.id()
}

func (h *Host) pointerTarget(surface, id string, c, r int) (pointerLocation, error) {
	at := pointerLocation{Cell: hotty.Cell{Col: c, Row: r}}
	if surface == "" {
		return at, nil
	}
	s, err := h.target(surface)
	if err != nil {
		return at, err
	}
	if s.detached {
		return at, ErrDetached
	}
	at.surface = s
	if id == "" {
		at.element = find(s.doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
		return at, nil
	}
	at.element, err = h.element(s, id)
	return at, err
}

// ContextMenu plays a secondary-button menu gesture at cell c, r.
// An 0.8 session reports a press with ButtonSecondary. Key plays the
// menu-key gesture.
func (h *Host) ContextMenu(surface, id string, c, r int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, err := h.pointerTarget(surface, id, c, r)
	if err != nil {
		return err
	}
	if at.surface != nil && !h.pointerHeld() && !h.alt1(keys) && modBits(keys)&hotty.ModShift == 0 {
		h.contextMenuAt(at, modBits(keys))
	}
	return nil
}

func (h *Host) contextMenuAt(at pointerLocation, mods int) {
	kind, button := hotty.EventContextMenu, hotty.ButtonPrimary
	var n *html.Node
	if h.version == "0.8" {
		n = closest(at.element, listens("secondary"))
		kind, button = hotty.EventPress, hotty.ButtonSecondary
	} else if n = closest(at.element, secondaryTarget); n != nil {
		if takesRightButton(n) || !listens("contextmenu")(n) {
			return
		}
	}
	if n != nil {
		id, _ := attr(n, "id")
		h.send(hotty.EncodeEvent(hotty.Event{Surface: at.surface.name, Target: id, Kind: kind, Button: button,
			At: &at.Cell, Mods: mods}))
	}
}

// AuxClick plays another button's press and release at the same location.
// Use PointerDown and PointerUp when the location or modifiers change.
func (h *Host) AuxClick(surface, id string, button, c, r int, keys ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, err := h.pointerTarget(surface, id, c, r)
	if err != nil {
		return err
	}
	if at.surface == nil || h.pointerHeld() || h.alt1(keys) || modBits(keys)&hotty.ModShift != 0 {
		return nil
	}
	if n := h.auxTarget(at.element, button); n != nil {
		h.auxClickAt(at, n, button, modBits(keys))
	}
	return nil
}

func (h *Host) auxClickAt(at pointerLocation, n *html.Node, button, mods int) {
	id, _ := attr(n, "id")
	value, hasValue := attr(n, "value")
	ev := hotty.Event{Surface: at.surface.name, Target: id, Kind: hotty.EventAuxClick, Button: button,
		At: &at.Cell, Mods: mods, Value: value, HasValue: hasValue}
	if n.DataAtom == atom.A {
		if href, ok := attr(n, "href"); ok {
			ev.Link = &hotty.Link{Href: href, URL: at.surface.resolve(n)}
		}
	}
	h.send(hotty.EncodeEvent(ev))
}

func takesRightButton(n *html.Node) bool {
	return !hyperlink(n) && listens("rightbutton")(n)
}

func secondaryTarget(n *html.Node) bool {
	return takesRightButton(n) || listens("contextmenu")(n)
}

func (h *Host) auxTarget(el *html.Node, button int) *html.Node {
	if h.version == "0.8" || button <= hotty.ButtonPrimary || button == 5 {
		return nil
	}
	if button == hotty.ButtonSecondary {
		if n := closest(el, secondaryTarget); n != nil && takesRightButton(n) {
			return n
		}
		return nil
	}
	return closest(el, func(n *html.Node) bool { return !hyperlink(n) && listens("aux")(n) })
}
