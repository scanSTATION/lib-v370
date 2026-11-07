package synth_test

import (
	"bytes"
	"context"
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
