package vendorapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMmToPixels(t *testing.T) {
	tests := []struct {
		mm   float64
		dpi  int
		want int
	}{
		{210, 300, 2480}, // A4 width at 300 DPI
		{297, 300, 3508}, // A4 height at 300 DPI
		{210, 600, 4961}, // A4 width at 600 DPI
		{297, 600, 7016}, // A4 height at 600 DPI
		{25.4, 300, 300}, // 1 inch at 300 DPI
		{0, 300, 0},      // zero
	}
	for _, tt := range tests {
		got := mmToPixels(tt.mm, tt.dpi)
		if got != tt.want {
			t.Errorf("mmToPixels(%.1f, %d) = %d, want %d", tt.mm, tt.dpi, got, tt.want)
		}
	}
}

func TestBuildA4At300DPI(t *testing.T) {
	req := Default() // 300 DPI, A4, JPEG, RGB
	data, err := Build(req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Verify BOM prefix
	if len(data) < 3 || data[0] != 0xEF || data[1] != 0xBB || data[2] != 0xBF {
		t.Fatal("Generated .SF2 missing UTF-8 BOM")
	}

	// Parse the JSON back (skip BOM) to verify structure
	var file sf2File
	if err := json.Unmarshal(data[3:], &file); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	if len(file.Preset) != 1 {
		t.Fatalf("Expected 1 preset, got %d", len(file.Preset))
	}

	group, ok := file.Preset[0]["0"]
	if !ok {
		t.Fatal("Expected key '0' in preset")
	}
	if len(group) != 1 {
		t.Fatalf("Expected 1 group, got %d", len(group))
	}

	g := group[0]

	// Verify resolution
	checkIntParam(t, g, "Resolution", 300)

	// Verify scan area for A4 at 300 DPI
	checkIntParam(t, g, "ScanAreaWidth", 2480)
	checkIntParam(t, g, "ScanAreaHeight", 3508)

	// Verify functional unit (flatbed)
	checkIntParam(t, g, "FunctionalUnit", 0)

	// Verify format (JPEG = 1)
	checkIntParam(t, g, "ImageFormat", 1)

	// Verify color mode (RGB)
	checkIntParam(t, g, "ColorType", 0)

	// Verify folder (Desktop = 2)
	checkIntParam(t, g, "Folder", 2)

	// FixedDocumentSize must be the "explicit area" value; presets override ScanArea*
	checkIntParam(t, g, "FixedDocumentSize", 0) // explicit area, no preset
}

func TestBuildA4At600DPI(t *testing.T) {
	req := Settings{
		Resolution:     600,
		Format:         FormatPDF,
		ColorMode:      ColorGrayscale,
		Size:           SizeA4,
		FileNamePrefix: "doc",
		JpegQuality:    85,
	}
	data, err := Build(req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	var file sf2File
	if err := json.Unmarshal(data[3:], &file); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	g := file.Preset[0]["0"][0]

	checkIntParam(t, g, "Resolution", 600)
	checkIntParam(t, g, "ScanAreaWidth", 4961)
	checkIntParam(t, g, "ScanAreaHeight", 7016)
	checkIntParam(t, g, "ImageFormat", 6) // PDF
	checkIntParam(t, g, "ColorType", 1)   // Grayscale

	// Verify filename prefix
	if str, ok := g["FileNamePrefix"]; !ok {
		t.Error("FileNamePrefix not found")
	} else {
		var ts typedString
		if err := json.Unmarshal(str, &ts); err != nil {
			t.Fatalf("FileNamePrefix not a string: %v", err)
		}
		if ts.String != "doc" {
			t.Errorf("FileNamePrefix = %q, want %q", ts.String, "doc")
		}
	}
}

func TestBuildUserDefinedSize(t *testing.T) {
	req := Settings{
		Resolution:     150,
		Format:         FormatRaw,
		ColorMode:      ColorLineArt,
		Size:           SizeUserDef,
		WidthMM:        100,
		HeightMM:       150,
		FileNamePrefix: "custom",
		JpegQuality:    85,
	}
	data, err := Build(req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	var file sf2File
	if err := json.Unmarshal(data[3:], &file); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	g := file.Preset[0]["0"][0]

	checkIntParam(t, g, "Resolution", 150)
	// 100mm at 150 DPI = 100/25.4*150 = 590.55 → 591
	checkIntParam(t, g, "ScanAreaWidth", 591)
	// 150mm at 150 DPI = 150/25.4*150 = 885.83 → 886
	checkIntParam(t, g, "ScanAreaHeight", 886)
	checkIntParam(t, g, "FixedDocumentSize", 0) // explicit area (7 would be the A6 preset)
}

func TestBuildInvalidResolution(t *testing.T) {
	tests := []int{0, 49, 6401, 10000}
	for _, dpi := range tests {
		req := Default()
		req.Resolution = dpi
		_, err := Build(req)
		if err == nil {
			t.Errorf("Build with DPI %d should have failed", dpi)
		}
	}
}

func TestBuildInvalidJpegQuality(t *testing.T) {
	tests := []int{0, -1, 101, 200}
	for _, q := range tests {
		req := Default()
		req.JpegQuality = q
		_, err := Build(req)
		if err == nil {
			t.Errorf("Build with quality %d should have failed", q)
		}
	}
}

func TestBuildJpegQuality100(t *testing.T) {
	req := Default()
	req.JpegQuality = 100
	data, err := Build(req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	var file sf2File
	if err := json.Unmarshal(data[3:], &file); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	g := file.Preset[0]["0"][0]
	checkIntParam(t, g, "JpegQuality", 100)
	checkIntParam(t, g, "JpegQualityForJpeg", 100)
	checkIntParam(t, g, "JpegQualityForPdf", 100)
}

func TestBuildJpegQualityDefault(t *testing.T) {
	req := Default()
	if req.JpegQuality != 85 {
		t.Fatalf("Default JpegQuality = %d, want 85", req.JpegQuality)
	}
	data, err := Build(req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	var file sf2File
	if err := json.Unmarshal(data[3:], &file); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	g := file.Preset[0]["0"][0]
	checkIntParam(t, g, "JpegQuality", 85)
	checkIntParam(t, g, "JpegQualityForJpeg", 85)
	checkIntParam(t, g, "JpegQualityForPdf", 85)
}

func TestBuildLetterSize(t *testing.T) {
	req := Settings{
		Resolution:     300,
		Format:         FormatJPEG,
		ColorMode:      ColorRGB,
		Size:           SizeLetter,
		FileNamePrefix: "letter",
		JpegQuality:    85,
	}
	data, err := Build(req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	var file sf2File
	if err := json.Unmarshal(data[3:], &file); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	g := file.Preset[0]["0"][0]

	// Letter: 8.5 × 11 in = 215.9 × 279.4 mm
	// At 300 DPI: 215.9/25.4*300 = 2550, 279.4/25.4*300 = 3300
	checkIntParam(t, g, "ScanAreaWidth", 2550)
	checkIntParam(t, g, "ScanAreaHeight", 3300)
	checkIntParam(t, g, "FixedDocumentSize", 0) // explicit area (4 is not Letter)
}

// checkIntParam verifies that a parameter exists and has the expected int value.
func checkIntParam(t *testing.T, g presetGroup, key string, want int) {
	t.Helper()
	raw, ok := g[key]
	if !ok {
		t.Errorf("Parameter %q not found in settings", key)
		return
	}
	var ti typedInt
	if err := json.Unmarshal(raw, &ti); err != nil {
		t.Errorf("Parameter %q is not an int: %v", key, err)
		return
	}
	if ti.Int != want {
		t.Errorf("Parameter %q = %d, want %d", key, ti.Int, want)
	}
}

func TestValidatePrefix(t *testing.T) {
	ok := []string{"scan", "scan_001", "scan_1700000000", "Rechnung 2026-11", "a.b"}
	for _, p := range ok {
		if err := ValidatePrefix(p); err != nil {
			t.Errorf("ValidatePrefix(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{"", "../etc/x", "a/b", `a\b`, ".hidden", "..", "scan*", "s?", "s[1]", "a\x00b", "a\nb", strings.Repeat("x", MaxPrefixLen+1)}
	for _, p := range bad {
		if err := ValidatePrefix(p); err == nil {
			t.Errorf("ValidatePrefix(%q) = nil, want error", p)
		}
	}
	r := Default()
	r.FileNamePrefix = "../../tmp/evil"
	if _, err := Build(r); err == nil {
		t.Error("Build must reject a traversal prefix")
	}
}

func TestValidateGlassLimits(t *testing.T) {
	base := Default()
	base.Resolution = 75
	cases := []struct {
		name string
		mut  func(*Settings)
		ok   bool
	}{
		{"a4", func(r *Settings) { r.Size = SizeA4 }, true},
		{"letter", func(r *Settings) { r.Size = SizeLetter }, true},
		{"max", func(r *Settings) { r.Size = SizeMaxArea }, true},
		{"legal does not fit", func(r *Settings) { r.Size = SizeLegal }, false},
		{"user ok", func(r *Settings) { r.Size, r.WidthMM, r.HeightMM = SizeUserDef, 100, 50 }, true},
		{"user full glass", func(r *Settings) { r.Size, r.WidthMM, r.HeightMM = SizeUserDef, GlassWidthMM, GlassHeightMM }, true},
		{"user too wide", func(r *Settings) { r.Size, r.WidthMM, r.HeightMM = SizeUserDef, 220, 50 }, false},
		{"user too long", func(r *Settings) { r.Size, r.WidthMM, r.HeightMM = SizeUserDef, 100, 300 }, false},
		{"user zero", func(r *Settings) { r.Size, r.WidthMM, r.HeightMM = SizeUserDef, 0, 50 }, false},
		{"unknown size", func(r *Settings) { r.Size = DocumentSize(42) }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := base
			c.mut(&r)
			if err := Validate(r); (err == nil) != c.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestMaxAreaPixels(t *testing.T) {
	r := Default()
	r.Size = SizeMaxArea
	r.Resolution = 75
	if w, h := r.scanArea(); w != 638 || h != 877 {
		t.Fatalf("max area at 75 DPI = %dx%d, want 638x877", w, h)
	}
}
