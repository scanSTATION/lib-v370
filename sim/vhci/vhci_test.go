package vhci

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const status = `hub port sta spd dev      sockfd local_busid
hs  0000 006 003 00010002 000003 1-1
hs  0001 004 000 00000000 000000 0-0
ss  0002 004 000 00000000 000000 0-0
`

func TestParseStatus(t *testing.T) {
	ports, err := ParseStatus(bufio.NewScanner(strings.NewReader(status)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 3 || ports[0].State != 6 || ports[1].Number != 1 || ports[2].Hub != "ss" {
		t.Fatalf("ports = %+v", ports)
	}
	if _, err := ParseStatus(bufio.NewScanner(strings.NewReader("hs xx 004"))); err == nil {
		t.Error("expected error for malformed line")
	}
}

func TestFreePortSkipsUsedAndSuperSpeed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644)
	p, err := Controller{Dir: dir}.FreePort()
	if err != nil || p != 1 {
		t.Fatalf("port=%d err=%v", p, err)
	}
	full := strings.Replace(status, "hs  0001 004", "hs  0001 006", 1)
	os.WriteFile(filepath.Join(dir, "status"), []byte(full), 0o644)
	if _, err := (Controller{Dir: dir}).FreePort(); err == nil {
		t.Fatal("expected no free port")
	}
}

func TestDetachWritesPort(t *testing.T) {
	dir := t.TempDir()
	if err := (Controller{Dir: dir}).Detach(3); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "detach"))
	if string(b) != "3" {
		t.Fatalf("detach wrote %q", b)
	}
	if err := (Controller{Dir: dir}).Detach(-1); err == nil {
		t.Fatal("negative port must be rejected")
	}
}
