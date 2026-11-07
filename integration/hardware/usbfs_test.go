//go:build hardware

// Package hardware holds read-only smoke tests against the REAL scanner.
// Run with the V370 connected and no vendor software running:
//
//	go test -tags hardware -count=1 -v ./integration/hardware/
package hardware

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/usb/usbfs"
	"github.com/scanSTATION/lib-v370/v370"
)

func TestRealScannerQueries(t *testing.T) {
	devs, err := usbfs.Find("", usb.VendorV370, usb.ProductV370)
	if err != nil {
		t.Fatal(err)
	}
	info, err := usbfs.Select(devs, usbfs.OnlyReal)
	if err != nil {
		t.Skipf("no real V370 connected: %v", err)
	}
	dev, err := usbfs.Open(info)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	t.Logf("opened %s", info)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	d, err := dev.Control(ctx, usb.Setup{RequestType: 0x80, Request: usb.ReqGetDescriptor, Value: 0x0100, Length: 18}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dd, err := usb.ParseDeviceDescriptor(d)
	if err != nil || dd.VendorID != usb.VendorV370 || dd.ProductID != usb.ProductV370 {
		t.Fatalf("device descriptor %x: %v", d, err)
	}

	query := func(cmd []byte, n int) []byte {
		t.Helper()
		if _, err := dev.BulkOut(ctx, usb.EndpointOut, cmd); err != nil {
			t.Fatalf("OUT %x: %v", cmd, err)
		}
		r, err := dev.BulkIn(ctx, usb.EndpointIn, n)
		if err != nil {
			t.Fatalf("IN after %x: %v", cmd, err)
		}
		return r
	}
	st := query([]byte{0x1b, 0x03}, 512)
	t.Logf("status 1b03 → %x", st)
	if len(st) != 2 {
		t.Fatalf("status reply %x, want 2 bytes", st)
	}
	switch st[0] {
	case 0x18:
		t.Log("firmware loaded, ready")
	case 0x28:
		t.Log("firmware NOT loaded (fresh power-on)")
	default:
		t.Logf("other status 0x%02x", st[0])
	}
	if st[0] == 0x28 {
		return // identity query needs firmware; nothing more to check read-only
	}
	id := query([]byte{0x1b, 0x13}, 512)
	t.Logf("identity 1b13 → %q", id)
	if len(id) != 28 || !bytes.Contains(id[8:24], []byte("GT-F740")) {
		t.Fatalf("identity %q is not a GT-F740 (V37/V370)", id)
	}
}

// TestRealScannerSession opens and closes a session with our protocol layer
// (uploads the firmware if the scanner was just switched on).
func TestRealScannerSession(t *testing.T) {
	devs, err := usbfs.Find("", usb.VendorV370, usb.ProductV370)
	if err != nil {
		t.Fatal(err)
	}
	info, err := usbfs.Select(devs, usbfs.OnlyReal)
	if err != nil {
		t.Skipf("no real V370 connected: %v", err)
	}
	dev, err := usbfs.Open(info)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := v370.Open(ctx, dev, v370.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("identity %s, firmware uploaded: %v", s.Identity(), s.FirmwareUploaded)
	if s.Identity().Model != "GT-F740" {
		t.Errorf("model %q", s.Identity().Model)
	}
	n, err := s.ReadRegister(ctx, 0x0005)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("scan counter (register 0x0005): %d", n)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
