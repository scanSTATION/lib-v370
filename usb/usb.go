// Package usb holds USB types shared by the simulator (package sim) and
// the future direct USB driver (see plan-direct.md).
//
// It has no dependencies on libusb or the kernel; it only models the wire
// level: setup packets, descriptors and a transport interface.
package usb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

// V370 identity (verified, see knowledge.md §1/§9).
const (
	VendorV370     uint16 = 0x04b8
	ProductV370    uint16 = 0x014a
	EndpointOut    uint8  = 0x02 // bulk OUT: host → scanner commands
	EndpointIn     uint8  = 0x81 // bulk IN: scanner → host responses + image data
	DirIn          uint8  = 0x80 // direction bit in endpoint addresses / bmRequestType
	MaxTransferLen        = 32 << 20
)

// ErrStall reports a STALL handshake (EPIPE on Linux).
var ErrStall = errors.New("usb: endpoint stalled")

// Request types (bmRequestType bits 6..5).
const (
	TypeStandard uint8 = 0x00
	TypeClass    uint8 = 0x20
	TypeVendor   uint8 = 0x40
	typeMask     uint8 = 0x60
)

// Standard requests (USB 2.0 §9.4).
const (
	ReqGetStatus        uint8 = 0x00
	ReqClearFeature     uint8 = 0x01
	ReqSetFeature       uint8 = 0x03
	ReqSetAddress       uint8 = 0x05
	ReqGetDescriptor    uint8 = 0x06
	ReqGetConfiguration uint8 = 0x08
	ReqSetConfiguration uint8 = 0x09
	ReqGetInterface     uint8 = 0x0a
	ReqSetInterface     uint8 = 0x0b
)

// Descriptor types.
const (
	DescDevice        uint8 = 0x01
	DescConfiguration uint8 = 0x02
	DescString        uint8 = 0x03
	DescInterface     uint8 = 0x04
	DescEndpoint      uint8 = 0x05
)

// Setup is a USB control setup packet (8 bytes, little endian).
type Setup struct {
	RequestType uint8
	Request     uint8
	Value       uint16
	Index       uint16
	Length      uint16
}

// ParseSetup decodes an 8-byte setup packet.
func ParseSetup(b []byte) (Setup, error) {
	if len(b) != 8 {
		return Setup{}, fmt.Errorf("usb: setup packet must be 8 bytes, got %d", len(b))
	}
	return Setup{
		RequestType: b[0],
		Request:     b[1],
		Value:       binary.LittleEndian.Uint16(b[2:4]),
		Index:       binary.LittleEndian.Uint16(b[4:6]),
		Length:      binary.LittleEndian.Uint16(b[6:8]),
	}, nil
}

// Bytes encodes the setup packet.
func (s Setup) Bytes() []byte {
	b := make([]byte, 8)
	b[0], b[1] = s.RequestType, s.Request
	binary.LittleEndian.PutUint16(b[2:4], s.Value)
	binary.LittleEndian.PutUint16(b[4:6], s.Index)
	binary.LittleEndian.PutUint16(b[6:8], s.Length)
	return b
}

// IsIn reports whether the data stage is device → host.
func (s Setup) IsIn() bool { return s.RequestType&DirIn != 0 }

// Type returns TypeStandard, TypeClass or TypeVendor.
func (s Setup) Type() uint8 { return s.RequestType & typeMask }

// IsStandard reports whether this is a standard (chapter 9) request.
func (s Setup) IsStandard() bool { return s.Type() == TypeStandard }

func (s Setup) String() string {
	return fmt.Sprintf("bmRequestType=%02x bRequest=%02x wValue=%04x wIndex=%04x wLength=%d",
		s.RequestType, s.Request, s.Value, s.Index, s.Length)
}

// Transport is the minimal host-side view of a USB device used by drivers.
// A real implementation (libusb/gousb) and the simulator's in-process
// transport both satisfy it, so driver code can be tested without hardware.
type Transport interface {
	// Control performs a control transfer. For IN requests the returned
	// slice holds the data stage; for OUT requests data is sent.
	Control(ctx context.Context, setup Setup, data []byte) ([]byte, error)
	// BulkOut writes data to an OUT endpoint.
	BulkOut(ctx context.Context, ep uint8, data []byte) (int, error)
	// BulkIn reads up to max bytes from an IN endpoint, blocking until the
	// device produces data or ctx is done.
	BulkIn(ctx context.Context, ep uint8, max int) ([]byte, error)
	// Close releases the device.
	Close() error
}
