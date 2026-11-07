package v370_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/scanSTATION/lib-v370/rawimg"
	"github.com/scanSTATION/lib-v370/v370"
)

// channelShift returns the vertical shift (−3…3 lines) at which channel c
// matches green best; correct alignment gives 0.
func channelShift(img *image.RGBA, c int) int {
	b := img.Bounds()
	best, bestV := 0, -1.0
	for dy := -3; dy <= 3; dy++ {
		var sum, n float64
		for y := 4; y < b.Dy()-4; y += 2 {
			for x := 0; x < b.Dx(); x += 2 {
				p := img.Pix[(y+dy)*img.Stride+4*x+c]
				g := img.Pix[y*img.Stride+4*x+1]
				d := float64(p) - float64(g)
				if d < 0 {
					d = -d
				}
				sum += d
				n++
			}
		}
		if v := sum / n; bestV < 0 || v < bestV {
			best, bestV = dy, v
		}
	}
	return best
}

// Every built-in profile's recorded image stream decodes, and the colour
// channels line up (best match at 0 lines shift for R and B against G).
func TestDecodeRecordedStreams(t *testing.T) {
	sources := map[string]string{
		"a4-100dpi-color":        "a4-75dpi-poweron",
		"a4-200dpi-color":        "a4-150dpi-color",
		"a4-300dpi-color":        "a4-300dpi-jpeg",
		"a4-600dpi-color":        "a4-600dpi-color",
		"user-148x210mm-1200dpi": "user-148x210mm-1200dpi",
		"user-100x50mm-300dpi":   "user-100x50mm-300dpi",
		"user-50x50mm-600dpi":    "user-50x50mm-600dpi",
		"user-25x25mm-1200dpi":   "user-25x25mm-1200dpi",
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			p, err := v370.LoadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			g, err := p.Geometry()
			if err != nil {
				t.Fatal(err)
			}
			stream := recordedImage(t, loadTrace(t, src))
			img, err := rawimg.Decode(bytes.NewReader(stream), g)
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != g.Width || img.Bounds().Dy() != p.HeightPx {
				t.Fatalf("size %v", img.Bounds())
			}
			for _, c := range []int{0, 2} {
				if s := channelShift(img, c); s != 0 {
					t.Errorf("channel %c best match at %+d lines, want 0", "RGB"[c], s)
				}
			}
		})
	}
}
