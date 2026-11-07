package rawimg

import (
	"bytes"
	"errors"
	"image/png"
	"testing"
)

// synth builds a raw stream whose aligned output pixel (x, y) is
// (x+y, 2x+y, 3x+y) mod 256, honouring the channel offsets.
func synth(g Geometry) []byte {
	off := g.ChannelOffsets()
	lineBytes := g.Width * 3
	raw := make([]byte, g.Lines*lineBytes)
	for c := 0; c < 3; c++ {
		for y := 0; y < g.OutputHeight(); y++ {
			ry := y + off[c]
			for x := 0; x < g.Width; x++ {
				raw[ry*lineBytes+3*x+c] = byte((c+1)*x + y)
			}
		}
	}
	var out bytes.Buffer
	for line := 0; line < g.Lines; line += g.LinesPerBlock {
		n := min(g.LinesPerBlock, g.Lines-line)
		p := raw[line*lineBytes : (line+n)*lineBytes]
		out.Write(p)
		out.Write(BlockTrailer(len(p)))
	}
	return out.Bytes()
}

var testGeom = Geometry{DPI: 300, Width: 37, Lines: 12 + 25, LinesPerBlock: 6}

func TestDecodeSynthetic(t *testing.T) {
	s := synth(testGeom)
	if len(s) != testGeom.StreamSize() {
		t.Fatalf("stream size %d, want %d", len(s), testGeom.StreamSize())
	}
	img, err := Decode(bytes.NewReader(s), testGeom)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 37 || b.Dy() != 25 {
		t.Fatalf("size %v", b)
	}
	for y := 0; y < 25; y++ {
		for x := 0; x < 37; x++ {
			c := img.RGBAAt(x, y)
			if c.R != byte(x+y) || c.G != byte(2*x+y) || c.B != byte(3*x+y) || c.A != 255 {
				t.Fatalf("pixel (%d,%d) = %v", x, y, c)
			}
		}
	}
}

func TestDecodeRejectsCorruptStreams(t *testing.T) {
	good := synth(testGeom)
	cases := map[string][]byte{
		"truncated":    good[:len(good)-1],
		"extra data":   append(append([]byte(nil), good...), 0),
		"bad trailer":  func() []byte { b := append([]byte(nil), good...); b[37*3*6+7] = 0x81; return b }(),
		"wrong length": func() []byte { b := append([]byte(nil), good...); b[37*3*6+1]++; return b }(),
	}
	for name, s := range cases {
		if _, err := Decode(bytes.NewReader(s), testGeom); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
	bad := []Geometry{
		{DPI: 300, Width: 0, Lines: 20, LinesPerBlock: 6},
		{DPI: 300, Width: 10, Lines: 12, LinesPerBlock: 6}, // nothing left after alignment
		{DPI: 300, Width: 10, Lines: 20, LinesPerBlock: 0},
		{DPI: 300, Width: 100000, Lines: 20, LinesPerBlock: 6},
	}
	for _, g := range bad {
		if _, err := Decode(bytes.NewReader(good), g); err == nil {
			t.Errorf("geometry %+v accepted", g)
		}
	}
}

func TestPNGResolution(t *testing.T) {
	img, _ := Decode(bytes.NewReader(synth(testGeom)), testGeom)
	var buf bytes.Buffer
	if err := WritePNG(&buf, img, 300); err != nil {
		t.Fatal(err)
	}
	if dpi := ReadPNGDPI(buf.Bytes()); dpi != 300 {
		t.Fatalf("pHYs dpi = %d", dpi)
	}
	back, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	// Lossless: every pixel identical.
	for y := 0; y < 25; y++ {
		for x := 0; x < 37; x++ {
			r1, g1, b1, _ := img.At(x, y).RGBA()
			r2, g2, b2, _ := back.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 {
				t.Fatalf("pixel (%d,%d) changed", x, y)
			}
		}
	}
	// Opaque image → stored as 8-bit RGB (colour type 2), not RGBA.
	if b := buf.Bytes(); b[25] != 2 || b[24] != 8 {
		t.Fatalf("IHDR bit depth %d colour type %d", b[24], b[25])
	}
}
