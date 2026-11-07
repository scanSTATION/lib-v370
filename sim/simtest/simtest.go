// Package simtest provides fixtures for simulator tests.
//
// The traces built here are SYNTHETIC: their command bytes merely look like
// ESC/I and are NOT what a V370 sends or answers. They exercise the replay
// machinery only. Real behaviour comes from a capture (scripts/capture-scan.sh).
package simtest

import (
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

// Synthetic command/response bytes used by Trace.
var (
	CmdStatus   = []byte{0x1b, 'F'}
	CmdStart    = []byte{0x1b, 'G'}
	CmdAck      = []byte{0x06}
	RespBusy    = []byte{0x02, 0x80}
	RespReady   = []byte{0x02, 0x00}
	ImageBlock1 = []byte("block-1-data")
	ImageBlock2 = []byte("block-2-data")
	VendorSetup = usb.Setup{RequestType: 0xc0, Request: 0x10, Value: 1, Length: 4}
	VendorReply = []byte{0xde, 0xad, 0xbe, 0xef}
)

// Trace returns a small synthetic session:
//
//	GET_DESCRIPTOR(device)            (standard, answered by the device)
//	vendor IN request 0x10            → deadbeef
//	OUT status                        → busy
//	OUT status                        → ready
//	OUT start                         → block-1 (split in two IN transfers)
//	OUT ack                           → block-2
func Trace() *trace.Trace {
	dev, _, _ := usb.DefaultV370Descriptors()
	devSetup := usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor, Value: 0x0100, Length: 18}
	seq := 0
	next := func(tr trace.Transfer) trace.Transfer { seq++; tr.Seq = seq; return tr }
	out := func(b []byte) trace.Transfer {
		return next(trace.Transfer{Type: trace.TypeBulk, Endpoint: usb.EndpointOut, Data: b})
	}
	in := func(b []byte) trace.Transfer {
		return next(trace.Transfer{Type: trace.TypeBulk, Endpoint: usb.EndpointIn, Data: b})
	}
	return &trace.Trace{
		Meta:        trace.Meta{VendorID: usb.VendorV370, ProductID: usb.ProductV370, Note: "synthetic test fixture"},
		Descriptors: []trace.Descriptor{{Value: 0x0100, Data: dev}},
		Transfers: []trace.Transfer{
			next(trace.Transfer{Type: trace.TypeControl, Setup: devSetup.Bytes(), Data: dev}),
			next(trace.Transfer{Type: trace.TypeControl, Setup: VendorSetup.Bytes(), Data: VendorReply}),
			out(CmdStatus), in(RespBusy),
			out(CmdStatus), in(RespReady),
			out(CmdStart), in(ImageBlock1[:6]), in(ImageBlock1[6:]),
			// a cancelled IN (host timeout) must be ignored by replay
			next(trace.Transfer{Type: trace.TypeBulk, Endpoint: usb.EndpointIn, Status: -2}),
			out(CmdAck), in(ImageBlock2),
		},
	}
}
