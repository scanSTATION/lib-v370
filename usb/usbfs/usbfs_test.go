package usbfs

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfs builds a sysfs-like tree; vhci devices are symlinks into a
// directory whose path contains "vhci_hcd", as on a real system.
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	devs := filepath.Join(root, "bus", "usb", "devices")
	os.MkdirAll(devs, 0o755)
	mk := func(name, parent string, vid, pid string, bus, dev int, auth string) {
		dir := filepath.Join(root, "devices", parent, name)
		os.MkdirAll(dir, 0o755)
		w := func(f, v string) { os.WriteFile(filepath.Join(dir, f), []byte(v+"\n"), 0o644) }
		w("idVendor", vid)
		w("idProduct", pid)
		w("busnum", itoa(bus))
		w("devnum", itoa(dev))
		w("bConfigurationValue", "1")
		w("authorized", auth)
		os.Symlink(dir, filepath.Join(devs, name))
	}
	mk("2-6", "pci0000:00/usb2", "04b8", "014a", 2, 6, "1")
	mk("4-1", "platform/vhci_hcd.0/usb4", "04b8", "014a", 4, 2, "1")
	mk("3-1", "pci0000:00/usb3", "046d", "c077", 3, 3, "1") // a mouse
	os.MkdirAll(filepath.Join(devs, "2-6:1.0"), 0o755)      // interface entry
	return devs
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestFindAndSelect(t *testing.T) {
	devs, err := Find(fakeSysfs(t), 0x04b8, 0x014a)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 || devs[0].Simulated || !devs[1].Simulated {
		t.Fatalf("devs = %+v", devs)
	}
	if devs[0].DevNode() != "/dev/bus/usb/002/006" {
		t.Errorf("devnode = %s", devs[0].DevNode())
	}
	for sel, want := range map[Selector]int{PreferReal: 2, OnlyReal: 2, OnlySimulated: 4} {
		d, err := Select(devs, sel)
		if err != nil || d.Bus != want {
			t.Errorf("selector %d: got bus %d err %v, want bus %d", sel, d.Bus, err, want)
		}
	}
	// A software-disabled real scanner is skipped.
	devs[0].Authorized = false
	if d, _ := Select(devs, PreferReal); !d.Simulated {
		t.Error("unauthorized real device must not be selected")
	}
	if _, err := Select(devs, OnlyReal); err == nil {
		t.Error("expected no real device")
	}
}
