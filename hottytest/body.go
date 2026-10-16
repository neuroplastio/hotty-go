package hottytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tinylib/msgp/msgp"
)

// Body is a body written as JSON, as the msgpack a host sends (SPEC §3.3):
// a number with a fraction or an exponent is a float, any other an int, as
// the conformance vectors write them, and a map keeps its keys in the order
// written. It is for a test that makes up what a host says, such as
// Body(`{"code":"ENOENT"}`) in an error reply, or what a program says, such
// as a delta's ops. It panics when js is not JSON.
func Body(js string) []byte {
	d := json.NewDecoder(strings.NewReader(js))
	d.UseNumber()
	v, err := value(d)
	if err == nil {
		_, err = d.Token()
		if err == io.EOF {
			err = nil
		} else if err == nil {
			err = fmt.Errorf("more than one value")
		}
	}
	var buf bytes.Buffer
	w := msgp.NewWriter(&buf)
	if err == nil {
		err = write(w, v)
	}
	if err == nil {
		err = w.Flush()
	}
	if err != nil {
		panic("hottytest.Body: " + err.Error())
	}
	return buf.Bytes()
}

// pair is a JSON object's key and value; an object is a list of them, so
// that it keeps its order.
type pair struct {
	key string
	val any
}

type object []pair

// value reads one JSON value: an object as an object, an array as a
// []any, a number as a json.Number.
func value(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '[':
		out := []any{}
		for d.More() {
			v, err := value(d)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		_, err := d.Token()
		return out, err
	case '{':
		out := object{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := value(d)
			if err != nil {
				return nil, err
			}
			out = append(out, pair{k.(string), v})
		}
		_, err := d.Token()
		return out, err
	}
	return nil, fmt.Errorf("unexpected %v", tok)
}

// write writes a value as msgpack: ints as ints, floats as floats.
func write(w *msgp.Writer, v any) error {
	switch v := v.(type) {
	case nil:
		return w.WriteNil()
	case bool:
		return w.WriteBool(v)
	case string:
		return w.WriteString(v)
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			f, err := v.Float64()
			if err != nil {
				return err
			}
			return w.WriteFloat64(f)
		}
		n, err := v.Int64()
		if err != nil {
			return err
		}
		return w.WriteInt64(n)
	case []any:
		if err := w.WriteArrayHeader(uint32(len(v))); err != nil {
			return err
		}
		for _, x := range v {
			if err := write(w, x); err != nil {
				return err
			}
		}
	case object:
		if err := w.WriteMapHeader(uint32(len(v))); err != nil {
			return err
		}
		for _, p := range v {
			if err := w.WriteString(p.key); err != nil {
				return err
			}
			if err := write(w, p.val); err != nil {
				return err
			}
		}
	}
	return nil
}
