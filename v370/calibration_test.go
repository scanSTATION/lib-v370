package v370

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/scanSTATION/lib-v370/internal/testtraces"
	"github.com/scanSTATION/lib-v370/sim/trace"
)

// geom41 builds a `1e 41` block with span start..end, lines and byte 15.
func geom41(start, end, lines int, b15 byte) []byte {
	g := make([]byte, 22)
	binary.LittleEndian.PutUint16(g[0:], uint16(start))
	binary.LittleEndian.PutUint16(g[2:], uint16(end))
	binary.LittleEndian.PutUint16(g[4:], uint16(lines))
	g[15] = b15
	return g
}

// read builds a calibration read whose sample (pixel x, channel ch) is f.
func synthRead(geom []byte, f func(x, ch int) int) calRead {
	r := calRead{geom: geom}
	w, lines := r.width(), r.lines()
	r.data = make([]byte, lines*w*6+8)
	for l := 0; l < lines; l++ {
		for x := 0; x < w; x++ {
			for ch := 0; ch < 3; ch++ {
				binary.LittleEndian.PutUint16(r.data[2*((l*w+x)*3+ch):], uint16(f(x, ch)))
			}
		}
	}
	return r
}

func u16(b []byte, i int) int { return int(binary.LittleEndian.Uint16(b[2*i:])) }

func synthCalibration(b15 byte) *calibration {
	white := func(x, ch int) int { return 20000 + 100*ch + 30*x }
	return &calibration{
		sensorMode: 4, widthPx: 75, scanDPI: 300, // 75 px at 300 dpi → 100 sensor px (+1 %)
		last41: geom41(81, 81+200, 3000, b15),
		reads: []calRead{
			synthRead(geom41(19, 35, 4, 0), func(x, ch int) int { return 2500 + 10*ch + x%2 }),
			synthRead(geom41(81, 281, 16, 0), white),
			synthRead(geom41(81, 281, 8, 0), func(x, ch int) int { return 1500 + 20*ch + x%7 }),
			synthRead(geom41(81, 281, 8, 0), func(x, ch int) int { return 1502 + 20*ch + x%7 }),
		},
	}
}

func TestCalibrationValues(t *testing.T) {
	c := synthCalibration(0x82)
	wo, err := c.value(CalcWhiteOffsets, levelsLen)
	if err != nil {
		t.Fatal(err)
	}
	// mean of 2500+10ch+{0,1} = 2500.5+10ch → rounds to 2501+10ch (half away from zero)
	for ch := range 3 {
		if u16(wo, ch) != 0 || u16(wo, 3+2*ch) != 2501+10*ch || u16(wo, 4+2*ch) != 2501+10*ch {
			t.Fatalf("white offsets %v", wo)
		}
	}
	il, err := c.value(CalcImageLevels, levelsLen)
	if err != nil {
		t.Fatal(err)
	}
	for ch := range 3 {
		// K = darkest white column (x = 0); offset = darkest dark column (mean of 1500 and 1502)
		if u16(il, ch) != 20000+100*ch || u16(il, 3+2*ch) != 1501+20*ch || u16(il, 4+2*ch) != 1501+20*ch {
			t.Fatalf("image levels %v", il)
		}
	}
	sh, err := c.value(CalcShading, 200*6)
	if err != nil {
		t.Fatal(err)
	}
	used := 101 // ceil(75 × 400/300 × 1.01)
	for x := 0; x < 200; x++ {
		for ch := range 3 {
			want := 30 * x
			if x >= used {
				want = 0
			}
			if got := u16(sh, x*3+ch); got != want {
				t.Fatalf("shading x=%d ch=%d: %d, want %d", x, ch, got, want)
			}
		}
	}
	// Small areas: table scaled ×2 (image `1e 41` byte 15 = 0x7_).
	sh2, _ := synthCalibration(0x72).value(CalcShading, 200*6)
	if u16(sh2, 50*3) != 2*30*50 {
		t.Fatalf("factor 2: %d", u16(sh2, 50*3))
	}
}

