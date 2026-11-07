package pcapusb

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

// pcapBuilder writes a synthetic usbmon capture (link type 220, little endian).
type pcapBuilder struct {
	buf bytes.Buffer
	id  uint64
	ts  int64
}

func newPcap(linkType uint32) *pcapBuilder {
	b := &pcapBuilder{}
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h[0:], 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 262144)
	binary.LittleEndian.PutUint32(h[20:], linkType)
	b.buf.Write(h)
	return b
}

type evSpec struct {
	id       uint64
	typ      byte
	xfer     uint8
	ep       uint8
	dev      uint8
	setup    []byte
	data     []byte
	urbLen   uint32
	status   int32
	snapData int // if >0, only this many data bytes are included
}

func (b *pcapBuilder) event(e evSpec) {
	h := make([]byte, 64)
	le := binary.LittleEndian
	le.PutUint64(h[0:], e.id)
	h[8], h[9], h[10], h[11] = e.typ, e.xfer, e.ep, e.dev
	le.PutUint16(h[12:], 3) // bus 3
	h[14] = '-'
	if e.setup != nil {
		h[14] = 0
		copy(h[40:48], e.setup)
	}
	b.ts++
	le.PutUint64(h[16:], uint64(b.ts))
	le.PutUint32(h[28:], uint32(e.status))
	urbLen := e.urbLen
	if urbLen == 0 {
		urbLen = uint32(len(e.data))
	}
	le.PutUint32(h[32:], urbLen)
	le.PutUint32(h[36:], uint32(len(e.data)))
	data := e.data
	if e.snapData > 0 {
		data = data[:e.snapData]
	}
	pkt := append(h, data...)
	rh := make([]byte, 16)
	le.PutUint32(rh[8:], uint32(len(pkt)))
	le.PutUint32(rh[12:], uint32(len(h)+len(e.data)))
	b.buf.Write(rh)
	b.buf.Write(pkt)
}

// transfer emits a submit + complete pair.
func (b *pcapBuilder) control(dev uint8, setup usb.Setup, in []byte) {
	b.id++
	b.event(evSpec{id: b.id, typ: 'S', xfer: XferControl, ep: 0x80, dev: dev, setup: setup.Bytes(), urbLen: uint32(setup.Length)})
	b.event(evSpec{id: b.id, typ: 'C', xfer: XferControl, ep: 0x80, dev: dev, data: in})
}

func (b *pcapBuilder) bulkOut(dev uint8, data []byte) {
	b.id++
	b.event(evSpec{id: b.id, typ: 'S', xfer: XferBulk, ep: 0x02, dev: dev, data: data})
	b.event(evSpec{id: b.id, typ: 'C', xfer: XferBulk, ep: 0x02, dev: dev, urbLen: uint32(len(data))})
}

func (b *pcapBuilder) bulkIn(dev uint8, data []byte, snap int) {
	b.id++
	b.event(evSpec{id: b.id, typ: 'S', xfer: XferBulk, ep: 0x81, dev: dev, urbLen: 512})
	b.event(evSpec{id: b.id, typ: 'C', xfer: XferBulk, ep: 0x81, dev: dev, data: data, snapData: snap})
}

func devDescriptor(vid, pid uint16) []byte {
	d, _, _ := usb.DefaultV370Descriptors()
	binary.LittleEndian.PutUint16(d[8:], vid)
	binary.LittleEndian.PutUint16(d[10:], pid)
	return d
}

var getDev = usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor, Value: 0x0100, Length: 18}

