// Package fakescan is a test double for the vendor app CLI (Tier-1
// integration tests). It accepts the same command line as the vendor app
// (--list, --get-status, --scan <device> <file.SF2>), reads the .SF2 file our
// code generates, and writes a real, decodable JPEG/PDF/raw file whose size
// follows the requested resolution and scan area — so tests can verify the
// whole pipeline without the vendor app's binary or a scanner.
//
// Test hooks (environment variables):
//
//	VENDORAPP_FAKE_LOG      append one JSON line per invocation (args + parsed settings)
//	VENDORAPP_FAKE_CONTROL  JSON file read on every call: {"fail":["scan"],"no_devices":true}
package fake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scanSTATION/lib-v370/output"
)

// DeviceName and ModelID mirror what the real vendor app prints for a V370.
const (
	DeviceName = "Perfection V37/V370/GT-S640/F740"
	ModelID    = "ES00DD"
	maxPixels  = 64 << 20 // refuse absurd areas instead of exhausting memory
)

// Image formats (scanconfig.Format values).
const (
	FormatRaw  = 0
	FormatJPEG = 1
	FormatPDF  = 6
)

// Settings are the SF2 parameters the fake honours.
type Settings struct {
	Resolution     int `json:"Resolution"`
	ColorType      int `json:"ColorType"`
	ImageFormat    int `json:"ImageFormat"`
	ScanAreaWidth  int `json:"ScanAreaWidth"`
	ScanAreaHeight int `json:"ScanAreaHeight"`
	// FixedDocumentSize: preset values override ScanAreaWidth/Height, as in
	// the real vendor app (see presetMM).
	FixedDocumentSize int    `json:"FixedDocumentSize"`
	JpegQuality       int    `json:"JpegQuality"`
	FileNamePrefix    string `json:"FileNamePrefix"`
	Folder            int    `json:"Folder"`
	UserDefinePath    string `json:"UserDefinePath"`
}

// presetMM is the vendor app's FixedDocumentSize preset table for the V370,
// measured on the real scanner on 2026-11-07 (75 DPI scans). Any other value
// (0, 1, -1, …) means "use ScanAreaWidth/Height".
var presetMM = map[int][2]float64{
	2: {215.7, 297.0}, 3: {209.6, 296.7}, 4: {215.7, 209.6}, 5: {147.7, 209.6},
	6: {209.6, 147.7}, 7: {104.6, 147.7}, 8: {147.7, 104.6}, 9: {51.8, 73.5},
	10: {73.5, 51.8}, 11: {215.7, 297.0}, 12: {215.7, 256.4},
}

// applyPreset mimics the vendor app: a preset FixedDocumentSize replaces the
// requested area.
func (s *Settings) applyPreset() {
	if mm, ok := presetMM[s.FixedDocumentSize]; ok {
		s.ScanAreaWidth = int(mm[0] / 25.4 * float64(s.Resolution))
		s.ScanAreaHeight = int(mm[1] / 25.4 * float64(s.Resolution))
	}
}

