package v370

import (
	"context"
	"strings"
	"testing"
)

func TestHeldButtonMapping(t *testing.T) {
	for raw, want := range map[byte]Button{0xF0: ButtonNone, 0xE0: ButtonPower, 0xD0: ButtonPrinter,
		0xB0: ButtonEmail, 0x70: ButtonPDF, 0x30: ButtonNone /* two held */, 0x7F: ButtonPDF} {
		if got := heldButton(raw); got != want {
			t.Errorf("0x%02x → %v, want %v", raw, got, want)
		}
	}
}

func TestReadMemoryRanges(t *testing.T) {
	s := &Scanner{} // the checks run before any I/O
	for _, tc := range []struct {
		addr uint32
		n    int
	}{{0x37F84E, 1}, {0xFFF, 2}, {0x0200FFFF, 2}, {0x450, 0}, {0x450, MaxMemoryRead + 1}} {
		if _, err := s.ReadMemory(context.Background(), tc.addr, tc.n); err == nil || !strings.Contains(err.Error(), "memory read") {
			t.Errorf("0x%x+%d: %v", tc.addr, tc.n, err)
		}
	}
}
