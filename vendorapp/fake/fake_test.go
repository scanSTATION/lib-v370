package fake

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	scanconfig "github.com/scanSTATION/lib-v370/vendorapp"
)

func sf2(t *testing.T, mut func(*scanconfig.Settings)) string {
	t.Helper()
	r := scanconfig.Default()
	r.Resolution = 75
	mut(&r)
	b, err := scanconfig.Build(r)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "s.SF2")
	os.WriteFile(p, b, 0o644)
	return p
}

func env(t *testing.T, home string, getenv map[string]string) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return Env{
		Getenv: func(k string) string { return getenv[k] },
		Home:   home, Stdout: &out, Stderr: &errb,
	}, &out, &errb
}

func TestParseSF2FromScanconfig(t *testing.T) {
	b, _ := os.ReadFile(sf2(t, func(r *scanconfig.Settings) { r.JpegQuality = 100; r.FileNamePrefix = "x" }))
	s, err := ParseSF2(b)
	if err != nil {
		t.Fatal(err)
	}
	// A4 at 75 DPI = 620 x 877 px
	if s.Resolution != 75 || s.ScanAreaWidth != 620 || s.ScanAreaHeight != 877 || s.JpegQuality != 100 ||
		s.FileNamePrefix != "x" || s.Folder != 2 || s.ImageFormat != FormatJPEG {
		t.Fatalf("settings = %+v", s)
	}
}

func TestListAndScanJPEG(t *testing.T) {
	home := t.TempDir()
	e, out, _ := env(t, home, nil)
	if Run([]string{"--list"}, e) != 0 || !strings.Contains(out.String(), "device ID :"+DeviceName) {
		t.Fatalf("list output: %s", out)
	}
	p := sf2(t, func(r *scanconfig.Settings) { r.FileNamePrefix = "page" })
	e, _, errb := env(t, home, nil)
	if code := Run([]string{"--scan", DeviceName, p}, e); code != 0 {
		t.Fatalf("scan failed: %s", errb)
	}
	f, err := os.Open(filepath.Join(home, "Desktop", "page.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil || cfg.Width != 620 || cfg.Height != 877 {
		t.Fatalf("jpeg %dx%d err=%v", cfg.Width, cfg.Height, err)
	}
}

func TestScanPDFIsValid(t *testing.T) {
	home := t.TempDir()
	p := sf2(t, func(r *scanconfig.Settings) {
		r.Format = scanconfig.FormatPDF
		r.ColorMode = scanconfig.ColorGrayscale
	})
	e, _, errb := env(t, home, nil)
	if Run([]string{"--scan", DeviceName, p}, e) != 0 {
		t.Fatalf("scan failed: %s", errb)
	}
	pdf := filepath.Join(home, "Desktop", "scan.pdf")
	b, _ := os.ReadFile(pdf)
	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.Contains(b, []byte("/DeviceGray")) {
		t.Fatal("not a grayscale PDF")
	}
	if _, err := exec.LookPath("pdfinfo"); err == nil {
		out, err := exec.Command("pdfinfo", pdf).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "Pages:           1") || strings.Contains(string(out), "Syntax Error") {
			t.Fatalf("pdfinfo: %v\n%s", err, out)
		}
	}
}

func TestFailureInjectionAndLog(t *testing.T) {
	dir := t.TempDir()
	ctl := filepath.Join(dir, "ctl.json")
	logf := filepath.Join(dir, "log.jsonl")
	vars := map[string]string{"VENDORAPP_FAKE_CONTROL": ctl, "VENDORAPP_FAKE_LOG": logf}

	os.WriteFile(ctl, []byte(`{"fail":["scan"]}`), 0o644)
	e, _, errb := env(t, dir, vars)
	if Run([]string{"--scan", DeviceName, sf2(t, func(*scanconfig.Settings) {})}, e) == 0 ||
		!strings.Contains(errb.String(), "injected failure") {
		t.Fatal("expected injected scan failure")
	}
	os.WriteFile(ctl, []byte(`{"no_devices":true}`), 0o644)
	e, out, _ := env(t, dir, vars)
	Run([]string{"--list"}, e)
	if strings.Contains(out.String(), "device ID") {
		t.Fatal("no_devices must hide the scanner")
	}
	b, _ := os.ReadFile(logf)
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Fatalf("expected 2 log lines, got %d: %s", n, b)
	}
}

func TestRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"garbage":    "not json",
		"no preset":  `{"Preset":[]}`,
		"traversal":  `{"Preset":[{"0":[{"Resolution":{"int":75},"ScanAreaWidth":{"int":10},"ScanAreaHeight":{"int":10},"FileNamePrefix":{"string":"../x"},"Folder":{"int":2},"ImageFormat":{"int":1}}]}]}`,
		"huge area":  `{"Preset":[{"0":[{"Resolution":{"int":75},"ScanAreaWidth":{"int":100000},"ScanAreaHeight":{"int":100000},"FileNamePrefix":{"string":"x"},"Folder":{"int":2},"ImageFormat":{"int":1}}]}]}`,
		"bad format": `{"Preset":[{"0":[{"Resolution":{"int":75},"ScanAreaWidth":{"int":10},"ScanAreaHeight":{"int":10},"FileNamePrefix":{"string":"x"},"Folder":{"int":2},"ImageFormat":{"int":9}}]}]}`,
		"wrong type": `{"Preset":[{"0":[{"Resolution":{"string":"75"}}]}]}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSF2([]byte(in)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	e, _, _ := env(t, t.TempDir(), nil)
	if Run([]string{"--scan", "Other Scanner", sf2(t, func(*scanconfig.Settings) {})}, e) == 0 {
		t.Fatal("unknown device must fail")
	}
}

// The fake must reproduce the vendor app's preset behaviour, otherwise Tier-1
// tests cannot catch a wrong FixedDocumentSize (the bug fixed in 2026-11).
func TestPresetOverridesArea(t *testing.T) {
	sf2 := func(fds int) []byte {
		return []byte(fmt.Sprintf(`{"Preset":[{"0":[{"Resolution":{"int":75},"ScanAreaWidth":{"int":295},"ScanAreaHeight":{"int":148},`+
			`"FixedDocumentSize":{"int":%d},"FileNamePrefix":{"string":"x"},"Folder":{"int":2},"ImageFormat":{"int":1}}]}]}`, fds))
	}
	cases := map[int][2]int{0: {295, 148}, 1: {295, 148}, 3: {618, 876}, 7: {308, 436}}
	for fds, want := range cases {
		s, err := ParseSF2(sf2(fds))
		if err != nil {
			t.Fatal(err)
		}
		if s.ScanAreaWidth != want[0] || s.ScanAreaHeight != want[1] {
			t.Errorf("FixedDocumentSize %d → %dx%d, want %dx%d", fds, s.ScanAreaWidth, s.ScanAreaHeight, want[0], want[1])
		}
	}
}