func TestCalibrationErrors(t *testing.T) {
	c := synthCalibration(0x82)
	if _, err := c.value(CalcShading, 100*6); err == nil {
		t.Error("table size mismatch accepted")
	}
	c.reads = c.reads[:2]
	if _, err := c.value(CalcImageLevels, levelsLen); err == nil {
		t.Error("missing dark reads accepted")
	}
	c = synthCalibration(0x82)
	c.reads[1].data = c.reads[1].data[:100]
	if _, err := c.value(CalcImageLevels, levelsLen); err == nil {
		t.Error("short read accepted")
	}
	c = synthCalibration(0x82)
	c.sensorMode = 9
	if _, err := c.value(CalcShading, 200*6); err == nil {
		t.Error("unknown sensor mode accepted")
	}
	if _, err := (&calibration{}).value(CalcWhiteOffsets, levelsLen); err == nil {
		t.Error("no reads accepted")
	}
}

// recordedCalibration replays a trace's host side: the calibration reads
// with their geometry, and the values the vendor app sent.
type recordedCalibration struct {
	cal                       calibration
	whiteOffsets, imageLevels []byte
	shading                   []byte
}

func loadRecordedCalibration(t *testing.T, tr *trace.Trace) recordedCalibration {
	t.Helper()
	var rc recordedCalibration
	cmd, header := "", []byte(nil)
	var geom, data []byte
	inData, seenWhite := false, false
	flush := func() {
		if inData && data != nil {
			rc.cal.reads = append(rc.cal.reads, calRead{geom: geom, data: data})
			if geomWidth(geom) > 64 {
				seenWhite = true
			}
		}
		inData, data = false, nil
	}
	for _, x := range tr.Transfers {
		if x.Type != trace.TypeBulk || x.Status != 0 {
			continue
		}
		if x.IsIn() {
			if inData && !(len(x.Data) == 1 && data == nil) {
				data = append(data, x.Data...)
			}
			continue
		}
		flush()
		d := x.Data
		if len(d) == 2 && (d[0] == 0x1b || d[0] == 0x1e) {
			cmd, header = fmt.Sprintf("%x", d), nil
			inData = cmd == "1e47"
			continue
		}
		switch {
		case cmd == "1e11" && len(d) == 1:
			rc.cal.sensorMode = d[0]
		case cmd == "1e41" && len(d) == 22:
			geom = d
			rc.cal.last41 = d
		case cmd == "1e42" && len(d) == levelsLen && !bytes.Equal(d, make([]byte, levelsLen)):
			if !seenWhite && rc.whiteOffsets == nil {
				rc.whiteOffsets = d
			} else {
				rc.imageLevels = d
			}
		case cmd == "1e84" && header == nil && len(d) == 8:
			header = d
		case cmd == "1e84" && header != nil && header[0] == 5:
			rc.shading = d
		}
	}
	// The image read is the last one; it is not a calibration read.
	if n := len(rc.cal.reads); n > 0 && len(rc.cal.reads[n-1].data) > 50<<20 || n > 0 && rc.cal.reads[n-1].lines() > 200 {
		rc.cal.reads = rc.cal.reads[:n-1]
	}
	return rc
}

func geomWidth(g []byte) int {
	if len(g) != 22 {
		return 0
	}
	return int(binary.LittleEndian.Uint16(g[2:])) - int(binary.LittleEndian.Uint16(g[0:]))
}

