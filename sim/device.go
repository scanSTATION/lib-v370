// Package usbsim simulates the V370 at the USB level.
//
// A Device answers chapter-9 standard requests itself (from its descriptor
// set) and forwards everything device-specific — class/vendor control
// requests and bulk transfers — to a Responder (see package replay).
//
// The device can be reached in-process (NewTransport, for Go drivers) or
// over USB/IP (package usbip), which lets the unmodified vendor app binary
// talk to it through the kernel's vhci-hcd driver.
package sim

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

// Responder implements the device-specific behaviour. Calls are serialised
// by the Device, so implementations need no locking of their own.
type Responder interface {
	// Control handles a class or vendor control request. For IN requests it
	// returns the data stage (truncated by the caller to setup.Length).
	Control(setup usb.Setup, data []byte) ([]byte, error)
	// BulkOut receives data written by the host to an OUT endpoint.
	BulkOut(ep uint8, data []byte) error
	// TryBulkIn returns up to max bytes for an IN endpoint, or ok=false if
	// the device has nothing to send right now (the host keeps waiting).
	TryBulkIn(ep uint8, max int) (data []byte, ok bool, err error)
	// Reset is called on USB reset / re-attach.
	Reset()
}

// Descriptors is the standard descriptor set the device reports.
type Descriptors struct {
	// byKey maps (wValue, wIndex) of GET_DESCRIPTOR to the response.
	byKey map[[2]uint16][]byte
}

// NewDescriptors builds a descriptor set from raw device/config/string
// descriptors.
func NewDescriptors(device, config []byte, strings map[uint8][]byte) *Descriptors {
	d := &Descriptors{byKey: map[[2]uint16][]byte{}}
	d.Set(uint16(usb.DescDevice)<<8, 0, device)
	d.Set(uint16(usb.DescConfiguration)<<8, 0, config)
	for i, s := range strings {
		lang := uint16(0x0409)
		if i == 0 {
			lang = 0
		}
		d.Set(uint16(usb.DescString)<<8|uint16(i), lang, s)
	}
	return d
}

// DefaultDescriptors returns the placeholder V370 descriptor set.
func DefaultDescriptors() *Descriptors {
	return NewDescriptors(usb.DefaultV370Descriptors())
}

// DescriptorsFromTrace overlays captured descriptors over the defaults.
func DescriptorsFromTrace(t *trace.Trace) *Descriptors {
	d := DefaultDescriptors()
	for _, td := range t.Descriptors {
		d.Set(td.Value, td.Index, td.Data)
	}
	return d
}

// Set stores a descriptor.
func (d *Descriptors) Set(value, index uint16, data []byte) {
	d.byKey[[2]uint16{value, index}] = append([]byte(nil), data...)
}

// Get looks up a descriptor. String descriptors fall back to any language.
func (d *Descriptors) Get(value, index uint16) ([]byte, bool) {
	if b, ok := d.byKey[[2]uint16{value, index}]; ok {
		return b, true
	}
	if value>>8 == uint16(usb.DescString) {
		for k, b := range d.byKey {
			if k[0] == value {
				return b, true
			}
		}
	}
	return nil, false
}

// Device returns the parsed device descriptor.
func (d *Descriptors) Device() (usb.DeviceDescriptor, error) {
	b, _ := d.Get(uint16(usb.DescDevice)<<8, 0)
	return usb.ParseDeviceDescriptor(b)
}

// Config returns the parsed first configuration.
func (d *Descriptors) Config() (usb.ConfigInfo, error) {
	b, _ := d.Get(uint16(usb.DescConfiguration)<<8, 0)
	return usb.ParseConfiguration(b)
}

// Device is a simulated USB device. It is safe for concurrent use.
type Device struct {
	desc    *Descriptors
	devDesc usb.DeviceDescriptor
	cfg     usb.ConfigInfo
	eps     map[uint8]usb.EndpointInfo

	mu      sync.Mutex
	resp    Responder
	config  uint8
	changed chan struct{}
	log     *slog.Logger
}

