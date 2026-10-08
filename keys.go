package hotty

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Action is what a text field does with a key (SPEC §10.2): a keymap binds
// keys to actions, and Lookup says which one a key does.
type Action string

// The actions a keymap binds (SPEC §10.2).
const (
	CharBackward       Action = "char-backward"
	CharForward        Action = "char-forward"
	WordBackward       Action = "word-backward"
	WordForward        Action = "word-forward"
	LineStart          Action = "line-start"
	LineEnd            Action = "line-end"
	DeleteCharBackward Action = "delete-char-backward"
	DeleteCharForward  Action = "delete-char-forward"
	DeleteWordBackward Action = "delete-word-backward"
	DeleteWordForward  Action = "delete-word-forward"
	DeleteToLineStart  Action = "delete-to-line-start"
	DeleteToLineEnd    Action = "delete-to-line-end"
	LinePrevious       Action = "line-previous"
	LineNext           Action = "line-next"
	PageUp             Action = "page-up"
	PageDown           Action = "page-down"
	InputStart         Action = "input-start"
	InputEnd           Action = "input-end"
	Newline            Action = "newline"
	Submit             Action = "submit"
	// Program binds a key to nothing: it reaches the program.
	Program Action = "program"
	// Insert is what Lookup returns for a character the field types. A
	// keymap does not bind it.
	Insert Action = "insert"
)

// actions is every action a keymap binds.
var actions = map[Action]bool{
	CharBackward: true, CharForward: true, WordBackward: true, WordForward: true,
	LineStart: true, LineEnd: true, DeleteCharBackward: true, DeleteCharForward: true,
	DeleteWordBackward: true, DeleteWordForward: true, DeleteToLineStart: true, DeleteToLineEnd: true,
	LinePrevious: true, LineNext: true, PageUp: true, PageDown: true,
	InputStart: true, InputEnd: true, Newline: true, Submit: true, Program: true,
}

// Multiline reports whether only a multi-line field (a textarea, an
// editing host) has the action. From an input, a key bound to one reaches
// the program.
func (a Action) Multiline() bool {
	switch a {
	case LinePrevious, LineNext, PageUp, PageDown, InputStart, InputEnd, Newline:
		return true
	}
	return false
}

// TerminalKeys is the SDK's keymap (SDK.md §3.10), as a data-keys value:
// the keys of Bubble Tea's text input and text area (bubbles). A program
// puts it in the data-keys of an element that holds its fields, and edits
// its fields in cells with Resolve(multiline, TerminalKeys), so that they
// edit the same on a surface and in cells. It leaves Enter to SPEC §10.2's
// default.
const TerminalKeys = "ArrowLeft=char-backward Control+b=char-backward ArrowRight=char-forward Control+f=char-forward " +
	"Alt+ArrowLeft=word-backward Control+ArrowLeft=word-backward Alt+b=word-backward " +
	"Alt+ArrowRight=word-forward Control+ArrowRight=word-forward Alt+f=word-forward " +
	"Home=line-start Control+a=line-start End=line-end Control+e=line-end " +
	"Backspace=delete-char-backward Control+h=delete-char-backward " +
	"Delete=delete-char-forward Control+d=delete-char-forward " +
	"Alt+Backspace=delete-word-backward Control+w=delete-word-backward Control+Backspace=delete-word-backward " +
	"Alt+Delete=delete-word-forward Alt+d=delete-word-forward Control+Delete=delete-word-forward " +
	"Control+u=delete-to-line-start Control+k=delete-to-line-end " +
	"ArrowUp=line-previous Control+p=line-previous ArrowDown=line-next Control+n=line-next " +
	"PageUp=page-up PageDown=page-down " +
	"Alt+<=input-start Control+Home=input-start Alt+>=input-end Control+End=input-end " +
	"Control+m=newline"

// The modifiers, in the order a key's name writes them.
var modifiers = [...]string{"Control", "Alt", "Meta", "Shift"}

// mods is a set of modifiers, a bit for each of modifiers.
type mods uint8

