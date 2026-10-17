package hotty

import (
	"github.com/neuroplastio/myelin/myelinkey"
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
	SelectAll          Action = "select-all"
	Newline            Action = "newline"
	Submit             Action = "submit"
	// Program binds a key to nothing: it reaches the program.
	Program Action = "program"
	// Prefix binds a key to the program as Program does, and every key
	// after it reaches the program too, until it sends a=focus or a=blur
	// (SPEC §10.5).
	Prefix Action = "prefix"
	// Copy, Cut, Paste, Undo and Redo are the field's clipboard and
	// history (SPEC §10.2). A hottyedit.Field keeps neither, and does
	// nothing with them.
	Copy  Action = "copy"
	Cut   Action = "cut"
	Paste Action = "paste"
	Undo  Action = "undo"
	Redo  Action = "redo"
	// The scroll actions (SPEC §10.2, scrolling keys): outside a text field,
	// a key bound to one, which the element does not use, scrolls the
	// nearest element that scrolls, from the focused one outward; a text
	// field's keymap leaves them out.
	ScrollUp           Action = "scroll-up"
	ScrollDown         Action = "scroll-down"
	ScrollLeft         Action = "scroll-left"
	ScrollRight        Action = "scroll-right"
	ScrollPageUp       Action = "scroll-page-up"
	ScrollPageDown     Action = "scroll-page-down"
	ScrollHalfPageUp   Action = "scroll-half-page-up"
	ScrollHalfPageDown Action = "scroll-half-page-down"
	ScrollStart        Action = "scroll-start"
	ScrollEnd          Action = "scroll-end"
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
	InputStart: true, InputEnd: true, SelectAll: true, Newline: true, Submit: true, Program: true, Prefix: true,
	Copy: true, Cut: true, Paste: true, Undo: true, Redo: true,
	ScrollUp: true, ScrollDown: true, ScrollLeft: true, ScrollRight: true,
	ScrollPageUp: true, ScrollPageDown: true, ScrollHalfPageUp: true, ScrollHalfPageDown: true,
	ScrollStart: true, ScrollEnd: true,
}

// Multiline reports whether only a multi-line field (a textarea, an
// editing host) has the action. From an input, a key bound to one reaches
// the program.
func (a Action) Multiline() bool {
	switch a {
	case LinePrevious, LineNext, PageUp, PageDown, Newline:
		return true
	}
	return false
}

// Moves reports whether the action is a move, which selects when done
// with Shift (SPEC §10.2, Shift selects; Keymap.Selects).
func (a Action) Moves() bool {
	switch a {
	case CharBackward, CharForward, WordBackward, WordForward, LineStart, LineEnd,
		LinePrevious, LineNext, PageUp, PageDown, InputStart, InputEnd:
		return true
	}
	return false
}

// Scrolls reports whether the action is a scroll action, which only an
// element that is not a text field has (SPEC §10.2).
func (a Action) Scrolls() bool {
	switch a {
	case ScrollUp, ScrollDown, ScrollLeft, ScrollRight, ScrollPageUp, ScrollPageDown,
		ScrollHalfPageUp, ScrollHalfPageDown, ScrollStart, ScrollEnd:
		return true
	}
	return false
}

// TerminalKeys is the SDK's keymap (SDK.md §3.10), as a data-keys value:
// the keys of Bubble Tea's text input and text area (bubbles), but for
// mod+a, which selects all, as in a GUI field and SPEC §10.2's default
// keymap: ctrl+a on Linux, while on macOS ctrl+a stays the line's start. A
// program puts it in the data-keys of an element that holds its fields, and
// edits its fields in cells with Resolve(platform, multiline, TerminalKeys),
// so that they edit the same on a surface and in cells. It leaves enter to
// SPEC §10.2's default.
const TerminalKeys = "arrow_left=char-backward ctrl+b=char-backward arrow_right=char-forward ctrl+f=char-forward " +
	"alt+arrow_left=word-backward ctrl+arrow_left=word-backward alt+b=word-backward " +
	"alt+arrow_right=word-forward ctrl+arrow_right=word-forward alt+f=word-forward " +
	"home=line-start end=line-end ctrl+e=line-end " +
	"backspace=delete-char-backward ctrl+h=delete-char-backward " +
	"delete=delete-char-forward ctrl+d=delete-char-forward " +
	"alt+backspace=delete-word-backward ctrl+w=delete-word-backward ctrl+backspace=delete-word-backward " +
	"alt+delete=delete-word-forward alt+d=delete-word-forward ctrl+delete=delete-word-forward " +
	"ctrl+u=delete-to-line-start ctrl+k=delete-to-line-end " +
	"arrow_up=line-previous ctrl+p=line-previous arrow_down=line-next ctrl+n=line-next " +
	"page_up=page-up page_down=page-down " +
	"alt+<=input-start ctrl+home=input-start alt+>=input-end ctrl+end=input-end " +
	"mod+a=select-all " +
	"ctrl+m=newline"

