package hottytest

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/tinylib/msgp/msgp"
)

// Body is a body written as JSON, as the msgpack a host sends (SPEC §3.3):
// a number with a fraction or an exponent is a float, any other an int, as
// the conformance vectors write them. It is for a test that makes up what
// a host says, such as Body(`{"code":"ENOENT"}`) in an error reply. It
// panics when js is not JSON.
func Body(js string) []byte {
	d := json.NewDecoder(strings.NewReader(js))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		panic("hottytest.Body: " + err.Error())
	}
	var buf bytes.Buffer
	w := msgp.NewWriter(&buf)
	if err := w.WriteIntf(typed(v)); err != nil {
		panic("hottytest.Body: " + err.Error())
	}
	if err := w.Flush(); err != nil {
		panic("hottytest.Body: " + err.Error())
	}
	return buf.Bytes()
}

// typed is v with its JSON numbers as int64 and float64.
func typed(v any) any {
	switch v := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			f, _ := v.Float64()
			return f
		}
		n, _ := v.Int64()
		return n
	case []any:
		for i := range v {
			v[i] = typed(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = typed(v[k])
		}
	}
	return v
}
