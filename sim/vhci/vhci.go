// Package vhci attaches a USB/IP-exported device to the local kernel through
// the vhci-hcd driver (the same sysfs interface `usbip attach` uses), so the
// `usbip` user-space tool is not required.
//
// Requires root (or CAP_SYS_ADMIN) and `modprobe vhci-hcd`.
package vhci

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/scanSTATION/lib-v370/sim/usbip"
)

// DefaultSysfs is the sysfs directory of the first vhci-hcd controller.
const DefaultSysfs = "/sys/devices/platform/vhci_hcd.0"

// vdevStNull is the "port free" state in the vhci status file.
const vdevStNull = 4

// Port is one row of the vhci status file.
type Port struct {
	Hub    string // "hs" or "ss"
	Number int
	State  int
}

// Controller is a vhci-hcd sysfs directory.
type Controller struct{ Dir string }

// Default returns the first controller, or an error if vhci-hcd is not loaded.
func Default() (Controller, error) {
	c := Controller{Dir: DefaultSysfs}
	if _, err := os.Stat(filepath.Join(c.Dir, "attach")); err != nil {
		return c, fmt.Errorf("vhci-hcd not available (run `sudo modprobe vhci-hcd`): %w", err)
	}
	return c, nil
}

// Ports parses the status file.
func (c Controller) Ports() ([]Port, error) {
	f, err := os.Open(filepath.Join(c.Dir, "status"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseStatus(bufio.NewScanner(f))
}

// ParseStatus parses vhci status lines of the form
//
//	hub port sta spd dev      sockfd local_busid
//	hs  0000 004 000 00000000 000000 0-0
func ParseStatus(sc *bufio.Scanner) ([]Port, error) {
	var ports []Port
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[0] == "hub" {
			continue
		}
		n, err1 := strconv.Atoi(f[1])
		st, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("vhci: malformed status line %q", sc.Text())
		}
		ports = append(ports, Port{Hub: f[0], Number: n, State: st})
	}
	return ports, sc.Err()
}

// FreePort returns a free high-speed port.
func (c Controller) FreePort() (int, error) {
	ports, err := c.Ports()
	if err != nil {
		return 0, err
	}
	for _, p := range ports {
		if p.Hub == "hs" && p.State == vdevStNull {
			return p.Number, nil
		}
	}
	return 0, fmt.Errorf("vhci: no free high-speed port")
}

// Attach connects to a USB/IP server, imports busID and hands the socket to
// the kernel. It returns the vhci port used. The kernel keeps the socket;
// the device stays attached after this process exits, until Detach.
func (c Controller) Attach(serverAddr, busID string) (int, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	info, err := usbip.Import(conn, busID)
	if err != nil {
		return 0, err
	}
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return 0, fmt.Errorf("vhci: not a TCP connection")
	}
	f, err := tc.File() // dup'ed fd; the kernel takes its own reference
	if err != nil {
		return 0, err
	}
	defer f.Close()
	port, err := c.FreePort()
	if err != nil {
		return 0, err
	}
	cmd := fmt.Sprintf("%d %d %d %d", port, f.Fd(), info.DevID(), info.Speed)
	if err := os.WriteFile(filepath.Join(c.Dir, "attach"), []byte(cmd), 0o600); err != nil {
		return 0, fmt.Errorf("vhci: attach (are you root?): %w", err)
	}
	return port, nil
}

// Detach removes the device on port.
func (c Controller) Detach(port int) error {
	if port < 0 {
		return fmt.Errorf("vhci: invalid port %d", port)
	}
	if err := os.WriteFile(filepath.Join(c.Dir, "detach"), []byte(strconv.Itoa(port)), 0o600); err != nil {
		return fmt.Errorf("vhci: detach port %d: %w", port, err)
	}
	return nil
}
