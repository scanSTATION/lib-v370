// Package pcapusb reads Linux usbmon captures (classic pcap, link types 189
// and 220 as written by `dumpcap -F pcap` or `tcpdump -i usbmonN`) and
// converts them into a simulator trace.
//
// Capture files are untrusted input: every length is bounds-checked.
package pcapusb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Link types for usbmon.
const (
	LinkTypeUSBLinux        = 189 // 48-byte header
	LinkTypeUSBLinuxMMapped = 220 // 64-byte header
)

const maxPacket = 64 << 20

// Event is one usbmon event (URB submit, completion or error).
type Event struct {
	ID           uint64
	Type         byte // 'S' submit, 'C' complete, 'E' error
	TransferType uint8
	Endpoint     uint8 // includes direction bit
	Device       uint8
	Bus          uint16
	SetupPresent bool
	Setup        [8]byte
	TimeSec      int64
	TimeUSec     int32
	Status       int32
	URBLen       uint32
	DataLen      uint32 // bytes announced in header
	Data         []byte // bytes actually present in the packet
	Truncated    bool   // Data shorter than DataLen, or DataLen shorter than URBLen
}

// usbmon transfer types.
const (
	XferIso       = 0
	XferInterrupt = 1
	XferControl   = 2
	XferBulk      = 3
)

// Reader iterates over the events of a usbmon pcap file.
type Reader struct {
	r        io.Reader
	order    binary.ByteOrder
	linkType uint32
	snapLen  uint32
}

// NewReader parses the pcap global header.
func NewReader(r io.Reader) (*Reader, error) {
	var hdr [24]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("pcap: reading header: %w", err)
	}
	var order binary.ByteOrder
	switch binary.LittleEndian.Uint32(hdr[0:4]) {
	case 0xa1b2c3d4, 0xa1b23c4d:
		order = binary.LittleEndian
	case 0xd4c3b2a1, 0x4d3cb2a1:
		order = binary.BigEndian
	case 0x0a0d0d0a:
		return nil, errors.New("pcap: file is pcapng; capture with `dumpcap -F pcap` or convert with `editcap -F pcap in.pcapng out.pcap`")
	default:
		return nil, errors.New("pcap: not a pcap file")
	}
	lt := order.Uint32(hdr[20:24])
	if lt != LinkTypeUSBLinux && lt != LinkTypeUSBLinuxMMapped {
		return nil, fmt.Errorf("pcap: link type %d is not usbmon (189/220)", lt)
	}
	return &Reader{r: r, order: order, linkType: lt, snapLen: order.Uint32(hdr[16:20])}, nil
}

// Next returns the next event, or io.EOF.
func (pr *Reader) Next() (*Event, error) {
	var rh [16]byte
	if _, err := io.ReadFull(pr.r, rh[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("pcap: truncated record header")
		}
		return nil, err
	}
	incl := pr.order.Uint32(rh[8:12])
	orig := pr.order.Uint32(rh[12:16])
	if incl > maxPacket {
		return nil, fmt.Errorf("pcap: record of %d bytes exceeds limit", incl)
	}
	pkt := make([]byte, incl)
	if _, err := io.ReadFull(pr.r, pkt); err != nil {
		return nil, fmt.Errorf("pcap: truncated record: %w", err)
	}
	ev, err := pr.parse(pkt)
	if err != nil {
		return nil, err
	}
	if orig > incl {
		ev.Truncated = true
	}
	return ev, nil
}

// parse decodes the usbmon header. usbmon headers are in the byte order of
// the capturing host, which is also the order the pcap file was written in.
func (pr *Reader) parse(p []byte) (*Event, error) {
	hl := 48
	if pr.linkType == LinkTypeUSBLinuxMMapped {
		hl = 64
	}
	if len(p) < hl {
		return nil, fmt.Errorf("pcap: usbmon packet shorter than header (%d < %d)", len(p), hl)
	}
	o := pr.order
	ev := &Event{
		ID:           o.Uint64(p[0:8]),
		Type:         p[8],
		TransferType: p[9],
		Endpoint:     p[10],
		Device:       p[11],
		Bus:          o.Uint16(p[12:14]),
		SetupPresent: p[14] == 0,
		TimeSec:      int64(o.Uint64(p[16:24])),
		TimeUSec:     int32(o.Uint32(p[24:28])),
		Status:       int32(o.Uint32(p[28:32])),
		URBLen:       o.Uint32(p[32:36]),
		DataLen:      o.Uint32(p[36:40]),
	}
	copy(ev.Setup[:], p[40:48])
	off := hl
	if pr.linkType == LinkTypeUSBLinuxMMapped && ev.TransferType == XferIso {
		ndesc := o.Uint32(p[60:64])
		if ndesc > 1024 || off+int(ndesc)*16 > len(p) {
			return nil, fmt.Errorf("pcap: invalid iso descriptor count %d", ndesc)
		}
		off += int(ndesc) * 16
	}
	data := p[off:]
	if uint32(len(data)) > ev.DataLen {
		data = data[:ev.DataLen]
	}
	if uint32(len(data)) < ev.DataLen {
		ev.Truncated = true
	}
	ev.Data = data
	return ev, nil
}
