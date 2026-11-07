//go:build integration && hwsim

package hwsim

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/sim/tracecheck"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/usb/usbfs"
)

// TestUsbfsReplay drives our pure-Go usbfs transport through the kernel
// (vhci-hcd) against the simulator and checks every answer against the
// trace. Run on its own: scripts/hwsim-test.sh TRACE --run TestUsbfsReplay
func TestUsbfsReplay(t *testing.T) {
	tracePath := env(t, "USBSIM_TRACE")
	statusURL := env(t, "USBSIM_STATUS_URL")
	tr, err := trace.Load(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	devs, err := usbfs.Find("", usb.VendorV370, usb.ProductV370)
	if err != nil {
		t.Fatal(err)
	}
	info, err := usbfs.Select(devs, usbfs.OnlySimulated)
	if err != nil {
		t.Fatalf("simulated scanner not found: %v (devices: %v)", err, devs)
	}
	t.Logf("using %s", info)
	dev, err := usbfs.Open(info)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rep, err := tracecheck.Run(ctx, dev, tr, tracecheck.Options{})
	if err != nil {
		t.Fatalf("after %+v: %v", rep, err)
	}
	st := simStats(t, statusURL)
	t.Logf("usbfs replay: %d commands, %d reads, %.1f MB in, %s (%.1f MB/s); simulator: %+v",
		rep.Commands, rep.Reads, float64(rep.BytesIn)/1e6, rep.Duration.Round(time.Millisecond),
		float64(rep.BytesIn)/1e6/rep.Duration.Seconds(), st)
	if st.Unmatched != 0 || st.Fuzzy != 0 {
		t.Errorf("simulator saw unmatched=%d fuzzy=%d, want exact replay", st.Unmatched, st.Fuzzy)
	}
	if os.Getenv("HWSIM_STRICT") == "1" && st.Discarded != 0 {
		t.Errorf("simulator discarded %d chunks", st.Discarded)
	}
}