const (
	modControl mods = 1 << iota
	modAlt
	modMeta
	modShift
)

// key is a key's name, split.
type key struct {
	mods  mods
	value string // " " for the space bar
}

// String is the key's canonical name (SPEC §10.4).
func (k key) String() string {
	var b strings.Builder
	for i, m := range modifiers {
		if k.mods&(1<<i) != 0 {
			b.WriteString(m)
			b.WriteByte('+')
		}
	}
	if k.value == " " {
		b.WriteString("Space")
	} else {
		b.WriteString(k.value)
	}
	return b.String()
}

// canonical shows Shift in a letter where it can: Shift with a small letter
// is its capital, and Shift with a capital is the capital.
func (k key) canonical() key {
	if k.mods&modShift == 0 || !isChar(k.value) {
		return k
	}
	r, n := utf8.DecodeRuneInString(k.value)
	if n != len(k.value) {
		return k
	}
	if up := unicode.ToUpper(r); up != r {
		return key{k.mods &^ modShift, string(up)}
	}
	if unicode.ToLower(r) != r {
		return key{k.mods &^ modShift, k.value}
	}
	return k
}

// isChar reports whether a key's value is a character: one grapheme
// cluster that is not a control.
func isChar(v string) bool {
	if v == "" || clusterLen(v) != len(v) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(v)
	return r != utf8.RuneError && !unicode.IsControl(r)
}

// isNamed reports whether a key's value is a named key value: a capital,
// then letters and digits ("Enter", "ArrowLeft", "F12").
func isNamed(v string) bool {
	if len(v) < 2 || v[0] < 'A' || v[0] > 'Z' {
		return false
	}
	for i := 1; i < len(v); i++ {
		c := v[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
			return false
		}
	}
	return true
}

// splitKey reads a key's name, its modifiers in any order.
func splitKey(name string) (key, bool) {
	var head, value string
	switch i := strings.LastIndexByte(name, '+'); {
	case name == "+":
		value = "+"
	case strings.HasSuffix(name, "++") && len(name) > 2:
		head, value = name[:len(name)-2], "+"
	case i >= 0:
		head, value = name[:i], name[i+1:]
	default:
		value = name
	}
	var k key
	if head != "" {
	next:
		for _, m := range strings.Split(head, "+") {
			for i, known := range modifiers {
				if m == known && k.mods&(1<<i) == 0 {
					k.mods |= 1 << i
					continue next
				}
			}
			return key{}, false
		}
	}
	if value == "Space" {
		value = " "
	}
	if !isChar(value) && !isNamed(value) {
		return key{}, false
	}
	k.value = value
	return k, true
}

// ParseKey reads a key's name as SPEC §10.4 writes it, its modifiers in any
// order ("Shift+Control+a", "Control+ "), and returns its canonical name
// ("Control+A", "Control+Space"): the modifiers in the order Control, Alt,
// Meta, Shift; Shift shown in a letter where it can be; Space for a space.
// ok is false for a name that does not parse.
func ParseKey(name string) (canonical string, ok bool) {
	k, ok := splitKey(name)
	if !ok {
		return "", false
	}
	return k.canonical().String(), true
}

// DecodeKeys reads input from the terminal, the bytes a program reads, as
// SPEC §10.4 names the keys in it: one entry for each key, its canonical
// name, or "" for input that is no key it names (a mouse report, a sequence
// it does not know, a key's release). It reads the input whole: an ESC at
// its end is Escape.
func DecodeKeys(input []byte) []string {
	var out []string
	for i := 0; i < len(input); {
		if input[i] != 0x1b {
			k, n := oneKey(input[i:])
			out = append(out, k)
			i += n
			continue
		}
		if i+1 == len(input) {
			out = append(out, "Escape")
			break
		}
		switch next := input[i+1]; {
		case next == 'O' && i+2 < len(input):
			out = append(out, ss3Key(input[i+2]))
			i += 3
		case next == '[' && i+2 < len(input):
			j := i + 2
			for j < len(input) && input[j] >= 0x30 && input[j] <= 0x3f {
				j++
			}
			params := string(input[i+2 : j])
			for j < len(input) && input[j] >= 0x20 && input[j] <= 0x2f {
				j++
			}
			if j == len(input) || input[j] < 0x40 || input[j] > 0x7e {
				out = append(out, "")
				i = j + 1
				continue
			}
			out = append(out, csiKey(params, input[j]))
			i = j + 1
		default:
			k, n := oneKey(input[i+1:])
			out = append(out, withAlt(k))
			i += 1 + n
		}
	}
	return out
}

