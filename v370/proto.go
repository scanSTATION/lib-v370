// Package v370 speaks the USB protocol of the Perfection V37/V370
// directly, without the vendor app. Everything here is derived from USB
// captures of the real scanner (testdata/traces, knowledge.md §14/§15) and
// verified against them through the simulator.
//
// Framing as observed: the host writes a 2-byte command (ESC `1b xx` or
// `1e xx`) to EP 0x02 and reads the reply from EP 0x81. Commands either
// return data directly (queries) or an ACK byte 0x06, after which a
// parameter block may follow, again answered by ACK or data.
package v370

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/scanSTATION/lib-v370/usb"
)

const (
	ack = 0x06

	// replyBuf is the IN buffer size for short replies (one bulk packet).
	replyBuf = 512
	// DefaultIOTimeout bounds every single transfer.
	DefaultIOTimeout = 10 * time.Second
)

// Command bytes observed in the captures.
var (
	cmdStatus     = []byte{0x1b, 0x03}
	cmdFwBegin    = []byte{0x1b, 0x06}
	cmdIdentity   = []byte{0x1b, 0x13}
	cmdFwRestart  = []byte{0x1b, 0x16}
	cmdSession    = []byte{0x1e, 0x93}
	cmdClock      = []byte{0x1e, 0x8e}
	cmdRegister   = []byte{0x1e, 0x8f}
	cmdResolution = []byte{0x1e, 0x53}
)

// ErrUnexpectedReply means the scanner answered something other than the
// recorded protocol allows (e.g. a NAK instead of ACK).
var ErrUnexpectedReply = errors.New("v370: unexpected reply")

// conn adds framing to a usb.Transport.
type conn struct {
	tp      usb.Transport
	timeout time.Duration
}

func (c *conn) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, c.timeout)
}

func (c *conn) write(ctx context.Context, b []byte) error {
	cx, cancel := c.ctx(ctx)
	defer cancel()
	n, err := c.tp.BulkOut(cx, usb.EndpointOut, b)
	if err != nil {
		return fmt.Errorf("v370: write %s: %w", hexShort(b), err)
	}
	if n != len(b) {
		return fmt.Errorf("v370: short write %d/%d", n, len(b))
	}
	return nil
}

func (c *conn) read(ctx context.Context, max int) ([]byte, error) {
	cx, cancel := c.ctx(ctx)
	defer cancel()
	b, err := c.tp.BulkIn(cx, usb.EndpointIn, max)
	if err != nil {
		return nil, fmt.Errorf("v370: read: %w", err)
	}
	return b, nil
}

// readWithin reads one reply with an explicit timeout (for commands the
// scanner answers late, e.g. lamp measurements while the lamp warms up).
func (c *conn) readWithin(ctx context.Context, max int, timeout time.Duration) ([]byte, error) {
	cx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, err := c.tp.BulkIn(cx, usb.EndpointIn, max)
	if err != nil {
		return nil, fmt.Errorf("v370: read (waited up to %s): %w", timeout, err)
	}
	return b, nil
}

// query sends cmd and expects exactly n reply bytes.
func (c *conn) query(ctx context.Context, cmd []byte, n int) ([]byte, error) {
	if err := c.write(ctx, cmd); err != nil {
		return nil, err
	}
	r, err := c.read(ctx, max(n, replyBuf))
	if err != nil {
		return nil, err
	}
	if len(r) != n {
		return nil, fmt.Errorf("%w: %s returned %d bytes (%s), want %d", ErrUnexpectedReply, hexShort(cmd), len(r), hexShort(r), n)
	}
	return r, nil
}

// expectACK reads one reply and requires it to be ACK.
func (c *conn) expectACK(ctx context.Context, after []byte) error {
	r, err := c.read(ctx, replyBuf)
	if err != nil {
		return err
	}
	if !bytes.Equal(r, []byte{ack}) {
		return fmt.Errorf("%w: %s answered %s, want ACK", ErrUnexpectedReply, hexShort(after), hexShort(r))
	}
	return nil
}

