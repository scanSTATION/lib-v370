// Package libv370 drives the Perfection V37/V370 (GT-F740, USB
// 04b8:014a) directly over USB — no vendor software, no libusb, no cgo.
//
// Quick start:
//
//	dev, err := libv370.OpenDevice(ctx, libv370.Options{})
//	if err != nil { … }
//	defer dev.Close()
//	scan, err := dev.Scan(ctx, "a4-300dpi-color")
//	// scan.Image is the channel-aligned, uncropped 8-bit RGB image
//	err = output.Write("scan", scan.Profile, scan.Raw, output.Options{Format: output.PNG}, output.Meta{})
//
// The firmware file esfwdd.bin (the vendor's, not included) is needed only after
// the scanner was switched off; it is loaded from ./firmware/esfwdd.bin
// unless Options.Firmware is set (see firmware/README.md).
//
// Lower layers: usb (transport interface), usb/usbfs (Linux), v370 (protocol,
// session, profiles), rawimg (image stream), output (PNG/JPEG/PDF + JSON),
// sim (USB simulator for tests). Protocol reference: docs/protocol.md.
package libv370

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"time"

	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/rawimg"
	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/synth"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/usb/usbfs"
	"github.com/scanSTATION/lib-v370/v370"
)

// Opener opens a USB transport to the scanner and describes the device.
// The default opens the real scanner via usbfs; tests can pass a simulator.
type Opener func() (usb.Transport, string, error)

// Options configure OpenDevice. The zero value opens the real scanner.
type Options struct {
	// Open overrides how the transport is opened (default: usbfs, real
	// scanner only).
	Open Opener
	// Firmware overrides ./firmware/esfwdd.bin.
	Firmware *firmware.Firmware
	Log      *slog.Logger
	// Now is used for the scanner clock (tests pin it).
	Now func() time.Time
	// NoSession opens without a host session: buttons can be read and the
	// Power button keeps working (long press); Scan opens a session only for
	// the duration of the scan.
	NoSession bool
	// VendorStart keeps the vendor app's carriage start position (scans
	// begin ≈ 1.2 mm after the glass corner); default: at the corner.
	VendorStart bool
	// StockFirmware uploads the firmware without firmware.LEDPatch.
	StockFirmware bool
}

// RealScanner opens the real V370 via usbfs (simulated devices attached via
// vhci-hcd are ignored).
func RealScanner() Opener { return usbfsOpener(usbfs.OnlyReal) }

// SimulatedScanner opens a V370 simulated by v370-usbsim through vhci-hcd.
func SimulatedScanner() Opener { return usbfsOpener(usbfs.OnlySimulated) }

// SyntheticScanner opens the synthetic V370 dev (package sim/synth: no
// hardware, no recordings — for tests and demos). Every call is a new
// connection to the same device, so its state (firmware, buttons) persists
// like a real scanner's.
func SyntheticScanner(dev *synth.Device) Opener {
	return func() (usb.Transport, string, error) {
		d, err := sim.NewDevice(sim.DefaultDescriptors(), dev, nil)
		if err != nil {
			return nil, "", err
		}
		return sim.NewTransport(d), "synthetic V370", nil
	}
}

func usbfsOpener(sel usbfs.Selector) Opener {
	return func() (usb.Transport, string, error) {
		devs, err := usbfs.Find("", usb.VendorV370, usb.ProductV370)
		if err != nil {
			return nil, "", err
		}
		info, err := usbfs.Select(devs, sel)
		if err != nil {
			return nil, "", fmt.Errorf("no V370 found on USB: %w", err)
		}
		dev, err := usbfs.Open(info)
		if err != nil {
			return nil, "", err
		}
		return dev, info.String(), nil
	}
}

// Device is an open session with the scanner. It is not safe for
// concurrent use; Close releases the scanner for other programs.
type Device struct {
	tp      usb.Transport
	s       *v370.Scanner
	desc    string
	closed  bool
	scanned bool
}

