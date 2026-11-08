package synth_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/rawimg"
	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/synth"
	"github.com/scanSTATION/lib-v370/v370"
)

func noSleep(context.Context, time.Duration) error { return nil }

// dummyFirmware is a 64 KiB payload that is not the vendor's firmware.
func dummyFirmware(t *testing.T) *firmware.Firmware {
	t.Helper()
	p := make([]byte, 65536)
	for i := range p {
		p[i] = byte(i * 7)
	}
	fw, err := firmware.FromPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	return fw
}

func open(t *testing.T, opt synth.Options) (*synth.Device, *v370.Scanner) {
	t.Helper()
	dev, err := synth.New(opt)
	if err != nil {
		t.Fatal(err)
	}
	usbdev, err := sim.NewDevice(sim.DefaultDescriptors(), dev, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := v370.Open(context.Background(), sim.NewTransport(usbdev),
		v370.Options{Sleep: noSleep, Firmware: dummyFirmware(t)})
	if err != nil {
		t.Fatal(err)
	}
	return dev, s
}

// scan runs a profile and returns the decoded image's mismatches against
// synth.Pattern.
func scan(t *testing.T, s *v370.Scanner, p *v370.Profile) {
	t.Helper()
	var img bytes.Buffer
	err := s.RunProfile(context.Background(), p, func(tag string, b []byte) error {
		if tag == v370.TagImage {
			img.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: %v", p.Name, err)
	}
	g, err := p.Geometry()
	if err != nil {
		t.Fatal(err)
	}
	out, err := rawimg.Decode(&img, g)
	if err != nil {
		t.Fatalf("%s: decode: %v", p.Name, err)
	}
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			r, gg, b := synth.Pattern(x, y)
			px := out.RGBAAt(x, y)
			if px.R != r || px.G != gg || px.B != b {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %d/%d/%d (colour lines misaligned?)", p.Name, x, y, px, r, gg, b)
			}
		}
	}
}

// Power-on state: the driver uploads (dummy) firmware, then every profile
// small enough for a unit test scans and decodes exactly to the pattern.
func TestProfilesScanAndDecode(t *testing.T) {
	dev, s := open(t, synth.Options{})
	if !s.FirmwareUploaded || !dev.FirmwareLoaded() {
		t.Fatal("firmware was not uploaded")
	}
	if got := s.Identity().String(); got != "SIM GT-F740 1.00" {
		t.Fatalf("identity %q", got)
	}
	n := 0
	for _, name := range v370.ProfileNames() {
		p, err := v370.LoadProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		if p.ImageBytes > 40<<20 {
			continue // large scans: covered by the same code path
		}
		scan(t, s, p)
		n++
	}
	if n < 5 || dev.Scans() != n {
		t.Fatalf("%d profiles scanned, device counted %d", n, dev.Scans())
	}
}

// A wrong firmware checksum is refused (NAK), like the ROM loader does.
func TestFirmwareChecksumChecked(t *testing.T) {
	dev, err := synth.New(synth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	usbdev, _ := sim.NewDevice(sim.DefaultDescriptors(), dev, nil)
	tp := sim.NewTransport(usbdev)
	ctx := context.Background()
	write := func(b []byte) {
		if _, err := tp.BulkOut(ctx, 0x02, b); err != nil {
			t.Fatal(err)
		}
	}
	read := func() []byte {
		b, err := tp.BulkIn(ctx, 0x81, 512)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	write([]byte{0x1b, 0x06})
	read()
	write([]byte{0x01, 0x00, 0x01, 0x00})
	write(make([]byte, 65536))
	write([]byte{0x01}) // sum of zeros is 0
	if r := read(); !bytes.Equal(r, []byte{0x15}) || dev.FirmwareLoaded() {
		t.Fatalf("bad checksum accepted: %x", r)
	}
}

// Buttons are reported once each, in order, without a session.
func TestButtons(t *testing.T) {
	dev, s := open(t, synth.Options{FirmwareLoaded: true})
	dev.PressButton(v370.ButtonPDF)
	dev.PressButton(v370.ButtonPower)
	for _, want := range []v370.Button{v370.ButtonPDF, v370.ButtonPower, v370.ButtonNone} {
		b, err := s.ReadButton(context.Background())
		if err != nil || b != want {
			t.Fatalf("button %v %v, want %v", b, err, want)
		}
	}
}

// A held press: reported once by 1e 85; the input byte (1e 83) shows it
// held until released, which gives the press duration.
func TestPressDuration(t *testing.T) {
	ctx := context.Background()
	dev, s := open(t, synth.Options{FirmwareLoaded: true})
	dev.HoldButton(v370.ButtonPDF, 700*time.Millisecond)
	if b, err := s.ReadButton(ctx); err != nil || b != v370.ButtonPDF {
		t.Fatalf("press %v %v", b, err)
	}
	if b, err := s.HeldButton(ctx); err != nil || b != v370.ButtonPDF {
		t.Fatalf("held %v %v", b, err)
	}
	d, err := s.PressDuration(ctx, v370.ButtonPDF, 20*time.Millisecond, 5*time.Second)
	if err != nil || d < 650*time.Millisecond || d > 900*time.Millisecond {
		t.Fatalf("duration %v %v, want ≈ 700ms", d, err)
	}
	if b, _ := s.HeldButton(ctx); b != v370.ButtonNone {
		t.Fatalf("still held: %v", b)
	}
	// A short press is released by the time it is reported.
	dev.PressButton(v370.ButtonEmail)
	s.ReadButton(ctx)
	if d, err := s.PressDuration(ctx, v370.ButtonEmail, 20*time.Millisecond, time.Second); err != nil || d > 350*time.Millisecond {
		t.Fatalf("short press %v %v", d, err)
	}
	// The limit caps a button that is never released.
	dev.HoldButton(v370.ButtonPDF, time.Hour)
	s.ReadButton(ctx)
	if d, _ := s.PressDuration(ctx, v370.ButtonPDF, 20*time.Millisecond, 400*time.Millisecond); d != 400*time.Millisecond {
		t.Fatalf("limit %v", d)
	}
}

func TestLED(t *testing.T) {
	ctx := context.Background()
	for _, patched := range []bool{false, true} {
		dev, s := open(t, synth.Options{FirmwareLoaded: true, LEDPatch: patched})
		if p, err := s.LEDPatched(ctx); err != nil || p != patched {
			t.Fatalf("patched = %v %v, want %v", p, err, patched)
		}
		for _, l := range []v370.LED{v370.LEDOrange, v370.LEDOff, v370.LEDGreen} {
			if err := s.SetLED(ctx, l); err != nil {
				t.Fatal(err)
			}
		}
		want := "[orange green green]" // stock: off shows green
		if patched {
			want = "[orange off green]"
		}
		if got := fmt.Sprint(dev.LEDs()); got != want {
			t.Fatalf("patched %v: LEDs %s, want %s", patched, got, want)
		}
	}
}

// With the LED patch the host switches to fast blinking just before the
// image pass; the end of the job clears it. Stock firmware gets no 1e de 80,
// and nothing is sent unless the patch is known to run.
func TestTransferBlink(t *testing.T) {
	for _, tc := range []struct {
		patch, off bool
		want       int
	}{{true, false, 1}, {true, true, 0}, {false, false, 0}} {
		dev, err := synth.New(synth.Options{LEDPatch: tc.patch, FirmwareLoaded: true})
		if err != nil {
			t.Fatal(err)
		}
		usbdev, err := sim.NewDevice(sim.DefaultDescriptors(), dev, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		s, err := v370.Open(ctx, sim.NewTransport(usbdev), v370.Options{NoTransferBlink: tc.off, Sleep: noSleep})
		if err != nil {
			t.Fatal(err)
		}
		// The firmware was loaded earlier: the host learns about the patch
		// from LEDPatched (as the scanSTATION server does before each scan).
		if _, err := s.LEDPatched(ctx); err != nil {
			t.Fatal(err)
		}
		p, _ := v370.LoadProfile("a4-100dpi-color")
		if err := s.RunProfile(ctx, p, nil); err != nil {
			t.Fatal(err)
		}
		if got := dev.FastImagePasses(); got != tc.want {
			t.Errorf("patch %v, off %v: %d fast image passes, want %d", tc.patch, tc.off, got, tc.want)
		}
		if len(dev.LEDs()) != 0 {
			t.Errorf("colour changed: %v", dev.LEDs())
		}
		s.Close(ctx)
	}
}
