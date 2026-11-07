package usbip_test

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/simtest"
	"github.com/scanSTATION/lib-v370/sim/usbip"
	"github.com/scanSTATION/lib-v370/usb"
)

func startServer(t *testing.T) string {
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
	srv, err := usbip.NewServer(dev, "1-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return ln.Addr().String()
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { c.Close() })
	return c
}

func importDevice(t *testing.T, addr string) *usbip.Client {
	t.Helper()
	conn := dial(t, addr)
	info, err := usbip.Import(conn, "1-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.VendorID != usb.VendorV370 || info.ProductID != usb.ProductV370 || info.Speed != usbip.SpeedHigh {
		t.Fatalf("import info = %+v", info)
	}
	return usbip.NewClient(conn, info)
}

func recv(t *testing.T, c *usbip.Client) usbip.Reply {
	t.Helper()
	r, err := c.Recv()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDevList(t *testing.T) {
	addr := startServer(t)
	devs, err := usbip.DevList(dial(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].BusID != "1-1" || devs[0].ProductID != usb.ProductV370 {
		t.Fatalf("devlist = %+v", devs)
	}
	if len(devs[0].Interfaces) != 1 || devs[0].Interfaces[0].Class != 0xff {
		t.Fatalf("interfaces = %+v", devs[0].Interfaces)
	}
}

func TestImportUnknownBusID(t *testing.T) {
	addr := startServer(t)
	if _, err := usbip.Import(dial(t, addr), "9-9"); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestSubmitControlAndBulk(t *testing.T) {
	addr := startServer(t)
	c := importDevice(t, addr)

	var setup [8]byte
	copy(setup[:], usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor, Value: 0x0100, Length: 18}.Bytes())
	if err := c.SendSubmit(1, 0, true, setup, nil, 18); err != nil {
		t.Fatal(err)
	}
	r := recv(t, c)
	if r.Seq != 1 || r.Status != 0 || len(r.Data) != 18 || r.Data[1] != usb.DescDevice {
		t.Fatalf("control reply = %+v", r)
	}

	// Queue the IN first (as a real driver does), then send the command.
	c.SendSubmit(2, usb.EndpointIn, true, [8]byte{}, nil, 512)
	c.SendSubmit(3, usb.EndpointOut, false, [8]byte{}, simtest.CmdStatus, 0)
	got := map[uint32]usbip.Reply{}
	for range 2 {
		r := recv(t, c)
		got[r.Seq] = r
	}
	if r := got[3]; r.Status != 0 || r.Actual != int32(len(simtest.CmdStatus)) {
		t.Fatalf("OUT reply = %+v", r)
	}
	if r := got[2]; r.Status != 0 || !bytes.Equal(r.Data, simtest.RespBusy) {
		t.Fatalf("IN reply = %+v", r)
	}

	// Undeclared endpoint → stall (-EPIPE).
	c.SendSubmit(4, 0x05, false, [8]byte{}, []byte{1}, 0)
	if r := recv(t, c); r.Seq != 4 || r.Status != -usbip.ErrnoEPIPE {
		t.Fatalf("stall reply = %+v", r)
	}
}

func TestUnlinkPendingIn(t *testing.T) {
	addr := startServer(t)
	c := importDevice(t, addr)
	c.SendSubmit(1, usb.EndpointIn, true, [8]byte{}, nil, 512)
	time.Sleep(20 * time.Millisecond)
	c.SendUnlink(2, 1)
	r := recv(t, c)
	if r.Command != usbip.RetUnlink || r.Seq != 2 || r.Status != -usbip.ErrnoECONNRESET {
		t.Fatalf("unlink reply = %+v", r)
	}
	// The unlinked URB must not consume the next response.
	c.SendSubmit(3, usb.EndpointIn, true, [8]byte{}, nil, 512)
	c.SendSubmit(4, usb.EndpointOut, false, [8]byte{}, simtest.CmdStatus, 0)
	got := map[uint32]usbip.Reply{}
	for range 2 {
		r := recv(t, c)
		got[r.Seq] = r
	}
	if _, stale := got[1]; stale {
		t.Fatal("unlinked URB was completed")
	}
	if !bytes.Equal(got[3].Data, simtest.RespBusy) {
		t.Fatalf("IN after unlink = %+v", got[3])
	}
	// Unlinking a completed URB answers status 0.
	c.SendUnlink(5, 3)
	if r := recv(t, c); r.Seq != 5 || r.Status != 0 {
		t.Fatalf("late unlink = %+v", r)
	}
}

func TestSecondImportRejected(t *testing.T) {
	addr := startServer(t)
	importDevice(t, addr)
	if _, err := usbip.Import(dial(t, addr), "1-1"); err == nil {
		t.Fatal("second concurrent import must be refused")
	}
}

func TestMalformedHeaderDropsConnection(t *testing.T) {
	addr := startServer(t)
	c := importDevice(t, addr)
	// CMD_SUBMIT with an absurd transfer length.
	c.SendSubmit(1, usb.EndpointIn, true, [8]byte{}, nil, 1<<30)
	if _, err := c.Recv(); err == nil {
		t.Fatal("expected the server to drop the connection")
	}
	// The device is free again afterwards.
	deadline := time.Now().Add(time.Second)
	for {
		conn := dial(t, addr)
		if _, err := usbip.Import(conn, "1-1"); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("device not released after bad client")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
