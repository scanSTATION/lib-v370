package usbip

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"

	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/usb"
)

const (
	maxNumPackets   = 1024
	endpointBacklog = 1024
)

// Server exports one simulated device over USB/IP.
type Server struct {
	dev    *sim.Device
	info   DeviceInfo
	log    *slog.Logger
	inUse  atomic.Bool
	connWG sync.WaitGroup
}

// NewServer creates a server exporting dev under busID (e.g. "1-1").
func NewServer(dev *sim.Device, busID string, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	if busID == "" || len(busID) >= busIDLen {
		return nil, fmt.Errorf("usbip: invalid bus id %q", busID)
	}
	dd := dev.DeviceDescriptor()
	ci := dev.ConfigInfo()
	info := DeviceInfo{
		Path:              "/sys/devices/usbsim/" + busID,
		BusID:             busID,
		BusNum:            1,
		DevNum:            2,
		Speed:             SpeedHigh,
		VendorID:          dd.VendorID,
		ProductID:         dd.ProductID,
		BcdDevice:         dd.BcdDevice,
		DeviceClass:       dd.DeviceClass,
		DeviceSubClass:    dd.DeviceSubClass,
		DeviceProtocol:    dd.DeviceProtocol,
		ConfigurationVal:  ci.Value,
		NumConfigurations: dd.NumConfigurations,
	}
	for _, it := range ci.Interfaces {
		info.Interfaces = append(info.Interfaces, InterfaceInfo{it.Class, it.SubClass, it.Protocol})
	}
	return &Server{dev: dev, info: info, log: log}, nil
}

// Info returns the exported device description.
func (s *Server) Info() DeviceInfo { return s.info }

