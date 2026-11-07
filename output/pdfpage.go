package output

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

// WriteJPEGPage writes a minimal single-page PDF that embeds a JPEG image
// (DCTDecode, no re-encoding) at its physical size: widthPx × heightPx at
// dpi. gray selects /DeviceGray instead of /DeviceRGB.
func WriteJPEGPage(w io.Writer, jpg []byte, widthPx, heightPx, dpi int, gray bool) error {
	return writeImagePage(w, jpg, "/DCTDecode", widthPx, heightPx, dpi, gray)
}

// WriteFlatePage writes a single-page PDF with a lossless image: pixels are
// 8-bit samples (RGB interleaved, or gray), row by row, zlib-compressed
// (/FlateDecode) — the same quality as PNG.
func WriteFlatePage(w io.Writer, samples []byte, widthPx, heightPx, dpi int, gray bool) error {
	comps := 3
	if gray {
		comps = 1
	}
	if len(samples) != widthPx*heightPx*comps {
		return fmt.Errorf("output: %d samples for a %dx%d page", len(samples), widthPx, heightPx)
	}
	var z bytes.Buffer
	zw, err := zlib.NewWriterLevel(&z, zlib.DefaultCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(samples); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return writeImagePage(w, z.Bytes(), "/FlateDecode", widthPx, heightPx, dpi, gray)
}

func writeImagePage(w io.Writer, data []byte, filter string, widthPx, heightPx, dpi int, gray bool) error {
	jpg := data
	if widthPx <= 0 || heightPx <= 0 || dpi <= 0 || len(jpg) == 0 {
		return fmt.Errorf("output: invalid page %dx%d @%d dpi (%d bytes)", widthPx, heightPx, dpi, len(jpg))
	}
	cs := "/DeviceRGB"
	if gray {
		cs = "/DeviceGray"
	}
	mw := float64(widthPx) * 72 / float64(dpi)
	mh := float64(heightPx) * 72 / float64(dpi)
	content := fmt.Sprintf("q %.2f 0 0 %.2f 0 0 cm /Im0 Do Q\n", mw, mh)

	var b bytes.Buffer
	offsets := make([]int, 6)
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	obj := func(n int, body string) {
		offsets[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] "+
		"/Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>", mw, mh))
	offsets[4] = b.Len()
	fmt.Fprintf(&b, "4 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s "+
		"/BitsPerComponent 8 /Filter %s /Length %d >>\nstream\n", widthPx, heightPx, cs, filter, len(jpg))
	b.Write(jpg)
	b.WriteString("\nendstream\nendobj\n")
	offsets[5] = b.Len()
	fmt.Fprintf(&b, "5 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(content), content)
	xref := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	_, err := w.Write(b.Bytes())
	return err
}
