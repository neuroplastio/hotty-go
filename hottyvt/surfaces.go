package hottyvt

import (
	"bytes"
	"html"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/neuroplastio/hotty-go"
)

// A screen shows the surfaces the program makes, as a HOTTY host would
// (SPEC §5): the HOTTY messages in what it is given make surfaces, place
// them, change them, and send them resources, and the screen keeps each
// surface's document and placement. Each one it shows is an element of the
// screen's own, above the rows, at its cells: a box the window's size
// (id-p<n>), with the document in it (id-d<n>) laid out at the placement's
// size and scaled with the screen.
//
// What changes reaches the screen's surface as deltas of its own: a delta
// to a surface is the same delta to its element in the screen, a placement
// is the box's style, a resource is a resource of the screen's
// (SPEC §6, §7.1). What the documents say is rewritten to live in one
// document (scope.go).

// surface is a surface the program made.
type surface struct {
	name  string
	n     int           // its number, in its elements' ids
	root  *nethtml.Node // its document, rewritten into its element
	sc    *scope
	alias map[string]bool // the ids its html and body had (rewritten): its element's

	placed     bool
	cols, rows int    // the placement's size; rows 0: auto
	win        [4]int // its window: x, y, w, h; w and h 0 reach the edges
	z          int
	col, row   int  // its top-left cell
	alt        bool // placed on the alternate screen

	// The box's style as the screen's surface has it; "" before it has
	// the box.
	sentStyle string
}

type resource struct {
	mime string
	data []byte
}

// op is a delta for the screen's surface, made as the program changed a
// surface, sent with the next Delta.
type op struct {
	op          hotty.Op
	target, key string
	payload     []byte
}

// The longest HOTTY sequence the screen holds while waiting for its end: a
// chunk is at most 4096 bytes of payload, and its control (SPEC §3.4).
const maxSequence = 64 << 10

var oscHotty = []byte("\x1b]" + hotty.Number)

// feed takes output: HOTTY messages to the surfaces, the rest to the cells.
//
// A HOTTY message is an OSC: it starts with ESC ] wherever it is, since an
// ESC ends any other string, and ends with BEL or ST (ESC \). Another ESC
// cuts it short, and starts what follows.
func (s *Screen) feed(p []byte) {
	buf := p
	if len(s.held) > 0 {
		buf = append(s.held, p...)
		s.held = nil
	}
	for len(buf) > 0 {
		i := bytes.Index(buf, oscHotty)
		if i < 0 {
			keep := partialPrefix(buf, oscHotty)
			s.cells(buf[:len(buf)-keep])
			if keep > 0 {
				s.held = append([]byte(nil), buf[len(buf)-keep:]...)
			}
			return
		}
		after := i + len(oscHotty)
		if after < len(buf) && buf[after] != ';' && buf[after] != '\a' && buf[after] != '\x1b' {
			s.cells(buf[:after]) // an OSC whose number begins with 7279
			buf = buf[after:]
			continue
		}
		s.cells(buf[:i])
		buf = buf[i:]
		end, cut := -1, false
		for j := len(oscHotty); j < len(buf); j++ {
			if buf[j] == '\a' {
				end = j + 1
				break
			}
			if buf[j] == '\x1b' {
				if j+1 < len(buf) {
					if buf[j+1] == '\\' {
						end = j + 2
					} else {
						end, cut = j, true
					}
				}
				break
			}
		}
		if end < 0 {
			if len(buf) > maxSequence {
				// Longer than any chunk: not a message. The emulator
				// skips it, to its end, as any OSC it does not know.
				s.cells(buf)
				return
			}
			s.held = append([]byte(nil), buf...)
			return
		}
		if !cut {
			if m, r := s.dec.Feed(string(buf[:end])); r == hotty.Complete {
				s.message(m)
			}
		}
		buf = buf[end:]
	}
}

