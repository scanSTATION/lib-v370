// Package the vendor app drives the vendor's own scanning software, the vendor app
// (Flatpak or native install), as an alternative to the direct USB driver:
// it builds .SF2 setting files (Settings, Build) and runs the vendor app
// CLI (runner.go). Package fake is a stand-in for the vendor app binary for
// tests.
//
// The .SF2 format is JSON with a "Preset" array containing one or more
// parameter groups. Each parameter is a map entry with a typed value
// (e.g. {"int": 300} or {"string": "img"}).
package vendorapp

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// Format enumerates the output image formats accepted by the vendor app.
// Values were determined empirically by scanning with each ImageFormat
// setting and inspecting the output file:
//   - 0: raw uncompressed RGB (very large, ~26 MB per A4 page at 300 DPI)
//   - 1: JPEG (baseline, configurable quality)
//   - 6: PDF (single or multi-page, uses Haru PDF library internally)
type Format int

const (
	FormatJPEG Format = 1 // ImageFormat 1 — JPEG (default, portable)
	FormatPDF  Format = 6 // ImageFormat 6 — PDF (multi-page document)
	FormatRaw  Format = 0 // ImageFormat 0 — raw uncompressed RGB
	// FormatPNG is lossless PNG. Only the direct backend (our own driver)
	// produces it; the vendor app has no PNG setting we know of, so Build
	// rejects it.
	FormatPNG Format = 100
)

// ColorMode controls the color depth of the scan.
type ColorMode int

const (
	ColorRGB       ColorMode = 0 // 24-bit colour (default)
	ColorGrayscale ColorMode = 1 // 8-bit grayscale
	ColorLineArt   ColorMode = 2 // 1-bit black & white / line art
)

// DocumentSize selects the scan area. These are this package's own
// identifiers, NOT the vendor app's FixedDocumentSize values: every size is sent
// as an explicit ScanAreaWidth/Height with FixedDocumentSize = customArea.
type DocumentSize int

const (
	SizeA4      DocumentSize = iota // 210 × 297 mm
	SizeLetter                      // 8.5 × 11 in (215.9 × 279.4 mm)
	SizeLegal                       // 8.5 × 14 in — does not fit the V370 glass, rejected by Validate
	SizeMaxArea                     // whole glass
	SizeUserDef                     // WidthMM × HeightMM
)

// Flatbed glass of the V370 (measured: the vendor app's "whole glass" preset
// scans 215.7 × 297.0 mm).
const (
	GlassWidthMM  = 215.9
	GlassHeightMM = 297.0
)

// customArea is the FixedDocumentSize value that makes the vendor app use
// ScanAreaWidth/Height. Measured on the real scanner (2026-11-07): values
// 2–12 are presets that override the area (3 = A4, 5 = A5, 7 = A6, 2/11 =
// whole glass, …); 0 uses the explicit area. See knowledge.md §5.
const customArea = 0

// Settings holds the parameters for a single scan job.
type Settings struct {
	Resolution int          // DPI (50–6400 for V370)
	Format     Format       // output image format
	ColorMode  ColorMode    // colour depth
	Size       DocumentSize // paper size
	// WidthMM and HeightMM are used only when Size == SizeUserDef.
	WidthMM  float64 // scan area width in millimetres
	HeightMM float64 // scan area height in millimetres
	// OutputDir selects the vendor app internal save folder index.
	// Known values: 2 = Desktop. Ignored when OutputPath is set.
	OutputDir int
	// OutputPath sets an absolute save directory. When non-empty, Folder is
	// set to 3 (user-defined) and UserDefinePath is set to this value.
	OutputPath string
	// FileNamePrefix is the prefix for the output filename.
	FileNamePrefix string
	// JpegQuality controls the JPEG compression quality (1–100).
	// Used for JpegQuality, JpegQualityForJpeg, and JpegQualityForPdf.
	// Default is 85; set to 100 for maximum quality.
	JpegQuality int
	// PDFLossless asks for a lossless page image in PDF output (direct
	// backend only; the vendor app always embeds JPEG).
	PDFLossless bool
}

// Default returns a Settings with sensible defaults: 300 DPI, A4, JPEG, RGB.
func Default() Settings {
	return Settings{
		Resolution:     300,
		Format:         FormatJPEG,
		ColorMode:      ColorRGB,
		Size:           SizeA4,
		OutputDir:      2, // Desktop (the vendor app's internal folder index)
		FileNamePrefix: "scan",
		JpegQuality:    85,
	}
}