// join writes a key from its modifiers and its value, as myelin key names
// do: the modifiers in the order super, ctrl, alt, shift.
func join(m myelinkey.Mods, value string) string {
	var b strings.Builder
	for _, mod := range [...]struct {
		bit  myelinkey.Mods
		name string
	}{{myelinkey.Super, "super+"}, {myelinkey.Ctrl, "ctrl+"}, {myelinkey.Alt, "alt+"}, {myelinkey.Shift, "shift+"}} {
		if m&mod.bit != 0 {
			b.WriteString(mod.name)
		}
	}
	b.WriteString(value)
	return b.String()
}

// ParseKey reads a key's name as SPEC §10.4 has hosts read one, with myelin
// key names' default reading ("Shift+Ctrl+A", "PageDown", "esc"), and
// returns its canonical name ("ctrl+shift+a", "page_down", "escape"). ok is
// false for a name the reading refuses (empty, "meta+a", "ctrl+", a
// modifier given twice), and for one with "mod", which only a Platform
// reads (ReadKey).
func ParseKey(name string) (canonical string, ok bool) {
	k, err := myelinkey.Parse(name)
	return string(k), err == nil
}

// KeyText is the text a key types in a field (SPEC §10.2): a character's, a
// letter with shift as its capital, a space for space. ok is false for any
// other key, a key with ctrl, alt or super, and a name that does not parse.
func KeyText(key string) (text string, ok bool) {
	k, err := myelinkey.Parse(key)
	if err != nil {
		return "", false
	}
	mods, value, kind := myelinkey.Split(k)
	switch {
	case mods&(myelinkey.Super|myelinkey.Ctrl|myelinkey.Alt) != 0:
		return "", false
	case value == "space":
		return " ", true
	case kind != myelinkey.Character:
		return "", false
	}
	if r, n := utf8.DecodeRuneInString(value); mods&myelinkey.Shift != 0 && n == len(value) {
		if up := unicode.ToUpper(r); up != r {
			return string(up), true
		}
	}
	return value, true
}

// DecodeKeys reads input from the terminal, the bytes a program reads, as
// SPEC §10.4 names the keys in it: one entry for each key, its name, or ""
// for input that is no key it names (a mouse report, a sequence it does not
// know, a key's release). It reads the input whole: an ESC at its end is
// escape.
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
			out = append(out, "escape")
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

// withAlt is a key's name with alt.
func withAlt(name string) string {
	k, err := myelinkey.Parse(name)
	if err != nil {
		return ""
	}
	mods, value, _ := myelinkey.Split(k)
	return join(mods|myelinkey.Alt, value)
}

// charKey is the key of a character with modifiers, "" for a character no
// key is: a capital is shift with its letter (SPEC §10.4).
func charKey(c string, m myelinkey.Mods) string {
	if c == " " {
		return join(m, "space")
	}
	value := strings.Map(unicode.ToLower, c)
	if r, n := utf8.DecodeRuneInString(c); n == len(c) && value != c && unicode.ToUpper(unicode.ToLower(r)) == r {
		m |= myelinkey.Shift
	}
	k, err := myelinkey.Parse(join(m, value))
	if err != nil {
		return ""
	}
	return string(k)
}

