// Package usbip exports a simulated USB device over the USB/IP protocol
// (Linux kernel Documentation/usb/usbip_protocol.rst, version 1.1.1).
//
// The kernel's vhci-hcd driver attaches such a device as if it were plugged
// in locally, so unmodified user-space drivers (libusb, the vendor app) can use
// it. All multi-byte fields are big endian.
package usbip

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Protocol constants.
const (
	Version uint16 = 0x0111

	OpReqDevlist uint16 = 0x8005
	OpRepDevlist uint16 = 0x0005
	OpReqImport  uint16 = 0x8003
	OpRepImport  uint16 = 0x0003

	CmdSubmit uint32 = 1
	CmdUnlink uint32 = 2
	RetSubmit uint32 = 3
	RetUnlink uint32 = 4

	DirOut uint32 = 0
	DirIn  uint32 = 1

	SpeedHigh uint32 = 3

	headerLen     = 48
	deviceInfoLen = 312
	busIDLen      = 32
	pathLen       = 256

	// Linux errno values used in URB status fields (negated on the wire).
	ErrnoEPIPE      = 32
	ErrnoEPROTO     = 71
	ErrnoECONNRESET = 104
	ErrnoEINVAL     = 22
)

// DeviceInfo is the exported device description (struct usbip_usb_device).
type DeviceInfo struct {
	Path              string
	BusID             string
	BusNum            uint32
	DevNum            uint32
	Speed             uint32
	VendorID          uint16
	ProductID         uint16
	BcdDevice         uint16
	DeviceClass       uint8
	DeviceSubClass    uint8
	DeviceProtocol    uint8
	ConfigurationVal  uint8
	NumConfigurations uint8
	Interfaces        []InterfaceInfo
}

// InterfaceInfo is struct usbip_usb_interface.
type InterfaceInfo struct {
	Class, SubClass, Protocol uint8
}

// DevID is the device id used in URB headers: busnum << 16 | devnum.
func (d DeviceInfo) DevID() uint32 { return d.BusNum<<16 | d.DevNum }

func putFixedString(b []byte, s string) {
	copy(b, s) // remainder stays zero; len(s) is validated by callers
}

func fixedString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func (d DeviceInfo) encode(withInterfaces bool) []byte {
	b := make([]byte, deviceInfoLen)
	putFixedString(b[0:pathLen], d.Path)
	putFixedString(b[pathLen:pathLen+busIDLen], d.BusID)
	o := pathLen + busIDLen
	binary.BigEndian.PutUint32(b[o:], d.BusNum)
	binary.BigEndian.PutUint32(b[o+4:], d.DevNum)
	binary.BigEndian.PutUint32(b[o+8:], d.Speed)
	binary.BigEndian.PutUint16(b[o+12:], d.VendorID)
	binary.BigEndian.PutUint16(b[o+14:], d.ProductID)
	binary.BigEndian.PutUint16(b[o+16:], d.BcdDevice)
	b[o+18] = d.DeviceClass
	b[o+19] = d.DeviceSubClass
	b[o+20] = d.DeviceProtocol
	b[o+21] = d.ConfigurationVal
	b[o+22] = d.NumConfigurations
	b[o+23] = uint8(len(d.Interfaces))
	if withInterfaces {
		for _, it := range d.Interfaces {
			b = append(b, it.Class, it.SubClass, it.Protocol, 0)
		}
	}
	return b
}

func decodeDeviceInfo(b []byte) DeviceInfo {
	o := pathLen + busIDLen
	return DeviceInfo{
		Path:              fixedString(b[0:pathLen]),
		BusID:             fixedString(b[pathLen : pathLen+busIDLen]),
		BusNum:            binary.BigEndian.Uint32(b[o:]),
		DevNum:            binary.BigEndian.Uint32(b[o+4:]),
		Speed:             binary.BigEndian.Uint32(b[o+8:]),
		VendorID:          binary.BigEndian.Uint16(b[o+12:]),
		ProductID:         binary.BigEndian.Uint16(b[o+14:]),
		BcdDevice:         binary.BigEndian.Uint16(b[o+16:]),
		DeviceClass:       b[o+18],
		DeviceSubClass:    b[o+19],
		DeviceProtocol:    b[o+20],
		ConfigurationVal:  b[o+21],
		NumConfigurations: b[o+22],
		Interfaces:        make([]InterfaceInfo, b[o+23]),
	}
}

