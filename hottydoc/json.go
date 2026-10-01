package hottydoc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"
)

// Value is a JSON value, with its object's keys in the order they came.
type Value struct {
	// Kind is '{' (an object), '[' (an array), 's' (a string), 'n' (a
	// number) or 'l' (true, false or null).
	Kind byte
	// Text is a leaf as JSON writes it: a string quoted, a number as it
	// was in the file.
	Text string
	Keys []string // an object's, quoted
	Kids []*Value // an object's values, or an array's items
}

// ParseJSON reads JSON: one value, or several in a row (JSON Lines). Its
// error says where the file stops being JSON.
func ParseJSON(src []byte) ([]*Value, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	var out []*Value
	for {
		v, err := parseValue(dec)
		if err == io.EOF && len(out) > 0 {
			return out, nil
		}
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return out, jsonError(src, dec, err)
		}
		out = append(out, v)
	}
}

func parseValue(dec *json.Decoder) (*Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			v := &Value{Kind: '{'}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("a key must be a string")
				}
				kid, err := parseValue(dec)
				if err != nil {
					return nil, unexpectedEOF(err)
				}
				v.Keys = append(v.Keys, quote(key))
				v.Kids = append(v.Kids, kid)
			}
			if _, err := dec.Token(); err != nil {
				return nil, unexpectedEOF(err)
			}
			return v, nil
		case '[':
			v := &Value{Kind: '['}
			for dec.More() {
				kid, err := parseValue(dec)
				if err != nil {
					return nil, unexpectedEOF(err)
				}
				v.Kids = append(v.Kids, kid)
			}
			if _, err := dec.Token(); err != nil {
				return nil, unexpectedEOF(err)
			}
			return v, nil
		}
		return nil, fmt.Errorf("unexpected %q", rune(t))
	case string:
		return &Value{Kind: 's', Text: quote(t)}, nil
	case json.Number:
		return &Value{Kind: 'n', Text: t.String()}, nil
	case bool:
		return &Value{Kind: 'l', Text: fmt.Sprint(t)}, nil
	case nil:
		return &Value{Kind: 'l', Text: "null"}, nil
	}
	return nil, fmt.Errorf("unexpected %v", tok)
}

func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// quote is a string as JSON writes it, without escaping HTML.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// jsonError says where the JSON went wrong, as line and column.
func jsonError(src []byte, dec *json.Decoder, err error) error {
	off := dec.InputOffset()
	var se *json.SyntaxError
	if errors.As(err, &se) {
		off = se.Offset
	}
	off = min(max(off, 0), int64(len(src)))
	line := bytes.Count(src[:off], []byte("\n")) + 1
	col := int(off) - bytes.LastIndexByte(src[:off], '\n')
	msg := err.Error()
	if err == io.ErrUnexpectedEOF {
		msg = "unexpected end of input"
	}
	return fmt.Errorf("invalid JSON at line %d, column %d: %s", line, col, msg)
}

// JSON is JSON as a document: pretty-printed, objects and arrays as
// <details open> that fold where they are, keys, strings, numbers and
// literals coloured by role. A top-level object or array is split between
// its members, so a long one takes several surfaces.
func JSON(src []byte, o Options) (*Doc, error) {
	vals, err := ParseJSON(src)
	if err != nil {
		return nil, err
	}
	cols := max(10, o.cols()-4)
	d := &Doc{CSS: "main.doc { padding-bottom: 0; } main.doc > .json { margin-top: 0; }"}
	part := func(s string, rows int) {
		d.Blocks = append(d.Blocks, Block{HTML: `<div class="json">` + s + `</div>`, Rows: rows})
	}
	for _, v := range vals {
		if (v.Kind != '{' && v.Kind != '[') || len(v.Kids) == 0 {
			var b strings.Builder
			rows := v.html(&b, "", false, cols)
			part(b.String(), rows)
			continue
		}
		open, end := string(v.Kind), "}"
		if v.Kind == '[' {
			end = "]"
		}
		part(`<div><span class="p">`+open+`</span></div>`, 1)
		for i, kid := range v.Kids {
			var b strings.Builder
			key := ""
			if v.Kind == '{' {
				key = v.Keys[i]
			}
			b.WriteString(`<div class="in">`)
			rows := kid.html(&b, key, i < len(v.Kids)-1, cols-2)
			b.WriteString(`</div>`)
			part(b.String(), rows)
		}
		part(`<div><span class="p">`+end+`</span></div>`, 1)
	}
	return d, nil
}

// html writes the value, after its key if it has one, and returns the rows
// it takes.
func (v *Value) html(b *strings.Builder, key string, comma bool, cols int) int {
	lead, width := "", 0
	if key != "" {
		lead = `<span class="k">` + html.EscapeString(key) + `</span><span class="p">: </span>`
		width = len([]rune(key)) + 2
	}
	tail := ""
	if comma {
		tail = `<span class="p">,</span>`
	}
	switch v.Kind {
	case '{', '[':
		open, end := string(v.Kind), "}"
		if v.Kind == '[' {
			end = "]"
		}
		if len(v.Kids) == 0 {
			b.WriteString("<div>" + lead + `<span class="p">` + open + end + "</span>" + tail + "</div>")
			return 1
		}
		b.WriteString(`<details open><summary>` + lead + `<span class="p">` + open + `</span>`)
		fmt.Fprintf(b, `<span class="shut p">…%s</span><span class="shut p"> %d</span>`, end, len(v.Kids))
		if comma {
			b.WriteString(`<span class="shut p">,</span>`)
		}
		b.WriteString(`</summary><div class="in">`)
		rows := 2
		for i, kid := range v.Kids {
			k := ""
			if v.Kind == '{' {
				k = v.Keys[i]
			}
			rows += kid.html(b, k, i < len(v.Kids)-1, max(10, cols-2))
		}
		b.WriteString(`</div><div><span class="p">` + end + `</span>` + tail + `</div></details>`)
		return rows
	}
	class := map[byte]string{'s': "s", 'n': "n", 'l': "l"}[v.Kind]
	b.WriteString("<div>" + lead + `<span class="` + class + `">` + html.EscapeString(Printable(v.Text)) + "</span>" + tail + "</div>")
	width += len([]rune(v.Text)) + 1
	return max(1, (width+cols-1)/cols)
}
