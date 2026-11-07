package v370

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Calibration values computed during a scan from the scanner's own
// calibration reads (docs/research/calibration.md). A profile names them
// with calc steps; nothing measured is stored in a profile.
const (
	// CalcWhiteOffsets is the `1e 42` block before the white read: the dark
	// level of the first (short) calibration read per channel.
	CalcWhiteOffsets = "white-offsets"
	// CalcImageLevels is the `1e 42` block before the image pass: per
	// channel the base K (darkest white column) and the dark offset
	// (darkest dark column).
	CalcImageLevels = "image-levels"
	// CalcShading is the per-pixel shading table (`1e 84` type 5):
	// f · (white − K) per pixel and channel, 0 beyond the scanned width.
	CalcShading = "shading"
)

const levelsLen = 18 // `1e 42` parameter block

// calRead is one calibration data read with the `1e 41` block it used.
type calRead struct {
	geom []byte // 22-byte `1e 41` block
	data []byte
}

func (r calRead) start() int { return int(binary.LittleEndian.Uint16(r.geom[0:])) }
func (r calRead) end() int   { return int(binary.LittleEndian.Uint16(r.geom[2:])) }
func (r calRead) width() int { return r.end() - r.start() }
func (r calRead) lines() int { return int(binary.LittleEndian.Uint16(r.geom[4:])) }

// samples checks the read's size (lines × width × 3 u16 + 8-byte trailer)
// and returns its samples, pixel-interleaved RGB.
func (r calRead) samples() ([]uint16, error) {
	if len(r.geom) != 22 || r.width() <= 0 || r.lines() <= 0 {
		return nil, fmt.Errorf("calibration read without valid geometry")
	}
	n := r.lines() * r.width() * 3
	if len(r.data) != 2*n+8 {
		return nil, fmt.Errorf("calibration read: %d bytes, want %d (%d lines × %d px)", len(r.data), 2*n+8, r.lines(), r.width())
	}
	v := make([]uint16, n)
	for i := range v {
		v[i] = binary.LittleEndian.Uint16(r.data[2*i:])
	}
	return v, nil
}

// columnMeans averages reads of equal width per pixel and channel.
func columnMeans(reads []calRead) ([]float64, error) {
	w := reads[0].width()
	m := make([]float64, w*3)
	lines := 0
	for _, r := range reads {
		v, err := r.samples()
		if err != nil {
			return nil, err
		}
		for i, x := range v {
			m[i%(w*3)] += float64(x)
		}
		lines += r.lines()
	}
	for i := range m {
		m[i] /= float64(lines)
	}
	return m, nil
}

// calibration collects a scan's calibration reads and computes the values
// that depend on them.
type calibration struct {
	reads      []calRead
	sensorMode byte   // `1e 11` parameter
	last41     []byte // latest `1e 41` block (the image pass's when the table is computed)
	widthPx    int    // requested width and resolution (profile)
	scanDPI    int
}

var errNoCalibration = errors.New("calibration: required calibration reads missing")

func isCalibrationTag(tag string) bool { return strings.HasPrefix(tag, "calibration-") }

// parts classifies the reads: the first is the short dark read, the first
// wide one is the white reference, later reads of the same span are dark.
func (c *calibration) parts() (short calRead, white calRead, darks []calRead, err error) {
	if len(c.reads) < 3 {
		return short, white, nil, errNoCalibration
	}
	short = c.reads[0]
	wi := -1
	for i, r := range c.reads[1:] {
		if r.width() > 64 {
			wi = i + 1
			break
		}
	}
	if wi < 0 {
		return short, white, nil, errNoCalibration
	}
	white = c.reads[wi]
	for _, r := range c.reads[wi+1:] {
		if r.start() == white.start() && r.end() == white.end() {
			darks = append(darks, r)
		}
	}
	if len(darks) == 0 {
		return short, white, nil, errNoCalibration
	}
	return short, white, darks, nil
}

// sensorDPI is the horizontal sensor resolution selected by `1e 11`.
func (c *calibration) sensorDPI() (int, error) {
	switch c.sensorMode {
	case 4:
		return 400, nil
	case 3:
		return 600, nil
	case 2:
		return 1200, nil
	}
	return 0, fmt.Errorf("calibration: unknown sensor mode %d", c.sensorMode)
}

