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

	"github.com/scanSTATION/lib-v370/output"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/v370"
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
		if err := json.Unmarshal(mb, &m); err != nil || m.Profile != "a4-100dpi-color" || m.FileFormat != f || m.Crop.Applied {
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
