//go:build integration && hwsim

// Package hwsim holds tests that need the simulator attached to the kernel
// via vhci-hcd (root). Run them with scripts/hwsim-test.sh TRACE --run TEST.
package hwsim

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/sim/replay"
)

func env(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		t.Skipf("%s not set — run via scripts/hwsim-test.sh", k)
	}
	return v
}
func simStats(t *testing.T, url string) replay.Stats {
	t.Helper()
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc struct {
		Replay replay.Stats `json:"replay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc.Replay
}