// NewDevice validates the descriptors and creates a device.
func NewDevice(desc *Descriptors, resp Responder, log *slog.Logger) (*Device, error) {
	if log == nil {
		log = slog.Default()
	}
	dd, err := desc.Device()
	if err != nil {
		return nil, fmt.Errorf("usbsim: %w", err)
	}
	ci, err := desc.Config()
	if err != nil {
		return nil, fmt.Errorf("usbsim: %w", err)
	}
	eps := map[uint8]usb.EndpointInfo{}
	for _, it := range ci.Interfaces {
		for _, ep := range it.Endpoints {
			eps[ep.Address] = ep
		}
	}
	return &Device{
		desc: desc, devDesc: dd, cfg: ci, eps: eps,
		resp: resp, changed: make(chan struct{}), log: log,
	}, nil
}

// DeviceDescriptor returns the parsed device descriptor.
func (d *Device) DeviceDescriptor() usb.DeviceDescriptor { return d.devDesc }

// ConfigInfo returns the parsed configuration.
func (d *Device) ConfigInfo() usb.ConfigInfo { return d.cfg }

// HasEndpoint reports whether the configuration declares the endpoint.
func (d *Device) HasEndpoint(addr uint8) bool { _, ok := d.eps[addr]; return ok }

// Changed returns a channel that is closed the next time the device state
// changes (new data may be available). Fetch it *before* calling TryBulkIn
// to avoid missing a wake-up.
func (d *Device) Changed() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.changed
}

func (d *Device) notifyLocked() {
	close(d.changed)
	d.changed = make(chan struct{})
}

// Reset resets the device (USB bus reset / re-attach).
func (d *Device) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.config = 0
	d.resp.Reset()
	d.notifyLocked()
}

// Control handles a control transfer on endpoint 0.
func (d *Device) Control(setup usb.Setup, data []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !setup.IsIn() && len(data) != int(setup.Length) {
		return nil, fmt.Errorf("usbsim: control OUT data length %d != wLength %d", len(data), setup.Length)
	}
	var out []byte
	var err error
	if setup.IsStandard() {
		out, err = d.standardLocked(setup)
	} else {
		out, err = d.resp.Control(setup, data)
		d.notifyLocked()
	}
	if err != nil {
		return nil, err
	}
	if !setup.IsIn() {
		return nil, nil
	}
	if len(out) > int(setup.Length) {
		out = out[:setup.Length]
	}
	return out, nil
}

func (d *Device) standardLocked(s usb.Setup) ([]byte, error) {
	switch s.Request {
	case usb.ReqGetDescriptor:
		b, ok := d.desc.Get(s.Value, s.Index)
		if !ok {
			d.log.Debug("usbsim: unknown descriptor requested", "setup", s.String())
			return nil, usb.ErrStall
		}
		return b, nil
	case usb.ReqGetStatus:
		return []byte{0, 0}, nil
	case usb.ReqGetConfiguration:
		return []byte{d.config}, nil
	case usb.ReqSetConfiguration:
		if v := uint8(s.Value); v != 0 && v != d.cfg.Value {
			return nil, usb.ErrStall
		}
		d.config = uint8(s.Value)
		return nil, nil
	case usb.ReqGetInterface:
		return []byte{0}, nil
	case usb.ReqSetInterface, usb.ReqClearFeature, usb.ReqSetFeature, usb.ReqSetAddress:
		return nil, nil
	default:
		d.log.Debug("usbsim: unsupported standard request", "setup", s.String())
		return nil, usb.ErrStall
	}
}

// BulkOut delivers host data to an OUT endpoint.
func (d *Device) BulkOut(ep uint8, data []byte) error {
	if ep&usb.DirIn != 0 || !d.HasEndpoint(ep) {
		return usb.ErrStall
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	err := d.resp.BulkOut(ep, data)
	d.notifyLocked()
	return err
}

// TryBulkIn returns pending data for an IN endpoint without blocking.
func (d *Device) TryBulkIn(ep uint8, max int) ([]byte, bool, error) {
	if ep&usb.DirIn == 0 || !d.HasEndpoint(ep) {
		return nil, false, usb.ErrStall
	}
	if max <= 0 {
		return []byte{}, true, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.resp.TryBulkIn(ep, max)
}
