package hotty

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"os"
	"regexp"
	"strings"
	"testing"
)

// What a peer sends is bounded before it is held: a message by the
// Decoder's Max, a body's containers by msgp's limits.

func deflate(n int) string {
	var b bytes.Buffer
	w, _ := zlib.NewWriterLevel(&b, zlib.BestCompression)
	w.Write(make([]byte, n))
	w.Close()
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestDecoderMaxInflate(t *testing.T) {
	bomb := deflate(4 << 20) // about 4 KB of base64
	for _, c := range []struct {
		max  int
		want Result
	}{{1 << 20, Invalid}, {4 << 20, Complete}, {4<<20 - 1, Invalid}} {
		d := Decoder{Max: c.max}
		if _, r := d.Feed(prefix + "a=res:id=x:type=text/plain:o=z;" + bomb + st); r != c.want {
			t.Errorf("Max %d: %v, want %v", c.max, r, c.want)
		}
	}
	var d Decoder // DefaultMax
	if _, r := d.Feed(prefix + "a=res:id=x:type=text/plain:o=z;" + deflate(DefaultMax+1) + st); r != Invalid || d.Invalid != 1 {
		t.Errorf("past DefaultMax: %v, %d invalid", r, d.Invalid)
	}
}

func TestDecoderMaxPlain(t *testing.T) {
	d := Decoder{Max: 4}
	if _, r := d.Feed(prefix + "a=res:id=x:type=text/plain;" + base64.StdEncoding.EncodeToString([]byte("abcd")) + st); r != Complete {
		t.Errorf("4 bytes: %v", r)
	}
	if _, r := d.Feed(prefix + "a=res:id=x:type=text/plain;" + base64.StdEncoding.EncodeToString([]byte("abcde")) + st); r != Invalid {
		t.Errorf("5 bytes: %v", r)
	}
}

func TestDecoderMaxChunks(t *testing.T) {
	d := Decoder{Max: 1024}
	chunk := strings.Repeat("A", 400) // 300 bytes each
	if _, r := d.Feed(prefix + "a=res:id=x:type=text/plain:m=1;" + chunk + st); r != Partial {
		t.Fatalf("first chunk: %v", r)
	}
	for i := 0; i < 10; i++ {
		if _, r := d.Feed(prefix + "m=1;" + chunk + st); r != Partial {
			t.Fatalf("chunk %d: %v", i, r)
		}
	}
	if d.body.Len() != 0 || d.Invalid != 1 {
		t.Errorf("past Max: %d bytes held, %d invalid", d.body.Len(), d.Invalid)
	}
	if _, r := d.Feed(prefix + "m=0;" + chunk + st); r != Invalid || d.Invalid != 1 {
		t.Errorf("last chunk: %v, %d invalid", r, d.Invalid)
	}
	// A message after it decodes as usual.
	if m, r := d.Feed(prefix + "a=hide:s=x" + st); r != Complete || Get(m.Control, "a") != "hide" {
		t.Errorf("after: %v", r)
	}
	// A dropped message that another interrupts is counted once.
	d.Feed(prefix + "a=res:id=x:type=text/plain:m=1;" + strings.Repeat("A", 2000) + st)
	if _, r := d.Feed(prefix + "a=hide:s=x" + st); r != Complete || d.Invalid != 2 {
		t.Errorf("interrupted: %v, %d invalid", r, d.Invalid)
	}
	// A malformed one too.
	d.Feed(prefix + "a=res:id=x:type=text/plain:m=1;" + strings.Repeat("A", 2000) + st)
	if _, r := d.Feed(prefix + "a b" + st); r != Invalid || d.Invalid != 4 {
		t.Errorf("malformed: %v, %d invalid", r, d.Invalid)
	}
}

// TestWireBounded checks the codec msgp generates: every make for a peer's
// array or map has msgp's limit in front of it (//msgp:limit), and a body
// is decoded only through decodeBody, which walks it whole first, so no
// str, bin or count claims more than the bytes in hand (plexos F130, F131).
func TestWireBounded(t *testing.T) {
	src, err := os.ReadFile("wire.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^//msgp:limit arrays:\d+ maps:\d+$`).Match(src) {
		t.Error("wire.go has no //msgp:limit")
	}
	gen, err := os.ReadFile("wire_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(gen), "\n")
	limit := regexp.MustCompile(`> z[0-9a-f]+limit(Arrays|Maps)\b`)
	for i, l := range lines {
		if !strings.Contains(l, "make(") {
			continue
		}
		ok := false
		for j := max(0, i-8); j < i && !ok; j++ {
			ok = limit.MatchString(lines[j])
		}
		if !ok {
			t.Errorf("wire_gen.go:%d: a make with no limit in front of it: %s", i+1, strings.TrimSpace(l))
		}
	}
	for _, f := range []string{"body.go", "decode.go", "messages.go", "detect.go", "hotty.go", "keys.go", "commands.go", "scan.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), ".DecodeMsg("); f != "body.go" && n > 0 || f == "body.go" && n != 1 {
			t.Errorf("%s: DecodeMsg called %d times; a body is decoded only in decodeBody", f, n)
		}
	}
}
