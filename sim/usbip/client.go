package usbip

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

// DevList queries the exported devices (OP_REQ_DEVLIST) on a fresh connection.
func DevList(conn net.Conn) ([]DeviceInfo, error) {
	if err := writeOp(conn, OpReqDevlist, 0, nil); err != nil {
		return nil, err
	}
	op, err := readOp(conn)
	if err != nil {
		return nil, err
	}
	if op.Code != OpRepDevlist || op.Status != 0 {
		return nil, fmt.Errorf("usbip: devlist failed (code %04x status %d)", op.Code, op.Status)
	}
	var n [4]byte
	if _, err := io.ReadFull(conn, n[:]); err != nil {
		return nil, err
	}
	count := binary.BigEndian.Uint32(n[:])
	if count > 64 {
		return nil, fmt.Errorf("usbip: implausible device count %d", count)
	}
	devs := make([]DeviceInfo, 0, count)
	for i := uint32(0); i < count; i++ {
		b := make([]byte, deviceInfoLen)
		if _, err := io.ReadFull(conn, b); err != nil {
			return nil, err
		}
		d := decodeDeviceInfo(b)
		for j := range d.Interfaces {
			var ib [4]byte
			if _, err := io.ReadFull(conn, ib[:]); err != nil {
				return nil, err
			}
			d.Interfaces[j] = InterfaceInfo{ib[0], ib[1], ib[2]}
		}
		devs = append(devs, d)
	}
	return devs, nil
}

// Import performs OP_REQ_IMPORT for busID. On success the connection carries
// URB traffic (use NewClient, or hand the socket to vhci-hcd).
func Import(conn net.Conn, busID string) (DeviceInfo, error) {
	if len(busID) >= busIDLen {
		return DeviceInfo{}, fmt.Errorf("usbip: bus id too long")
	}
	b := make([]byte, busIDLen)
	copy(b, busID)
	if err := writeOp(conn, OpReqImport, 0, b); err != nil {
		return DeviceInfo{}, err
	}
	op, err := readOp(conn)
	if err != nil {
		return DeviceInfo{}, err
	}
	if op.Code != OpRepImport {
		return DeviceInfo{}, fmt.Errorf("usbip: unexpected reply %04x", op.Code)
	}
	if op.Status != 0 {
		return DeviceInfo{}, fmt.Errorf("usbip: import of %q refused (status %d)", busID, op.Status)
	}
	info := make([]byte, deviceInfoLen)
	if _, err := io.ReadFull(conn, info); err != nil {
		return DeviceInfo{}, err
	}
	d := decodeDeviceInfo(info)
	d.Interfaces = nil
	return d, nil
}

// Reply is a decoded RET_SUBMIT or RET_UNLINK.
type Reply struct {
	Command uint32
	Seq     uint32
	Status  int32
	Actual  int32
	Data    []byte
}

// Client is a minimal URB-level USB/IP client (used by tests and tools).
// Send* may be called concurrently with Recv.
type Client struct {
	conn  net.Conn
	r     *bufio.Reader
	wmu   sync.Mutex
	devID uint32
	// dirIn remembers the direction of outstanding submits (to know whether
	// a RET_SUBMIT carries data).
	mu    sync.Mutex
	dirIn map[uint32]bool
}

// NewClient wraps an imported connection.
func NewClient(conn net.Conn, info DeviceInfo) *Client {
	return &Client{conn: conn, r: bufio.NewReader(conn), devID: info.DevID(), dirIn: map[uint32]bool{}}
}

// SendSubmit sends CMD_SUBMIT. For OUT transfers data is the payload; for
// IN transfers length is the buffer size.
func (c *Client) SendSubmit(seq uint32, ep uint8, in bool, setup [8]byte, data []byte, length int) error {
	h := Header{Command: CmdSubmit, Seq: seq, DevID: c.devID, Endpoint: uint32(ep & 0x0f), Setup: setup}
	if in {
		h.Direction = DirIn
		h.TransferLength = int32(length)
		data = nil
	} else {
		h.Direction = DirOut
		h.TransferLength = int32(len(data))
	}
	c.mu.Lock()
	c.dirIn[seq] = in
	c.mu.Unlock()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.conn.Write(append(h.encode(), data...))
	return err
}

// SendUnlink sends CMD_UNLINK for target.
func (c *Client) SendUnlink(seq, target uint32) error {
	h := Header{Command: CmdUnlink, Seq: seq, DevID: c.devID, UnlinkSeq: target}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.conn.Write(h.encode())
	return err
}

// Recv reads the next reply.
func (c *Client) Recv() (Reply, error) {
	h, err := readHeader(c.r)
	if err != nil {
		return Reply{}, err
	}
	rep := Reply{Command: h.Command, Seq: h.Seq, Status: h.Status, Actual: h.ActualLen}
	if h.Command == RetSubmit {
		c.mu.Lock()
		in := c.dirIn[h.Seq]
		delete(c.dirIn, h.Seq)
		c.mu.Unlock()
		if in && h.ActualLen > 0 {
			if h.ActualLen > 32<<20 {
				return Reply{}, fmt.Errorf("usbip: reply too large")
			}
			rep.Data = make([]byte, h.ActualLen)
			if _, err := io.ReadFull(c.r, rep.Data); err != nil {
				return Reply{}, err
			}
		}
	}
	return rep, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }
