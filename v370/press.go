package v370

import (
	"context"
	"time"
)

// Press duration.
//
// `1e 85` reports a press once, however long the button is held. The
// firmware's button sampler (0x02004830, every 25 ms) keeps the current
// input state in RAM 0x452 (active low: bit 4 Power, 5 Printer, 6 Email,
// 7 PDF; 0xF0 = nothing held) and latches a press after 10 stable samples
// (≈ 250 ms). Reading 0x452 with the read-only memory read therefore tells
// whether a button is still held (verified on the scanner 2026-11-08).

// ramButtonInput is the sampler's current input byte.
const ramButtonInput = 0x452

// PressDebounce is how long a button must be held before the firmware
// reports it (10 samples).
const PressDebounce = 250 * time.Millisecond

// heldButton maps the input byte to the single button held; ButtonNone if
// none or several are held.
func heldButton(raw byte) Button {
	switch raw & 0xF0 {
	case 0xE0:
		return ButtonPower
	case 0xD0:
		return ButtonPrinter
	case 0xB0:
		return ButtonEmail
	case 0x70:
		return ButtonPDF
	}
	return ButtonNone
}

// HeldButton returns the button held right now (ButtonNone if none or
// several). Never call it during a scan.
func (s *Scanner) HeldButton(ctx context.Context) (Button, error) {
	b, err := s.ReadMemory(ctx, ramButtonInput, 1)
	if err != nil {
		return ButtonNone, err
	}
	return heldButton(b[0]), nil
}

// PressDuration is called right after ReadButton reported b: it polls
// every poll until b is released and returns how long b was held (from
// the start of the press, i.e. including PressDebounce; the time between
// the latch and the ReadButton call is not known and counts as zero). It
// stops at limit and returns limit.
func (s *Scanner) PressDuration(ctx context.Context, b Button, poll, limit time.Duration) (time.Duration, error) {
	start := time.Now()
	for {
		held, err := s.HeldButton(ctx)
		if err != nil {
			return 0, err
		}
		d := PressDebounce + time.Since(start)
		if held != b {
			return d, nil
		}
		if d >= limit {
			return limit, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(poll):
		}
	}
}
