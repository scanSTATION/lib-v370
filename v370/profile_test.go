package v370_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"github.com/scanSTATION/lib-v370/internal/testtraces"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/v370"
)

// recordedImage returns the bytes of the largest 1e 47 data read in a trace.
func recordedImage(t *testing.T, tr *trace.Trace) []byte {
	t.Helper()
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

func loadTrace(t *testing.T, name string) *trace.Trace {
	t.Helper()
	tr, err := trace.Load(testtraces.Path(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// Extract a profile from a capture, replay it with our driver against the
// simulator, and get exactly the recorded image bytes.
func TestProfileReplaysRecordedScan(t *testing.T) {
	for _, name := range []string{"a4-75dpi-poweron", "a4-300dpi-jpeg"} {
		t.Run(name, func(t *testing.T) {
			tr := loadTrace(t, name)
			p, err := v370.ExtractProfile(tr, name, "test")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("profile: %d steps, scan %d dpi %dx%d mode %#x, image %d bytes",
				len(p.Steps), p.ScanDPI, p.WidthPx, p.HeightPx, p.Mode, p.ImageBytes)

			tp, r := simulator(t, name)
			s := open(t, tp)
			var img bytes.Buffer
			calib := map[string]int{}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			err = s.RunProfile(ctx, p, func(tag string, b []byte) error {
				if tag == v370.TagImage {
					img.Write(b)
				} else {
					calib[tag] += len(b)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("%v (simulator %+v)", err, r.Stats())
			}
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			want := recordedImage(t, tr)
			if img.Len() != p.ImageBytes || sha256.Sum256(img.Bytes()) != sha256.Sum256(want) {
				t.Fatalf("image %d bytes differs from recorded %d bytes", img.Len(), len(want))
			}
			if len(calib) == 0 {
				t.Fatal("no calibration reads")
			}
			checkExact(t, r)
			t.Logf("calibration reads: %v; simulator: %d commands", calib, r.Stats().Commands)
		})
	}
}

func TestProfileJSONRoundTrip(t *testing.T) {
	p, err := v370.ExtractProfile(loadTrace(t, "a4-75dpi-poweron"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	b, err := jsonMarshal(p)
	if err != nil {
		t.Fatal(err)
	}
	q, err := v370.ParseProfile(b)
	if err != nil || len(q.Steps) != len(p.Steps) || q.ImageBytes != p.ImageBytes {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := v370.ParseProfile([]byte(`{"name":"x","steps":[{"k":"write"}]}`)); err == nil {
		t.Fatal("invalid profile accepted")
	}
}

// Every built-in profile replays its source capture exactly.
func TestBuiltinProfiles(t *testing.T) {
	sources := map[string]string{
		"a4-100dpi-color":        "a4-75dpi-poweron",
		"a4-200dpi-color":        "a4-150dpi-color",
		"a4-300dpi-color":        "a4-300dpi-jpeg",
		"a4-600dpi-color":        "a4-600dpi-color",
		"user-148x210mm-1200dpi": "user-148x210mm-1200dpi",
		"a4-300dpi-gray":         "a4-300dpi-gray",
		"user-100x50mm-300dpi":   "user-100x50mm-300dpi",
		"user-50x50mm-600dpi":    "user-50x50mm-600dpi",
		"user-25x25mm-1200dpi":   "user-25x25mm-1200dpi",
	}
	names := v370.ProfileNames()
	if len(names) != len(sources) {
		t.Fatalf("built-in profiles %v, want %d", names, len(sources))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			p, err := v370.LoadProfile(name)
			if err != nil {
				t.Fatal(err)
			}
			src := sources[name]
			tr := loadTrace(t, src)
			tp, r := simulator(t, src)
			s := openAt(t, tp, clockOf(t, tr))
			h := sha256.New()
			n := 0
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := s.RunProfile(ctx, p, func(tag string, b []byte) error {
				if tag == v370.TagImage {
					h.Write(b)
					n += len(b)
				}
				return nil
			}); err != nil {
				t.Fatalf("%v (simulator %+v)", err, r.Stats())
			}
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			want := sha256.Sum256(recordedImage(t, tr))
			if n != p.ImageBytes || !bytes.Equal(h.Sum(nil), want[:]) {
				t.Fatalf("image differs (%d bytes)", n)
			}
			checkExact(t, r)
		})
	}
	if _, err := v370.LoadProfile("nope"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}