// used is the number of table pixels covering the requested width (the
// vendor app keeps ≈ 1 % more; the rest of the table is 0).
func (c *calibration) used(width int) (int, error) {
	dpi, err := c.sensorDPI()
	if err != nil {
		return 0, err
	}
	if c.widthPx <= 0 || c.scanDPI <= 0 {
		return width, nil
	}
	n := int(math.Ceil(float64(c.widthPx) * float64(dpi) / float64(c.scanDPI) * 1.01))
	return min(max(n, 1), width), nil
}

// value computes a calc step's bytes.
func (c *calibration) value(src string, n int) ([]byte, error) {
	switch src {
	case CalcWhiteOffsets:
		return c.whiteOffsets()
	case CalcImageLevels:
		return c.imageLevels()
	case CalcShading:
		return c.shading(n)
	}
	return nil, fmt.Errorf("calibration: unknown value %q", src)
}

// whiteOffsets: zero bases, per channel the mean of the short dark read
// (twice, as the vendor app sends it).
func (c *calibration) whiteOffsets() ([]byte, error) {
	if len(c.reads) == 0 {
		return nil, errNoCalibration
	}
	m, err := columnMeans(c.reads[:1])
	if err != nil {
		return nil, err
	}
	var off [3]float64
	for i, x := range m {
		off[i%3] += x
	}
	px := float64(len(m) / 3)
	b := make([]byte, levelsLen)
	for ch := range 3 {
		v := clampU16(math.Round(off[ch] / px))
		binary.LittleEndian.PutUint16(b[6+4*ch:], v)
		binary.LittleEndian.PutUint16(b[8+4*ch:], v)
	}
	return b, nil
}

// levels returns the white and dark column means, the used width and per
// channel the base K and the dark offset.
func (c *calibration) levels() (white []float64, n int, k, off [3]float64, err error) {
	_, wr, darks, err := c.parts()
	if err != nil {
		return nil, 0, k, off, err
	}
	white, err = columnMeans([]calRead{wr})
	if err != nil {
		return nil, 0, k, off, err
	}
	dark, err := columnMeans(darks)
	if err != nil {
		return nil, 0, k, off, err
	}
	w := wr.width()
	if n, err = c.used(w); err != nil {
		return nil, 0, k, off, err
	}
	for ch := range 3 {
		k[ch], off[ch] = math.Inf(1), math.Inf(1)
		for p := 0; p < n; p++ {
			if x := white[p*3+ch]; x > 0 {
				k[ch] = math.Min(k[ch], x)
			}
			off[ch] = math.Min(off[ch], dark[p*3+ch])
		}
		if math.IsInf(k[ch], 1) {
			return nil, 0, k, off, fmt.Errorf("calibration: no white signal in channel %d", ch)
		}
	}
	return white, n, k, off, nil
}

// imageLevels: bases K (words 0–2) and dark offsets (words 3–8, twice).
func (c *calibration) imageLevels() ([]byte, error) {
	_, _, k, off, err := c.levels()
	if err != nil {
		return nil, err
	}
	b := make([]byte, levelsLen)
	for ch := range 3 {
		binary.LittleEndian.PutUint16(b[2*ch:], clampU16(math.Round(k[ch])))
		o := clampU16(math.Round(off[ch]))
		binary.LittleEndian.PutUint16(b[6+4*ch:], o)
		binary.LittleEndian.PutUint16(b[8+4*ch:], o)
	}
	return b, nil
}

// shadingFactor is the table scale selected by the image pass's `1e 41`
// byte 15 (high nibble 7: ×2, used for small areas; 8: ×1).
func (c *calibration) shadingFactor() float64 {
	if len(c.last41) == 22 && c.last41[15]>>4 == 7 {
		return 2
	}
	return 1
}

// shading: f · (white − K) per pixel and channel for the used width, 0
// beyond; size bytes (= calibration width × 6).
func (c *calibration) shading(size int) ([]byte, error) {
	white, n, k, _, err := c.levels()
	if err != nil {
		return nil, err
	}
	if size != len(white)*2 {
		return nil, fmt.Errorf("calibration: shading table of %d bytes for %d calibration pixels", size, len(white)/3)
	}
	f := c.shadingFactor()
	b := make([]byte, size)
	for p := 0; p < n; p++ {
		for ch := range 3 {
			i := p*3 + ch
			binary.LittleEndian.PutUint16(b[2*i:], clampU16(math.Round(f*(white[i]-k[ch]))))
		}
	}
	return b, nil
}

func clampU16(x float64) uint16 {
	return uint16(math.Max(0, math.Min(65535, x)))
}
