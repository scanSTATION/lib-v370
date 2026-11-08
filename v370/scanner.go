package v370

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/usb"
)

// Options configure a session.
type Options struct {
	// Firmware to upload when the scanner reports none. nil: load
	// firmware.DefaultPath (./firmware/esfwdd.bin) — only when an upload is
	// actually needed, so a scanner that has its firmware works without the
	// file.
	Firmware *firmware.Firmware
	// Now returns the current time for the clock command (default time.Now).
	Now func() time.Time
	// Sleep waits (default: context-aware sleep). Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// IOTimeout bounds each transfer (default DefaultIOTimeout).
	IOTimeout time.Duration
	// ReadyTimeout bounds the wait for warm-up after Open (default 90 s).
	ReadyTimeout time.Duration
	// NoSession opens the device without starting a host session
	// (`1e 93 01`). Button polling (`1e 85`) works without one, and the
	// scanner's Power button only works while no session is open (verified:
	// with a session, Power is blocked and just reported as button 01).
	// Scans need BeginSession/EndSession (libv370.Device.Scan does that).
	NoSession bool
	// ProfileReplyTimeout bounds each reply while running a scan profile
	// (default 90 s): some commands are answered only after the lamp has
	// warmed up, e.g. after lamp sleep.
	ProfileReplyTimeout time.Duration
	// PollInterval between status queries while waiting (default 80 ms,
	// as the vendor app does).
	PollInterval time.Duration
	// FirmwareSettle is the pause after a firmware upload before the next
	// command (the vendor app waits ~2.7 s; default 2.5 s).
	FirmwareSettle time.Duration
	// StockFirmware uploads the firmware unchanged. Default: the known
	// vendor build gets firmware.LEDPatch (LED off state, buttons active
	// while the LED is orange); other files are uploaded unchanged.
	StockFirmware bool
	// NoTransferBlink keeps the normal busy blink during the image pass.
	// Default with firmware.LEDPatch running: the LED blinks 4 times faster
	// while the image is transferred.
	NoTransferBlink bool
	// VendorStart keeps the vendor app's carriage start position: scans
	// begin ≈ 1.2 mm after the glass corner. Default: start at the corner
	// (start.go).
	VendorStart bool
	Log         *slog.Logger
}

