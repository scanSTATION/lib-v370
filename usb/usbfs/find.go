// Package usbfs is a pure-Go USB host transport for Linux using the kernel's
// usbfs interface (/dev/bus/usb/BBB/DDD + ioctls). It needs neither cgo nor
// libusb, so binaries stay static and no third-party native code runs.
//
// Access to the device node is required (see udev/60-v370.rules).
package usbfs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultSysfs is where the kernel lists USB devices.
const DefaultSysfs = "/sys/bus/usb/devices"

// DeviceInfo describes a USB device found in sysfs.
type DeviceInfo struct {
	SysPath    string // e.g. /sys/bus/usb/devices/2-6
	Bus        int
	Address    int
	VendorID   uint16
	ProductID  uint16
	Config     int  // active bConfigurationValue (0 = unconfigured)
	Simulated  bool // attached through vhci-hcd (USB/IP), e.g. v370-usbsim
	Authorized bool
}

// DevNode returns the usbfs device node path.
func (d DeviceInfo) DevNode() string {
	return fmt.Sprintf("/dev/bus/usb/%03d/%03d", d.Bus, d.Address)
}

func (d DeviceInfo) String() string {
	kind := "real"
	if d.Simulated {
		kind = "simulated"
	}
	return fmt.Sprintf("%04x:%04x bus %d dev %d (%s, %s)", d.VendorID, d.ProductID, d.Bus, d.Address, kind, filepath.Base(d.SysPath))
}

func readTrim(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func readHex16(path string) (uint16, error) {
	s, err := readTrim(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 16, 16)
	return uint16(v), err
}

func readInt(path string) (int, error) {
	s, err := readTrim(path)
	if err != nil {
		return 0, err
	}
	if s == "" {
		return 0, nil // e.g. bConfigurationValue of an unconfigured device
	}
	return strconv.Atoi(s)
}

// Find lists devices with the given VID/PID under sysfsRoot (DefaultSysfs
// when empty), sorted with real devices first.
func Find(sysfsRoot string, vid, pid uint16) ([]DeviceInfo, error) {
	if sysfsRoot == "" {
		sysfsRoot = DefaultSysfs
	}
	entries, err := os.ReadDir(sysfsRoot)
	if err != nil {
		return nil, err
	}
	var out []DeviceInfo
	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, ":") { // interfaces, e.g. 2-6:1.0
			continue
		}
		dir := filepath.Join(sysfsRoot, name)
		v, err1 := readHex16(filepath.Join(dir, "idVendor"))
		p, err2 := readHex16(filepath.Join(dir, "idProduct"))
		if err1 != nil || err2 != nil || v != vid || p != pid {
			continue
		}
		bus, err1 := readInt(filepath.Join(dir, "busnum"))
		addr, err2 := readInt(filepath.Join(dir, "devnum"))
		if err1 != nil || err2 != nil {
			continue
		}
		cfg, _ := readInt(filepath.Join(dir, "bConfigurationValue"))
		auth := true
		if a, err := readTrim(filepath.Join(dir, "authorized")); err == nil {
			auth = a == "1"
		}
		real, _ := filepath.EvalSymlinks(dir)
		out = append(out, DeviceInfo{
			SysPath: dir, Bus: bus, Address: addr, VendorID: v, ProductID: p,
			Config: cfg, Authorized: auth, Simulated: strings.Contains(real, "vhci_hcd"),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Simulated != out[j].Simulated {
			return !out[i].Simulated
		}
		return out[i].SysPath < out[j].SysPath
	})
	return out, nil
}

// Selector picks one device from Find's result.
type Selector int

const (
	// PreferReal picks an authorized real device, else a simulated one.
	PreferReal Selector = iota
	// OnlyReal ignores simulated devices.
	OnlyReal
	// OnlySimulated ignores real devices (tests against v370-usbsim).
	OnlySimulated
)

// Select applies the selector; unauthorized (software-disabled) devices are
// never chosen.
func Select(devs []DeviceInfo, sel Selector) (DeviceInfo, error) {
	for _, d := range devs {
		if !d.Authorized {
			continue
		}
		switch {
		case sel == OnlyReal && d.Simulated, sel == OnlySimulated && !d.Simulated:
			continue
		}
		return d, nil
	}
	return DeviceInfo{}, fmt.Errorf("usbfs: no matching device found (%d candidates)", len(devs))
}
