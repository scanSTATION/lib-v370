package v370

import (
	"bytes"
	"encoding/binary"
)

// Carriage start position.
//
// Before the image pass the host moves the carriage from the calibration
// position to the start of the scan (`1e 01` with this tail). With the
// vendor app's step counts the first scanned line lies ≈ 1.2 mm after the
// glass corner, so the top of a page placed in the corner is never
// captured and the scan runs that far past its lower edge. Measured on the
// real scanner (2026-11-08, sheet of known length in the corner, lid open;
// docs/protocol.md "Scan origin"): with the step counts reduced by
// startCorrection the first line is the glass corner within ±0.1 mm at
// every resolution. The motor moves ≈ 93.4 steps per mm.
var preMoveTail = []byte{0x00, 0x00, 0x3f, 0x00, 0x3f, 0x00, 0x18, 0x00}

// startCorrection is the number of pre-move steps to omit per scan
// resolution (vendor → corrected: 568 → 468, 556 → 446, 752 → 639,
// 748 → 632, 812 → 695).
var startCorrection = map[int]uint32{100: 100, 200: 110, 300: 113, 600: 116, 1200: 117}

// IsPreMove reports whether b is the `1e 01` parameter of the move to the
// scan start.
func IsPreMove(b []byte) bool { return len(b) == 12 && bytes.Equal(b[4:], preMoveTail) }

// cornerStart returns the pre-move parameter b corrected for a scan at dpi
// so that the scan starts at the glass corner; b itself if dpi has no
// measured correction.
func cornerStart(b []byte, dpi int) []byte {
	corr, ok := startCorrection[dpi]
	if !ok || !IsPreMove(b) {
		return b
	}
	steps := binary.LittleEndian.Uint32(b)
	if steps <= corr {
		return b
	}
	out := append([]byte(nil), b...)
	binary.LittleEndian.PutUint32(out, steps-corr)
	return out
}
