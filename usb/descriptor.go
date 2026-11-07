package usb

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// DeviceDescriptor is the parsed 18-byte standard device descriptor.
type DeviceDescriptor struct {
	BcdUSB            uint16
	DeviceClass       uint8
	DeviceSubClass    uint8
	DeviceProtocol    uint8
	MaxPacketSize0    uint8
	VendorID          uint16
	ProductID         uint16
	BcdDevice         uint16
	NumConfigurations uint8
}

// ParseDeviceDescriptor decodes a device descriptor.
func ParseDeviceDescriptor(b []byte) (DeviceDescriptor, error) {
	if len(b) < 18 || b[0] < 18 || b[1] != DescDevice {
		return DeviceDescriptor{}, fmt.Errorf("usb: invalid device descriptor (len %d)", len(b))
	}
	return DeviceDescriptor{
		BcdUSB:            binary.LittleEndian.Uint16(b[2:4]),
		DeviceClass:       b[4],
		DeviceSubClass:    b[5],
		DeviceProtocol:    b[6],
		MaxPacketSize0:    b[7],
		VendorID:          binary.LittleEndian.Uint16(b[8:10]),
		ProductID:         binary.LittleEndian.Uint16(b[10:12]),
		BcdDevice:         binary.LittleEndian.Uint16(b[12:14]),
		NumConfigurations: b[17],
	}, nil
}

// InterfaceInfo summarises one interface of a configuration.
type InterfaceInfo struct {
	Number    uint8
	Class     uint8
	SubClass  uint8
	Protocol  uint8
	Endpoints []EndpointInfo
}

// EndpointInfo summarises one endpoint descriptor.
type EndpointInfo struct {
	Address       uint8
	Attributes    uint8 // bits 1..0: 0 control, 1 iso, 2 bulk, 3 interrupt
	MaxPacketSize uint16
}

// ConfigInfo is the parsed full configuration descriptor.
type ConfigInfo struct {
	Value      uint8
	Interfaces []InterfaceInfo
}

// ParseConfiguration decodes a full configuration descriptor (configuration +
// interface + endpoint descriptors). Unknown descriptor types are skipped.
func ParseConfiguration(b []byte) (ConfigInfo, error) {
	if len(b) < 9 || b[1] != DescConfiguration {
		return ConfigInfo{}, fmt.Errorf("usb: invalid configuration descriptor")
	}
	total := int(binary.LittleEndian.Uint16(b[2:4]))
	if total > len(b) {
		return ConfigInfo{}, fmt.Errorf("usb: configuration truncated (%d of %d bytes)", len(b), total)
	}
	ci := ConfigInfo{Value: b[5]}
	var cur *InterfaceInfo
	for off := 0; off < total; {
		l := int(b[off])
		if l < 2 || off+l > total {
			return ConfigInfo{}, fmt.Errorf("usb: malformed descriptor at offset %d", off)
		}
		d := b[off : off+l]
		switch d[1] {
		case DescInterface:
			if l < 9 {
				return ConfigInfo{}, fmt.Errorf("usb: short interface descriptor")
			}
			if d[3] == 0 { // only alternate setting 0
				ci.Interfaces = append(ci.Interfaces, InterfaceInfo{Number: d[2], Class: d[5], SubClass: d[6], Protocol: d[7]})
				cur = &ci.Interfaces[len(ci.Interfaces)-1]
			} else {
				cur = nil
			}
		case DescEndpoint:
			if l < 7 {
				return ConfigInfo{}, fmt.Errorf("usb: short endpoint descriptor")
			}
			if cur != nil {
				cur.Endpoints = append(cur.Endpoints, EndpointInfo{
					Address: d[2], Attributes: d[3], MaxPacketSize: binary.LittleEndian.Uint16(d[4:6]),
				})
			}
		}
		off += l
	}
	return ci, nil
}

// StringDescriptor encodes s as a USB string descriptor (UTF-16LE).
func StringDescriptor(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2+2*len(u))
	b[0], b[1] = byte(len(b)), DescString
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2+2*i:], c)
	}
	return b
}

// DefaultV370Descriptors returns placeholder descriptors that match the
// verified V370 facts (VID/PID, one vendor-specific interface, bulk EP 0x02
// OUT and EP 0x81 IN, 512-byte packets). Fields that were not verified
// (bcdDevice, strings, power) are placeholders; a captured trace overrides
// them with the real bytes.
func DefaultV370Descriptors() (device, config []byte, strings map[uint8][]byte) {
	device = []byte{
		18, DescDevice, 0x00, 0x02, // bcdUSB 2.00
		0xff, 0xff, 0xff, 64, // class/subclass/protocol vendor, EP0 64 bytes
		byte(VendorV370 & 0xff), byte(VendorV370 >> 8),
		byte(ProductV370 & 0xff), byte(ProductV370 >> 8),
		0x00, 0x01, // bcdDevice 1.00 (placeholder)
		1, 2, 0, // iManufacturer, iProduct, iSerial
		1, // bNumConfigurations
	}
	config = []byte{
		9, DescConfiguration, 32, 0, 1, 1, 0, 0x80, 250, // 1 interface, bus powered, 500 mA
		9, DescInterface, 0, 0, 2, 0xff, 0xff, 0xff, 0,
		7, DescEndpoint, EndpointIn, 0x02, 0x00, 0x02, 0, // bulk IN 512
		7, DescEndpoint, EndpointOut, 0x02, 0x00, 0x02, 0, // bulk OUT 512
	}
	strings = map[uint8][]byte{
		0: {4, DescString, 0x09, 0x04}, // LANGID en-US
		1: StringDescriptor("scanSTATION"),
		2: StringDescriptor("V370 Scanner (simulated)"),
	}
	return device, config, strings
}
