// Package tracecheck drives a usb.Transport through the host side of a
// recorded trace and verifies that the device answers exactly as recorded.
//
// Pointed at the simulator (in-process or attached via vhci-hcd) it tests
// the transport end to end with real V370 traffic, without the scanner.
package tracecheck

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

// Report summarises a run.
type Report struct {
	Commands int   // bulk OUT transfers sent
	Reads    int   // bulk IN transfers compared
	BytesOut int64 // bytes sent
	BytesIn  int64 // bytes received and compared
	Duration time.Duration
}

// Options tune a run.
type Options struct {
	// MaxCommands stops after this many OUT transfers (0 = whole trace).
	MaxCommands int
	// ReadTimeout bounds each IN transfer (default 10 s).
	ReadTimeout time.Duration
}

func hexPrefix(b []byte) string {
	if len(b) > 16 {
		return hex.EncodeToString(b[:16]) + "…"
	}
	return hex.EncodeToString(b)
}

// Run replays the host side of tr through tp. Standard control requests are
// skipped (the kernel/transport handles enumeration); a GET_DESCRIPTOR of
// the device descriptor is compared with the recorded one first.
func Run(ctx context.Context, tp usb.Transport, tr *trace.Trace, opt Options) (Report, error) {
	if opt.ReadTimeout == 0 {
		opt.ReadTimeout = 10 * time.Second
	}
	start := time.Now()
	var rep Report

	for _, d := range tr.Descriptors {
		if d.Value != uint16(usb.DescDevice)<<8 {
			continue
		}
		got, err := tp.Control(ctx, usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor,
			Value: d.Value, Length: uint16(len(d.Data))}, nil)
		if err != nil {
			return rep, fmt.Errorf("GET_DESCRIPTOR(device): %w", err)
		}
		if !bytes.Equal(got, d.Data) {
			return rep, fmt.Errorf("device descriptor differs: got %x want %x", got, d.Data)
		}
	}

	for _, t := range tr.Transfers {
		if t.Type != trace.TypeBulk || t.Status != 0 {
			continue // control transfers are enumeration; failed URBs carry nothing
		}
		if !t.IsIn() {
			if opt.MaxCommands > 0 && rep.Commands >= opt.MaxCommands {
				break
			}
			n, err := tp.BulkOut(ctx, t.Endpoint, t.Data)
			if err != nil {
				return rep, fmt.Errorf("seq %d: OUT %s: %w", t.Seq, hexPrefix(t.Data), err)
			}
			if n != len(t.Data) {
				return rep, fmt.Errorf("seq %d: short write %d/%d", t.Seq, n, len(t.Data))
			}
			rep.Commands++
			rep.BytesOut += int64(n)
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, opt.ReadTimeout)
		got, err := tp.BulkIn(rctx, t.Endpoint, max(len(t.Data), 1))
		cancel()
		if err != nil {
			return rep, fmt.Errorf("seq %d: IN (%d bytes expected): %w", t.Seq, len(t.Data), err)
		}
		if !bytes.Equal(got, t.Data) {
			return rep, fmt.Errorf("seq %d: IN differs: got %d bytes %s, want %d bytes %s",
				t.Seq, len(got), hexPrefix(got), len(t.Data), hexPrefix(t.Data))
		}
		rep.Reads++
		rep.BytesIn += int64(len(got))
	}
	rep.Duration = time.Since(start)
	return rep, nil
}
