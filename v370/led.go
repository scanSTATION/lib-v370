package v370

import (
	"bytes"
	"context"
	"fmt"

	"github.com/scanSTATION/lib-v370/firmware"
)

// LED is a state of the panel LED (a two-colour LED on two outputs).
type LED uint8

const (
	LEDGreen  LED = 0 // normal
	LEDOrange LED = 1
	LEDOff    LED = 2 // needs firmware.LEDPatch; stock firmware shows green
)

func (l LED) String() string {
	switch l {
	case LEDGreen:
		return "green"
	case LEDOrange:
		return "orange"
	case LEDOff:
		return "off"
	}
	return fmt.Sprintf("led-%d", uint8(l))
}

// cmdLED is `1e de` + 1 byte (firmware handler 0x02006A12): stock, it sets
// RAM 0x3C2 bit 2 to the parameter's bit 0 and the timer tick shows orange
// instead of green (and skips the button sampling meanwhile). With
// firmware.LEDPatch bit 1 selects off, and buttons work in every state.
// Not used by the vendor app.
var cmdLED = []byte{0x1e, 0xde}

// SetLED sets the panel LED. Only while idle (never during a scan).
func (s *Scanner) SetLED(ctx context.Context, l LED) error {
	if l > LEDOff {
		return fmt.Errorf("v370: LED state %d", l)
	}
	if err := s.c.commandParam(ctx, cmdLED, []byte{byte(l)}); err != nil {
		return fmt.Errorf("v370: LED: %w", err)
	}
	return nil
}

// LEDPatched reports whether the running firmware has firmware.LEDPatch
// (read back from RAM with the read-only memory read).
func (s *Scanner) LEDPatched(ctx context.Context) (bool, error) {
	m := firmware.LEDPatchMarker
	b, err := s.ReadMemory(ctx, m.Address, len(m.Bytes))
	if err != nil {
		return false, err
	}
	return bytes.Equal(b, m.Bytes), nil
}
