package firmware

import (
	"bytes"
	"os"
	"testing"
)

func TestPatchNeedsItsBase(t *testing.T) {
	fw, _ := FromPayload(make([]byte, PayloadLen))
	if _, err := fw.Apply(LEDPatch); err == nil {
		t.Fatal("patch applied to an unknown firmware")
	}
}

func TestLEDPatchDefinition(t *testing.T) {
	end := 0
	for _, c := range LEDPatch.Changes {
		if c.Offset < 0 || c.Offset+len(c.Bytes) > PayloadLen || len(c.Bytes)%2 != 0 || c.Offset%2 != 0 {
			t.Errorf("change at 0x%x (%d bytes)", c.Offset, len(c.Bytes))
		}
		end = max(end, c.Offset+len(c.Bytes))
	}
	if !bytes.Equal(LEDPatchMarker.Bytes, LEDPatch.Changes[0].Bytes[:len(LEDPatchMarker.Bytes)]) ||
		LEDPatchMarker.Address != 0x02000000+uint32(LEDPatch.Changes[0].Offset) {
		t.Fatal("marker does not match the routine")
	}
}

// With the real firmware (private: LIBV370_FIRMWARE=/path/to/esfwdd.bin)
// the patch applies, hashes as pinned and changes nothing else.
func TestLEDPatchOnRealFirmware(t *testing.T) {
	path := os.Getenv("LIBV370_FIRMWARE")
	if path == "" {
		t.Skip("set LIBV370_FIRMWARE to the vendor's esfwdd.bin")
	}
	fw, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	p, err := fw.Apply(LEDPatch)
	if err != nil {
		t.Fatal(err)
	}
	if p.SHA256() != fw.SHA256() || len(p.Patches()) != 1 || len(fw.Patches()) != 0 {
		t.Fatalf("patch bookkeeping %q %v", p.SHA256(), p.Patches())
	}
	a, b := fw.Payload(), p.Payload()
	changed := map[int]bool{}
	for _, c := range LEDPatch.Changes {
		for i := range c.Bytes {
			changed[c.Offset+i] = true
		}
	}
	for i := range a {
		if a[i] != b[i] && !changed[i] {
			t.Fatalf("byte 0x%x changed outside the patch", i)
		}
	}
	// The free area really was free.
	for i := 0xC9D4; i < 0xC9D4+len(LEDPatch.Changes[0].Bytes); i++ {
		if a[i] != 0xFF {
			t.Fatalf("payload 0x%x is not free", i)
		}
	}
}
