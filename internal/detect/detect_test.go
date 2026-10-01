package detect

import (
	"testing"

	"github.com/neuroplastio/hotty-go"
)

func reply(ctl hotty.Control, payload string) hotty.Reply {
	var d hotty.Decoder
	m, _ := d.Feed(hotty.Encode(ctl, []byte(payload)))
	r, _ := m.Reply()
	return r
}

func TestAnswer(t *testing.T) {
	caps := `{"v":"0.1","host":"h"}`
	if c, ok := Answer(reply(hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "q"}, {K: "n", V: "1"}}, caps), N); !ok || c.Host != "h" {
		t.Errorf("the answer: %+v %v", c, ok)
	}
	for name, r := range map[string]hotty.Reply{
		"another number": reply(hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "q"}, {K: "n", V: "2"}}, caps),
		"no number":      reply(hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "q"}}, caps),
		"an error":       reply(hotty.Control{{K: "a", V: "err"}, {K: "re", V: "q"}, {K: "n", V: "1"}}, `{"code":"EINVAL"}`),
		"another action": reply(hotty.Control{{K: "a", V: "ok"}, {K: "re", V: "doc"}, {K: "n", V: "1"}}, ""),
	} {
		if _, ok := Answer(r, N); ok {
			t.Errorf("%s answers the query", name)
		}
	}
}
