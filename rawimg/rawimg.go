// Package rawimg decodes the V370's raw image stream (the final `1e 47`
// data read) into an image, without altering pixel values.
//
// Format (docs/protocol.md §8, verified on all recordings):
//   - the stream is a sequence of blocks, one per USB transfer; each block is
//     LinesPerBlock whole lines followed by an 8-byte trailer
//     `02 <u24 LE payload length> 00 00 80 00`; the last block holds the
//     remaining lines;
//   - lines are 8-bit RGB, pixel-interleaved, Width pixels;
//   - the sensor's colour rows see different page lines: output line y takes
//     B from raw line y, G from y + 2·dpi/100 and R from y + 4·dpi/100. The
//     scanner sends exactly 4·dpi/100 extra lines for this.
//
// Deliberately NOT done here: cropping, resampling, tone curves, deskewing,
// flattening — the output is the scanner's calibrated data, aligned.
package rawimg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
)

// Geometry describes one raw image stream.
type Geometry struct {
	DPI           int // scan resolution (x = y)
	Width         int // pixels per raw line (scanner-rounded)
	Lines         int // raw lines in the stream
	LinesPerBlock int // lines per USB transfer
}

// Limits guard against corrupt geometry (the largest real scan is the whole
// glass at 1200 dpi: ~10 200 × 14 100 px).
const (
	maxWidth = 20000
	maxLines = 30000
	trailer  = 8
)

// Validate checks the geometry.
func (g Geometry) Validate() error {
	switch {
	case g.DPI < 50 || g.DPI > 4800:
		return fmt.Errorf("rawimg: dpi %d out of range", g.DPI)
	case g.Width <= 0 || g.Width > maxWidth:
		return fmt.Errorf("rawimg: width %d out of range", g.Width)
	case g.Lines <= g.ChannelSpread() || g.Lines > maxLines:
		return fmt.Errorf("rawimg: %d lines out of range", g.Lines)
	case g.LinesPerBlock <= 0 || g.LinesPerBlock > g.Lines:
		return fmt.Errorf("rawimg: %d lines per block out of range", g.LinesPerBlock)
	}
	return nil
}

// ChannelOffsets returns the raw line offsets of R, G and B relative to the
// output line.
func (g Geometry) ChannelOffsets() [3]int {
	k := g.DPI / 100
	return [3]int{4 * k, 2 * k, 0}
}

// ChannelSpread is the number of extra raw lines used for channel alignment.
func (g Geometry) ChannelSpread() int { return g.ChannelOffsets()[0] }

// OutputHeight is the number of lines after channel alignment.
func (g Geometry) OutputHeight() int { return g.Lines - g.ChannelSpread() }

// StreamSize is the expected number of bytes of the raw stream.
func (g Geometry) StreamSize() int {
	blocks := (g.Lines + g.LinesPerBlock - 1) / g.LinesPerBlock
	return g.Lines*g.Width*3 + blocks*trailer
}

// ErrFormat reports a stream that does not match the geometry.
var ErrFormat = errors.New("rawimg: stream does not match the expected format")

// Decode reads the raw stream from r and returns the channel-aligned image
// (Width × OutputHeight). The stream must contain exactly the expected
// blocks; every trailer is verified.
func Decode(r io.Reader, g Geometry) (*image.RGBA, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	lineBytes := g.Width * 3
	raw := make([]byte, g.Lines*lineBytes)
	block := make([]byte, g.LinesPerBlock*lineBytes+trailer)
	for line, n := 0, 0; line < g.Lines; n++ {
		lines := min(g.LinesPerBlock, g.Lines-line)
		size := lines*lineBytes + trailer
		b := block[:size]
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, fmt.Errorf("%w: block %d (line %d): %v", ErrFormat, n, line, err)
		}
		t := b[size-trailer:]
		payload := uint32(t[1]) | uint32(t[2])<<8 | uint32(t[3])<<16
		if t[0] != 0x02 || int(payload) != size-trailer || !bytes.Equal(t[4:], []byte{0x00, 0x00, 0x80, 0x00}) {
			return nil, fmt.Errorf("%w: block %d has trailer %x (want 02 + length %d + 00008000)", ErrFormat, n, t, size-trailer)
		}
		copy(raw[line*lineBytes:], b[:size-trailer])
		line += lines
	}
	// Nothing may follow the last block.
	var extra [1]byte
	if n, _ := r.Read(extra[:]); n != 0 {
		return nil, fmt.Errorf("%w: data after the last block", ErrFormat)
	}

	off := g.ChannelOffsets()
	h := g.OutputHeight()
	img := image.NewRGBA(image.Rect(0, 0, g.Width, h))
	for y := 0; y < h; y++ {
		rl := raw[(y+off[0])*lineBytes:]
		gl := raw[(y+off[1])*lineBytes:]
		bl := raw[(y+off[2])*lineBytes:]
		row := img.Pix[y*img.Stride:]
		for x := 0; x < g.Width; x++ {
			row[4*x] = rl[3*x]
			row[4*x+1] = gl[3*x+1]
			row[4*x+2] = bl[3*x+2]
			row[4*x+3] = 0xff
		}
	}
	return img, nil
}

// BlockTrailer builds the 8-byte trailer for a payload (used by tests and
// tools that synthesise streams).
func BlockTrailer(payload int) []byte {
	t := make([]byte, 8)
	t[0] = 0x02
	binary.LittleEndian.PutUint32(t[1:5], uint32(payload)) // t[4] is overwritten below
	t[4], t[5], t[6], t[7] = 0x00, 0x00, 0x80, 0x00
	return t
}