// withAlt is a key's name with Alt.
func withAlt(name string) string {
	k, ok := splitKey(name)
	if !ok {
		return ""
	}
	k.mods |= modAlt
	return k.canonical().String()
}

// c0Keys are the C0 controls and DEL that are not Control with a letter.
var c0Keys = map[byte]string{
	0x00: "Control+Space", 0x08: "Control+h", 0x09: "Tab", 0x0d: "Enter", 0x1b: "Escape", 0x7f: "Backspace",
	0x1c: `Control+\`, 0x1d: "Control+]", 0x1e: "Control+^", 0x1f: "Control+_",
}

// oneKey is the key of the control or the character input starts with, and
// its length in bytes.
func oneKey(input []byte) (string, int) {
	b := input[0]
	if b < 0x20 || b == 0x7f {
		if k, ok := c0Keys[b]; ok {
			return k, 1
		}
		return "Control+" + string(rune(b+0x60)), 1
	}
	r, n := utf8.DecodeRune(input)
	if r == utf8.RuneError {
		return "", 1
	}
	if r == ' ' {
		return "Space", 1
	}
	n = max(clusterLen(string(input[:min(len(input), 64)])), n)
	return string(input[:n]), n
}

// csiFinals are the keys CSI and SS3 name by their final byte.
var csiFinals = map[byte]string{'A': "ArrowUp", 'B': "ArrowDown", 'C': "ArrowRight", 'D': "ArrowLeft", 'H': "Home", 'F': "End"}

// tildeKeys are the keys CSI n ~ names.
var tildeKeys = map[int]string{1: "Home", 7: "Home", 4: "End", 8: "End", 2: "Insert", 3: "Delete", 5: "PageUp", 6: "PageDown"}

// codeKeys are the codes of the kitty protocol and modifyOtherKeys that are
// not characters.
var codeKeys = map[int]string{9: "Tab", 13: "Enter", 27: "Escape", 8: "Backspace", 127: "Backspace"}

// kittyKeys are the kitty protocol's codes from 57344 that name keys.
var kittyKeys = map[int]string{
	57399: "0", 57400: "1", 57401: "2", 57402: "3", 57403: "4", 57404: "5", 57405: "6", 57406: "7", 57407: "8", 57408: "9",
	57409: ".", 57410: "/", 57411: "*", 57412: "-", 57413: "+", 57414: "Enter", 57415: "=",
	57417: "ArrowLeft", 57418: "ArrowRight", 57419: "ArrowUp", 57420: "ArrowDown", 57421: "PageUp", 57422: "PageDown",
	57423: "Home", 57424: "End", 57425: "Insert", 57426: "Delete",
	57441: "Shift", 57442: "Control", 57443: "Alt", 57444: "Meta", 57447: "Shift", 57448: "Control", 57449: "Alt", 57450: "Meta",
}

// ss3Key is the key SS3 with a final byte names.
func ss3Key(final byte) string {
	return csiFinals[final]
}

// modsOf reads a CSI parameter m[:e]: its modifiers, and whether the event
// is a release. ok is false when it does not parse.
func modsOf(field string) (m mods, release, ok bool) {
	num, ev, _ := strings.Cut(field, ":")
	n, e := 1, 1
	var err error
	if num != "" {
		if n, err = strconv.Atoi(num); err != nil {
			return 0, false, false
		}
	}
	if ev != "" {
		if e, err = strconv.Atoi(ev); err != nil {
			return 0, false, false
		}
	}
	bits := max(n-1, 0)
	if bits&1 != 0 {
		m |= modShift
	}
	if bits&2 != 0 {
		m |= modAlt
	}
	if bits&4 != 0 {
		m |= modControl
	}
	if bits&(8|32) != 0 {
		m |= modMeta
	}
	return m, e == 3, true
}

// codeKey is the key a code of the kitty protocol or of modifyOtherKeys
// names, with its modifiers, its shifted key (0 when not given) and its
// text ("" when not given).
func codeKey(code int, m mods, shifted int, text string) string {
	if v, ok := codeKeys[code]; ok {
		return key{m, v}.String()
	}
	if code >= 57344 {
		if v, ok := kittyKeys[code]; ok {
			return key{m, v}.String()
		}
		return ""
	}
	if code < 0x20 || code == 0x7f || code > unicode.MaxRune {
		return ""
	}
	base := string(rune(code))
	value := base
	if m&modShift != 0 {
		switch {
		case shifted > 0 && shifted <= unicode.MaxRune:
			value = string(rune(shifted))
		case text != "":
			value = text
		default:
			value = string(unicode.ToUpper(rune(code)))
		}
		if value != base {
			m &^= modShift
		}
	}
	return key{m, value}.String()
}

// csiKey is the key a CSI sequence names, or "".
func csiKey(params string, final byte) string {
	if params != "" && strings.ContainsRune("<=>?", rune(params[0])) {
		return ""
	}
	fields := strings.Split(params, ";")
	field := func(i int) string {
		if i < len(fields) {
			return fields[i]
		}
		return ""
	}
	switch {
	case final == 'u':
		codes := strings.Split(fields[0], ":")
		code, err := strconv.Atoi(codes[0])
		if err != nil {
			return ""
		}
		shifted := 0
		if len(codes) > 1 && codes[1] != "" {
			if shifted, err = strconv.Atoi(codes[1]); err != nil {
				return ""
			}
		}
		m, release, ok := modsOf(field(1))
		if !ok || release {
			return ""
		}
		var text strings.Builder
		if t := field(2); t != "" {
			for _, c := range strings.Split(t, ":") {
				r, err := strconv.Atoi(c)
				if err != nil || r < 0 || r > unicode.MaxRune {
					return ""
				}
				text.WriteRune(rune(r))
			}
		}
		return codeKey(code, m, shifted, text.String())
	case final == '~':
		n, err := strconv.Atoi(field(0))
		if err != nil {
			return ""
		}
		if n == 27 && len(fields) >= 3 {
			m, release, ok := modsOf(fields[1])
			code, err := strconv.Atoi(fields[2])
			if !ok || release || err != nil {
				return ""
			}
			return codeKey(code, m, 0, "")
		}
		v, known := tildeKeys[n]
		m, release, ok := modsOf(field(1))
		if !known || !ok || release {
			return ""
		}
		return key{m, v}.String()
	case final == 'Z':
		return "Shift+Tab"
	}
	v, known := csiFinals[final]
	m, release, ok := modsOf(field(1))
	if !known || !ok || release {
		return ""
	}
	return key{m, v}.String()
}

// clusterLen is the length in bytes of the grapheme cluster s starts with,
// near enough for a key: a character with the marks, joiners, variation
// selectors, skin tones and tags that follow it, a pair of regional
// indicators, or CR LF. A field's characters are counted by hottyedit, with
// Unicode's segmentation.
func clusterLen(s string) int {
	r, n := utf8.DecodeRuneInString(s)
	if r == '\r' && strings.HasPrefix(s[n:], "\n") {
		return 2
	}
	if unicode.IsControl(r) {
		return n
	}
	if isRegional(r) {
		if r2, m := utf8.DecodeRuneInString(s[n:]); isRegional(r2) {
			return n + m
		}
		return n
	}
	for n < len(s) {
		r2, m := utf8.DecodeRuneInString(s[n:])
		switch {
		case r2 == 0x200d:
			n += m
			if n < len(s) {
				_, m2 := utf8.DecodeRuneInString(s[n:])
				n += m2
			}
		case unicode.In(r2, unicode.Mn, unicode.Me, unicode.Mc), r2 >= 0xfe00 && r2 <= 0xfe0f,
			r2 >= 0x1f3fb && r2 <= 0x1f3ff, r2 >= 0xe0020 && r2 <= 0xe007f:
			n += m
		default:
			return n
		}
	}
	return n
}

func isRegional(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }

// Keymap binds keys to actions (SPEC §10.2). ParseKeymap reads one from a
// data-keys value; Resolve makes the one a field uses, whose Lookup says
// what the field does with a key.
type Keymap struct {
	keys      []string
	actions   map[string]Action
	multiline bool
}

// ParseKeymap reads a data-keys value as SPEC §10.2 has hosts read it:
// bindings separated by ASCII white space, each key=action, split at its
// last '='. It drops the bindings a host ignores: a key that does not parse, an
// action it does not know, and Tab, Shift+Tab and Escape.
func ParseKeymap(value string) *Keymap {
	m := &Keymap{}
	for _, b := range strings.FieldsFunc(value, func(r rune) bool { return strings.ContainsRune(" \t\n\f\r", r) }) {
		i := strings.LastIndexByte(b, '=')
		if i < 0 {
			continue
		}
		m.Bind(b[:i], Action(b[i+1:]))
	}
	return m
}

// Bind binds a key to an action, and reports whether it did: a host
// ignores the bindings ParseKeymap drops, and so does Bind.
func (m *Keymap) Bind(name string, a Action) bool {
	k, ok := ParseKey(name)
	if !ok || !actions[a] || k == "Tab" || k == "Shift+Tab" || k == "Escape" {
		return false
	}
	if m.actions == nil {
		m.actions = map[string]Action{}
	}
	if _, bound := m.actions[k]; !bound {
		m.keys = append(m.keys, k)
	}
	m.actions[k] = a
	return true
}

// Format writes the keymap as a data-keys value: each key once, where it
// was first bound, with its last action.
func (m *Keymap) Format() string {
	var b strings.Builder
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(string(m.actions[k]))
	}
	return b.String()
}

// Resolve makes a field's keymap (SPEC §10.2): the default keymap, for an
// input or for a multi-line field, then each data-keys value in turn, the
// root's first, each overriding the bindings before it key by key.
func Resolve(multiline bool, values ...string) *Keymap {
	m := &Keymap{multiline: multiline}
	enter := Submit
	if multiline {
		enter = Newline
	}
	for _, b := range []struct {
		key    string
		action Action
	}{
		{"ArrowLeft", CharBackward}, {"ArrowRight", CharForward}, {"Home", LineStart}, {"End", LineEnd},
		{"Backspace", DeleteCharBackward}, {"Delete", DeleteCharForward},
		{"ArrowUp", LinePrevious}, {"ArrowDown", LineNext}, {"PageUp", PageUp}, {"PageDown", PageDown},
		{"Enter", enter},
	} {
		m.Bind(b.key, b.action)
	}
	for _, v := range values {
		p := ParseKeymap(v)
		for _, k := range p.keys {
			m.Bind(k, p.actions[k])
		}
	}
	return m
}

// Lookup says what a field with this keymap does with a key (SPEC §10.2):
// an action; Insert for a character it types; or "" when the key is not the
// field's, and reaches the program (or, for Tab, moves focus). A key with
// Shift that no binding names is looked up without Shift.
func (m *Keymap) Lookup(name string) Action {
	k, ok := splitKey(name)
	if !ok {
		return ""
	}
	k = k.canonical()
	c := k.String()
	if c == "Tab" || c == "Shift+Tab" || c == "Escape" {
		return ""
	}
	a, bound := m.actions[c]
	if !bound && k.mods&modShift != 0 {
		a, bound = m.actions[key{k.mods &^ modShift, k.value}.String()]
	}
	switch {
	case bound && (a == Program || a.Multiline() && !m.multiline):
		return ""
	case bound:
		return a
	case isChar(k.value) && k.mods&(modControl|modAlt|modMeta) == 0:
		return Insert
	}
	return ""
}
