package firmware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// A Patch is our own change to one known firmware build: bytes written at
// payload offsets. It contains none of the vendor's code; it is applied in
// memory to the vendor's file just before the upload (the firmware lives
// in RAM only, so a power cycle restores the original). Both ends are
// pinned: the input file's SHA-256 and the patched payload's SHA-256.
type Patch struct {
	Name        string
	Description string
	Base        string // SHA-256 of the firmware file the patch applies to
	Result      string // SHA-256 of the patched payload
	Changes     []Change
}

// Change writes Bytes at payload offset Offset.
type Change struct {
	Offset int
	Bytes  []byte
}

// Apply returns a copy of f with p applied. f must be the file p was made
// for, and the result must hash to p.Result.
func (f *Firmware) Apply(p Patch) (*Firmware, error) {
	if f.hash != p.Base {
		return nil, fmt.Errorf("firmware: patch %s applies to %s, not to %q", p.Name, p.Base, f.hash)
	}
	out := append([]byte(nil), f.payload...)
	for _, c := range p.Changes {
		if c.Offset < 0 || c.Offset+len(c.Bytes) > len(out) {
			return nil, fmt.Errorf("firmware: patch %s: change at 0x%x outside the payload", p.Name, c.Offset)
		}
		copy(out[c.Offset:], c.Bytes)
	}
	sum := sha256.Sum256(out)
	if got := hex.EncodeToString(sum[:]); got != p.Result {
		return nil, fmt.Errorf("firmware: patch %s gives payload %s, want %s", p.Name, got, p.Result)
	}
	return &Firmware{payload: out, hash: f.hash, patches: append(append([]string(nil), f.patches...), p.Name)}, nil
}

// Patches lists the patches applied to f.
func (f *Firmware) Patches() []string { return append([]string(nil), f.patches...) }

// LEDPatch gives the panel LED a third state and keeps the buttons working
// in every LED state (docs/firmware-patches.md):
//
//   - `1e de` takes two bits: bit 0 orange, bit 1 off (stock: bit 0 only);
//   - the timer tick's normal LED selection is replaced by a routine in the
//     free area (payload 0xC9D4) that shows off / orange / green and then
//     always continues with the button sampling and the idle timer (the
//     stock orange state skipped both, so buttons did not work while the LED
//     was orange).
var LEDPatch = Patch{
	Name:        "led",
	Description: "LED green / orange / off via 1e de; buttons active in every LED state",
	Base:        SHA256,
	Result:      "680e8a64e141cf78e6f424b58f4ce5a66f3a358efa7722bf9f7b9b6853ac53e1",
	Changes: []Change{
		// New LED routine (load address 0x0200C9D4):
		//   ld.w %r11,0x37fd00            LED outputs (bits 4 and 5)
		//   btst [%r8+0x1c2],3 / jreq L1  RAM 0x3C2 bit 3 set: off
		//   bset [%r11],4 / bset [%r11],5 / jp 0x02001392
		//   L1: btst [%r8+0x1c2],2 / jreq L2   bit 2 set: orange
		//   bclr [%r11],4 / bset [%r11],5 / jp 0x02001392
		//   L2: bset [%r11],4 / bclr [%r11],5 / jp 0x02001392   green
		{0xC9D4, mustHex("06c0f4df0b6c00c0c2c183a800c00518b4b0b5b0a4dfd41e00c0c2c182a800c00518b4acb5b0a4dfcb1eb4b0b5aca4dfc71e")},
		// Tick (0x02001374): jump to the new routine instead of the stock
		// two-state selection; the rest of that selection becomes unreachable.
		{0x1374, mustHex("5bc02f1e00000000")},
		// 1e de handler: keep two parameter bits (and 0x3) and clear both
		// RAM bits before setting them (and 0xf3).
		{0x6A2A, mustHex("3b70")},
		{0x6A32, mustHex("3a73")},
	},
}

// LEDPatchMarker is the payload offset and the first bytes of the patch's
// LED routine: reading them back from the scanner's RAM tells whether the
// patched firmware is running.
var LEDPatchMarker = struct {
	Address uint32
	Bytes   []byte
}{0x0200C9D4, LEDPatch.Changes[0].Bytes[:8]}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
