//go:build linux

package usbfs

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/scanSTATION/lib-v370/usb"
)

// Values from <linux/usbdevice_fs.h> on 64-bit Linux.
func TestIoctlNumbers(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("reference values are for 64-bit")
	}
	cases := map[string][2]uintptr{
		"CONTROL":          {ioctlControl, 0xC0185500},
		"BULK":             {ioctlBulk, 0xC0185502},
		"SETCONFIGURATION": {ioctlSetConfig, 0x80045505},
		"CLAIMINTERFACE":   {ioctlClaimIntf, 0x8004550F},
		"RELEASEINTERFACE": {ioctlReleaseIntf, 0x80045510},
		"CLEAR_HALT":       {ioctlClearHalt, 0x80045515},
		"RESET":            {ioctlReset, 0x5514},
	}
	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s = %#x, want %#x", name, c[0], c[1])
		}
	}
}

func TestErrnoMapping(t *testing.T) {
	if !errors.Is(mapErrno(syscall.EPIPE), usb.ErrStall) ||
		!errors.Is(mapErrno(syscall.ETIMEDOUT), ErrTimeout) ||
		!errors.Is(mapErrno(syscall.ENODEV), ErrNoDevice) ||
		!errors.Is(mapErrno(syscall.EIO), syscall.EIO) {
		t.Fatal("errno mapping wrong")
	}
}

func TestTimeoutMS(t *testing.T) {
	if ms, err := timeoutMS(context.Background(), time.Second); err != nil || ms != 1000 {
		t.Fatalf("no deadline: %d %v", ms, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if ms, err := timeoutMS(ctx, time.Second); err != nil || ms > 300 || ms < 200 {
		t.Fatalf("deadline: %d %v", ms, err)
	}
	cancel()
	if _, err := timeoutMS(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
