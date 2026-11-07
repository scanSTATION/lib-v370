package sim

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/scanSTATION/lib-v370/usb"
)

// ErrClosed is returned by a closed transport.
var ErrClosed = errors.New("usbsim: transport closed")

// Transport is an in-process usb.Transport backed by a simulated Device.
// It lets Go drivers be tested without kernel support or root.
type Transport struct {
	dev       *Device
	closed    atomic.Bool
	done      chan struct{}
	closeOnce sync.Once
}

var _ usb.Transport = (*Transport)(nil)

// NewTransport opens the device in-process.
func NewTransport(dev *Device) *Transport { return &Transport{dev: dev, done: make(chan struct{})} }

// Control implements usb.Transport.
func (t *Transport) Control(ctx context.Context, setup usb.Setup, data []byte) ([]byte, error) {
	if t.closed.Load() {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return t.dev.Control(setup, data)
}

// BulkOut implements usb.Transport.
func (t *Transport) BulkOut(ctx context.Context, ep uint8, data []byte) (int, error) {
	if t.closed.Load() {
		return 0, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := t.dev.BulkOut(ep, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// BulkIn implements usb.Transport; it blocks until data arrives or ctx ends.
func (t *Transport) BulkIn(ctx context.Context, ep uint8, max int) ([]byte, error) {
	for {
		if t.closed.Load() {
			return nil, ErrClosed
		}
		changed := t.dev.Changed()
		data, ok, err := t.dev.TryBulkIn(ep, max)
		if err != nil {
			return nil, err
		}
		if ok {
			return data, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.done:
			return nil, ErrClosed
		}
	}
}

// Close implements usb.Transport.
func (t *Transport) Close() error {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		close(t.done)
	})
	return nil
}