func (o *Options) defaults() error {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	if o.IOTimeout == 0 {
		o.IOTimeout = DefaultIOTimeout
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 90 * time.Second
	}
	if o.ProfileReplyTimeout == 0 {
		o.ProfileReplyTimeout = 90 * time.Second
	}
	if o.PollInterval == 0 {
		o.PollInterval = 80 * time.Millisecond
	}
	if o.FirmwareSettle == 0 {
		o.FirmwareSettle = 2500 * time.Millisecond
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Scanner is an open session with a V370.
type Scanner struct {
	c        conn
	opt      Options
	identity Identity
	// FirmwareUploaded reports whether Open had to upload the firmware.
	FirmwareUploaded bool
	open             bool
	ledPatched       *bool // LED patch running (nil: not checked yet)
}

// Open starts a session: uploads the firmware if the scanner has none,
// opens the session, sets the clock, waits until the scanner is ready and
// reads its identity. The transport stays owned by the caller.
func Open(ctx context.Context, tp usb.Transport, opt Options) (*Scanner, error) {
	if err := opt.defaults(); err != nil {
		return nil, err
	}
	s := &Scanner{c: conn{tp: tp, timeout: opt.IOTimeout}, opt: opt}

	st, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	opt.Log.Debug("v370: initial status", "status", st)
	if !st.FirmwareLoaded() {
		if err := s.uploadFirmware(ctx); err != nil {
			return nil, err
		}
		s.FirmwareUploaded = true
	}
	if !opt.NoSession {
		if err := s.BeginSession(ctx); err != nil {
			return nil, err
		}
	}
	if err := s.SetClock(ctx, opt.Now()); err != nil {
		return nil, err
	}
	if _, err := s.WaitReady(ctx); err != nil {
		return nil, err
	}
	id, err := s.c.query(ctx, cmdIdentity, 28)
	if err != nil {
		return nil, err
	}
	if s.identity, err = parseIdentity(id); err != nil {
		return nil, err
	}
	opt.Log.Debug("v370: opened", "identity", s.identity.String(), "firmware_uploaded", s.FirmwareUploaded)
	return s, nil
}

// uploadFirmware performs the recorded sequence: `1b 06`→ACK, header
// `01 00 01 00`, 64 KiB payload, checksum →ACK, `1b 16`→ACK, `80`→ACK.
func (s *Scanner) uploadFirmware(ctx context.Context) error {
	fw := s.opt.Firmware
	if fw == nil {
		var err error
		if fw, err = firmware.Default(); err != nil {
			return fmt.Errorf("v370: scanner needs its firmware: %w", err)
		}
	}
	patched := false
	if !s.opt.StockFirmware && fw.SHA256() == firmware.LEDPatch.Base {
		p, err := fw.Apply(firmware.LEDPatch)
		if err != nil {
			return fmt.Errorf("v370: %w", err)
		}
		fw, patched = p, true
	}
	s.ledPatched = &patched
	s.opt.Log.Info("v370: uploading firmware", "sha256", fw.SHA256(), "patches", fw.Patches())
	if err := s.c.command(ctx, cmdFwBegin); err != nil {
		return fmt.Errorf("v370: firmware begin: %w", err)
	}
	if err := s.c.write(ctx, []byte{0x01, 0x00, 0x01, 0x00}); err != nil {
		return err
	}
	if err := s.c.write(ctx, fw.Payload()); err != nil {
		return err
	}
	if err := s.c.write(ctx, []byte{fw.Checksum()}); err != nil {
		return err
	}
	if err := s.c.expectACK(ctx, []byte{fw.Checksum()}); err != nil {
		return fmt.Errorf("v370: firmware rejected: %w", err)
	}
	if err := s.c.commandParam(ctx, cmdFwRestart, []byte{0x80}); err != nil {
		return fmt.Errorf("v370: firmware restart: %w", err)
	}
	return s.opt.Sleep(ctx, s.opt.FirmwareSettle)
}

// Identity returns the identity read during Open.
func (s *Scanner) Identity() Identity { return s.identity }

// Status queries `1b 03`.
func (s *Scanner) Status(ctx context.Context) (Status, error) {
	r, err := s.c.query(ctx, cmdStatus, 2)
	if err != nil {
		return Status{}, err
	}
	return Status{r[0], r[1]}, nil
}

// WaitReady polls the status until the scanner is ready.
func (s *Scanner) WaitReady(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, s.opt.ReadyTimeout)
	defer cancel()
	for {
		st, err := s.Status(ctx)
		if err != nil {
			return st, err
		}
		if st.Ready() {
			return st, nil
		}
		if !st.FirmwareLoaded() {
			return st, fmt.Errorf("v370: scanner lost its firmware (status %s)", st)
		}
		if err := s.opt.Sleep(ctx, s.opt.PollInterval); err != nil {
			return st, fmt.Errorf("v370: scanner not ready (last status %s): %w", st, err)
		}
	}
}

// SetClock sends `1e 8e` + `0b 00 <hours since 2024-01-01 UTC>`.
func (s *Scanner) SetClock(ctx context.Context, t time.Time) error {
	if err := s.c.commandParam(ctx, cmdClock, clockParam(t)); err != nil {
		return fmt.Errorf("v370: set clock: %w", err)
	}
	return nil
}

// ReadRegister sends `1e 8f` + 2-byte address and returns the u32 reply.
// Observed: 0x0010 → 120, 0x0005 → 849/850 (changes between scans).
func (s *Scanner) ReadRegister(ctx context.Context, addr uint16) (uint32, error) {
	p := make([]byte, 2)
	binary.LittleEndian.PutUint16(p, addr)
	r, err := s.c.commandQuery(ctx, cmdRegister, p, 4)
	if err != nil {
		return 0, fmt.Errorf("v370: read register 0x%04x: %w", addr, err)
	}
	return binary.LittleEndian.Uint32(r), nil
}

// ResolutionInfo returns the 42-byte reply of `1e 53` (resolution/area
// capabilities; layout not decoded yet).
func (s *Scanner) ResolutionInfo(ctx context.Context) ([]byte, error) {
	return s.c.query(ctx, cmdResolution, 42)
}

// Query sends a raw 2-byte command and returns exactly n reply bytes. For
// exploration (e.g. button registers); prefer the typed methods.
func (s *Scanner) Query(ctx context.Context, cmd [2]byte, n int) ([]byte, error) {
	return s.c.query(ctx, cmd[:], n)
}

// Raw sends a raw 2-byte command and returns whatever single reply the
// scanner sends within timeout (nil if none). For protocol exploration only.
func (s *Scanner) Raw(ctx context.Context, cmd [2]byte, timeout time.Duration) ([]byte, error) {
	if err := s.c.write(ctx, cmd[:]); err != nil {
		return nil, err
	}
	cx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := s.c.tp.BulkIn(cx, usb.EndpointIn, replyBuf)
	if err != nil {
		if cx.Err() != nil && ctx.Err() == nil {
			return nil, nil // no reply within timeout
		}
		return nil, err
	}
	return r, nil
}

// BeginSession starts a host session (`1e 93` + `01`) if none is open.
// While it is open the scanner's Power button is blocked.
func (s *Scanner) BeginSession(ctx context.Context) error {
	if s.open {
		return nil
	}
	if err := s.c.commandParam(ctx, cmdSession, []byte{0x01}); err != nil {
		return fmt.Errorf("v370: open session: %w", err)
	}
	s.open = true
	return nil
}

// EndSession ends the host session (`1e 93` + `00`) if one is open.
func (s *Scanner) EndSession(ctx context.Context) error {
	if !s.open {
		return nil
	}
	s.open = false
	if err := s.c.commandParam(ctx, cmdSession, []byte{0x00}); err != nil {
		return fmt.Errorf("v370: close session: %w", err)
	}
	return nil
}

// InSession reports whether a host session is open.
func (s *Scanner) InSession() bool { return s.open }

// Close ends the session (`1e 93` + `00`). The transport is not closed.
func (s *Scanner) Close(ctx context.Context) error { return s.EndSession(ctx) }

// Button identifies a scanner panel button reported by `1e 85`.
type Button uint8

// Button values verified on the real scanner (2026-11-07).
const (
	ButtonNone    Button = 0
	ButtonPower   Button = 1 // reported only; switching off needs a ~3 s press without session
	ButtonPrinter Button = 2 // "copy" / printer symbol
	ButtonEmail   Button = 3
	ButtonPDF     Button = 4
)

func (b Button) String() string {
	switch b {
	case ButtonNone:
		return "none"
	case ButtonPower:
		return "power"
	case ButtonPrinter:
		return "printer"
	case ButtonEmail:
		return "email"
	case ButtonPDF:
		return "pdf"
	}
	return fmt.Sprintf("button-%d", uint8(b))
}

var cmdButton = []byte{0x1e, 0x85}

// ReadButton queries `1e 85` (no parameter, 1-byte reply). A press is
// reported once and cleared by reading, so poll it regularly (e.g. every
// 200 ms) while the scanner is idle. Never call it during a scan: it would
// interleave with the image data reads.
func (s *Scanner) ReadButton(ctx context.Context) (Button, error) {
	r, err := s.c.query(ctx, cmdButton, 1)
	if err != nil {
		return ButtonNone, fmt.Errorf("v370: read button: %w", err)
	}
	if r[0] == ack {
		// Would mean the scanner treats 1e 85 as a command expecting a
		// parameter; never observed. Do not continue blindly.
		return ButtonNone, fmt.Errorf("%w: 1e85 answered ACK", ErrUnexpectedReply)
	}
	return Button(r[0]), nil
}