// The computed values agree with the vendor app's within the measurement
// noise (private recordings).
func TestCalibrationMatchesRecordings(t *testing.T) {
	for _, name := range []string{
		"a4-75dpi-poweron", "a4-150dpi-color", "a4-300dpi-jpeg", "a4-300dpi-gray", "a4-300dpi-lineart",
		"a4-600dpi-color", "a4-1200dpi-color", "a6-300dpi-userbug", "a6-600dpi-userbug", "a6-1200dpi-userbug",
		"user-100x50mm-300dpi", "user-148x210mm-1200dpi", "user-25x25mm-1200dpi",
		"user-50x50mm-400dpi", "user-50x50mm-600dpi", "user-50x50mm-800dpi",
	} {
		t.Run(name, func(t *testing.T) {
			tr, err := trace.Load(testtraces.Path(t, name))
			if err != nil {
				t.Fatal(err)
			}
			p, err := ExtractProfile(tr, name, "")
			if err != nil {
				t.Fatal(err)
			}
			rc := loadRecordedCalibration(t, tr)
			rc.cal.widthPx, rc.cal.scanDPI = p.WidthPx, p.ScanDPI
			if rc.whiteOffsets == nil || rc.imageLevels == nil || rc.shading == nil {
				t.Fatal("recorded calibration values not found")
			}
			wo, err := rc.cal.whiteOffsets()
			if err != nil {
				t.Fatal(err)
			}
			il, err := rc.cal.imageLevels()
			if err != nil {
				t.Fatal(err)
			}
			sh, err := rc.cal.shading(len(rc.shading))
			if err != nil {
				t.Fatal(err)
			}
			for ch := range 3 {
				// The vendor averages the short dark read slightly differently (up to ~55 of 65535).
				if d := u16(wo, 3+2*ch) - u16(rc.whiteOffsets, 3+2*ch); d < -80 || d > 80 {
					t.Errorf("white offset ch%d: %d vs recorded %d", ch, u16(wo, 3+2*ch), u16(rc.whiteOffsets, 3+2*ch))
				}
				if d := u16(il, 3+2*ch) - u16(rc.imageLevels, 3+2*ch); d < -40 || d > 40 {
					t.Errorf("image offset ch%d: %d vs recorded %d", ch, u16(il, 3+2*ch), u16(rc.imageLevels, 3+2*ch))
				}
				if d := u16(il, ch) - u16(rc.imageLevels, ch); d < -600 || d > 600 {
					t.Errorf("base K ch%d: %d vs recorded %d", ch, u16(il, ch), u16(rc.imageLevels, ch))
				}
			}
			// Compare the white reference the scanner rebuilds: K + S/f.
			f := rc.cal.shadingFactor()
			var se float64
			n := 0
			for i := 0; i < len(sh)/2; i++ {
				rec := u16(rc.shading, i)
				if rec == 0 || u16(sh, i) == 0 {
					continue
				}
				ch := i % 3
				want := float64(u16(rc.imageLevels, ch)) + float64(rec)/f
				got := float64(u16(il, ch)) + float64(u16(sh, i))/f
				se += (got - want) * (got - want) / (want * want)
				n++
			}
			if rms := math.Sqrt(se / float64(n)); n == 0 || rms > 0.015 {
				t.Errorf("white reference differs by %.4f rms over %d values", rms, n)
			} else {
				t.Logf("white reference rms difference %.4f over %d values (factor %.0f)", rms, n, f)
			}
		})
	}
}

// Built-in profiles compute every calibration value; none is stored.
func TestBuiltinProfilesComputeCalibration(t *testing.T) {
	for _, name := range ProfileNames() {
		p, err := LoadProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		cmd := ""
		var header []byte
		for i, s := range p.Steps {
			if s.Kind == StepCalc {
				got[s.Src]++
				continue
			}
			if s.Kind != StepWrite {
				continue
			}
			if isCmd(s.Data) {
				cmd, header = fmt.Sprintf("%x", s.Data), nil
				continue
			}
			switch {
			case cmd == "1e84" && header == nil && len(s.Data) == 8:
				header = s.Data
			case cmd == "1e84" && header != nil && header[0] == 5:
				t.Errorf("%s step %d: shading table stored", name, i)
			case cmd == "1e42" && !bytes.Equal(s.Data, make([]byte, len(s.Data))):
				t.Errorf("%s step %d: calibration levels stored", name, i)
			}
		}
		for _, src := range []string{CalcWhiteOffsets, CalcImageLevels, CalcShading} {
			if got[src] != 1 {
				t.Errorf("%s: %d calc steps %q, want 1", name, got[src], src)
			}
		}
	}
}
