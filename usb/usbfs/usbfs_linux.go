//go:build linux

package usbfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/scanSTATION/lib-v370/usb"
)

// Kernel structures from <linux/usbdevice_fs.h>. Go lays these out like C
// on every Linux ABI, so their sizes (and thus the ioctl numbers) are
// computed instead of hard-coded per architecture.
type ctrlTransfer struct {
	RequestType uint8
	Request     uint8
	Value       uint16
	Index       uint16
	Length      uint16
	Timeout     uint32 // ms
	Data        unsafe.Pointer
}

type bulkTransfer struct {
	Endpoint uint32
	Len      uint32
	Timeout  uint32 // ms
	Data     unsafe.Pointer
}

// ioctl request encoding (<asm-generic/ioctl.h>).
const (
	iocWrite = 1
	iocRead  = 2
)

func ioc(dir, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | uintptr('U')<<8 | nr
}

var (
	ioctlControl     = ioc(iocRead|iocWrite, 0, unsafe.Sizeof(ctrlTransfer{}))
	ioctlBulk        = ioc(iocRead|iocWrite, 2, unsafe.Sizeof(bulkTransfer{}))
	ioctlSetConfig   = ioc(iocRead, 5, 4)
	ioctlClaimIntf   = ioc(iocRead, 15, 4)
	ioctlReleaseIntf = ioc(iocRead, 16, 4)
	ioctlClearHalt   = ioc(iocRead, 21, 4)
	ioctlReset       = ioc(0, 20, 0)
)

// Limits.
const (
	// MaxTransfer caps a single bulk transfer (usbfs default memory limit is
	// 16 MiB for all transfers of a process).
	MaxTransfer = 1 << 20
	// DefaultTimeout applies when the context has no deadline (OUT/control).
	DefaultTimeout = 10 * time.Second
	// inSlice is how long one blocking IN attempt lasts before the context is
	// re-checked. Long, because a timed-out URB can drop a partial packet.
	inSlice = 2 * time.Second
)

// Errors.
var (
	ErrNoDevice = errors.New("usbfs: device disconnected")
	ErrBusy     = errors.New("usbfs: interface is claimed by another program (is the vendor app running?)")
	ErrTimeout  = errors.New("usbfs: transfer timed out")
	ErrClosed   = errors.New("usbfs: device closed")
)

// Device is an open, claimed USB device. It implements usb.Transport.
// Transfers on one Device are serialised.
type Device struct {
	info  DeviceInfo
	f     *os.File
	intf  uint32
	eps   map[uint8]usb.EndpointInfo
	mu    sync.Mutex
	close sync.Once
}

var _ usb.Transport = (*Device)(nil)

// Open opens the device node, selects its configuration if necessary and
// claims the first interface.
func Open(info DeviceInfo) (*Device, error) {
	f, err := os.OpenFile(info.DevNode(), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("usbfs: open %s: %w (udev rule installed? see udev/60-v370.rules)", info.DevNode(), err)
	}
	d := &Device{info: info, f: f, eps: map[uint8]usb.EndpointInfo{}}

	cfgDesc, intfNum, err := readConfig(info.SysPath)
	if err != nil {
		f.Close()
		return nil, err
	}
	for _, it := range cfgDesc.Interfaces {
		if uint32(it.Number) == intfNum {
			for _, ep := range it.Endpoints {
				d.eps[ep.Address] = ep
			}
		}
	}
	if info.Config != int(cfgDesc.Value) {
		v := uint32(cfgDesc.Value)
		if err := d.ioctl(ioctlSetConfig, unsafe.Pointer(&v)); err != nil {
			f.Close()
			return nil, fmt.Errorf("usbfs: set configuration %d: %w", v, err)
		}
	}
	d.intf = intfNum
	if err := d.ioctl(ioctlClaimIntf, unsafe.Pointer(&d.intf)); err != nil {
		f.Close()
		if errors.Is(err, syscall.EBUSY) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("usbfs: claim interface %d: %w", intfNum, err)
	}
	return d, nil
}

// readConfig parses the raw descriptors sysfs exposes (device descriptor
// followed by the active configuration).
func readConfig(sysPath string) (usb.ConfigInfo, uint32, error) {
	raw, err := os.ReadFile(filepath.Join(sysPath, "descriptors"))
	if err != nil {
		return usb.ConfigInfo{}, 0, fmt.Errorf("usbfs: reading descriptors: %w", err)
	}
	if len(raw) < 18+9 {
		return usb.ConfigInfo{}, 0, errors.New("usbfs: descriptors too short")
	}
	ci, err := usb.ParseConfiguration(raw[18:])
	if err != nil {
		return usb.ConfigInfo{}, 0, err
	}
	if len(ci.Interfaces) == 0 {
		return usb.ConfigInfo{}, 0, errors.New("usbfs: device has no interfaces")
	}
	return ci, uint32(ci.Interfaces[0].Number), nil
}

// Info returns the device description.
func (d *Device) Info() DeviceInfo { return d.info }

func (d *Device) ioctl(req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, d.f.Fd(), req, uintptr(arg))
	runtime.KeepAlive(d.f)
	if errno != 0 {
		return errno
	}
	return nil
}

// ioctlN is ioctl for transfer requests, which return the byte count.
func (d *Device) ioctlN(req uintptr, arg unsafe.Pointer) (int, error) {
	r, _, errno := syscall.Syscall(syscall.SYS_IOCTL, d.f.Fd(), req, uintptr(arg))
	runtime.KeepAlive(d.f)
	if errno != 0 {
		return 0, mapErrno(errno)
	}
	return int(r), nil
}