// OpenDevice opens the scanner and starts a session (uploading the
// firmware first if the scanner was switched off).
func OpenDevice(ctx context.Context, opt Options) (*Device, error) {
	open := opt.Open
	if open == nil {
		open = RealScanner()
	}
	tp, desc, err := open()
	if err != nil {
		return nil, err
	}
	s, err := v370.Open(ctx, tp, v370.Options{Firmware: opt.Firmware, Log: opt.Log, Now: opt.Now, NoSession: opt.NoSession, VendorStart: opt.VendorStart, StockFirmware: opt.StockFirmware})
	if err != nil {
		tp.Close()
		return nil, fmt.Errorf("%s: %w", desc, err)
	}
	return &Device{tp: tp, s: s, desc: desc}, nil
}

// Description names the USB device (bus/address or simulator).
func (d *Device) Description() string { return d.desc }

// Identity returns the scanner's identity (e.g. GT-F740 1.00).
func (d *Device) Identity() v370.Identity { return d.s.Identity() }

// FirmwareUploaded reports whether opening had to upload the firmware.
func (d *Device) FirmwareUploaded() bool { return d.s.FirmwareUploaded }

// Status queries the scanner status.
func (d *Device) Status(ctx context.Context) (v370.Status, error) { return d.s.Status(ctx) }

// ReadButton returns the panel button pressed since the last call
// (v370.ButtonNone if none). Poll it while idle, never during Scan.
func (d *Device) ReadButton(ctx context.Context) (v370.Button, error) { return d.s.ReadButton(ctx) }

// SetLED sets the panel LED (v370.Scanner.SetLED); LEDPatched reports
// whether LEDOff is available.
func (d *Device) SetLED(ctx context.Context, l v370.LED) error { return d.s.SetLED(ctx, l) }

// LEDPatched reports whether the firmware LED patch is running.
func (d *Device) LEDPatched(ctx context.Context) (bool, error) { return d.s.LEDPatched(ctx) }

// PressDuration measures how long the button b just reported by ReadButton
// is held (see v370.Scanner.PressDuration). Only while idle.
func (d *Device) PressDuration(ctx context.Context, b v370.Button, poll, limit time.Duration) (time.Duration, error) {
	return d.s.PressDuration(ctx, b, poll, limit)
}

// Protocol gives access to the low-level session (registers, raw queries).
func (d *Device) Protocol() *v370.Scanner { return d.s }

// Scan is the result of Device.Scan.
type Scan struct {
	Profile  *v370.Profile
	Geometry rawimg.Geometry
	// Image is the decoded image: full scanned width (not cropped to the
	// requested area), colour channels aligned, pixel values untouched.
	Image *image.RGBA
	// Raw is the scanner's image stream, for archiving or re-decoding.
	Raw      []byte
	Started  time.Time
	Duration time.Duration
}

// Profiles lists the built-in scan profiles.
func Profiles() []string { return v370.ProfileNames() }

// ErrClosed is returned after Close.
var ErrClosed = errors.New("libv370: device closed")

// Scan runs a built-in profile (see Profiles) and decodes the image.
func (d *Device) Scan(ctx context.Context, profile string) (*Scan, error) {
	if d.closed {
		return nil, ErrClosed
	}
	p, err := v370.LoadProfile(profile)
	if err != nil {
		return nil, err
	}
	return d.ScanProfile(ctx, p)
}

// ScanProfile runs a profile (e.g. loaded from a file) and decodes the image.
func (d *Device) ScanProfile(ctx context.Context, p *v370.Profile) (*Scan, error) {
	if d.closed {
		return nil, ErrClosed
	}
	g, err := p.Geometry()
	if err != nil {
		return nil, err
	}
	if !d.s.InSession() {
		// Session only for the scan (NoSession mode).
		if err := d.s.BeginSession(ctx); err != nil {
			return nil, err
		}
		defer d.s.EndSession(context.Background())
	}
	start := time.Now()
	raw := make([]byte, 0, p.ImageBytes)
	if err := d.s.RunProfile(ctx, p, func(tag string, b []byte) error {
		if tag == v370.TagImage {
			raw = append(raw, b...)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	img, err := rawimg.Decode(bytes.NewReader(raw), g)
	if err != nil {
		return nil, err
	}
	return &Scan{Profile: p, Geometry: g, Image: img, Raw: raw, Started: start, Duration: time.Since(start)}, nil
}

// Close ends the session and releases the scanner.
func (d *Device) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	err := d.s.Close(context.Background())
	return errors.Join(err, d.tp.Close())
}