// ParseSF2 decodes an .SF2 file ({"Preset":[{"0":[{key:{"int":v}|{"string":s}}]}]}).
func ParseSF2(b []byte) (Settings, error) {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	var doc struct {
		Preset []map[string][]map[string]map[string]json.RawMessage `json:"Preset"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Settings{}, fmt.Errorf("invalid SF2: %w", err)
	}
	if len(doc.Preset) == 0 || len(doc.Preset[0]["0"]) == 0 {
		return Settings{}, errors.New("invalid SF2: missing Preset[0][\"0\"][0]")
	}
	flat := map[string]any{}
	for k, typed := range doc.Preset[0]["0"][0] {
		if v, ok := typed["int"]; ok {
			var n int
			if err := json.Unmarshal(v, &n); err != nil {
				return Settings{}, fmt.Errorf("invalid SF2 int %s: %w", k, err)
			}
			flat[k] = n
		} else if v, ok := typed["string"]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return Settings{}, fmt.Errorf("invalid SF2 string %s: %w", k, err)
			}
			flat[k] = s
		}
	}
	j, _ := json.Marshal(flat)
	var s Settings
	if err := json.Unmarshal(j, &s); err != nil {
		return Settings{}, fmt.Errorf("invalid SF2 value types: %w", err)
	}
	if err := s.validate(); err != nil {
		return s, err
	}
	s.applyPreset()
	return s, nil
}

func (s Settings) validate() error {
	if s.Resolution < 50 || s.Resolution > 6400 {
		return fmt.Errorf("resolution %d out of range", s.Resolution)
	}
	if s.ScanAreaWidth <= 0 || s.ScanAreaHeight <= 0 {
		return fmt.Errorf("invalid scan area %dx%d", s.ScanAreaWidth, s.ScanAreaHeight)
	}
	if int64(s.ScanAreaWidth)*int64(s.ScanAreaHeight) > maxPixels {
		return fmt.Errorf("scan area %dx%d too large for the fake", s.ScanAreaWidth, s.ScanAreaHeight)
	}
	if s.FileNamePrefix == "" || strings.ContainsAny(s.FileNamePrefix, `/\`) || strings.HasPrefix(s.FileNamePrefix, ".") {
		return fmt.Errorf("invalid FileNamePrefix %q", s.FileNamePrefix)
	}
	switch s.ImageFormat {
	case FormatRaw, FormatJPEG, FormatPDF:
	default:
		return fmt.Errorf("unsupported ImageFormat %d", s.ImageFormat)
	}
	return nil
}

// OutputPath returns where the vendor app would save the file.
func (s Settings) OutputPath(home string) (string, error) {
	var dir string
	switch s.Folder {
	case 2:
		dir = filepath.Join(home, "Desktop")
	case 3:
		if !filepath.IsAbs(s.UserDefinePath) {
			return "", fmt.Errorf("UserDefinePath %q must be absolute", s.UserDefinePath)
		}
		dir = s.UserDefinePath
	default:
		return "", fmt.Errorf("Folder %d not supported by the fake (use 2 or 3)", s.Folder)
	}
	ext := map[int]string{FormatRaw: ".raw", FormatJPEG: ".jpg", FormatPDF: ".pdf"}[s.ImageFormat]
	return filepath.Join(dir, s.FileNamePrefix+ext), nil
}

// Render draws a deterministic test pattern of the requested size.
func (s Settings) Render() image.Image {
	w, h := s.ScanAreaWidth, s.ScanAreaHeight
	r := image.Rect(0, 0, w, h)
	if s.ColorType != 0 {
		img := image.NewGray(r)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Pix[y*img.Stride+x] = uint8((x + y) * 255 / (w + h))
			}
		}
		return img
	}
	img := image.NewRGBA(r)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

// Encode writes the scan in the requested format.
func (s Settings) Encode(w io.Writer) error {
	img := s.Render()
	q := s.JpegQuality
	if q < 1 || q > 100 {
		q = 85
	}
	switch s.ImageFormat {
	case FormatJPEG:
		return jpeg.Encode(w, img, &jpeg.Options{Quality: q})
	case FormatPDF:
		var jb bytes.Buffer
		if err := jpeg.Encode(&jb, img, &jpeg.Options{Quality: q}); err != nil {
			return err
		}
		return output.WriteJPEGPage(w, jb.Bytes(), s.ScanAreaWidth, s.ScanAreaHeight, s.Resolution, s.ColorType != 0)
	default: // raw: plain pixel bytes
		switch m := img.(type) {
		case *image.Gray:
			_, err := w.Write(m.Pix)
			return err
		case *image.RGBA:
			buf := make([]byte, 0, len(m.Pix)/4*3)
			for i := 0; i < len(m.Pix); i += 4 {
				buf = append(buf, m.Pix[i], m.Pix[i+1], m.Pix[i+2])
			}
			_, err := w.Write(buf)
			return err
		}
		return errors.New("unexpected image type")
	}
}

// Control is the failure-injection file.
type Control struct {
	Fail      []string `json:"fail"`       // commands that fail: "list", "status", "scan"
	NoDevices bool     `json:"no_devices"` // --list reports no scanner
	DelayMS   int      `json:"delay_ms"`   // sleep before scanning (max 10 s)
	Caution   string   `json:"caution"`    // text --get-status prints (scanner not ready)
}

func loadControl(path string) (Control, error) {
	var c Control
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("control file: %w", err)
	}
	return c, nil
}

func (c Control) fails(cmd string) bool {
	for _, f := range c.Fail {
		if f == cmd || f == "all" {
			return true
		}
	}
	return false
}

// Invocation is one line of the VENDORAPP_FAKE_LOG file.
type Invocation struct {
	Time     string    `json:"time"`
	Args     []string  `json:"args"`
	Settings *Settings `json:"settings,omitempty"`
	Output   string    `json:"output,omitempty"`
	ExitCode int       `json:"exit_code"`
}

// Env abstracts the process environment for testing.
type Env struct {
	Getenv func(string) string
	Home   string
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes the fake with the vendor app's arguments and returns the exit code.
func Run(args []string, env Env) int {
	inv := Invocation{Time: time.Now().UTC().Format(time.RFC3339Nano), Args: args}
	code := run(args, env, &inv)
	inv.ExitCode = code
	if p := env.Getenv("VENDORAPP_FAKE_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			json.NewEncoder(f).Encode(inv)
			f.Close()
		}
	}
	return code
}

func run(args []string, env Env, inv *Invocation) int {
	errf := func(format string, a ...any) int {
		fmt.Fprintf(env.Stderr, "vendorapp-fake: "+format+"\n", a...)
		return 1
	}
	ctl, err := loadControl(env.Getenv("VENDORAPP_FAKE_CONTROL"))
	if err != nil {
		return errf("%v", err)
	}
	if len(args) == 0 {
		return errf("usage: --list | --get-status | --scan <device> <settings.SF2>")
	}
	switch args[0] {
	case "--list", "-l":
		if ctl.fails("list") {
			return errf("injected failure: list")
		}
		fmt.Fprintln(env.Stdout, "=== List of available devices ==")
		if !ctl.NoDevices {
			fmt.Fprintf(env.Stdout, "device ID :%s\nModelID:%s\n", DeviceName, ModelID)
		}
		return 0
	case "--get-status", "-gs":
		// Like the real vendor app 6.7.82.0: silent on success; errors are
		// "ERROR : ..." with exit code 255.
		if ctl.fails("status") || ctl.NoDevices {
			fmt.Fprintln(env.Stderr, "ERROR : An unexpected error occurred. The vendor app will close.")
			return 255
		}
		if ctl.Caution != "" {
			fmt.Fprintln(env.Stdout, ctl.Caution)
		}
		return 0
	case "--scan", "-s":
		if len(args) != 3 {
			return errf("--scan needs <device> <settings.SF2>")
		}
		if ctl.fails("scan") {
			return errf("injected failure: scan")
		}
		if ctl.NoDevices || args[1] != DeviceName {
			return errf("device %q not found", args[1])
		}
		b, err := os.ReadFile(args[2])
		if err != nil {
			return errf("reading settings: %v", err)
		}
		s, err := ParseSF2(b)
		if err != nil {
			return errf("%v", err)
		}
		inv.Settings = &s
		out, err := s.OutputPath(env.Home)
		if err != nil {
			return errf("%v", err)
		}
		if d := ctl.DelayMS; d > 0 {
			time.Sleep(time.Duration(min(d, 10000)) * time.Millisecond)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return errf("%v", err)
		}
		f, err := os.Create(out)
		if err != nil {
			return errf("%v", err)
		}
		if err := s.Encode(f); err != nil {
			f.Close()
			os.Remove(out)
			return errf("encoding: %v", err)
		}
		if err := f.Close(); err != nil {
			return errf("%v", err)
		}
		inv.Output = out
		fmt.Fprintf(env.Stdout, "Scan completed: %s\n", out)
		return 0
	default:
		return errf("unknown option %q", args[0])
	}
}