func mapErrno(e syscall.Errno) error {
	switch e {
	case syscall.EPIPE:
		return usb.ErrStall
	case syscall.ETIMEDOUT:
		return ErrTimeout
	case syscall.ENODEV, syscall.ESHUTDOWN:
		return ErrNoDevice
	default:
		return fmt.Errorf("usbfs: %w", e)
	}
}

// timeoutMS converts the context deadline (or def) into a usbfs timeout.
func timeoutMS(ctx context.Context, def time.Duration) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	t := def
	if dl, ok := ctx.Deadline(); ok {
		t = time.Until(dl)
		if t <= 0 {
			return 0, context.DeadlineExceeded
		}
		t = min(t, def)
	}
	ms := t.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return uint32(ms), nil
}

func (d *Device) checkEndpoint(ep uint8, in bool) error {
	if (ep&usb.DirIn != 0) != in {
		return fmt.Errorf("usbfs: endpoint 0x%02x has the wrong direction", ep)
	}
	if _, ok := d.eps[ep]; !ok {
		return fmt.Errorf("usbfs: endpoint 0x%02x not in the claimed interface", ep)
	}
	return nil
}

// Control implements usb.Transport.
func (d *Device) Control(ctx context.Context, setup usb.Setup, data []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return nil, ErrClosed
	}
	tmo, err := timeoutMS(ctx, DefaultTimeout)
	if err != nil {
		return nil, err
	}
	var buf []byte
	if setup.IsIn() {
		buf = make([]byte, setup.Length)
	} else {
		if len(data) != int(setup.Length) {
			return nil, fmt.Errorf("usbfs: control OUT data %d bytes != wLength %d", len(data), setup.Length)
		}
		buf = append([]byte(nil), data...)
	}
	ct := ctrlTransfer{
		RequestType: setup.RequestType, Request: setup.Request,
		Value: setup.Value, Index: setup.Index, Length: setup.Length, Timeout: tmo,
	}
	if len(buf) > 0 {
		ct.Data = unsafe.Pointer(&buf[0])
	}
	n, err := d.ioctlN(ioctlControl, unsafe.Pointer(&ct))
	runtime.KeepAlive(buf)
	if err != nil {
		return nil, err
	}
	if setup.IsIn() {
		return buf[:n], nil
	}
	return nil, nil
}

func (d *Device) bulk(ep uint8, buf []byte, tmo uint32) (int, error) {
	bt := bulkTransfer{Endpoint: uint32(ep), Len: uint32(len(buf)), Timeout: tmo}
	if len(buf) > 0 {
		bt.Data = unsafe.Pointer(&buf[0])
	}
	n, err := d.ioctlN(ioctlBulk, unsafe.Pointer(&bt))
	runtime.KeepAlive(buf)
	return n, err
}

// BulkOut implements usb.Transport.
func (d *Device) BulkOut(ctx context.Context, ep uint8, data []byte) (int, error) {
	if err := d.checkEndpoint(ep, false); err != nil {
		return 0, err
	}
	if len(data) > MaxTransfer {
		return 0, fmt.Errorf("usbfs: transfer of %d bytes exceeds %d", len(data), MaxTransfer)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return 0, ErrClosed
	}
	tmo, err := timeoutMS(ctx, DefaultTimeout)
	if err != nil {
		return 0, err
	}
	buf := append([]byte(nil), data...) // never hand caller memory to the kernel
	return d.bulk(ep, buf, tmo)
}

// BulkIn implements usb.Transport. It waits in slices of inSlice until data
// arrives or ctx ends (context.DeadlineExceeded / context.Canceled).
// Cancellation is noticed at the end of the current slice.
func (d *Device) BulkIn(ctx context.Context, ep uint8, max int) ([]byte, error) {
	if err := d.checkEndpoint(ep, true); err != nil {
		return nil, err
	}
	if max <= 0 || max > MaxTransfer {
		return nil, fmt.Errorf("usbfs: invalid read size %d", max)
	}
	buf := make([]byte, max)
	for {
		d.mu.Lock()
		if d.f == nil {
			d.mu.Unlock()
			return nil, ErrClosed
		}
		tmo, err := timeoutMS(ctx, inSlice)
		if err != nil {
			d.mu.Unlock()
			return nil, err
		}
		n, err := d.bulk(ep, buf, tmo)
		d.mu.Unlock()
		switch {
		case err == nil:
			return buf[:n], nil
		case errors.Is(err, ErrTimeout):
			// Slice expired; timeoutMS reports the context's own expiry.
			continue
		default:
			return nil, err
		}
	}
}

// ClearHalt clears a stall on an endpoint.
func (d *Device) ClearHalt(ep uint8) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return ErrClosed
	}
	v := uint32(ep)
	return d.ioctl(ioctlClearHalt, unsafe.Pointer(&v))
}

// Reset performs a USB port reset (USBDEVFS_RESET). The device stays
// powered, so state such as loaded firmware survives, but any half-finished
// command exchange is aborted. The device may re-enumerate; reopen it
// afterwards (this Device is closed).
func (d *Device) Reset() error {
	d.mu.Lock()
	err := d.ioctl(ioctlReset, nil)
	d.mu.Unlock()
	cerr := d.Close()
	if err != nil {
		return fmt.Errorf("usbfs: reset: %w", err)
	}
	return cerr
}

// Close implements usb.Transport: releases the interface and closes the node.
func (d *Device) Close() error {
	var err error
	d.close.Do(func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if rerr := d.ioctl(ioctlReleaseIntf, unsafe.Pointer(&d.intf)); rerr != nil && !errors.Is(rerr, syscall.ENODEV) {
			err = fmt.Errorf("usbfs: release interface: %w", rerr)
		}
		if cerr := d.f.Close(); err == nil {
			err = cerr
		}
		d.f = nil
	})
	return err
}
