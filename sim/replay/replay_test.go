package replay_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/simtest"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

func newTransport(t *testing.T) (*sim.Transport, *replay.Responder) {
	t.Helper()
	tr := simtest.Trace()
	r, err := replay.New(tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := sim.NewDevice(sim.DescriptorsFromTrace(tr), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return sim.NewTransport(dev), r
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return c
}

func command(t *testing.T, tp *sim.Transport, cmd []byte) {
	t.Helper()
	if _, err := tp.BulkOut(ctx(t), usb.EndpointOut, cmd); err != nil {
		t.Fatalf("BulkOut %x: %v", cmd, err)
	}
}

func read(t *testing.T, tp *sim.Transport, max int) []byte {
	t.Helper()
	b, err := tp.BulkIn(ctx(t), usb.EndpointIn, max)
	if err != nil {
		t.Fatalf("BulkIn: %v", err)
	}
	return b
}

func TestReplaySession(t *testing.T) {
	tp, r := newTransport(t)

	// Vendor control request answered from the trace.
	got, err := tp.Control(ctx(t), simtest.VendorSetup, nil)
	if err != nil || !bytes.Equal(got, simtest.VendorReply) {
		t.Fatalf("vendor control = %x, %v", got, err)
	}

	// Repeated identical command returns recorded responses in order.
	command(t, tp, simtest.CmdStatus)
	if b := read(t, tp, 512); !bytes.Equal(b, simtest.RespBusy) {
		t.Fatalf("first status = %x", b)
	}
	command(t, tp, simtest.CmdStatus)
	if b := read(t, tp, 512); !bytes.Equal(b, simtest.RespReady) {
		t.Fatalf("second status = %x", b)
	}
	// Exhausted occurrences repeat the last response.
	command(t, tp, simtest.CmdStatus)
	if b := read(t, tp, 512); !bytes.Equal(b, simtest.RespReady) {
		t.Fatalf("third status = %x", b)
	}

	// Multi-transfer response, read with a smaller buffer than recorded.
	command(t, tp, simtest.CmdStart)
	var img []byte
	img = append(img, read(t, tp, 4)...)
	img = append(img, read(t, tp, 512)...)
	img = append(img, read(t, tp, 512)...)
	if !bytes.Equal(img, simtest.ImageBlock1) {
		t.Fatalf("image block 1 = %q", img)
	}
	command(t, tp, simtest.CmdAck)
	if b := read(t, tp, 512); !bytes.Equal(b, simtest.ImageBlock2) {
		t.Fatalf("image block 2 = %q", b)
	}

	st := r.Stats()
	if st.Commands != 6 || st.Matched != 5 || st.Repeated != 1 || st.Unmatched != 0 {
		t.Errorf("stats = %+v", st)
	}
}

func TestReplayBlocksUntilCommand(t *testing.T) {
	tp, _ := newTransport(t)
	c, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := tp.BulkIn(c, usb.EndpointIn, 512); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("IN without a pending response must block, got %v", err)
	}

	done := make(chan []byte, 1)
	go func() {
		b, _ := tp.BulkIn(ctx(t), usb.EndpointIn, 512)
		done <- b
	}()
	time.Sleep(20 * time.Millisecond)
	command(t, tp, simtest.CmdStatus)
	select {
	case b := <-done:
		if !bytes.Equal(b, simtest.RespBusy) {
			t.Fatalf("got %x", b)
		}
	case <-time.After(time.Second):
		t.Fatal("pending IN was not woken by the command")
	}
}

func TestReplayDivergence(t *testing.T) {
	tp, r := newTransport(t)
	command(t, tp, []byte("not-in-trace"))
	if _, err := tp.Control(ctx(t), usb.Setup{RequestType: 0x40, Request: 0x99}, nil); !errors.Is(err, usb.ErrStall) {
		t.Fatalf("unknown control must stall, got %v", err)
	}
	st := r.Stats()
	if st.Unmatched != 2 || len(st.Divergences) != 2 || st.Divergences[0].Kind != "bulk" {
		t.Fatalf("stats = %+v", st)
	}
}

func TestDeviceStandardRequests(t *testing.T) {
	tp, r := newTransport(t)
	d, err := tp.Control(ctx(t), usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor, Value: 0x0100, Length: 8}, nil)
	if err != nil || len(d) != 8 || d[1] != usb.DescDevice {
		t.Fatalf("device descriptor (8 bytes) = %x %v", d, err)
	}
	cfg, err := tp.Control(ctx(t), usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor, Value: 0x0200, Length: 255}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := usb.ParseConfiguration(cfg)
	if err != nil || len(ci.Interfaces) != 1 || len(ci.Interfaces[0].Endpoints) != 2 {
		t.Fatalf("config = %+v %v", ci, err)
	}
	if _, err := tp.Control(ctx(t), usb.Setup{RequestType: 0x00, Request: usb.ReqSetConfiguration, Value: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Control(ctx(t), usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor, Value: 0x0f00, Length: 5}, nil); !errors.Is(err, usb.ErrStall) {
		t.Fatalf("unknown descriptor must stall, got %v", err)
	}
	if _, err := tp.BulkOut(ctx(t), 0x05, []byte{1}); !errors.Is(err, usb.ErrStall) {
		t.Fatalf("undeclared endpoint must stall, got %v", err)
	}
	if r.Stats().Commands != 0 {
		t.Error("standard requests must not reach the responder")
	}
}

func TestTransportClose(t *testing.T) {
	tp, _ := newTransport(t)
	done := make(chan error, 1)
	go func() {
		_, err := tp.BulkIn(context.Background(), usb.EndpointIn, 512)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	tp.Close()
	select {
	case err := <-done:
		if !errors.Is(err, sim.ErrClosed) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not wake BulkIn")
	}
}

// A command carrying a changing value (like the vendor app's clock command
// `0b 00 <hours>`) still matches its recorded counterpart.
func TestReplayFuzzyMatch(t *testing.T) {
	clock := []byte{0x0b, 0x00, 0x99, 0x61, 0x00, 0x00}
	tr := &trace.Trace{Transfers: []trace.Transfer{
		{Seq: 1, Type: trace.TypeBulk, Endpoint: usb.EndpointOut, Data: []byte{0x1b, 0x03}},
		{Seq: 2, Type: trace.TypeBulk, Endpoint: usb.EndpointIn, Data: []byte{0x18, 0x01}},
		{Seq: 3, Type: trace.TypeBulk, Endpoint: usb.EndpointOut, Data: clock},
		{Seq: 4, Type: trace.TypeBulk, Endpoint: usb.EndpointIn, Data: []byte{0x06}},
		{Seq: 5, Type: trace.TypeBulk, Endpoint: usb.EndpointOut, Data: []byte{0x1b, 0x13}},
		{Seq: 6, Type: trace.TypeBulk, Endpoint: usb.EndpointIn, Data: []byte{0x06}},
	}}
	r, err := replay.New(tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := sim.NewDevice(sim.DefaultDescriptors(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	tp := sim.NewTransport(dev)

	command(t, tp, []byte{0x1b, 0x03})
	read(t, tp, 512)
	command(t, tp, []byte{0x0b, 0x00, 0x9a, 0x61, 0x00, 0x00}) // one hour later
	if b := read(t, tp, 512); !bytes.Equal(b, []byte{0x06}) {
		t.Fatalf("fuzzy-matched reply = %x", b)
	}
	// Short commands never match fuzzily (2 bytes: limit 0).
	command(t, tp, []byte{0x1b, 0x14})
	// Too many differing bytes.
	command(t, tp, []byte{0x0b, 0xff, 0xff, 0xff, 0x00, 0x00})

	st := r.Stats()
	if st.Fuzzy != 1 || st.Matched != 1 || st.Unmatched != 2 || len(st.FuzzyHits) != 1 || st.FuzzyHits[0].Seq != 3 {
		t.Fatalf("stats = %+v", st)
	}
}

// A short parameter that follows different commands gets the answer
// recorded after the same previous command, even if the host skips commands.
func TestReplayContextMatch(t *testing.T) {
	out := func(seq int, b ...byte) trace.Transfer {
		return trace.Transfer{Seq: seq, Type: trace.TypeBulk, Endpoint: usb.EndpointOut, Data: b}
	}
	in := func(seq int, b ...byte) trace.Transfer {
		return trace.Transfer{Seq: seq, Type: trace.TypeBulk, Endpoint: usb.EndpointIn, Data: b}
	}
	tr := &trace.Trace{Transfers: []trace.Transfer{
		out(1, 0x1e, 0x87), in(2, 0x06), out(3, 0x00), in(4, 0x01, 0x02, 0x03), // query: 00 → 3 data bytes
		out(5, 0x1e, 0x93), in(6, 0x06), out(7, 0x00), in(8, 0x06), // session end: 00 → ACK
	}}
	r, err := replay.New(tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := sim.NewDevice(sim.DefaultDescriptors(), r, nil)
	tp := sim.NewTransport(dev)
	command(t, tp, []byte{0x1e, 0x93}) // skip 1e 87 entirely
	read(t, tp, 512)
	command(t, tp, []byte{0x00})
	if b := read(t, tp, 512); !bytes.Equal(b, []byte{0x06}) {
		t.Fatalf("00 after 1e93 = %x, want 06", b)
	}
	// Without matching context the plain order applies.
	command(t, tp, []byte{0x00})
	if b := read(t, tp, 512); !bytes.Equal(b, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("00 without context = %x", b)
	}
	if st := r.Stats(); st.Matched != 3 || st.Unmatched != 0 {
		t.Fatalf("stats %+v", st)
	}
}
