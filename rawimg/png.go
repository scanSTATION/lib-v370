package rawimg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"math"
)

// WritePNG encodes img losslessly (8-bit RGB for opaque images) and records
// the resolution in a pHYs chunk so viewers and later processing know the
// physical size.
func WritePNG(w io.Writer, img image.Image, dpi int) error {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return err
	}
	b := buf.Bytes()
	// Signature (8) + IHDR chunk (4 len + 4 type + 13 data + 4 crc) = 33.
	const ihdrEnd = 33
	if len(b) < ihdrEnd || string(b[12:16]) != "IHDR" {
		return errors.New("rawimg: unexpected PNG layout")
	}
	ppm := uint32(math.Round(float64(dpi) / 0.0254))
	data := make([]byte, 9)
	binary.BigEndian.PutUint32(data[0:], ppm)
	binary.BigEndian.PutUint32(data[4:], ppm)
	data[8] = 1 // unit: metre
	chunk := make([]byte, 0, 21)
	chunk = binary.BigEndian.AppendUint32(chunk, uint32(len(data)))
	chunk = append(chunk, "pHYs"...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	for _, part := range [][]byte{b[:ihdrEnd], chunk, b[ihdrEnd:]} {
		if _, err := w.Write(part); err != nil {
			return err
		}
	}
	return nil
}

// ReadPNGDPI returns the resolution stored in a PNG's pHYs chunk (0 if none).
func ReadPNGDPI(b []byte) int {
	for off := 8; off+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[off:]))
		typ := string(b[off+4 : off+8])
		if off+12+n > len(b) {
			return 0
		}
		if typ == "pHYs" && n == 9 && b[off+16] == 1 {
			ppm := binary.BigEndian.Uint32(b[off+8:])
			return int(math.Round(float64(ppm) * 0.0254))
		}
		if typ == "IDAT" {
			return 0
		}
		off += 12 + n
	}
	return 0
}