// partialPrefix is the length of the longest end of b that begins prefix.
func partialPrefix(b, prefix []byte) int {
	for n := min(len(b), len(prefix)-1); n > 0; n-- {
		if bytes.HasPrefix(prefix, b[len(b)-n:]) {
			return n
		}
	}
	return 0
}

// cells gives the emulator output, and moves the placements with the lines
// they are on (SPEC §5.4).
func (s *Screen) cells(b []byte) {
	if len(b) == 0 {
		return
	}
	stamped := s.stamp()
	_, _ = s.emu.Write(b)
	if stamped {
		s.follow()
	}
}

// --- placements and the lines they are on -------------------------------------

// A placement moves with the line it is on, as the screen scrolls. The
// emulator says nothing of scrolling, so before output the screen stamps
// each row, in the first cell's hyperlink parameters, which nothing draws;
// after it, where the stamps went is where the rows went. A row the output
// wrote over loses its stamp, and goes the way the nearest stamped row went.

const stampKey = "hottyvt-row="

func (s *Screen) stamp() bool {
	alt := s.emu.IsAltScreen()
	placed := false
	for _, sf := range s.order {
		if sf.placed && sf.alt == alt {
			placed = true
			break
		}
	}
	if !placed {
		return false
	}
	s.stamps++
	gen := strconv.Itoa(s.stamps) + "."
	_, rows := s.Size()
	for y := range rows {
		c := uv.EmptyCell
		if old := s.emu.CellAt(0, y); old != nil {
			c = *old
		}
		c.Link.Params = stampKey + gen + strconv.Itoa(y)
		s.emu.SetCell(0, y, &c)
	}
	return true
}

func (s *Screen) follow() {
	_, rows := s.Size()
	gen := stampKey + strconv.Itoa(s.stamps) + "."
	moved := map[int]int{} // a row before the output: where it is now
	for y := range rows {
		if c := s.emu.CellAt(0, y); c != nil {
			if was, ok := strings.CutPrefix(c.Link.Params, gen); ok {
				if old, err := strconv.Atoi(was); err == nil {
					moved[old] = y
				}
			}
		}
	}
	if len(moved) == 0 {
		return
	}
	alt := s.emu.IsAltScreen()
	for _, sf := range s.order {
		if !sf.placed || sf.alt != alt {
			continue
		}
		sf.row += shift(moved, sf.row, rows)
		// A placement whose lines all left the screen is gone with them:
		// the screen keeps no scrollback.
		if sf.row+sf.height() <= 0 || sf.row >= rows {
			sf.placed = false
		}
	}
}

