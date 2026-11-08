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
// firmware.LEDPatch bit 1 selects off, buttons work in every state, and
// 0x80 (ledFast) makes the busy blink fast until the end of the job.
// Not used by the vendor app.
var cmdLED = []byte{0x1e, 0xde}

// ledFast is the `1e de` parameter that, with firmware.LEDPatch, makes the
// busy blink 4 times faster (0.125 s) and keeps the colour; the firmware
// clears it at the end of the job (`1b 40`). Stock firmware would read it as
// "green", so it is only sent to the patched firmware.
const ledFast = 0x80

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

// transferBlink makes the LED blink fast during the image pass, if the LED
// patch is known to run (this session uploaded it, or LEDPatched found it)
// and Options.NoTransferBlink is not set. Called while the scanner is ready,
// just before the image pass; it sends nothing else (no check mid-scan).
func (s *Scanner) transferBlink(ctx context.Context) error {
	if s.opt.NoTransferBlink || s.ledPatched == nil || !*s.ledPatched {
		return nil
	}
	if err := s.c.commandParam(ctx, cmdLED, []byte{ledFast}); err != nil {
		return fmt.Errorf("v370: LED fast blink: %w", err)
	}
	return nil
}

// LEDPatched reports whether the running firmware has firmware.LEDPatch
// (read back from RAM with the read-only memory read). The answer is kept
// for the session (fast blinking during the image pass needs it).
func (s *Scanner) LEDPatched(ctx context.Context) (bool, error) {
	m := firmware.LEDPatchMarker
	b, err := s.ReadMemory(ctx, m.Address, len(m.Bytes))
	if err != nil {
		return false, err
	}
	ok := bytes.Equal(b, m.Bytes)
	s.ledPatched = &ok
	return ok, nil
}
