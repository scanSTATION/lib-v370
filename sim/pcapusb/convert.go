package pcapusb

import (
	"encoding/binary"
	"fmt"
	"io"
	"slices"

	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

// Options selects which device of the capture is converted.
type Options struct {
	VendorID  uint16 // default usb.VendorV370
	ProductID uint16 // default usb.ProductV370
	// Bus/Address force a device; when Address is 0 the device is
	// auto-detected from captured GET_DESCRIPTOR(device) responses.
	Bus     uint16
	Address uint8
	// AllowTruncated keeps transfers whose payload was cut by the capture
	// (normally an error, because replay would return wrong data).
	AllowTruncated bool
}

// Stats summarises a conversion.
type Stats struct {
	Events     int
	Transfers  int
	Unpaired   int // completions without a matching submit (capture began mid-URB)
	Truncated  int
	Iso        int
	OtherDevs  int // events belonging to other devices on the bus
	Addresses  []uint8
	Descriptor int
}

type devKey struct {
	bus  uint16
	addr uint8
}

// Convert reads a usbmon pcap and returns the trace of the selected device.
func Convert(r io.Reader, opts Options) (*trace.Trace, Stats, error) {
	if opts.VendorID == 0 {
		opts.VendorID = usb.VendorV370
	}
	if opts.ProductID == 0 {
		opts.ProductID = usb.ProductV370
	}
	pr, err := NewReader(r)
	if err != nil {
		return nil, Stats{}, err
	}
	var events []*Event
	for {
		ev, err := pr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, Stats{}, err
		}
		events = append(events, ev)
	}
	return convertEvents(events, opts)
}

func isGetDeviceDescriptor(s usb.Setup) bool {
	return s.RequestType == 0x80 && s.Request == usb.ReqGetDescriptor && s.Value>>8 == uint16(usb.DescDevice)
}

func convertEvents(events []*Event, opts Options) (*trace.Trace, Stats, error) {
	st := Stats{Events: len(events)}

	// Pass 1: find device addresses by their device descriptor.
	targets := map[devKey]bool{}
	if opts.Address != 0 {
		targets[devKey{opts.Bus, opts.Address}] = true
	} else {
		submits := map[uint64]*Event{}
		for _, ev := range events {
			if ev.TransferType != XferControl {
				continue
			}
			switch ev.Type {
			case 'S':
				submits[ev.ID] = ev
			case 'C':
				sub := submits[ev.ID]
				delete(submits, ev.ID)
				if sub == nil || !sub.SetupPresent || ev.Status != 0 {
					continue
				}
				s, _ := usb.ParseSetup(sub.Setup[:])
				if !isGetDeviceDescriptor(s) || len(ev.Data) < 12 {
					continue
				}
				vid := binary.LittleEndian.Uint16(ev.Data[8:10])
				pid := binary.LittleEndian.Uint16(ev.Data[10:12])
				if vid == opts.VendorID && pid == opts.ProductID {
					targets[devKey{ev.Bus, ev.Device}] = true
				}
			}
		}
		if len(targets) == 0 {
			return nil, st, fmt.Errorf("no %04x:%04x device descriptor in capture; "+
				"replug the scanner while capturing, or pass --bus/--dev", opts.VendorID, opts.ProductID)
		}
	}
	match := func(ev *Event) bool {
		if opts.Address != 0 && opts.Bus == 0 {
			return ev.Device == opts.Address
		}
		return targets[devKey{ev.Bus, ev.Device}]
	}

	// Pass 2: pair submit/complete and build transfers in completion order.
	t := &trace.Trace{Meta: trace.Meta{VendorID: opts.VendorID, ProductID: opts.ProductID}}
	submits := map[uint64]*Event{}
	descs := map[[2]uint16]int{} // (value,index) → index in t.Descriptors
	addrSeen := map[uint8]bool{}
	var t0 int64 = -1

	for _, ev := range events {
		if !match(ev) {
			st.OtherDevs++
			continue
		}
		if ev.TransferType == XferIso {
			st.Iso++
			continue
		}
		if ev.Type == 'S' {
			submits[ev.ID] = ev
			continue
		}
		if ev.Type != 'C' && ev.Type != 'E' {
			continue
		}
		sub := submits[ev.ID]
		delete(submits, ev.ID)

		tr := trace.Transfer{Endpoint: ev.Endpoint, Status: ev.Status}
		in := ev.Endpoint&usb.DirIn != 0
		switch ev.TransferType {
		case XferControl:
			if sub == nil || !sub.SetupPresent {
				st.Unpaired++
				continue
			}
			tr.Type = trace.TypeControl
			tr.Endpoint = 0
			tr.Setup = append([]byte(nil), sub.Setup[:]...)
			in = sub.Setup[0]&usb.DirIn != 0
		case XferBulk:
			tr.Type = trace.TypeBulk
		case XferInterrupt:
			tr.Type = trace.TypeInterrupt
		default:
			continue
		}

		src := ev // IN data arrives with the completion
		if !in {
			if sub == nil {
				st.Unpaired++
				continue
			}
			src = sub // OUT data is captured at submit time
		}
		truncated := src.Truncated
		if in && ev.Status == 0 && ev.DataLen < ev.URBLen {
			truncated = true
		}
		if truncated {
			st.Truncated++
			if !opts.AllowTruncated {
				return nil, st, fmt.Errorf("transfer on ep 0x%02x (urb %x) was truncated by the capture; "+
					"increase the usbmon/pcap snap length or pass --allow-truncated", ev.Endpoint, ev.ID)
			}
		}
		tr.Data = append([]byte(nil), src.Data...)

		ts := ev.TimeSec*1_000_000 + int64(ev.TimeUSec)
		if t0 < 0 {
			t0 = ts
		}
		tr.TimeUS = ts - t0
		tr.Seq = len(t.Transfers) + 1
		if err := tr.Validate(); err != nil {
			return nil, st, err
		}
		t.Transfers = append(t.Transfers, tr)
		addrSeen[ev.Device] = true
		if t.Meta.Bus == 0 {
			t.Meta.Bus = ev.Bus
		}

		// Record standard GET_DESCRIPTOR responses (keep the longest).
		if tr.Type == trace.TypeControl && ev.Status == 0 && len(tr.Data) >= 2 {
			s, _ := usb.ParseSetup(tr.Setup)
			if s.RequestType == 0x80 && s.Request == usb.ReqGetDescriptor {
				k := [2]uint16{s.Value, s.Index}
				if i, ok := descs[k]; ok {
					if len(tr.Data) > len(t.Descriptors[i].Data) {
						t.Descriptors[i].Data = tr.Data
					}
				} else {
					descs[k] = len(t.Descriptors)
					t.Descriptors = append(t.Descriptors, trace.Descriptor{Value: s.Value, Index: s.Index, Data: tr.Data})
				}
			}
		}
	}
	st.Unpaired += len(submits) // submitted but never completed
	for a := range addrSeen {
		t.Meta.Addresses = append(t.Meta.Addresses, a)
	}
	slices.Sort(t.Meta.Addresses)
	st.Addresses = t.Meta.Addresses
	st.Transfers = len(t.Transfers)
	st.Descriptor = len(t.Descriptors)
	if len(t.Transfers) == 0 {
		return nil, st, fmt.Errorf("no transfers for the selected device")
	}
	return t, st, nil
}
