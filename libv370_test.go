package libv370_test

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/scanSTATION/lib-v370/internal/testtraces"
	"github.com/scanSTATION/lib-v370/sim/synth"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370"
	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/v370"
)

// The high-level API end to end against the simulator replaying the
// power-on capture: firmware upload, scan, decode, buttons query refused by
// nothing, close.
func TestOpenDeviceScan(t *testing.T) {
	tr, err := trace.Load(testtraces.Path(t, "a4-75dpi-poweron"))
	if err != nil {
		t.Fatal(err)
	}
	var fw *firmware.Firmware
	var clock time.Time
	for _, x := range tr.Transfers {
		if x.Type == trace.TypeBulk && !x.IsIn() {
			if len(x.Data) == firmware.PayloadLen && fw == nil {
				fw, _ = firmware.FromPayload(x.Data)
			}
			if len(x.Data) == 6 && x.Data[0] == 0x0b && clock.IsZero() {
				clock = v370.ClockEpoch.Add(time.Duration(binary.LittleEndian.Uint32(x.Data[2:]))*time.Hour + time.Minute)
			}
		}
	}
	var r *replay.Responder
	opener := func() (usb.Transport, string, error) {
		var err error
		if r, err = replay.New(tr, nil); err != nil {
			return nil, "", err
		}
		dev, err := sim.NewDevice(sim.DescriptorsFromTrace(tr), r, nil)
		if err != nil {
			return nil, "", err
		}
		return sim.NewTransport(dev), "simulator", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dev, err := libv370.OpenDevice(ctx, libv370.Options{Open: opener, Firmware: fw, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	if !dev.FirmwareUploaded() || dev.Identity().Model != "GT-F740" {
		t.Fatalf("firmware %v identity %v", dev.FirmwareUploaded(), dev.Identity())
	}
	scan, err := dev.Scan(ctx, "a4-100dpi-color")
	if err != nil {
		t.Fatal(err)
	}
	if b := scan.Image.Bounds(); b.Dx() != 848 || b.Dy() != 1168 || len(scan.Raw) != scan.Profile.ImageBytes {
		t.Fatalf("image %v raw %d", b, len(scan.Raw))
	}
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Scan(ctx, "a4-100dpi-color"); !errors.Is(err, libv370.ErrClosed) {
		t.Fatalf("scan after close: %v", err)
	}
	if st := r.Stats(); st.Unmatched != 0 || st.Fuzzy != 0 {
		t.Fatalf("simulator diverged: %+v", st)
	}
	if len(libv370.Profiles()) != 9 {
		t.Fatalf("profiles %v", libv370.Profiles())
	}
}

// The high-level API end to end against the synthetic scanner (no
// recordings): dummy firmware upload, two scans in one session (exact
// pattern), session-less button reads, close.
func TestOpenDeviceSynthetic(t *testing.T) {
	dev, err := synth.New(synth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := make([]byte, firmware.PayloadLen)
	for i := range p {
		p[i] = byte(i)
	}
	fw, err := firmware.FromPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d, err := libv370.OpenDevice(ctx, libv370.Options{Open: libv370.SyntheticScanner(dev), Firmware: fw, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.FirmwareUploaded() {
		t.Fatal("firmware not uploaded")
	}
	for _, name := range []string{"a4-100dpi-color", "user-50x50mm-600dpi"} {
		scan, err := d.Scan(ctx, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r, g, b := synth.Pattern(17, 23)
		if px := scan.Image.RGBAAt(17, 23); px.R != r || px.G != g || px.B != b {
			t.Fatalf("%s: pixel %v", name, px)
		}
	}
	dev.PressButton(v370.ButtonPDF)
	if b, err := d.ReadButton(ctx); err != nil || b != v370.ButtonPDF {
		t.Fatalf("button %v %v", b, err)
	}
	if dev.Scans() != 2 {
		t.Fatalf("device saw %d scans", dev.Scans())
	}
}