// opHeader is the 8-byte header of the connection-setup operations.
type opHeader struct {
	Version uint16
	Code    uint16
	Status  uint32
}

func writeOp(w io.Writer, code uint16, status uint32, body []byte) error {
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint16(b[0:], Version)
	binary.BigEndian.PutUint16(b[2:], code)
	binary.BigEndian.PutUint32(b[4:], status)
	_, err := w.Write(append(b, body...))
	return err
}

func readOp(r io.Reader) (opHeader, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return opHeader{}, err
	}
	return opHeader{
		Version: binary.BigEndian.Uint16(b[0:]),
		Code:    binary.BigEndian.Uint16(b[2:]),
		Status:  binary.BigEndian.Uint32(b[4:]),
	}, nil
}

// Header is a URB command/reply header (48 bytes).
type Header struct {
	Command   uint32
	Seq       uint32
	DevID     uint32
	Direction uint32
	Endpoint  uint32
	// CMD_SUBMIT
	TransferFlags  uint32
	TransferLength int32
	StartFrame     int32
	NumPackets     int32
	Interval       int32
	Setup          [8]byte
	// RET_SUBMIT
	Status     int32 // RET_SUBMIT / RET_UNLINK
	ActualLen  int32
	ErrorCount int32
	UnlinkSeq  uint32 // CMD_UNLINK
}

func (h Header) encode() []byte {
	b := make([]byte, headerLen)
	be := binary.BigEndian
	be.PutUint32(b[0:], h.Command)
	be.PutUint32(b[4:], h.Seq)
	be.PutUint32(b[8:], h.DevID)
	be.PutUint32(b[12:], h.Direction)
	be.PutUint32(b[16:], h.Endpoint)
	switch h.Command {
	case CmdSubmit:
		be.PutUint32(b[20:], h.TransferFlags)
		be.PutUint32(b[24:], uint32(h.TransferLength))
		be.PutUint32(b[28:], uint32(h.StartFrame))
		be.PutUint32(b[32:], uint32(h.NumPackets))
		be.PutUint32(b[36:], uint32(h.Interval))
		copy(b[40:48], h.Setup[:])
	case RetSubmit:
		be.PutUint32(b[20:], uint32(h.Status))
		be.PutUint32(b[24:], uint32(h.ActualLen))
		be.PutUint32(b[28:], uint32(h.StartFrame))
		be.PutUint32(b[32:], uint32(h.NumPackets))
		be.PutUint32(b[36:], uint32(h.ErrorCount))
	case CmdUnlink:
		be.PutUint32(b[20:], h.UnlinkSeq)
	case RetUnlink:
		be.PutUint32(b[20:], uint32(h.Status))
	}
	return b
}

func readHeader(r io.Reader) (Header, error) {
	var b [headerLen]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Header{}, err
	}
	be := binary.BigEndian
	h := Header{
		Command:   be.Uint32(b[0:]),
		Seq:       be.Uint32(b[4:]),
		DevID:     be.Uint32(b[8:]),
		Direction: be.Uint32(b[12:]),
		Endpoint:  be.Uint32(b[16:]),
	}
	switch h.Command {
	case CmdSubmit:
		h.TransferFlags = be.Uint32(b[20:])
		h.TransferLength = int32(be.Uint32(b[24:]))
		h.StartFrame = int32(be.Uint32(b[28:]))
		h.NumPackets = int32(be.Uint32(b[32:]))
		h.Interval = int32(be.Uint32(b[36:]))
		copy(h.Setup[:], b[40:48])
	case RetSubmit:
		h.Status = int32(be.Uint32(b[20:]))
		h.ActualLen = int32(be.Uint32(b[24:]))
		h.StartFrame = int32(be.Uint32(b[28:]))
		h.NumPackets = int32(be.Uint32(b[32:]))
		h.ErrorCount = int32(be.Uint32(b[36:]))
	case CmdUnlink:
		h.UnlinkSeq = be.Uint32(b[20:])
	case RetUnlink:
		h.Status = int32(be.Uint32(b[20:]))
	default:
		return h, fmt.Errorf("usbip: unknown command %d", h.Command)
	}
	return h, nil
}
