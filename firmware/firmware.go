// Package firmware loads and validates the V370 firmware file esfwdd.bin.
//
// The file is the vendor's proprietary data and is NOT part of this library. It
// must be placed at DefaultPath (relative to the working directory the
// program is started in), or passed explicitly. See README.md for how to
// obtain it; scripts/install-firmware.sh copies it out of the vendor app.
package firmware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultPath is where the firmware is expected, relative to the working
// directory of the program.
const DefaultPath = "firmware/esfwdd.bin"

// Known-good file from the vendor app 6.7.82.0.
const (
	Size   = 65793
	SHA256 = "2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954"

	headerLen  = 256
	PayloadLen = 65536
)

var magic = []byte("EDLA")

// ErrNotFound means the firmware file is missing at the expected location.
var ErrNotFound = errors.New("firmware: esfwdd.bin not found")

// Firmware is a validated firmware image.
type Firmware struct {
	payload []byte
	hash    string // SHA-256 of the file; "" for FromPayload
}

// Default loads DefaultPath (relative to the working directory). The error
// names the absolute path that was searched.
func Default() (*Firmware, error) {
	abs, err := filepath.Abs(DefaultPath)
	if err != nil {
		abs = DefaultPath
	}
	fw, err := Load(abs, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s (copy the vendor's esfwdd.bin there; see lib-v370/scanstation/README.md "+
			"or run lib-v370/scanstation/firmware.sh .)", ErrNotFound, abs)
	}
	return fw, err
}

// Load reads a firmware file. Unless allowUnknown is set, its SHA-256 must
// match the known-good file.
func Load(path string, allowUnknown bool) (*Firmware, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b, allowUnknown)
}

// Parse validates a complete firmware file.
func Parse(b []byte, allowUnknown bool) (*Firmware, error) {
	if len(b) != Size {
		return nil, fmt.Errorf("firmware: size %d, want %d", len(b), Size)
	}
	if !bytes.HasPrefix(b, magic) {
		return nil, errors.New("firmware: missing EDLA header")
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	if h != SHA256 && !allowUnknown {
		return nil, fmt.Errorf("firmware: sha256 %s is not the known V370 firmware (%s)", h, SHA256)
	}
	return &Firmware{payload: append([]byte(nil), b[headerLen:headerLen+PayloadLen]...), hash: h}, nil
}

// FromPayload wraps the 65 536 bytes that are sent to the scanner, e.g. taken
// from a recorded USB trace. For simulators and tests: there is no file
// header to verify, so no SHA-256 check is possible.
func FromPayload(p []byte) (*Firmware, error) {
	if len(p) != PayloadLen {
		return nil, fmt.Errorf("firmware: payload has %d bytes, want %d", len(p), PayloadLen)
	}
	return &Firmware{payload: append([]byte(nil), p...)}, nil
}

// SHA256 returns the hex digest of the file ("" for FromPayload).
func (f *Firmware) SHA256() string { return f.hash }

// Payload returns the bytes that are sent to the scanner (65 536 bytes).
func (f *Firmware) Payload() []byte { return append([]byte(nil), f.payload...) }

// Checksum is the byte sent after the payload: sum of the payload mod 256.
func (f *Firmware) Checksum() byte {
	var s byte
	for _, c := range f.payload {
		s += c
	}
	return s
}
