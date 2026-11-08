//go:build integration && hwsim

package hwsim

import (
	"context"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/usb/usbfs"
	"github.com/scanSTATION/lib-v370/v370"
)

// TestDirectOpen opens a session with our protocol layer over usbfs →
// vhci-hcd → simulator. With the power-on trace this includes the firmware
// upload. Run on its own: scripts/hwsim-test.sh TRACE --run TestDirectOpen
func TestDirectOpen(t *testing.T) {
	tr, err := trace.Load(env(t, "USBSIM_TRACE"))
	if err != nil {
		t.Fatal(err)
	}
	statusURL := env(t, "USBSIM_STATUS_URL")
	devs, err := usbfs.Find("", usb.VendorV370, usb.ProductV370)
	if err != nil {
		t.Fatal(err)
	}
	info, err := usbfs.Select(devs, usbfs.OnlySimulated)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := usbfs.Open(info)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	recorded := v370.ClockEpoch.Add(24985*time.Hour + 30*time.Minute) // clock value in the captures
	var fw *firmware.Firmware                                         // the power-on trace contains the payload
	for _, x := range tr.Transfers {
		if x.Type == trace.TypeBulk && !x.IsIn() && len(x.Data) == firmware.PayloadLen {
			fw, _ = firmware.FromPayload(x.Data)
		}
	}
	s, err := v370.Open(ctx, dev, v370.Options{
		Firmware: fw,
		Now:      func() time.Time { return recorded },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		// replaying a recording: keep the recorded (vendor) carriage start
		VendorStart: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("identity %s, firmware uploaded: %v (trace %s)", s.Identity(), s.FirmwareUploaded, tr.Meta.Note)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st := simStats(t, statusURL)
	t.Logf("simulator: commands=%d matched=%d fuzzy=%d unmatched=%d", st.Commands, st.Matched, st.Fuzzy, st.Unmatched)
	if st.Unmatched != 0 || st.Fuzzy != 0 {
		t.Fatalf("diverged from the capture: %+v", st.Divergences)
	}
}