// c0Keys are the C0 controls and DEL that are not ctrl with a letter.
var c0Keys = map[byte]string{
	0x00: "ctrl+space", 0x08: "ctrl+h", 0x09: "tab", 0x0d: "enter", 0x1b: "escape", 0x7f: "backspace",
	0x1c: `ctrl+\`, 0x1d: "ctrl+]", 0x1e: "ctrl+^", 0x1f: "ctrl+_",
}

// oneKey is the key of the control or the character input starts with, and
// its length in bytes.
func oneKey(input []byte) (string, int) {
	b := input[0]
	if b < 0x20 || b == 0x7f {
		if k, ok := c0Keys[b]; ok {
			return k, 1
		}
		return "ctrl+" + string(rune(b+0x60)), 1
	}
	r, n := utf8.DecodeRune(input)
	if r == utf8.RuneError {
		return "", 1
	}
	if r == ' ' {
		return "space", 1
	}
	n = max(clusterLen(string(input[:min(len(input), 64)])), n)
	return charKey(string(input[:n]), 0), n
}

// csiFinals are the keys CSI and SS3 name by their final byte.
var csiFinals = map[byte]string{'A': "arrow_up", 'B': "arrow_down", 'C': "arrow_right", 'D': "arrow_left", 'H': "home", 'F': "end"}

// tildeKeys are the keys CSI n ~ names.
var tildeKeys = map[int]string{1: "home", 7: "home", 4: "end", 8: "end", 2: "insert", 3: "delete", 5: "page_up", 6: "page_down"}

// codeKeys are the codes of the kitty protocol and modifyOtherKeys that are
// not characters.
var codeKeys = map[int]string{9: "tab", 13: "enter", 27: "escape", 8: "backspace", 127: "backspace", 32: "space"}

// kittyKeys are the kitty protocol's codes from 57344 that name keys: those
// of its keypad, as a legacy terminal sends the keys.
var kittyKeys = map[int]string{
	57399: "0", 57400: "1", 57401: "2", 57402: "3", 57403: "4", 57404: "5", 57405: "6", 57406: "7", 57407: "8", 57408: "9",
	57409: ".", 57410: "/", 57411: "*", 57412: "-", 57413: "+", 57414: "enter", 57415: "=",
	57417: "arrow_left", 57418: "arrow_right", 57419: "arrow_up", 57420: "arrow_down", 57421: "page_up", 57422: "page_down",
	57423: "home", 57424: "end", 57425: "insert", 57426: "delete",
}

// ss3Key is the key SS3 with a final byte names.
func ss3Key(final byte) string {
	return csiFinals[final]
}

// modsOf reads a CSI parameter m[:e]: its modifiers, and whether the event
// is a release. ok is false when it does not parse.
func modsOf(field string) (m myelinkey.Mods, release, ok bool) {
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
		m |= myelinkey.Shift
	}
	if bits&2 != 0 {
		m |= myelinkey.Alt
	}
	if bits&4 != 0 {
		m |= myelinkey.Ctrl
	}
	if bits&(8|32) != 0 {
		m |= myelinkey.Super
	}
	return m, e == 3, true
}

// isLetter reports whether a character has case.
func isLetter(r rune) bool {
	return unicode.ToUpper(r) != r || unicode.ToLower(r) != r && unicode.ToUpper(unicode.ToLower(r)) == r
}

// codeKey is the key a code of the kitty protocol or of modifyOtherKeys
// names, with its modifiers, its shifted key (0 when not given) and its
// text ("" when not given).
func codeKey(code int, m myelinkey.Mods, shifted int, text string) string {
	if v, ok := codeKeys[code]; ok {
		return join(m, v)
	}
	if code >= 57344 {
		if v, ok := kittyKeys[code]; ok {
			return join(m, v)
		}
		return ""
	}
	if code < 0x20 || code == 0x7f || code > unicode.MaxRune {
		return ""
	}
	r := rune(code)
	if isLetter(r) {
		return charKey(string(unicode.ToLower(r)), m)
	}
	if m&myelinkey.Shift != 0 {
		switch {
		case shifted > 0 && shifted <= unicode.MaxRune:
			return charKey(string(rune(shifted)), m&^myelinkey.Shift)
		case text != "":
			return charKey(text, m&^myelinkey.Shift)
		}
	}
	return charKey(string(r), m)
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
		return join(m, v)
	case final == 'Z':
		return "shift+tab"
	}
	v, known := csiFinals[final]
	m, release, ok := modsOf(field(1))
	if !known || !ok || release {
		return ""
	}
	return join(m, v)
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

// Platform is how a host reads bindings (SDK.md §3.10): the modifier mod
// stands for, and its edit shortcuts. The zero Platform is ctrl with the
// default edit shortcuts.
type Platform struct {
	// Mod is "ctrl" or "super" (SPEC §10.4); "" is ctrl.
	Mod string
	// Edit is the host's edit shortcuts, from each action to its keys
	// (SPEC §10.2); nil is DefaultEdit(Mod).
	Edit map[string][]string
}

// PlatformOf is the Platform of a host's capabilities: its Mod and Edit,
// the zero Platform's where it reports none.
func PlatformOf(c Caps) Platform { return Platform{Mod: c.Mod, Edit: c.Edit} }

// The edit actions, in SPEC §10.2's order.
var editActions = [...]Action{SelectAll, Copy, Cut, Paste, Undo, Redo}

// DefaultEdit is the default edit shortcuts for a mod (SPEC §10.2), "ctrl"
// or "super", each key read: ctrl+shift+z and ctrl+y for redo where it is
// ctrl.
func DefaultEdit(mod string) map[string][]string {
	p := Platform{Mod: mod}
	e := map[string][]string{
		string(SelectAll): {"mod+a"}, string(Copy): {"mod+c"}, string(Cut): {"mod+x"},
		string(Paste): {"mod+v"}, string(Undo): {"mod+z"}, string(Redo): {"mod+shift+z"},
	}
	if p.modOf() == myelinkey.Ctrl {
		e[string(Redo)] = append(e[string(Redo)], "ctrl+y")
	}
	for a, keys := range e {
		for i, k := range keys {
			keys[i], _ = p.ReadKey(k)
		}
		e[a] = keys
	}
	return e
}

// modOf is the modifier mod stands for on a Platform.
func (p Platform) modOf() myelinkey.Mods {
	if p.Mod == "super" {
		return myelinkey.Super
	}
	return myelinkey.Ctrl
}

// ReadKey reads a key's name as the Platform reads it (SPEC §10.4): as
// ParseKey does, with mod standing for the Platform's, so that where Mod is
// ctrl "mod+a" and "ctrl+a" read alike, and where it is super they are two
// keys. A menu's items (MenuItem.Key, MenuLevelOf) compare with the keys
// typed this way. ok is false for a name that does not parse, "mod+ctrl+a"
// among them.
func (p Platform) ReadKey(name string) (canonical string, ok bool) {
	k, err := myelinkey.ParseWith(name, myelinkey.Options{Mod: p.modOf()})
	return string(k), err == nil
}

// written is a key's name with mod where name was written with it
// ("mod+shift+z"), and k, the key it reads as, otherwise.
func (p Platform) written(name string, k myelinkey.Key) string {
	mods, value, _ := myelinkey.Split(k)
	if !strings.Contains("+"+strings.ToLower(name)+"+", "+mod+") || mods&p.modOf() == 0 {
		return string(k)
	}
	return "mod+" + join(mods&^p.modOf(), value)
}

// Keymap binds keys to actions (SPEC §10.2), read on a Platform.
// ParseKeymap reads one from a data-keys value; Resolve makes the one a
// field uses, whose Lookup says what the field does with a key, and Selects
// whether it selects as it moves. Binds says whether it binds a key,
// Program whether any focused element's keymap gives a key to the program,
// and Scroll which scroll action it binds a key to.
type Keymap struct {
	keys      []string          // canonical, mod read, in the order first bound
	actions   map[string]Action // by key
	written   map[string]string // by key: its last binding's key, mod kept
	multiline bool
	platform  Platform
}

// ParseKeymap reads a data-keys value as SPEC §10.2 has hosts read it:
// bindings separated by ASCII white space, each key=action, split at its
// last '='. It drops the bindings a host ignores: a key that does not
// parse, an action it does not know, and tab, shift+tab and escape. Mod
// reads as the Platform's: on ctrl, mod+a and ctrl+a are one key.
func ParseKeymap(p Platform, value string) *Keymap {
	m := &Keymap{platform: p}
	bindings(value, func(k string, a Action) { m.Bind(k, a) })
	return m
}

// bindings calls f with each binding of a data-keys value, in order, as
// written: the key not yet parsed, and the action not yet checked.
func bindings(value string, f func(key string, a Action)) {
	for _, b := range strings.FieldsFunc(value, func(r rune) bool { return strings.ContainsRune(" \t\n\f\r", r) }) {
		if i := strings.LastIndexByte(b, '='); i >= 0 {
			f(b[:i], Action(b[i+1:]))
		}
	}
}

// Bind binds a key to an action, and reports whether it did: a host
// ignores the bindings ParseKeymap drops, and so does Bind.
func (m *Keymap) Bind(name string, a Action) bool {
	k, ok := m.platform.ReadKey(name)
	if !ok || !actions[a] || k == "tab" || k == "shift+tab" || k == "escape" {
		return false
	}
	if m.actions == nil {
		m.actions, m.written = map[string]Action{}, map[string]string{}
	}
	if _, bound := m.actions[k]; !bound {
		m.keys = append(m.keys, k)
	}
	m.actions[k] = a
	m.written[k] = m.platform.written(name, myelinkey.Key(k))
	return true
}

// Format writes the keymap as a data-keys value: each key once, where it
// was first bound, as its last binding wrote it (mod kept), with its last
// action.
func (m *Keymap) Format() string {
	var b strings.Builder
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(m.written[k])
		b.WriteByte('=')
		b.WriteString(string(m.actions[k]))
	}
	return b.String()
}

// The default keymap (SPEC §10.2): every host's keys, then those of its
// mod. The edit shortcuts and enter follow.
var (
	defaultKeys = [...]struct {
		key    string
		action Action
	}{
		{"arrow_left", CharBackward}, {"arrow_right", CharForward},
		{"alt+arrow_left", WordBackward}, {"alt+arrow_right", WordForward},
		{"home", LineStart}, {"end", LineEnd},
		{"backspace", DeleteCharBackward}, {"delete", DeleteCharForward},
		{"alt+backspace", DeleteWordBackward}, {"alt+delete", DeleteWordForward},
		{"arrow_up", LinePrevious}, {"arrow_down", LineNext}, {"page_up", PageUp}, {"page_down", PageDown},
	}
	ctrlKeys = [...]struct {
		key    string
		action Action
	}{
		{"ctrl+arrow_left", WordBackward}, {"ctrl+arrow_right", WordForward},
		{"ctrl+home", InputStart}, {"ctrl+end", InputEnd},
		{"ctrl+backspace", DeleteWordBackward}, {"ctrl+delete", DeleteWordForward},
	}
	superKeys = [...]struct {
		key    string
		action Action
	}{
		{"super+arrow_left", LineStart}, {"super+arrow_right", LineEnd},
		{"super+arrow_up", InputStart}, {"super+arrow_down", InputEnd},
		{"super+backspace", DeleteToLineStart},
		{"ctrl+a", LineStart}, {"ctrl+e", LineEnd},
		{"ctrl+b", CharBackward}, {"ctrl+f", CharForward},
		{"ctrl+p", LinePrevious}, {"ctrl+n", LineNext},
		{"ctrl+h", DeleteCharBackward}, {"ctrl+d", DeleteCharForward},
		{"ctrl+k", DeleteToLineEnd},
	}
)

// Resolve makes a field's keymap (SPEC §10.2): the default keymap for the
// Platform's Mod, with its edit shortcuts, for an input or for a
// multi-line field, then each data-keys value in turn, the root's first,
// each overriding the bindings before it key by key. It leaves out each
// binding to a scroll action where it stands, in its own value too: one
// neither acts nor overrides an earlier binding of its key.
func Resolve(p Platform, multiline bool, values ...string) *Keymap {
	m := &Keymap{multiline: multiline, platform: p}
	for _, b := range defaultKeys {
		m.Bind(b.key, b.action)
	}
	enter := Submit
	if multiline {
		enter = Newline
	}
	m.Bind("enter", enter)
	if p.modOf() == myelinkey.Super {
		for _, b := range superKeys {
			m.Bind(b.key, b.action)
		}
	} else {
		for _, b := range ctrlKeys {
			m.Bind(b.key, b.action)
		}
	}
	edit := p.Edit
	if edit == nil {
		edit = DefaultEdit(p.Mod)
	}
	for _, a := range editActions {
		for _, k := range edit[string(a)] {
			m.Bind(k, a)
		}
	}
	for _, v := range values {
		bindings(v, func(k string, a Action) {
			if !a.Scrolls() {
				m.Bind(k, a)
			}
		})
	}
	return m
}

// bound is the action the keymap binds a key to, and the key, canonical:
// a key with shift that is not a character and that no binding names is
// looked up without shift (SPEC §10.2). ok is false when the name does not
// parse, and mod is not read: no key pressed is named with it (SPEC §10.4).
func (m *Keymap) bound(name string) (k myelinkey.Key, a Action, bound, ok bool) {
	k, err := myelinkey.Parse(name)
	if err != nil {
		return "", "", false, false
	}
	a, bound = m.actions[string(k)]
	if mods, value, kind := myelinkey.Split(k); !bound && mods&myelinkey.Shift != 0 && kind != myelinkey.Character {
		a, bound = m.actions[join(mods&^myelinkey.Shift, value)]
	}
	return k, a, bound, true
}

// Binds reports whether the keymap binds a key: its own binding, or, for a
// key with shift that is not a character and that no binding names, the
// binding without shift (SPEC §10.2). A host that has two names for a press
// (SPEC §10.4) asks of the physical one first, and of the other only where
// the keymap does not bind it.
func (m *Keymap) Binds(name string) bool {
	_, _, bound, _ := m.bound(name)
	return bound
}

// Program reports whether the keymap gives a key to the program: binds it
// to program or prefix, or, for a key with shift that is not a character
// and that no binding names, binds it without shift (SPEC §10.2, keys for
// the program).
//
// Every focused element has a keymap, and outside a text field program and
// the scroll actions are the only actions it gives. A host reads the
// element's as a field's, from the data-keys values of the elements from
// the root down to it, but with its scroll actions and no default keymap:
// ParseKeymap(platform, strings.Join(values, " ")). It gives a key the keymap binds
// to program or prefix to the program before the element uses it, and
// before the surface scrolls with it; a nearer binding of the key to
// another action takes it back. In a field, Lookup says as much.
func (m *Keymap) Program(name string) bool {
	_, a, bound, _ := m.bound(name)
	return bound && (a == Program || a == Prefix)
}

// Prefix reports whether the keymap binds a key to prefix, or, for a key
// with shift that is not a character and that no binding names, binds it
// without shift. A host asks it of a key Program gives the program, and
// from then on gives the program every key, tab, shift+tab and escape too,
// until the program sends a=focus or a=blur, the surface loses the
// keyboard, or the user focuses an element of it with a press (SPEC §10.5).
func (m *Keymap) Prefix(name string) bool {
	_, a, bound, _ := m.bound(name)
	return bound && a == Prefix
}

// Scroll is the scroll action the keymap binds a key to, or "" for none:
// the key's own binding, or, for a key with shift that is not a character
// and that no binding names, the binding without shift (SPEC §10.2,
// scrolling keys).
//
// A host asks it of the keymap it asks Program of, for a key the focused
// element does not use (space on a button, a character on a select are the
// element's). A key bound to one scrolls the nearest element that scrolls,
// from the focused one outward, never the terminal; along an axis the
// document does not scroll (Scroll, SPEC §5.1), it goes on as if the
// keymap did not bind it.
func (m *Keymap) Scroll(name string) Action {
	if _, a, bound, _ := m.bound(name); bound && a.Scrolls() {
		return a
	}
	return ""
}

// Lookup says what a field with this keymap does with a key (SPEC §10.2):
// an action; Insert for a key it types (KeyText); or "" when the key is not
// the field's, and reaches the program (or, for tab, moves focus). A key
// with shift that is not a character and that no binding names is looked up
// without shift.
func (m *Keymap) Lookup(name string) Action {
	k, a, bound, ok := m.bound(name)
	if !ok {
		return ""
	}
	if k == "tab" || k == "shift+tab" || k == "escape" {
		return ""
	}
	switch {
	case bound && (a == Program || a == Prefix || a.Multiline() && !m.multiline):
		return ""
	case bound:
		return a
	}
	if _, types := KeyText(string(k)); types {
		return Insert
	}
	return ""
}

// Selects reports whether a field with this keymap selects with a key
// (SPEC §10.2, Shift selects): Lookup returns a move for it, and its name
// has shift, bound so or looked up without it ("shift+arrow_left",
// "ctrl+shift+end", not "alt+<"). The field then keeps its anchor and moves
// the caret (hottyedit.Field.Extend).
func (m *Keymap) Selects(name string) bool {
	k, err := myelinkey.Parse(name)
	if err != nil {
		return false
	}
	mods, _, _ := myelinkey.Split(k)
	return mods&myelinkey.Shift != 0 && m.Lookup(name).Moves()
}