// Serve accepts connections until ctx is cancelled or ln fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	defer s.connWG.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			defer conn.Close()
			if err := s.handleConn(ctx, conn); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
				s.log.Warn("usbip: connection closed", "remote", conn.RemoteAddr().String(), "err", err)
			}
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) error {
	op, err := readOp(conn)
	if err != nil {
		return err
	}
	if op.Version != Version {
		return fmt.Errorf("unsupported protocol version %04x", op.Version)
	}
	switch op.Code {
	case OpReqDevlist:
		body := make([]byte, 4)
		body[3] = 1 // one device (big endian u32)
		body = append(body, s.info.encode(true)...)
		return writeOp(conn, OpRepDevlist, 0, body)
	case OpReqImport:
		busID := make([]byte, busIDLen)
		if _, err := io.ReadFull(conn, busID); err != nil {
			return err
		}
		if fixedString(busID) != s.info.BusID {
			return writeOp(conn, OpRepImport, 1, nil)
		}
		if !s.inUse.CompareAndSwap(false, true) {
			s.log.Warn("usbip: device already imported, rejecting second client")
			return writeOp(conn, OpRepImport, 1, nil)
		}
		defer s.inUse.Store(false)
		if err := writeOp(conn, OpRepImport, 0, s.info.encode(false)); err != nil {
			return err
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
		s.log.Info("usbip: device imported", "remote", conn.RemoteAddr().String())
		s.dev.Reset()
		defer s.dev.Reset()
		return newSession(s, conn).run(ctx)
	default:
		return fmt.Errorf("unknown operation %04x", op.Code)
	}
}

// --- URB session -----------------------------------------------------------

type urb struct {
	hdr      Header
	data     []byte // OUT payload
	cancel   chan struct{}
	finished bool // completed or unlinked (guarded by session.mu)
}

type session struct {
	srv  *Server
	conn net.Conn
	r    *bufio.Reader

	wmu sync.Mutex // serialises writes to conn

	mu      sync.Mutex // guards urbs and urb.finished
	urbs    map[uint32]*urb
	workers map[uint32]chan *urb

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newSession(s *Server, conn net.Conn) *session {
	return &session{
		srv: s, conn: conn, r: bufio.NewReaderSize(conn, 64<<10),
		urbs: map[uint32]*urb{}, workers: map[uint32]chan *urb{},
	}
}

func (ss *session) run(parent context.Context) error {
	ss.ctx, ss.cancel = context.WithCancel(parent)
	defer func() {
		ss.cancel()
		ss.conn.Close()
		ss.mu.Lock()
		for _, ch := range ss.workers {
			close(ch)
		}
		ss.workers = map[uint32]chan *urb{}
		ss.mu.Unlock()
		ss.wg.Wait()
	}()
	for {
		h, err := readHeader(ss.r)
		if err != nil {
			return err
		}
		switch h.Command {
		case CmdSubmit:
			u, err := ss.readSubmit(h)
			if err != nil {
				return err
			}
			if u == nil {
				continue // rejected with an error reply
			}
			ss.dispatch(u)
		case CmdUnlink:
			if err := ss.unlink(h); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected command %d from client", h.Command)
		}
	}
}

// readSubmit validates a CMD_SUBMIT and reads its payload. Protocol
// violations that make the stream unparsable return an error (connection is
// dropped); well-formed but unsupported requests are answered with an error
// status and nil is returned.
func (ss *session) readSubmit(h Header) (*urb, error) {
	if h.Direction != DirIn && h.Direction != DirOut {
		return nil, fmt.Errorf("submit %d: invalid direction %d", h.Seq, h.Direction)
	}
	if h.Endpoint > 15 {
		return nil, fmt.Errorf("submit %d: invalid endpoint %d", h.Seq, h.Endpoint)
	}
	if h.TransferLength < 0 || h.TransferLength > usb.MaxTransferLen {
		return nil, fmt.Errorf("submit %d: transfer length %d out of range", h.Seq, h.TransferLength)
	}
	var data []byte
	if h.Direction == DirOut && h.TransferLength > 0 {
		data = make([]byte, h.TransferLength)
		if _, err := io.ReadFull(ss.r, data); err != nil {
			return nil, err
		}
	}
	iso := h.NumPackets > 0 && h.NumPackets != -1
	if iso {
		if h.NumPackets > maxNumPackets {
			return nil, fmt.Errorf("submit %d: too many iso packets", h.Seq)
		}
		if _, err := io.CopyN(io.Discard, ss.r, int64(h.NumPackets)*16); err != nil {
			return nil, err
		}
		return nil, ss.reply(h, -ErrnoEINVAL, nil)
	}
	if h.DevID != ss.srv.info.DevID() {
		ss.srv.log.Debug("usbip: submit for unexpected devid", "devid", h.DevID)
	}
	u := &urb{hdr: h, data: data, cancel: make(chan struct{})}
	ss.mu.Lock()
	if _, dup := ss.urbs[h.Seq]; dup {
		ss.mu.Unlock()
		return nil, fmt.Errorf("duplicate seqnum %d", h.Seq)
	}
	ss.urbs[h.Seq] = u
	ss.mu.Unlock()
	return u, nil
}

// dispatch queues the URB on its endpoint worker. URBs on the same
// endpoint complete in submission order, as on real hardware.
func (ss *session) dispatch(u *urb) {
	key := u.hdr.Endpoint | u.hdr.Direction<<8
	if u.hdr.Endpoint == 0 {
		key = 0 // control: one pipe for both directions
	}
	ss.mu.Lock()
	ch, ok := ss.workers[key]
	if !ok {
		ch = make(chan *urb, endpointBacklog)
		ss.workers[key] = ch
		ss.wg.Add(1)
		go ss.worker(ch)
	}
	ss.mu.Unlock()
	ch <- u
}

func (ss *session) worker(ch <-chan *urb) {
	defer ss.wg.Done()
	for u := range ch {
		ss.process(u)
	}
}

func errnoFor(err error) int32 {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, usb.ErrStall):
		return -ErrnoEPIPE
	default:
		return -ErrnoEPROTO
	}
}