// command sends cmd and expects ACK.
func (c *conn) command(ctx context.Context, cmd []byte) error {
	if err := c.write(ctx, cmd); err != nil {
		return err
	}
	return c.expectACK(ctx, cmd)
}

// commandParam sends cmd (→ ACK) and then param (→ ACK).
func (c *conn) commandParam(ctx context.Context, cmd, param []byte) error {
	if err := c.command(ctx, cmd); err != nil {
		return err
	}
	if err := c.write(ctx, param); err != nil {
		return err
	}
	return c.expectACK(ctx, param)
}

// commandQuery sends cmd (→ ACK), then param, and expects n data bytes.
func (c *conn) commandQuery(ctx context.Context, cmd, param []byte, n int) ([]byte, error) {
	if err := c.command(ctx, cmd); err != nil {
		return nil, err
	}
	return c.query(ctx, param, n)
}

func hexShort(b []byte) string {
	if len(b) > 12 {
		return fmt.Sprintf("%x… (%d bytes)", b[:12], len(b))
	}
	return fmt.Sprintf("%x", b)
}

// --- replies ---------------------------------------------------------------

// Status is the 2-byte reply to `1b 03`. Bit meanings are inferred from the
// captures (power-on: 28 00 → 1a 01 → 5a 01 → 1a 01 → 18 01; scanning: 58 01).
type Status [2]byte

const (
	statusWarmingUp  = 0x02
	statusReadyBase  = 0x10
	statusNoFirmware = 0x20
	statusBusy       = 0x40
)

// FirmwareLoaded reports whether the firmware is present.
func (s Status) FirmwareLoaded() bool { return s[0]&statusNoFirmware == 0 }

// WarmingUp reports the lamp/initialisation phase after power-on.
func (s Status) WarmingUp() bool { return s[0]&statusWarmingUp != 0 }

// Busy reports a running operation (carriage moving, scanning).
func (s Status) Busy() bool { return s[0]&statusBusy != 0 }

// Ready reports an idle, initialised scanner (observed as `18 01`).
func (s Status) Ready() bool { return s.FirmwareLoaded() && !s.WarmingUp() && !s.Busy() }

func (s Status) String() string {
	var parts []string
	switch {
	case !s.FirmwareLoaded():
		parts = append(parts, "no firmware")
	case s.Ready():
		parts = append(parts, "ready")
	}
	if s.WarmingUp() {
		parts = append(parts, "warming up")
	}
	if s.Busy() {
		parts = append(parts, "busy")
	}
	if len(parts) == 0 {
		parts = append(parts, "unknown")
	}
	return fmt.Sprintf("%02x %02x (%s)", s[0], s[1], joinComma(parts))
}

func joinComma(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// Identity is the 28-byte reply to `1b 13`: vendor (8 bytes), model (16) and
// firmware version (4), space padded, e.g. model "GT-F740", version "1.00".
type Identity struct {
	Vendor  string
	Model   string
	Version string
}

func parseIdentity(b []byte) (Identity, error) {
	if len(b) != 28 || !printable(b) || len(bytes.TrimSpace(b[8:24])) == 0 {
		return Identity{}, fmt.Errorf("%w: identity %q", ErrUnexpectedReply, b)
	}
	return Identity{
		Vendor:  string(bytes.TrimSpace(b[0:8])),
		Model:   string(bytes.TrimSpace(b[8:24])),
		Version: string(bytes.TrimSpace(b[24:28])),
	}, nil
}

// printable reports whether b is plain ASCII text (an identity, not a status
// byte or image data).
func printable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func (i Identity) String() string { return i.Vendor + " " + i.Model + " " + i.Version }

// ClockEpoch is the reference of the clock command: the value sent is the
// number of whole hours since this instant (verified in two captures).
var ClockEpoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// clockParam encodes the clock command parameter `0b 00 <u32 LE hours>`.
func clockParam(t time.Time) []byte {
	h := t.Sub(ClockEpoch) / time.Hour
	if h < 0 {
		h = 0
	}
	b := []byte{0x0b, 0x00, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[2:], uint32(h))
	return b
}