func TestConvertAutoDetectsScanner(t *testing.T) {
	p := newPcap(LinkTypeUSBLinuxMMapped)
	p.control(5, getDev, devDescriptor(0x046d, 0xc077)) // a mouse on the same bus
	p.control(7, getDev, devDescriptor(usb.VendorV370, usb.ProductV370))
	p.bulkOut(5, []byte("mouse"))
	p.bulkOut(7, []byte{0x1b, 'F'})
	p.bulkIn(7, []byte{0x02, 0x00}, 0)

	tr, st, err := Convert(&p.buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Addresses) != 1 || st.Addresses[0] != 7 {
		t.Fatalf("addresses = %v, want [7]", st.Addresses)
	}
	if len(tr.Transfers) != 3 {
		t.Fatalf("got %d transfers, want 3: %+v", len(tr.Transfers), tr.Transfers)
	}
	if tr.Transfers[0].Type != trace.TypeControl || len(tr.Transfers[0].Data) != 18 {
		t.Errorf("first transfer should be the device descriptor: %+v", tr.Transfers[0])
	}
	if out := tr.Transfers[1]; out.Endpoint != 0x02 || !bytes.Equal(out.Data, []byte{0x1b, 'F'}) {
		t.Errorf("OUT data must come from submit: %+v", out)
	}
	if in := tr.Transfers[2]; in.Endpoint != 0x81 || !bytes.Equal(in.Data, []byte{2, 0}) {
		t.Errorf("IN data must come from completion: %+v", in)
	}
	if len(tr.Descriptors) != 1 || tr.Descriptors[0].Value != 0x0100 {
		t.Errorf("descriptors = %+v", tr.Descriptors)
	}
	if tr.Meta.Bus != 3 {
		t.Errorf("bus = %d", tr.Meta.Bus)
	}
}

func TestConvertFollowsReEnumeration(t *testing.T) {
	p := newPcap(LinkTypeUSBLinuxMMapped)
	p.control(7, getDev, devDescriptor(usb.VendorV370, usb.ProductV370))
	p.bulkOut(7, []byte("firmware"))
	p.control(8, getDev, devDescriptor(usb.VendorV370, usb.ProductV370)) // after firmware reboot
	p.bulkOut(8, []byte("scan"))
	tr, st, err := Convert(&p.buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Addresses) != 2 || len(tr.Transfers) != 4 {
		t.Fatalf("addresses=%v transfers=%d", st.Addresses, len(tr.Transfers))
	}
}

func TestConvertForcedAddress(t *testing.T) {
	p := newPcap(LinkTypeUSBLinuxMMapped)
	p.bulkOut(9, []byte("x")) // no descriptor in capture
	if _, _, err := Convert(bytes.NewReader(p.buf.Bytes()), Options{}); err == nil {
		t.Fatal("expected auto-detect failure")
	}
	tr, _, err := Convert(bytes.NewReader(p.buf.Bytes()), Options{Address: 9})
	if err != nil || len(tr.Transfers) != 1 {
		t.Fatalf("err=%v tr=%v", err, tr)
	}
}

func TestConvertRejectsTruncated(t *testing.T) {
	p := newPcap(LinkTypeUSBLinuxMMapped)
	p.control(7, getDev, devDescriptor(usb.VendorV370, usb.ProductV370))
	p.bulkIn(7, bytes.Repeat([]byte{1}, 100), 10)
	raw := p.buf.Bytes()
	if _, _, err := Convert(bytes.NewReader(raw), Options{}); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("expected truncation error, got %v", err)
	}
	_, st, err := Convert(bytes.NewReader(raw), Options{AllowTruncated: true})
	if err != nil || st.Truncated != 1 {
		t.Fatalf("err=%v truncated=%d", err, st.Truncated)
	}
}

func TestReaderRejectsGarbage(t *testing.T) {
	if _, err := NewReader(strings.NewReader("not a pcap file at all.....")); err == nil {
		t.Error("expected error for garbage")
	}
	ng := make([]byte, 24)
	binary.LittleEndian.PutUint32(ng, 0x0a0d0d0a)
	if _, err := NewReader(bytes.NewReader(ng)); err == nil || !strings.Contains(err.Error(), "pcapng") {
		t.Errorf("expected pcapng hint, got %v", err)
	}
	eth := newPcap(1) // ethernet
	if _, err := NewReader(&eth.buf); err == nil {
		t.Error("expected link type error")
	}
	// record claiming a huge length
	p := newPcap(LinkTypeUSBLinuxMMapped)
	rh := make([]byte, 16)
	binary.LittleEndian.PutUint32(rh[8:], 1<<30)
	p.buf.Write(rh)
	r, _ := NewReader(&p.buf)
	if _, err := r.Next(); err == nil {
		t.Error("expected oversize record error")
	}
}
