package hottytest

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestFocusableAnchor(t *testing.T) {
	for _, c := range []struct {
		markup string
		want   bool
	}{
		{`<a href="#x">x</a>`, true},
		{`<a>x</a>`, false},
		{`<a tabindex="0">x</a>`, true},
		{`<a tabindex="-1">x</a>`, false},
		{`<a href="https://example.org" target="_blank" tabindex="0">x</a>`, false},
	} {
		doc, err := html.Parse(strings.NewReader(c.markup))
		if err != nil {
			t.Fatal(err)
		}
		var a *html.Node
		var find func(*html.Node)
		find = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "a" {
				a = n
			}
			for k := n.FirstChild; k != nil && a == nil; k = k.NextSibling {
				find(k)
			}
		}
		find(doc)
		if got := focusable(a); got != c.want {
			t.Errorf("%s: focusable = %v, want %v", c.markup, got, c.want)
		}
	}
}
