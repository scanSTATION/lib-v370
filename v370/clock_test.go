package v370

import (
	"bytes"
	"testing"
	"time"
)

func TestClockParam(t *testing.T) {
	cases := map[string][]byte{
		"2026-11-07T01:53:00Z": {0x0b, 0x00, 0x99, 0x61, 0x00, 0x00}, // capture 1
		"2026-11-07T02:01:00Z": {0x0b, 0x00, 0x9a, 0x61, 0x00, 0x00}, // replay an hour later
		"2023-01-01T00:00:00Z": {0x0b, 0x00, 0x00, 0x00, 0x00, 0x00}, // before the epoch
	}
	for ts, want := range cases {
		tm, _ := time.Parse(time.RFC3339, ts)
		if got := clockParam(tm); !bytes.Equal(got, want) {
			t.Errorf("%s: %x want %x", ts, got, want)
		}
	}
}

func TestParseIdentity(t *testing.T) {
	id, err := parseIdentity([]byte("SIM     GT-F740         1.00"))
	if err != nil || id.Vendor != "SIM" || id.Model != "GT-F740" || id.Version != "1.00" {
		t.Fatalf("%+v %v", id, err)
	}
	if _, err := parseIdentity([]byte("short")); err == nil {
		t.Fatal("expected error")
	}
}
