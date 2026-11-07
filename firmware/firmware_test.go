package firmware

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// synthetic builds a structurally valid file (unknown hash).
func synthetic() []byte {
	b := make([]byte, Size)
	copy(b, magic)
	for i := headerLen; i < headerLen+PayloadLen; i++ {
		b[i] = byte(i * 7)
	}
	return b
}

func TestParse(t *testing.T) {
	good := synthetic()
	if _, err := Parse(good, false); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("unknown hash must be rejected by default: %v", err)
	}
	fw, err := Parse(good, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fw.Payload(), good[headerLen:headerLen+PayloadLen]) {
		t.Fatal("payload")
	}
	var sum byte
	for _, c := range good[headerLen : headerLen+PayloadLen] {
		sum += c
	}
	if fw.Checksum() != sum {
		t.Fatal("checksum")
	}
	if _, err := Parse(good[:100], true); err == nil {
		t.Error("short file accepted")
	}
	bad := append([]byte(nil), good...)
	copy(bad, "XXXX")
	if _, err := Parse(bad, true); err == nil {
		t.Error("missing EDLA accepted")
	}
}

func TestFromPayload(t *testing.T) {
	if _, err := FromPayload(make([]byte, 10)); err == nil {
		t.Fatal("short payload accepted")
	}
	fw, err := FromPayload(make([]byte, PayloadLen))
	if err != nil || fw.SHA256() != "" || fw.Checksum() != 0 {
		t.Fatalf("%v", err)
	}
}

func TestDefaultMissing(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)
	_, err := Default()
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), filepath.Join(dir, DefaultPath)) {
		t.Fatalf("err = %v", err)
	}
	os.MkdirAll(filepath.Join(dir, "firmware"), 0o755)
	os.WriteFile(filepath.Join(dir, DefaultPath), synthetic(), 0o644)
	if _, err := Default(); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("a wrong file in ./firmware must be rejected: %v", err)
	}
}

// The real file, when available (LIBV370_FIRMWARE=/path/to/esfwdd.bin).
func TestRealFirmware(t *testing.T) {
	p := os.Getenv("LIBV370_FIRMWARE")
	if p == "" {
		t.Skip("LIBV370_FIRMWARE not set")
	}
	fw, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if fw.Checksum() != 0xaf || !bytes.HasPrefix(fw.Payload(), []byte{0x00, 0x10, 0x00, 0x02, 0x2c, 0x01}) {
		t.Fatal("unexpected content")
	}
}