// mmToPixels converts millimetres to pixels at the given DPI.
//
// 1 inch = 25.4 mm, so pixels = mm / 25.4 * dpi.
func mmToPixels(mm float64, dpi int) int {
	return int(math.Round(mm / 25.4 * float64(dpi)))
}

// areaMM returns the scan area in millimetres.
func (r Settings) areaMM() (width, height float64, err error) {
	switch r.Size {
	case SizeA4:
		return 210, 297, nil
	case SizeLetter:
		return 215.9, 279.4, nil
	case SizeLegal:
		return 215.9, 355.6, nil
	case SizeMaxArea:
		return GlassWidthMM, GlassHeightMM, nil
	case SizeUserDef:
		return r.WidthMM, r.HeightMM, nil
	default:
		return 0, 0, fmt.Errorf("unknown document size %d", r.Size)
	}
}

// scanArea returns (width, height) in pixels for a validated request.
func (r Settings) scanArea() (width, height int) {
	w, h, _ := r.areaMM()
	return mmToPixels(w, r.Resolution), mmToPixels(h, r.Resolution)
}

// folderValue returns the Folder parameter value. When OutputPath is set,
// the folder is set to 3 (user-defined) so that UserDefinePath is used.
func folderValue(r Settings) int {
	if r.OutputPath != "" {
		return 3 // user-defined
	}
	return r.OutputDir
}

// userDefinePath returns the save path for user-defined folder mode.
func userDefinePath(r Settings) string {
	if r.OutputPath != "" {
		return r.OutputPath
	}
	return ""
}

// typedInt is the JSON encoding for an integer parameter: {"int": value}.
type typedInt struct {
	Int int `json:"int"`
}

// typedString is the JSON encoding for a string parameter: {"string": value}.
type typedString struct {
	String string `json:"string"`
}

// presetGroup is the flat map of parameter name → typed value.
// We use json.RawMessage so int and string values can coexist.
type presetGroup map[string]json.RawMessage

func intParam(v int) json.RawMessage {
	b, _ := json.Marshal(typedInt{Int: v})
	return json.RawMessage(b)
}

func strParam(v string) json.RawMessage {
	b, _ := json.Marshal(typedString{String: v})
	return json.RawMessage(b)
}

// sf2File is the top-level structure of an .SF2 settings file.
type sf2File struct {
	Preset []map[string][]presetGroup `json:"Preset"`
}

// MaxPrefixLen is the maximum length of a filename prefix.
const MaxPrefixLen = 128

// ValidatePrefix checks a filename prefix. The prefix becomes part of file
// paths (the vendor app output, glob patterns, the combined multi-page PDF), so
// path separators, glob metacharacters, control characters and a leading
// dot (hidden files, "..") are rejected.
func ValidatePrefix(p string) error {
	if p == "" {
		return fmt.Errorf("filename prefix is empty")
	}
	if len(p) > MaxPrefixLen {
		return fmt.Errorf("filename prefix longer than %d bytes", MaxPrefixLen)
	}
	if strings.HasPrefix(p, ".") {
		return fmt.Errorf("filename prefix %q must not start with '.'", p)
	}
	if strings.ContainsAny(p, `/\*?[]`) {
		return fmt.Errorf("filename prefix %q contains a path separator or glob character", p)
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return fmt.Errorf("filename prefix contains a control character")
		}
	}
	return nil
}

// Validate checks a scan request without building it.
func Validate(r Settings) error {
	if r.Resolution < 50 || r.Resolution > 6400 {
		return fmt.Errorf("resolution %d out of range (50–6400)", r.Resolution)
	}
	if r.JpegQuality < 1 || r.JpegQuality > 100 {
		return fmt.Errorf("jpeg quality %d out of range (1–100)", r.JpegQuality)
	}
	w, h, err := r.areaMM()
	if err != nil {
		return err
	}
	const tolerance = 0.05 // mm, rounding of inch-based sizes
	if !(w > 0 && h > 0 && w <= GlassWidthMM+tolerance && h <= GlassHeightMM+tolerance) {
		return fmt.Errorf("scan area %.1f × %.1f mm does not fit the glass (max %.1f × %.1f mm)",
			w, h, GlassWidthMM, GlassHeightMM)
	}
	return ValidatePrefix(r.FileNamePrefix)
}