// claim marks the URB finished; it returns false if it was unlinked.
func (ss *session) claim(u *urb) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if u.finished {
		return false
	}
	u.finished = true
	delete(ss.urbs, u.hdr.Seq)
	return true
}

func (ss *session) process(u *urb) {
	h := u.hdr
	dev := ss.srv.dev
	switch {
	case h.Endpoint == 0:
		if !ss.isLive(u) {
			return
		}
		setup, _ := usb.ParseSetup(h.Setup[:])
		var out []byte
		if !setup.IsIn() {
			out = u.data
		}
		data, err := dev.Control(setup, out)
		if !ss.claim(u) {
			return
		}
		if err != nil {
			ss.srv.log.Debug("usbip: control failed", "setup", setup.String(), "err", err)
		}
		if setup.IsIn() {
			if int32(len(data)) > h.TransferLength {
				data = data[:h.TransferLength]
			}
			ss.replyLogged(h, errnoFor(err), data)
		} else {
			ss.replyLoggedLen(h, errnoFor(err), int32(len(u.data)))
		}
	case h.Direction == DirOut:
		if !ss.isLive(u) {
			return
		}
		err := dev.BulkOut(uint8(h.Endpoint), u.data)
		if !ss.claim(u) {
			return
		}
		n := int32(len(u.data))
		if err != nil {
			n = 0
		}
		ss.replyLoggedLen(h, errnoFor(err), n)
	default:
		ep := uint8(h.Endpoint) | usb.DirIn
		for {
			changed := dev.Changed()
			ss.mu.Lock()
			if u.finished {
				ss.mu.Unlock()
				return
			}
			// Consume data and mark the URB finished atomically with respect
			// to UNLINK, so data is never lost to a racing unlink.
			data, ok, err := dev.TryBulkIn(ep, int(h.TransferLength))
			if ok || err != nil {
				u.finished = true
				delete(ss.urbs, h.Seq)
				ss.mu.Unlock()
				ss.replyLogged(h, errnoFor(err), data)
				return
			}
			ss.mu.Unlock()
			select {
			case <-changed:
			case <-u.cancel:
				return
			case <-ss.ctx.Done():
				return
			}
		}
	}
}

func (ss *session) isLive(u *urb) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return !u.finished
}

func (ss *session) unlink(h Header) error {
	ss.mu.Lock()
	status := int32(0) // already completed
	if u, ok := ss.urbs[h.UnlinkSeq]; ok && !u.finished {
		u.finished = true
		close(u.cancel)
		delete(ss.urbs, h.UnlinkSeq)
		status = -ErrnoECONNRESET
	}
	ss.mu.Unlock()
	return ss.write(Header{Command: RetUnlink, Seq: h.Seq, Status: status}.encode())
}

func (ss *session) reply(h Header, status int32, data []byte) error {
	actual := int32(len(data))
	if h.Direction == DirOut {
		data = nil
	}
	return ss.writeSubmit(h, status, actual, data)
}

func (ss *session) writeSubmit(h Header, status, actual int32, data []byte) error {
	rh := Header{Command: RetSubmit, Seq: h.Seq, Status: status, ActualLen: actual}
	return ss.write(append(rh.encode(), data...))
}

func (ss *session) replyLogged(h Header, status int32, data []byte) {
	if err := ss.writeSubmit(h, status, int32(len(data)), data); err != nil {
		ss.fail(err)
	}
}

func (ss *session) replyLoggedLen(h Header, status, actual int32) {
	if err := ss.writeSubmit(h, status, actual, nil); err != nil {
		ss.fail(err)
	}
}

func (ss *session) fail(err error) {
	if ss.ctx.Err() == nil {
		ss.srv.log.Warn("usbip: write failed", "err", err)
	}
	ss.cancel()
	ss.conn.Close()
}

func (ss *session) write(b []byte) error {
	ss.wmu.Lock()
	defer ss.wmu.Unlock()
	_, err := ss.conn.Write(b)
	return err
}
