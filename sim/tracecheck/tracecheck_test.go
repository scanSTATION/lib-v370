package tracecheck_test

import (
	"context"
	"github.com/scanSTATION/lib-v370/internal/testtraces"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/simtest"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/sim/tracecheck"
)

func transport(t *testing.T, tr *trace.Trace) *sim.Transport {
	t.Helper()
	r, err := replay.New(tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := sim.NewDevice(sim.DescriptorsFromTrace(tr), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return sim.NewTransport(dev)
}

func TestSyntheticTrace(t *testing.T) {
	tr := simtest.Trace()
	rep, err := tracecheck.Run(context.Background(), transport(t, tr), tr, tracecheck.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Commands != 4 || rep.Reads != 5 {
		t.Fatalf("report = %+v", rep)
	}
}

// The real power-on capture (firmware upload + 75 DPI scan) replays
// completely through the in-process simulator.
func TestRealPowerOnTrace(t *testing.T) {
	path := testtraces.Path(t, "a4-75dpi-poweron")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("trace missing: %v", err)
	}
	tr, err := trace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := tracecheck.Run(context.Background(), transport(t, tr), tr, tracecheck.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Commands != 407 || rep.BytesIn < 4_000_000 {
		t.Fatalf("report = %+v", rep)
	}
}

// A mismatching answer is reported with its sequence number.
func TestDetectsDifference(t *testing.T) {
	tr := simtest.Trace()
	served := simtest.Trace()
	for i := range served.Transfers {
		if string(served.Transfers[i].Data) == string(simtest.RespReady) {
			served.Transfers[i].Data = []byte{0x02, 0x7f}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tracecheck.Run(ctx, transport(t, served), tr, tracecheck.Options{})
	if err == nil || !strings.Contains(err.Error(), "IN differs") {
		t.Fatalf("expected a difference, got %v", err)
	}
}
