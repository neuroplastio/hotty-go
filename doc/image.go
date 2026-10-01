package doc

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"html"
	"image"
	_ "image/gif" // DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path"
	"strconv"
	"strings"
)

// Info is what a program needs to know about an image to place it: its
// MIME type and its size in pixels.
type Info struct {
	// Type is its MIME type: "image/png", "image/svg+xml".
	Type string
	// W and H are its size in CSS pixels.
	W, H int
}

// ImageInfo reads an image's type and size from its bytes: PNG, JPEG,
// GIF, WebP and SVG. name is only a hint, for SVG.
func ImageInfo(data []byte, name string) (Info, bool) {
	switch {
	case IsSVG(data) || (strings.EqualFold(path.Ext(name), ".svg") && bytes.Contains(data[:min(len(data), 4096)], []byte("<svg"))):
		w, h := svgSize(data)
		return Info{"image/svg+xml", w, h}, true
	case isWebP(data):
		w, h, ok := webpSize(data)
		return Info{"image/webp", w, h}, ok
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Info{}, false
	}
	return Info{"image/" + format, cfg.Width, cfg.Height}, true
}

// IsImage reports whether data starts like an image this package reads.
func IsImage(data []byte) bool {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(data, []byte("\xff\xd8\xff")),
		bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")),
		isWebP(data), IsSVG(data):
		return true
	}
	return false
}

func isWebP(data []byte) bool {
	return len(data) >= 16 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

// IsSVG reports whether data is an SVG document: an <svg> root, perhaps
// after an XML declaration, a doctype and comments.
func IsSVG(data []byte) bool {
	head := data[:min(len(data), 1024)]
	s := strings.TrimLeft(string(head), " \t\r\n\uFEFF")
	for {
		switch {
		case strings.HasPrefix(s, "<?"):
			i := strings.Index(s, "?>")
			if i < 0 {
				return false
			}
			s = strings.TrimLeft(s[i+2:], " \t\r\n")
		case strings.HasPrefix(s, "<!--"):
			i := strings.Index(s, "-->")
			if i < 0 {
				return false
			}
			s = strings.TrimLeft(s[i+3:], " \t\r\n")
		case strings.HasPrefix(s, "<!DOCTYPE"), strings.HasPrefix(s, "<!doctype"):
			i := strings.Index(s, ">")
			if i < 0 {
				return false
			}
			s = strings.TrimLeft(s[i+1:], " \t\r\n")
		default:
			return strings.HasPrefix(s, "<svg")
		}
	}
}

// webpSize reads the canvas size from a WebP file's first chunk.
func webpSize(d []byte) (int, int, bool) {
	if len(d) < 30 {
		return 0, 0, false
	}
	switch string(d[12:16]) {
	case "VP8 ": // lossy: after the frame tag and start code, 14-bit sizes
		w := int(binary.LittleEndian.Uint16(d[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(d[28:30]) & 0x3fff)
		return w, h, w > 0 && h > 0
	case "VP8L": // lossless: a signature byte, then 14 bits each, less one
		b := binary.LittleEndian.Uint32(d[21:25])
		return int(b&0x3fff) + 1, int(b>>14&0x3fff) + 1, true
	case "VP8X": // extended: 24 bits each, less one
		w := int(d[24]) | int(d[25])<<8 | int(d[26])<<16
		h := int(d[27]) | int(d[28])<<8 | int(d[29])<<16
		return w + 1, h + 1, true
	}
	return 0, 0, false
}

// svgSize is an SVG's size in CSS pixels: its width and height, else its
// viewBox, else 300×150 as a browser has it. A size that is not a
// positive number does not count, and one past maxSVG comes down to it,
// in shape.
func svgSize(data []byte) (int, int) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return 300, 150
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "svg" {
			continue
		}
		var w, h, vw, vh float64
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "width":
				w = cssPx(a.Value)
			case "height":
				h = cssPx(a.Value)
			case "viewBox":
				f := strings.FieldsFunc(a.Value, func(r rune) bool { return r == ' ' || r == ',' })
				if len(f) == 4 {
					vw, _ = strconv.ParseFloat(f[2], 64)
					vh, _ = strconv.ParseFloat(f[3], 64)
				}
			}
		}
		w, h, vw, vh = positive(w), positive(h), positive(vw), positive(vh)
		switch {
		case w > 0 && h > 0:
		case vw > 0 && vh > 0 && w > 0:
			h = w * (vh / vw)
		case vw > 0 && vh > 0 && h > 0:
			w = h * (vw / vh)
		case vw > 0 && vh > 0:
			w, h = vw, vh
		default:
			w, h = 300, 150
		}
		if m := math.Max(w, h); math.IsInf(m, 1) {
			w, h = math.Min(w, maxSVG), math.Min(h, maxSVG)
		} else if m > maxSVG {
			w, h = w/m*maxSVG, h/m*maxSVG
		}
		return max(1, int(math.Round(w))), max(1, int(math.Round(h)))
	}
}

// maxSVG is the most CSS pixels an SVG is taken to be, either way.
const maxSVG = 1e6

// positive is v if it is a finite number above 0, else 0.
func positive(v float64) float64 {
	if v > 0 && !math.IsInf(v, 1) {
		return v
	}
	return 0
}

func cssPx(v string) float64 {
	v = strings.TrimSpace(v)
	for _, u := range []struct {
		s string
		f float64
	}{{"px", 1}, {"pt", 4.0 / 3}, {"em", 16}, {"in", 96}, {"cm", 96 / 2.54}, {"mm", 96 / 25.4}} {
		if n, ok := strings.CutSuffix(v, u.s); ok {
			f, _ := strconv.ParseFloat(n, 64)
			return f * u.f
		}
	}
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

// MaxImageRows is the tallest an image is shown, in rows.
const MaxImageRows = 30

// Fit is the cells an image of w×h CSS pixels takes: its own size, made
// smaller to fit the width and MaxImageRows rows (fewer on a short
// screen), never larger.
func (o *Options) Fit(w, h int) (cols, rows int) {
	cw, ch := o.cell()
	if w <= 0 || h <= 0 {
		return min(o.cols(), 40), min(10, o.imageRows())
	}
	c, r := float64(w)/cw, float64(h)/ch
	s := math.Min(1, math.Min(float64(o.cols())/c, float64(o.imageRows())/r))
	return max(1, int(math.Ceil(c*s-0.01))), max(1, int(math.Ceil(r*s-0.01)))
}

// Image is an image file as a document: the image as a resource, in a
// surface of a fixed size (Fit).
func Image(name string, data []byte, o Options) (*Doc, Info, error) {
	info, ok := ImageInfo(data, name)
	if !ok {
		return nil, Info{}, fmt.Errorf("%s: not an image this can show", path.Base(name))
	}
	cols, rows := o.Fit(info.W, info.H)
	id := o.resID("img", data)
	return &Doc{
		CSS:  ImageCSS,
		Cols: cols,
		Rows: rows,
		Blocks: []Block{{
			HTML: fmt.Sprintf(`<img class="image" src="cid:%s" alt="%s">`, id, html.EscapeString(path.Base(name))),
			Rows: rows,
			Res:  []Resource{{ID: id, Type: info.Type, Data: data}},
		}},
	}, info, nil
}
