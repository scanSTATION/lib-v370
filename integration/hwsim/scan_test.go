//go:build integration && hwsim

package hwsim

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scanSTATION/lib-v370/sim/trace"
)

// profileFor maps capture traces to the built-in profile extracted from them.
var profileFor = map[string]string{
	"a4-75dpi-poweron":     "a4-100dpi-color",
	"a4-150dpi-color":      "a4-200dpi-color",
	"a4-300dpi-jpeg":       "a4-300dpi-color",
	"a4-300dpi-gray":       "a4-300dpi-gray",
	"user-100x50mm-300dpi": "user-100x50mm-300dpi",
	"user-50x50mm-600dpi":  "user-50x50mm-600dpi",
	"user-25x25mm-1200dpi": "user-25x25mm-1200dpi",
}

// TestDirectScan runs the v370 binary (pure Go, no vendor software)
// against the simulator via usbfs → vhci-hcd and requires the recorded
// image bytes. Run: scripts/hwsim-test.sh TRACE --run TestDirectScan
func TestDirectScan(t *testing.T) {
	tracePath := env(t, "USBSIM_TRACE")
	statusURL := env(t, "USBSIM_STATUS_URL")
	base := strings.TrimSuffix(filepath.Base(tracePath), ".jsonl.gz")
	profile, ok := profileFor[base]
	if !ok {
		t.Skipf("no built-in profile for trace %s", base)
	}
	tr, err := trace.Load(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "v370")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/v370").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir := t.TempDir()
	raw := filepath.Join(dir, "scan.raw")
	args := []string{"scan", "-sim", "-profile", profile, "-keep-raw", "-o", filepath.Join(dir, "scan.png")}
	for _, x := range tr.Transfers {
		if x.Type == trace.TypeBulk && !x.IsIn() && len(x.Data) == 65536 { // trace includes a firmware upload
			fw := os.Getenv("LIBV370_FIRMWARE")
			if fw == "" {
				t.Skip("this trace needs the vendor's esfwdd.bin: set LIBV370_FIRMWARE=/path/to/esfwdd.bin")
			}
			args = append(args, "-firmware", fw)
			break
		}
	}
	out, err := exec.Command(bin, args...).CombinedOutput()
	t.Logf("v370 output:\n%s", out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := recordedImage(tr)
	if len(got) != len(want) || sha256.Sum256(got) != sha256.Sum256(want) {
		t.Fatalf("raw image (%d bytes) differs from the recorded one (%d bytes)", len(got), len(want))
	}
	// The decoded PNG and its metadata exist and match the profile.
	meta, err := os.ReadFile(filepath.Join(dir, "scan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		DPI    int `json:"dpi"`
		Height int `json:"height_px"`
		ReqH   int `json:"requested_height_px"`
	}
	if err := json.Unmarshal(meta, &m); err != nil || m.Height != m.ReqH || m.DPI == 0 {
		t.Fatalf("metadata %s: %v", meta, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "scan.png")); err != nil || fi.Size() == 0 {
		t.Fatalf("png: %v", err)
	}
	st := simStats(t, statusURL)
	t.Logf("simulator: commands=%d matched=%d fuzzy=%d unmatched=%d", st.Commands, st.Matched, st.Fuzzy, st.Unmatched)
	if st.Unmatched != 0 || st.Fuzzy > 1 { // the clock command may be fuzzy (current hour)
		t.Fatalf("diverged: %+v %+v", st.Divergences, st.FuzzyHits)
	}
}

// recordedImage returns the largest 1e 47 data read of the trace.
func recordedImage(tr *trace.Trace) []byte {
	var best, cur []byte
	inData := false
	for _, x := range tr.Transfers {
		if x.Type != trace.TypeBulk || x.Status != 0 {
			continue
		}
		if !x.IsIn() {
			if inData && len(cur) > len(best) {
				best = cur
			}
			inData = bytes.Equal(x.Data, []byte{0x1e, 0x47})
			cur = nil
			continue
		}
		if inData {
			cur = append(cur, x.Data...)
		}
	}
	return best
}
