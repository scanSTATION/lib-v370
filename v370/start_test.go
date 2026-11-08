package v370

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestCornerStart(t *testing.T) {
	for dpi, want := range map[int]uint32{100: 468, 200: 446, 300: 639, 600: 632, 1200: 695} {
		vendor := map[int]uint32{100: 568, 200: 556, 300: 752, 600: 748, 1200: 812}[dpi]
		b := make([]byte, 12)
		binary.LittleEndian.PutUint32(b, vendor)
		copy(b[4:], preMoveTail)
		got := cornerStart(b, dpi)
		if binary.LittleEndian.Uint32(got) != want || !IsPreMove(got) {
			t.Errorf("%d dpi: %x, want %d steps", dpi, got, want)
		}
		if binary.LittleEndian.Uint32(b) != vendor {
			t.Fatal("input modified")
		}
	}
	// Other moves and unmeasured resolutions are left alone.
	for _, tc := range []struct {
		hex string
		dpi int
	}{
		{"140000000400000000001800", 300}, // short move before the white read
		{"606e000068003f003f000800", 300}, // the scan move
		{"f002000000003f003f001800", 450}, // no measured correction
	} {
		b, _ := hex.DecodeString(tc.hex)
		if got := cornerStart(b, tc.dpi); hex.EncodeToString(got) != tc.hex {
			t.Errorf("%s at %d dpi changed to %x", tc.hex, tc.dpi, got)
		}
	}
}

// The built-in profiles contain exactly one pre-move each, with a
// measured correction for their resolution.
func TestBuiltinProfilesPreMove(t *testing.T) {
	for _, name := range ProfileNames() {
		p, err := LoadProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range p.Steps {
			if s.Kind == StepWrite && IsPreMove(s.Data) {
				n++
			}
		}
		if _, ok := startCorrection[p.ScanDPI]; n != 1 || !ok {
			t.Errorf("%s: %d pre-moves, correction for %d dpi: %v", name, n, p.ScanDPI, ok)
		}
	}
}

func TestCornerStartShortInput(t *testing.T) {
	if got := cornerStart([]byte{0x1e, 0x01}, 300); len(got) != 2 {
		t.Fatalf("%x", got)
	}
}
