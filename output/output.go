// Package output turns a raw V370 image stream into stored files: the image
// (PNG, JPEG or PDF), optionally the raw stream, and a JSON sidecar that
// documents exactly how the file was produced.
//
// Pixel values are never altered (no tone curve, resampling, deskew or
// flattening) and the image is not cropped; see docs/protocol.md §8.
package output

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"time"

	"github.com/scanSTATION/lib-v370/rawimg"
	"github.com/scanSTATION/lib-v370/v370"
)

// Format of the stored image.
type Format string

const (
	PNG  Format = "png"  // lossless (recommended)
	JPEG Format = "jpeg" // lossy, Options.Quality
	PDF  Format = "pdf"  // single page; lossless (Flate) or JPEG inside, see Options.PDFLossless
	Raw  Format = "raw"  // the scanner stream itself (decoded only for validation)
)

// Ext returns the file extension of the format.
func (f Format) Ext() string {
	switch f {
	case JPEG:
		return ".jpg"
	case PDF:
		return ".pdf"
	case Raw:
		return ".raw"
	default:
		return ".png"
	}
}

// Options control the output.
type Options struct {
	Format  Format
	Quality int  // JPEG/PDF quality 1–100 (default 85)
	KeepRaw bool // additionally keep the raw stream as BASE.raw
	// PDFLossless stores the PDF page image losslessly (/FlateDecode, PNG
	// quality) instead of as JPEG.
	PDFLossless bool
}

// Meta is the JSON sidecar (BASE.json).
type Meta struct {
	Version    string    `json:"format_version"`
	File       string    `json:"file"`
	FileFormat Format    `json:"file_format"`
	FileSHA256 string    `json:"file_sha256"`
	Quality    int       `json:"jpeg_quality,omitempty"`
	PDFImage   string    `json:"pdf_image,omitempty"` // "flate (lossless)" or "jpeg"
	Profile    string    `json:"profile"`
	Identity   string    `json:"identity,omitempty"`
	Time       time.Time `json:"time"`
	Seconds    float64   `json:"scan_seconds,omitempty"`
	DPI        int       `json:"dpi"`
	WidthPx    int       `json:"width_px"`
	HeightPx   int       `json:"height_px"`
	ReqWidth   int       `json:"requested_width_px"`
	ReqHeight  int       `json:"requested_height_px"`
	Crop       Crop      `json:"crop"`
	Raw        RawInfo   `json:"raw"`
	Values     string    `json:"values"`
	Calibrated string    `json:"calibration"`
}

// Crop documents that (and why) the image is not cropped.
type Crop struct {
	Applied bool   `json:"applied"`
	Note    string `json:"note"`
}

// RawInfo describes the decoded stream.
type RawInfo struct {
	Width          int    `json:"width_px"`
	Lines          int    `json:"lines"`
	LinesPerBlock  int    `json:"lines_per_block"`
	ChannelOffsets [3]int `json:"channel_offsets_lines_rgb"`
	StreamBytes    int    `json:"stream_bytes"`
	StreamSHA256   string `json:"stream_sha256"`
	File           string `json:"file,omitempty"`
}

// MetaVersion identifies the sidecar layout.
const MetaVersion = "v370-scan/2"

const (
	valuesNote = "8-bit RGB exactly as delivered by the scanner (scanner-side calibration), " +
		"colour channels line-aligned; no host tone curve, no resampling, no deskew, no flattening"
	calibNote = "calibration tables replayed from the vendor-app recording the profile was extracted from"
)

// Write decodes stream with the profile's geometry and writes BASE.<ext>
// and BASE.json (and BASE.raw with KeepRaw). It returns the written paths,
// image first. meta may carry Identity, Time and Seconds; the rest is filled.
func Write(base string, p *v370.Profile, stream []byte, opt Options, meta Meta) ([]string, error) {
	if opt.Format == "" {
		opt.Format = PNG
	}
	if opt.Quality == 0 {
		opt.Quality = 85
	}
	if opt.Quality < 1 || opt.Quality > 100 {
		return nil, fmt.Errorf("output: quality %d out of range", opt.Quality)
	}
	g, err := p.Geometry()
	if err != nil {
		return nil, err
	}
	img, err := rawimg.Decode(bytes.NewReader(stream), g)
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(base); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	var file bytes.Buffer
	switch opt.Format {
	case PNG:
		err = rawimg.WritePNG(&file, img, g.DPI)
	case JPEG:
		err = jpeg.Encode(&file, img, &jpeg.Options{Quality: opt.Quality})
	case PDF:
		b := img.Bounds()
		if opt.PDFLossless {
			err = WriteFlatePage(&file, rgbSamples(img), b.Dx(), b.Dy(), g.DPI, false)
		} else {
			var jb bytes.Buffer
			if err = jpeg.Encode(&jb, img, &jpeg.Options{Quality: opt.Quality}); err == nil {
				err = WriteJPEGPage(&file, jb.Bytes(), b.Dx(), b.Dy(), g.DPI, false)
			}
		}
	case Raw:
		file.Write(stream)
	default:
		return nil, fmt.Errorf("output: unknown format %q", opt.Format)
	}
	if err != nil {
		return nil, err
	}

	imgPath := base + opt.Format.Ext()
	if err := os.WriteFile(imgPath, file.Bytes(), 0o644); err != nil {
		return nil, err
	}
	paths := []string{imgPath}

	fs := sha256.Sum256(file.Bytes())
	ss := sha256.Sum256(stream)
	meta.Version = MetaVersion
	meta.File = filepath.Base(imgPath)
	meta.FileFormat = opt.Format
	meta.FileSHA256 = hex.EncodeToString(fs[:])
	switch {
	case opt.Format == PDF && opt.PDFLossless:
		meta.PDFImage = "flate (lossless)"
	case opt.Format == PDF:
		meta.PDFImage, meta.Quality = "jpeg", opt.Quality
	case opt.Format == JPEG:
		meta.Quality = opt.Quality
	}
	meta.Profile = p.Name
	meta.DPI = g.DPI
	meta.WidthPx, meta.HeightPx = img.Bounds().Dx(), img.Bounds().Dy()
	meta.ReqWidth, meta.ReqHeight = p.WidthPx, p.HeightPx
	meta.Crop = Crop{Applied: false, Note: fmt.Sprintf(
		"not cropped: full scanned width (%d px); the requested area is %d px wide, starts at y=0 "+
			"and at an x offset not yet determined (docs/protocol.md §8)", g.Width, p.WidthPx)}
	meta.Raw = RawInfo{
		Width: g.Width, Lines: g.Lines, LinesPerBlock: g.LinesPerBlock,
		ChannelOffsets: g.ChannelOffsets(), StreamBytes: len(stream),
		StreamSHA256: hex.EncodeToString(ss[:]),
	}
	meta.Values, meta.Calibrated = valuesNote, calibNote
	if opt.KeepRaw && opt.Format != Raw {
		if err := os.WriteFile(base+".raw", stream, 0o644); err != nil {
			return nil, err
		}
		meta.Raw.File = filepath.Base(base + ".raw")
		paths = append(paths, base+".raw")
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(base+".json", append(mb, '\n'), 0o644); err != nil {
		return nil, err
	}
	return append(paths, base+".json"), nil
}

// rgbSamples returns the image as packed 8-bit RGB rows.
func rgbSamples(img *image.RGBA) []byte {
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := 0; y < b.Dy(); y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+4*b.Dx()]
		for x := 0; x < len(row); x += 4 {
			out = append(out, row[x], row[x+1], row[x+2])
		}
	}
	return out
}
