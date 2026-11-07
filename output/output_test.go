package output_test

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"github.com/scanSTATION/lib-v370/internal/testtraces"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"fmt"
	"github.com/scanSTATION/lib-v370/output"
	"github.com/scanSTATION/lib-v370/sim/synth"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/v370"
	"image"
)

// recordedImage returns the largest 1e 47 data read of a trace.
func recordedImage(t *testing.T, tr *trace.Trace) []byte {
	t.Helper()
	var best, cur []byte
	in := false
	for _, x := range tr.Transfers {
		if x.Type != trace.TypeBulk || x.Status != 0 {
			continue
		}
		if !x.IsIn() {
			if in && len(cur) > len(best) {
				best = cur
			}
			in, cur = bytes.Equal(x.Data, []byte{0x1e, 0x47}), nil
			continue
		}
		if in {
			cur = append(cur, x.Data...)
		}
	}
	return best
}

func TestWriteFormats(t *testing.T) {
	tr, err := trace.Load(testtraces.Path(t, "a4-75dpi-poweron"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := v370.LoadProfile("a4-100dpi-color")
	if err != nil {
		t.Fatal(err)
	}
	stream := recordedImage(t, tr)
	dir := t.TempDir()
	for _, f := range []output.Format{output.PNG, output.JPEG, output.PDF, output.Raw} {
		base := filepath.Join(dir, string(f))
		files, err := output.Write(base, p, stream, output.Options{Format: f, KeepRaw: f == output.PNG}, output.Meta{})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		b, _ := os.ReadFile(files[0])
		switch f {
		case output.PNG:
			img, err := png.Decode(bytes.NewReader(b))
			if err != nil || img.Bounds().Dx() != 848 || img.Bounds().Dy() != 1168 {
				t.Fatalf("png %v %v", img, err)
			}
			if len(files) != 3 { // png, raw, json
				t.Fatalf("files %v", files)
			}
		case output.JPEG:
			if _, err := jpeg.DecodeConfig(bytes.NewReader(b)); err != nil {
				t.Fatal(err)
			}
		case output.PDF:
			if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.Contains(b, []byte("/DCTDecode")) {
				t.Fatal("pdf")
			}
		case output.Raw:
			if !bytes.Equal(b, stream) {
				t.Fatal("raw differs")
			}
		}
		var m output.Meta
		mb, _ := os.ReadFile(base + ".json")
		if err := json.Unmarshal(mb, &m); err != nil || m.Profile != "a4-100dpi-color" || m.FileFormat != f || m.Processing.Steps != nil {
			t.Fatalf("%s meta %s %v", f, mb, err)
		}
	}
	if _, err := output.Write(filepath.Join(dir, "x"), p, stream[:100], output.Options{}, output.Meta{}); err == nil {
		t.Fatal("truncated stream accepted")
	}
}

// The lossless PDF contains exactly the decoded pixels.
func TestLosslessPDF(t *testing.T) {
	tr, err := trace.Load(testtraces.Path(t, "a4-75dpi-poweron"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := v370.LoadProfile("a4-100dpi-color")
	stream := recordedImage(t, tr)
	dir := t.TempDir()
	files, err := output.Write(filepath.Join(dir, "l"), p, stream, output.Options{Format: output.PDF, PDFLossless: true}, output.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	pdf, _ := os.ReadFile(files[0])
	if !bytes.Contains(pdf, []byte("/FlateDecode")) || bytes.Contains(pdf, []byte("/DCTDecode")) {
		t.Fatal("not a Flate PDF")
	}
	// Extract the image stream and compare with the PNG of the same scan.
	i := bytes.Index(pdf, []byte("stream\n")) + len("stream\n")
	j := bytes.Index(pdf[i:], []byte("\nendstream"))
	zr, err := zlib.NewReader(bytes.NewReader(pdf[i : i+j]))
	if err != nil {
		t.Fatal(err)
	}
	samples, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	pngFiles, _ := output.Write(filepath.Join(dir, "p"), p, stream, output.Options{Format: output.PNG}, output.Meta{})
	pf, _ := os.Open(pngFiles[0])
	img, _ := png.Decode(pf)
	pf.Close()
	b := img.Bounds()
	if len(samples) != b.Dx()*b.Dy()*3 {
		t.Fatalf("samples %d", len(samples))
	}
	for y := 0; y < b.Dy(); y += 7 {
		for x := 0; x < b.Dx(); x += 5 {
			r, g, bl, _ := img.At(x, y).RGBA()
			k := (y*b.Dx() + x) * 3
			if samples[k] != byte(r>>8) || samples[k+1] != byte(g>>8) || samples[k+2] != byte(bl>>8) {
				t.Fatalf("pixel (%d,%d) differs", x, y)
			}
		}
	}
	var m output.Meta
	mb, _ := os.ReadFile(filepath.Join(dir, "l.json"))
	json.Unmarshal(mb, &m)
	if m.PDFImage != "flate (lossless)" {
		t.Fatalf("meta pdf_image %q", m.PDFImage)
	}
}

// Transform runs before encoding; its record lands in the sidecar, and
// KeepOriginal stores the scan as well (synthetic stream, no recordings).
func TestTransformAndOriginal(t *testing.T) {
	p, err := v370.LoadProfile("user-100x50mm-300dpi")
	if err != nil {
		t.Fatal(err)
	}
	stream := synth.Stream(p)
	dir := t.TempDir()
	crop := func(img *image.RGBA, dpi int) (*image.RGBA, any, error) {
		if dpi != 300 {
			return nil, nil, fmt.Errorf("dpi %d", dpi)
		}
		return img.SubImage(image.Rect(0, 0, 100, 50)).(*image.RGBA), map[string]any{"step": "crop"}, nil
	}
	files, err := output.Write(filepath.Join(dir, "t"), p, stream,
		output.Options{Format: output.PNG, Transform: crop, KeepOriginal: true}, output.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || filepath.Base(files[1]) != "t-original.png" {
		t.Fatalf("files %v", files)
	}
	dims := func(path string) (int, int) {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		c, err := png.DecodeConfig(f)
		if err != nil {
			t.Fatal(err)
		}
		return c.Width, c.Height
	}
	if w, h := dims(files[0]); w != 100 || h != 50 {
		t.Fatalf("result %dx%d", w, h)
	}
	g, _ := p.Geometry()
	if w, h := dims(files[1]); w != g.Width || h != g.OutputHeight() {
		t.Fatalf("original %dx%d, want %dx%d", w, h, g.Width, g.OutputHeight())
	}
	var m output.Meta
	mb, _ := os.ReadFile(filepath.Join(dir, "t.json"))
	if err := json.Unmarshal(mb, &m); err != nil {
		t.Fatal(err)
	}
	if m.WidthPx != 100 || m.HeightPx != 50 || m.Processing.OriginalFile != "t-original.png" || m.Processing.Steps == nil {
		t.Fatalf("meta %s", mb)
	}
	// Raw output is never transformed.
	files, err = output.Write(filepath.Join(dir, "r"), p, stream, output.Options{Format: output.Raw, Transform: crop}, output.Meta{})
	if err != nil || len(files) != 2 {
		t.Fatalf("raw: %v %v", files, err)
	}
	// A failing transform fails the write.
	bad := func(*image.RGBA, int) (*image.RGBA, any, error) { return nil, nil, fmt.Errorf("boom") }
	if _, err := output.Write(filepath.Join(dir, "b"), p, stream, output.Options{Transform: bad}, output.Meta{}); err == nil {
		t.Fatal("transform error ignored")
	}
}