// Build generates the .SF2 JSON content for the given scan request.
// The returned bytes include a UTF-8 BOM prefix, as required by the vendor app.
// The bytes can be written directly to a .SF2 file.
func Build(r Settings) ([]byte, error) {
	if err := Validate(r); err != nil {
		return nil, err
	}
	if r.Format == FormatPNG {
		return nil, fmt.Errorf("format png is only available with the direct backend (--direct)")
	}

	w, h := r.scanArea()

	jsonBytes, err := buildJSON(r, w, h)
	if err != nil {
		return nil, err
	}

	// The vendor app expects a UTF-8 BOM at the start of .SF2 files.
	bom := []byte{0xEF, 0xBB, 0xBF}
	return append(bom, jsonBytes...), nil
}

// buildJSON generates the .SF2 JSON body (without BOM) for the given scan request.
func buildJSON(r Settings, w, h int) ([]byte, error) {
	g := presetGroup{
		"AFMMode":                         intParam(0),
		"AFMTimeout":                      intParam(180),
		"AddPages":                        intParam(0),
		"AutoColorPixelLevel":             intParam(2),
		"AutoColorPixelType":              intParam(0),
		"AutoCroppingInscribed":           intParam(0),
		"AutoSize":                        intParam(0),
		"BackgroundColor":                 intParam(0),
		"BackgroundRemoval":               intParam(0),
		"BlankPageSkip":                   intParam(0),
		"BlankPageSkipLevel":              intParam(10),
		"Brightness":                      intParam(0),
		"ColorType":                       intParam(int(r.ColorMode)),
		"Contrast":                        intParam(0),
		"DeficiencyCorrection":            intParam(0),
		"DoubleFeedDetection":             intParam(1),
		"DoubleFeedDetectionAreaLength":   intParam(1550),
		"DoubleFeedDetectionAreaMin":      intParam(0),
		"DoubleFeedDetectionLevel":        intParam(0),
		"DropoutColor":                    intParam(0),
		"DuplexType":                      intParam(0),
		"FileNameCounter":                 intParam(0),
		"FileNameOverWrite":               intParam(1),
		"FileNamePrefix":                  strParam(r.FileNamePrefix),
		"FixedDocumentSize":               intParam(customArea),
		"Folder":                          intParam(folderValue(r)),
		"FunctionalUnit":                  intParam(0), // 0 = flatbed
		"FunctionalUnit_Auto":             intParam(0),
		"Gamma":                           intParam(22),
		"ImageFormat":                     intParam(int(r.Format)),
		"ImageOption":                     intParam(0),
		"JpegQuality":                     intParam(r.JpegQuality),
		"JpegQualityForJpeg":              intParam(r.JpegQuality),
		"JpegQualityForPdf":               intParam(r.JpegQuality),
		"MultiTiffCompression":            intParam(0),
		"MultiTiffEnabled":                intParam(0),
		"NearDurationAlert":               intParam(0),
		"OFDEnabled":                      intParam(0),
		"Orientation":                     intParam(0),
		"OverDurationAlert":               intParam(0),
		"PDFAllPages":                     intParam(1),
		"PDFSelectPage":                   intParam(1),
		"PagesTobeScanned":                intParam(0),
		"PaperDeskew":                     intParam(1),
		"PaperEndDetection":               intParam(0),
		"PaperEndDetectionMemory":         intParam(0),
		"RemovePunchHole":                 intParam(0),
		"Resolution":                      intParam(r.Resolution),
		"ScanAreaHeight":                  intParam(h),
		"ScanAreaOffsetX":                 intParam(0),
		"ScanAreaOffsetY":                 intParam(0),
		"ScanAreaWidth":                   intParam(w),
		"Threshold":                       intParam(110),
		"TransferCompression":             intParam(1),
		"UserDefinePath":                  strParam(userDefinePath(r)),
		"UserScanAreaHeight":              intParam(0),
		"UserScanAreaWidth":               intParam(0),
		"continuousAutoFeedingMode":       intParam(0),
		"documentType":                    intParam(0),
		"focusPosition":                   intParam(0),
		"jpegProgressive":                 intParam(0),
		"textEnhance":                     intParam(0),
		"textEnhanceNoiseReductionLevel":  intParam(1),
		"textEnhancePaperCreaseReduction": intParam(1),
		"textEnhanceSensitivity":          intParam(0),
		"thresholdAdjustment":             intParam(0),
	}

	file := sf2File{
		Preset: []map[string][]presetGroup{
			{
				"0": {g},
			},
		},
	}

	return json.MarshalIndent(file, "", "    ")
}