// shift is how far a row moved: as its stamp did, or the nearest stamped
// row's.
func shift(moved map[int]int, row, rows int) int {
	for d := 0; d < rows+abs(row); d++ {
		for _, r := range [2]int{row - d, row + d} {
			if y, ok := moved[r]; ok {
				return y - r
			}
		}
	}
	return 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// height is the rows the placement covers: its window's.
func (sf *surface) height() int {
	if sf.win[3] > 0 {
		return sf.win[3]
	}
	if sf.rows > 0 {
		return sf.rows - sf.win[1]
	}
	return sf.estimate()
}

// estimate is the rows a placement with auto rows takes from the cursor: the
// host that showed the program laid its document out and chose them, and
// the screen, which lays nothing out, takes a row for each line of its text.
// The document itself shows at its height.
func (sf *surface) estimate() int {
	rows := 0
	for _, line := range strings.Split(strings.TrimSpace(textOf(sf.root)), "\n") {
		n := utf8.RuneCountInString(strings.TrimSpace(line))
		rows += max(1, (n+sf.cols-1)/max(1, sf.cols))
	}
	return max(1, rows)
}

func textOf(n *nethtml.Node) string {
	var b strings.Builder
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		switch {
		case n.Type == nethtml.TextNode:
			b.WriteString(n.Data)
		case n.DataAtom == atom.Style || n.DataAtom == atom.Script:
			return
		case n.DataAtom == atom.Br || n.DataAtom == atom.P || n.DataAtom == atom.Div || n.DataAtom == atom.Li ||
			n.DataAtom == atom.Tr || n.DataAtom == atom.H1 || n.DataAtom == atom.H2 || n.DataAtom == atom.H3:
			b.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// --- messages -----------------------------------------------------------------

// message does what a HOTTY message from the program says. The screen
// answers nothing: the program that made a recording had its answers.
func (s *Screen) message(m hotty.Message) {
	name := m.Get("s")
	sf := s.surfaces[name]
	switch m.Get("a") {
	case "doc":
		if !hotty.ValidName(name) {
			return
		}
		s.doc(name, sf, string(m.Payload))
	case "place":
		if sf != nil {
			s.place(sf, m)
		}
	case "hide":
		if sf != nil {
			sf.placed = false
		}
	case "delta":
		if sf != nil {
			s.delta(sf, m)
		}
	case "res":
		s.resource(m.Get("id"), m.Get("type"), m.Payload)
	case "del":
		switch id := m.Get("id"); {
		case name != "":
			if sf != nil {
				s.drop(sf)
			}
		case id != "":
			if _, ok := s.res[id]; ok {
				delete(s.res, id)
				s.resOut = append(s.resOut, hotty.DelRes(s.resID(id)))
			}
		default:
			s.dropAll()
		}
	}
}

// boxID and rootID are the ids of a surface's box and of its document's
// element; an element of its document has rootID-id.
func (s *Screen) boxID(sf *surface) string  { return s.id + "-p" + strconv.Itoa(sf.n) }
func (s *Screen) rootID(sf *surface) string { return s.id + "-d" + strconv.Itoa(sf.n) }
func (s *Screen) resID(id string) string    { return s.id + "-" + id }

func (s *Screen) doc(name string, sf *surface, markup string) {
	if sf == nil {
		s.made++
		sf = &surface{name: name, n: s.made}
		s.surfaces[name] = sf
		s.order = append(s.order, sf)
		s.parse(sf, markup)
		if s.sent != nil {
			sf.sentStyle = s.boxStyle(sf)
			s.ops = append(s.ops, op{op: hotty.OpAppend, target: s.id, payload: []byte(s.box(sf))})
		}
		return
	}
	s.parse(sf, markup)
	if s.sent != nil && sf.sentStyle != "" {
		s.ops = append(s.ops, op{op: hotty.OpMorph, target: s.rootID(sf), payload: []byte(render(sf.root))})
	}
}

// parse makes a surface's element of its document: what its head has that
// a document shows (its styles), then its body, in an element with the
// html's and body's attributes.
func (s *Screen) parse(sf *surface, markup string) {
	rootID := s.rootID(sf)
	sf.sc = &scope{ids: rootID + "-", res: s.id + "-", root: "#" + rootID}
	sf.alias = map[string]bool{}
	doc, err := nethtml.Parse(strings.NewReader(markup))
	if err != nil {
		doc, _ = nethtml.Parse(strings.NewReader(""))
	}
	root := &nethtml.Node{Type: nethtml.ElementNode, Data: "div", DataAtom: atom.Div}
	classes := []string{"vt-d"}
	var styles []string
	attrs := map[string]nethtml.Attribute{}
	var keys []string
	var head, body *nethtml.Node
	for h := doc.FirstChild; h != nil; h = h.NextSibling {
		if h.Type != nethtml.ElementNode || h.DataAtom != atom.Html {
			continue
		}
		for c := h.FirstChild; c != nil; c = c.NextSibling {
			switch c.DataAtom {
			case atom.Head:
				head = c
			case atom.Body:
				body = c
			}
		}
		for _, el := range []*nethtml.Node{h, body} {
			if el == nil {
				continue
			}
			for _, a := range el.Attr {
				switch a.Key {
				case "class":
					classes = append(classes, a.Val)
				case "style":
					styles = append(styles, sf.sc.values(a.Val))
				case "id":
					sf.alias[sf.sc.ids+a.Val] = true
				default:
					if _, ok := attrs[a.Key]; !ok {
						keys = append(keys, a.Key)
					}
					attrs[a.Key] = a
				}
			}
		}
	}
	for _, k := range keys {
		if a := attrs[k]; a.Key != "data-on" && a.Key != "autofocus" {
			root.Attr = append(root.Attr, a)
		}
	}
	root.Attr = append(root.Attr, nethtml.Attribute{Key: "class", Val: strings.Join(classes, " ")},
		nethtml.Attribute{Key: "id", Val: rootID})
	if len(styles) > 0 {
		root.Attr = append(root.Attr, nethtml.Attribute{Key: "style", Val: strings.Join(styles, ";")})
	}
	sf.sc.base = nil
	if head != nil {
		for c := head.FirstChild; c != nil; {
			next := c.NextSibling
			switch {
			case c.DataAtom == atom.Base && sf.sc.base == nil:
				sf.sc.base = baseURL(c)
			case c.DataAtom == atom.Style, c.DataAtom == atom.Link && stylesheet(c):
				head.RemoveChild(c)
				root.AppendChild(c)
			}
			c = next
		}
	}
	if body != nil {
		for c := body.FirstChild; c != nil; {
			next := c.NextSibling
			body.RemoveChild(c)
			root.AppendChild(c)
			c = next
		}
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		sf.sc.element(c)
	}
	sf.root = root
}

// baseURL is a <base> element's href when it is an absolute http or https
// URL, and nil otherwise (SPEC §7.3).
func baseURL(n *nethtml.Node) *url.URL {
	u, err := url.Parse(strings.TrimSpace(attrOf(n, "href")))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	return u
}

func stylesheet(n *nethtml.Node) bool {
	for _, a := range n.Attr {
		if a.Key == "rel" {
			for _, r := range strings.Fields(strings.ToLower(a.Val)) {
				if r == "stylesheet" {
					return true
				}
			}
		}
	}
	return false
}

// place places a surface at the cursor (SPEC §5.2). A placement the host
// refused (EINVAL: a size, a window or a z out of range) changes nothing,
// as it changed nothing where the program was recorded.
func (s *Screen) place(sf *surface, m hotty.Message) {
	bad := false
	num := func(k string, least, most, def int) int {
		v, ok := m.Control[k]
		if !ok {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < least || n > most {
			bad = true
		}
		return n
	}
	cols := num("c", 1, hotty.MaxSize, 0)
	rows := 0
	if r := m.Get("r"); r != "" && r != "auto" {
		rows = num("r", 1, hotty.MaxSize, 0)
	}
	x, y := num("x", 0, hotty.MaxSize-1, 0), num("y", 0, hotty.MaxSize-1, 0)
	w, h := num("w", 1, hotty.MaxSize, cols-x), num("h", 1, hotty.MaxSize, max(0, rows-y))
	z := num("z", -1000, 1000, 0)
	if bad || cols == 0 || x+w > cols || rows > 0 && y+h > rows {
		return
	}
	if _, ok := m.Control["h"]; !ok && rows == 0 {
		h = 0 // auto: to the document's end
	}
	sf.cols, sf.rows, sf.win, sf.z = cols, rows, [4]int{x, y, w, h}, z
	cur := s.emu.CursorPosition()
	sf.col, sf.row = cur.X, cur.Y
	sf.alt = s.emu.IsAltScreen()
	sf.placed = true
	if m.Get("C") != "1" {
		// The cursor goes to the start of the line below the placement,
		// scrolling if it must (SPEC §5.2).
		s.cells(append(bytes.Repeat([]byte("\x1bD"), sf.height()), '\r'))
	}
}

// drop deletes a surface, and its box from the screen's surface.
func (s *Screen) drop(sf *surface) {
	delete(s.surfaces, sf.name)
	for i, o := range s.order {
		if o == sf {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if s.sent != nil && sf.sentStyle != "" {
		s.ops = append(s.ops, op{op: hotty.OpRemove, target: s.boxID(sf)})
	}
}

func (s *Screen) dropAll() {
	for len(s.order) > 0 {
		s.drop(s.order[0])
	}
}

// altScreen is the emulator's word that the program entered or left the
// alternate screen. The placements made on it go when it does, and their
// surfaces with them (SPEC §5.4); those of the main screen show again.
func (s *Screen) altScreen(on bool) {
	if on {
		return
	}
	for _, sf := range append([]*surface(nil), s.order...) {
		if sf.placed && sf.alt {
			s.drop(sf)
		}
	}
}

func (s *Screen) resource(id, mime string, data []byte) {
	if id == "" {
		return
	}
	if strings.HasPrefix(strings.ToLower(mime), "text/css") {
		// Shared by the surfaces that link it: scoped to any of them.
		sc := &scope{res: s.id + "-", root: "#" + s.id + " :where(.vt-d)"}
		data = []byte(sc.sheet(string(data)))
	}
	if _, ok := s.res[id]; !ok {
		s.resOrder = append(s.resOrder, id)
	}
	s.res[id] = resource{mime, data}
	s.resOut = append(s.resOut, hotty.Res(s.resID(id), mime, data))
}

// Resources is the commands that give a host the resources the screen's
// surfaces refer to, as they are now. Delta sends each as it arrives; a
// program that gives a host the screen's element afresh, a host that has
// not had them, sends these with it.
func (s *Screen) Resources() []string {
	var out []string
	for _, id := range s.resOrder {
		if r, ok := s.res[id]; ok {
			out = append(out, hotty.Res(s.resID(id), r.mime, r.data))
		}
	}
	return out
}

// --- deltas -------------------------------------------------------------------

// delta applies a delta to a surface's document (SPEC §6), and makes it a
// delta of the screen's surface. One the screen cannot apply, it leaves
// out: the host would refuse it too.
func (s *Screen) delta(sf *surface, m hotty.Message) {
	o := hotty.Op(m.Get("op"))
	if o == "" {
		o = hotty.OpMorph
	}
	key, payload := m.Get("k"), string(m.Payload)
	if o == hotty.OpMorph && m.Get("t") == "" {
		s.morphByIDs(sf, payload)
		return
	}
	tid := sf.sc.ids + m.Get("t")
	target := sf.find(tid)
	if target == nil {
		return
	}
	isRoot := target == sf.root
	outT := tid
	if isRoot {
		outT = s.rootID(sf)
	}
	out := op{op: o, target: outT, key: key}
	switch o {
	case hotty.OpMorph, hotty.OpReplace, hotty.OpBefore, hotty.OpAfter, hotty.OpRemove:
		if isRoot {
			return // the screen keeps the element; its children may change
		}
		var nodes []*nethtml.Node
		if o != hotty.OpRemove {
			nodes = sf.fragment(payload, target.Parent)
			out.payload = []byte(renderAll(nodes))
		}
		switch o {
		case hotty.OpBefore:
			for _, n := range nodes {
				target.Parent.InsertBefore(n, target)
			}
		case hotty.OpAfter:
			next := target.NextSibling
			for _, n := range nodes {
				target.Parent.InsertBefore(n, next)
			}
		default:
			for _, n := range nodes {
				target.Parent.InsertBefore(n, target)
			}
			target.Parent.RemoveChild(target)
		}
	case hotty.OpInner:
		nodes := sf.fragment(payload, target)
		out.payload = []byte(renderAll(nodes))
		removeChildren(target)
		for _, n := range nodes {
			target.AppendChild(n)
		}
	case hotty.OpAppend, hotty.OpPrepend:
		nodes := sf.fragment(payload, target)
		out.payload = []byte(renderAll(nodes))
		first := target.FirstChild
		for _, n := range nodes {
			if id := attrOf(n, "id"); id != "" && n.Type == nethtml.ElementNode {
				if old := childByID(target, id); old != nil {
					target.InsertBefore(n, old)
					target.RemoveChild(old)
					continue
				}
			}
			if o == hotty.OpAppend || first == nil {
				target.AppendChild(n)
			} else {
				target.InsertBefore(n, first)
			}
		}
	case hotty.OpText:
		removeChildren(target)
		target.AppendChild(&nethtml.Node{Type: nethtml.TextNode, Data: payload})
		out.payload = m.Payload
	case hotty.OpVar:
		v := sf.sc.values(payload)
		name := key
		if !strings.HasPrefix(name, "--") {
			name = "--" + name
		}
		setAttr(target, "style", setStyleProp(attrOf(target, "style"), name, v))
		out.payload = []byte(v)
	case hotty.OpAttr:
		v, keep := sf.sc.attr(target, "", key, payload)
		switch {
		case isRoot && key == "id":
			return
		case isRoot && key == "class":
			v = "vt-d " + payload
		case !keep && key == "href":
			delAttr(target, key)
			out.op = hotty.OpUnattr
			s.queue(out)
			return
		case !keep:
			return
		}
		setAttr(target, key, v)
		out.payload = []byte(v)
	case hotty.OpUnattr:
		switch {
		case key == "data-on" || key == "autofocus" || isRoot && key == "id":
			return
		case isRoot && key == "class":
			setAttr(target, "class", "vt-d")
			out = op{op: hotty.OpAttr, target: outT, key: "class", payload: []byte("vt-d")}
		default:
			delAttr(target, key)
		}
	default:
		return
	}
	s.queue(out)
}

// morphByIDs is morph without a target (SPEC §6.2): each of the payload's
// top-level elements takes the place of the element with its id. Those
// whose id the document lacks are left out of the screen's delta.
func (s *Screen) morphByIDs(sf *surface, payload string) {
	var b strings.Builder
	for _, n := range sf.fragment(payload, sf.root) {
		id := attrOf(n, "id")
		if n.Type != nethtml.ElementNode || id == "" {
			continue
		}
		old := sf.find(id)
		if old == nil || old == sf.root {
			continue
		}
		b.WriteString(render(n))
		old.Parent.InsertBefore(n, old)
		old.Parent.RemoveChild(old)
	}
	if b.Len() > 0 {
		s.queue(op{op: hotty.OpMorph, payload: []byte(b.String())})
	}
}

func (s *Screen) queue(o op) {
	if s.sent != nil {
		s.ops = append(s.ops, o)
	}
}

// fragment parses a payload in the context of the element it will be a
// child of (SPEC §6.1), and rewrites it.
func (sf *surface) fragment(payload string, context *nethtml.Node) []*nethtml.Node {
	if context == nil || context.Type != nethtml.ElementNode {
		context = &nethtml.Node{Type: nethtml.ElementNode, Data: "body", DataAtom: atom.Body}
	}
	nodes, err := nethtml.ParseFragment(strings.NewReader(payload), context)
	if err != nil {
		return nil
	}
	for _, n := range nodes {
		sf.sc.element(n)
	}
	return nodes
}

// find is the element of the surface's document with the (rewritten) id.
func (sf *surface) find(id string) *nethtml.Node {
	if sf.alias[id] {
		return sf.root
	}
	var found *nethtml.Node
	var walk func(*nethtml.Node) bool
	walk = func(n *nethtml.Node) bool {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != nethtml.ElementNode {
				continue
			}
			if attrOf(c, "id") == id {
				found = c
				return true
			}
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(sf.root)
	return found
}

func childByID(parent *nethtml.Node, id string) *nethtml.Node {
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == nethtml.ElementNode && attrOf(c, "id") == id {
			return c
		}
	}
	return nil
}

func removeChildren(n *nethtml.Node) {
	for n.FirstChild != nil {
		n.RemoveChild(n.FirstChild)
	}
}

func attrOf(n *nethtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *nethtml.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, nethtml.Attribute{Key: key, Val: val})
}

func delAttr(n *nethtml.Node, key string) {
	for i, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
			return
		}
	}
}

// setStyleProp sets a property in a style attribute, keeping the others.
func setStyleProp(style, name, value string) string {
	var out []string
	found := false
	for decl := range strings.SplitSeq(style, ";") {
		k, _, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == name {
			out = append(out, name+": "+value)
			found = true
			continue
		}
		out = append(out, strings.TrimSpace(decl))
	}
	if !found {
		out = append(out, name+": "+value)
	}
	return strings.Join(out, "; ")
}

func render(n *nethtml.Node) string {
	var b strings.Builder
	_ = nethtml.Render(&b, n)
	return b.String()
}

func renderAll(nodes []*nethtml.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		_ = nethtml.Render(&b, n)
	}
	return b.String()
}

// --- the screen's element -----------------------------------------------------

// box is a surface's box: its window, at its cells (vt-p), with the
// surface's viewport in it, laid out at the placement's size and scaled
// with the screen (vt-v), and in that its document (vt-d), whose root
// sizes against the viewport as a document's does.
func (s *Screen) box(sf *surface) string {
	return `<div class="vt-p" id="` + html.EscapeString(s.boxID(sf)) + `" style="` +
		html.EscapeString(s.boxStyle(sf)) + `"><div class="vt-v">` + render(sf.root) + `</div></div>`
}

// boxStyle places a surface's box (CSS): where its window is, in the
// screen's cells, and the size its document is laid out at. A surface that
// is not placed, or is placed on the screen the terminal is not showing,
// is not shown.
func (s *Screen) boxStyle(sf *surface) string {
	if !sf.placed || sf.alt != s.emu.IsAltScreen() {
		return "display:none"
	}
	v := "--vt-px:" + strconv.Itoa(sf.col) + ";--vt-py:" + strconv.Itoa(sf.row) + ";--vt-pc:" + strconv.Itoa(sf.cols)
	if sf.rows > 0 {
		v += ";--vt-pr:" + strconv.Itoa(sf.rows)
	}
	x, y, w, h := sf.win[0], sf.win[1], sf.win[2], sf.win[3]
	if x > 0 {
		v += ";--vt-wx:" + strconv.Itoa(x)
	}
	if y > 0 {
		v += ";--vt-wy:" + strconv.Itoa(y)
	}
	if w == 0 {
		w = sf.cols - x
	}
	if w != sf.cols {
		v += ";--vt-pw:" + strconv.Itoa(w)
	}
	if h == 0 && sf.rows > 0 {
		h = sf.rows - y
	}
	if h > 0 && h != sf.rows {
		v += ";--vt-ph:" + strconv.Itoa(h)
	}
	if sf.z != 0 {
		v += ";z-index:" + strconv.Itoa(sf.z+1001)
	}
	return v
}

// surfacesHTML is the boxes of every surface, in the order they were made,
// which is their stacking (SPEC §5.2).
func (s *Screen) surfacesHTML() string {
	var b strings.Builder
	for _, sf := range s.order {
		sf.sentStyle = s.boxStyle(sf)
		b.WriteString(s.box(sf))
	}
	s.ops = nil
	return b.String()
}

// surfaceDelta is the commands that bring the screen's surface up to the
// surfaces: the resources that arrived, the deltas the program made, and
// the boxes that moved.
func (s *Screen) surfaceDelta(surface string) []string {
	out := s.resOut
	s.resOut = nil
	for _, o := range s.ops {
		if o.op == hotty.OpMorph && o.target != "" {
			out = append(out, hotty.MorphTo(surface, o.target, string(o.payload)))
			continue
		}
		out = append(out, hotty.Delta(surface, o.op, o.target, o.key, o.payload))
	}
	s.ops = nil
	for _, sf := range s.order {
		if st := s.boxStyle(sf); sf.sentStyle != "" && st != sf.sentStyle {
			sf.sentStyle = st
			out = append(out, hotty.SetAttr(surface, s.boxID(sf), "style", st))
		}
	}
	return out
}
