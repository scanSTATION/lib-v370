// Package synth is a synthetic V370: a sim.Responder that implements the
// native protocol from docs/protocol.md instead of replaying a recording.
// It needs no captured data and no manufacturer files, so tests built on it
// can run anywhere:
//
//   - status, identity, session, clock, register reads, buttons;
//   - the firmware upload (any payload; the checksum byte is verified), so a
//     dummy firmware works;
//   - the scanner side of a scan profile: replies of the profile's reads
//     (recorded value when the profile pins one, otherwise a plausible
//     synthetic value), the lamp measurement, calibration reads (dark and
//     white references, see Calibration), and the image stream as a
//     synthetic pattern with the sensor's colour-line offsets, so decoding
//     can be checked exactly (Pattern).
//
// After a profile has run to its end the device is ready for the next scan,
// so several scans per session work (unlike a replayed recording).
package synth

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/rawimg"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/v370"
)

// Identity is the 28-byte `1b 13` reply.
const Identity = "SIM     GT-F740         1.00"

// Options configure the device.
type Options struct {
	// Profiles the device can play. All profiles share their first steps;
	// the device follows every profile that matches the host's writes so
	// far and narrows down as they diverge. Default: all built-in profiles.
	Profiles []*v370.Profile
	// FirmwareLoaded starts the device with firmware (status 18 01) instead
	// of the ROM loader state after power-on (28 00, upload expected).
	FirmwareLoaded bool
	// Registers answers `1e 8f` reads; defaults: 0x0010 = 120 (auto
	// power-off minutes), 0x0005 = 0 (scan counter).
	Registers map[uint16]uint32
}

// Device is the synthetic scanner. Safe for concurrent use.
type Device struct {
	mu        sync.Mutex
	opt       Options
	fwLoaded  bool
	upload    int    // firmware upload phase: 0 none, 1 header, 2 payload, 3 checksum
	fwSum     byte   // sum of the uploaded payload
	param     []byte // command waiting for its parameter (after ACK)
	in        [][]byte
	cands     []cand // profiles matching the current scan so far
	buttons   []byte
	scans     int
	lastImage []byte
}

// cand is a profile being played and its next step.
type cand struct {
	p   *v370.Profile
	cur int
}

// New creates a synthetic V370.
func New(opt Options) (*Device, error) {
	if len(opt.Profiles) == 0 {
		for _, n := range v370.ProfileNames() {
			p, err := v370.LoadProfile(n)
			if err != nil {
				return nil, err
			}
			opt.Profiles = append(opt.Profiles, p)
		}
	}
	for _, p := range opt.Profiles {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if _, err := p.Geometry(); err != nil {
			return nil, fmt.Errorf("synth: profile %s: %w", p.Name, err)
		}
	}
	if opt.Registers == nil {
		opt.Registers = map[uint16]uint32{0x0010: 120, 0x0005: 0}
	}
	return &Device{opt: opt, fwLoaded: opt.FirmwareLoaded}, nil
}

// PressButton queues a panel button press (reported by the next `1e 85`).
func (d *Device) PressButton(b v370.Button) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.buttons = append(d.buttons, byte(b))
}

// Scans returns the number of completed profile runs.
func (d *Device) Scans() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.scans
}

// FirmwareLoaded reports whether firmware was uploaded (or preloaded).
func (d *Device) FirmwareLoaded() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fwLoaded
}

// Control implements sim.Responder (the V370 uses no vendor requests).
func (d *Device) Control(usb.Setup, []byte) ([]byte, error) { return nil, nil }

// Reset implements sim.Responder: a USB reset keeps the firmware but drops
// all pending exchanges.
func (d *Device) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.in, d.param, d.upload, d.cands = nil, nil, 0, nil
}

// TryBulkIn implements sim.Responder.
func (d *Device) TryBulkIn(ep uint8, max int) ([]byte, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ep != usb.EndpointIn || len(d.in) == 0 {
		return nil, false, nil
	}
	head := d.in[0]
	if len(head) <= max {
		d.in = d.in[1:]
		return head, true, nil
	}
	d.in[0] = head[max:]
	return head[:max], true, nil
}

func (d *Device) send(b ...[]byte) { d.in = append(d.in, b...) }

var (
	ack          = []byte{0x06}
	cmdStatus    = []byte{0x1b, 0x03}
	cmdFwBegin   = []byte{0x1b, 0x06}
	cmdIdentity  = []byte{0x1b, 0x13}
	cmdFwRestart = []byte{0x1b, 0x16}
	cmdLamp      = []byte{0x1e, 0x04}
	cmdButton    = []byte{0x1e, 0x85}
	cmdClock     = []byte{0x1e, 0x8e}
	cmdRegister  = []byte{0x1e, 0x8f}
	cmdSession   = []byte{0x1e, 0x93}
)

// BulkOut implements sim.Responder.
func (d *Device) BulkOut(ep uint8, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ep != usb.EndpointOut {
		return fmt.Errorf("synth: write to endpoint %#x", ep)
	}
	if d.upload > 0 {
		return d.uploadLocked(data)
	}
	if d.param != nil {
		return d.paramLocked(data)
	}
	if !d.fwLoaded {
		// ROM loader: status and the upload start only.
		switch {
		case bytes.Equal(data, cmdStatus):
			d.send([]byte{0x28, 0x00})
		case bytes.Equal(data, cmdFwBegin):
			d.upload = 1
			d.send(ack)
		default:
			d.send([]byte{0x15}) // NAK
		}
		return nil
	}
	if d.cands == nil {
		for _, p := range d.opt.Profiles {
			d.cands = append(d.cands, cand{p: p})
		}
	}
	if d.profileLocked(data) {
		return nil
	}
	return d.genericLocked(data)
}

// genericLocked answers commands outside a profile.
func (d *Device) genericLocked(data []byte) error {
	switch {
	case bytes.Equal(data, cmdStatus):
		d.send([]byte{0x18, 0x01})
	case bytes.Equal(data, cmdIdentity):
		d.send([]byte(Identity))
	case bytes.Equal(data, cmdButton):
		b := byte(0)
		if len(d.buttons) > 0 {
			b, d.buttons = d.buttons[0], d.buttons[1:]
		}
		d.send([]byte{b})
	case bytes.Equal(data, cmdLamp):
		d.send([]byte{0x01}) // measurement done, levels OK
	case bytes.Equal(data, cmdSession), bytes.Equal(data, cmdClock),
		bytes.Equal(data, cmdRegister), bytes.Equal(data, cmdFwRestart):
		d.param = append([]byte(nil), data...)
		d.send(ack)
	default:
		d.send([]byte{0x15}) // NAK: unknown outside a profile
	}
	return nil
}

func (d *Device) paramLocked(p []byte) error {
	cmd := d.param
	d.param = nil
	switch {
	case bytes.Equal(cmd, cmdRegister):
		if len(p) != 2 {
			return fmt.Errorf("synth: register read with %d-byte parameter", len(p))
		}
		v := make([]byte, 4)
		binary.LittleEndian.PutUint32(v, d.opt.Registers[binary.LittleEndian.Uint16(p)])
		d.send(v)
	case bytes.Equal(cmd, cmdClock):
		if len(p) != 6 || p[0] != 0x0b || p[1] != 0x00 {
			// Only the clock function is modelled; anything else would write
			// non-volatile memory on the real scanner.
			return fmt.Errorf("synth: 1e 8e with parameter %x (only 0b 00 + u32 clock is allowed)", p)
		}
		d.send(ack)
	default:
		d.send(ack)
	}
	return nil
}

func (d *Device) uploadLocked(data []byte) error {
	switch d.upload {
	case 1:
		if !bytes.Equal(data, []byte{0x01, 0x00, 0x01, 0x00}) {
			return fmt.Errorf("synth: firmware header %x", data)
		}
		d.upload = 2
	case 2:
		if len(data) != 65536 {
			return fmt.Errorf("synth: firmware payload of %d bytes", len(data))
		}
		var sum byte
		for _, b := range data {
			sum += b
		}
		d.fwSum, d.upload = sum, 3
	case 3:
		d.upload = 0
		if len(data) != 1 || data[0] != d.fwSum {
			d.send([]byte{0x15})
			return nil
		}
		d.fwLoaded = true
		d.send(ack)
	}
	return nil
}

// skip moves a candidate past host-side waits and recorded lamp polls (the
// host polls on its own) and reports whether steps remain.
func (c *cand) skip() bool {
	steps := c.p.Steps
	for c.cur < len(steps) {
		s := steps[c.cur]
		switch {
		case s.Kind == v370.StepWait:
			c.cur++
		case s.Kind == v370.StepWrite && bytes.Equal(s.Data, cmdLamp) &&
			c.cur+1 < len(steps) && steps[c.cur+1].Kind == v370.StepRead:
			c.cur += 2
		default:
			return true
		}
	}
	return false
}

// accepts reports whether the host write data is the candidate's next step.
func (c *cand) accepts(data []byte) bool {
	if !c.skip() {
		return false
	}
	s := c.p.Steps[c.cur]
	return (s.Kind == v370.StepWrite && bytes.Equal(s.Data, data)) || s.Kind == v370.StepEcho ||
		(s.Kind == v370.StepCalc && len(data) == s.Len)
}

// profileLocked plays the scanner side of the profiles that match the scan
// so far; it returns false if data belongs to none of them at this point
// (the host polls status or the lamp result in between).
func (d *Device) profileLocked(data []byte) bool {
	var keep []cand
	for _, c := range d.cands {
		if c.accepts(data) {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		return false
	}
	// Answer from the first matching profile; all advance the same way.
	for i := range keep {
		keep[i].cur++
	}
	c := keep[0]
	steps := c.p.Steps
	for ; c.cur < len(steps); c.cur++ {
		s := steps[c.cur]
		if s.Kind == v370.StepRead {
			d.send(readReply(data, s))
		} else if s.Kind == v370.StepData {
			if s.Tag == v370.TagImage {
				img := Stream(c.p)
				d.lastImage = img
				d.send(img)
			} else {
				d.send(Calibration(c.p, c.cur))
			}
		} else {
			break
		}
	}
	// Keep the candidates' cursors in step with the replies just sent.
	adv := c.cur - (keep[0].cur)
	for i := range keep {
		keep[i].cur += adv
	}
	d.cands = keep
	if !keep[0].skip() {
		d.cands = nil // scan complete; the next write starts a new one
		d.scans++
	}
	return true
}

// readReply is the reply to one recorded read: the pinned value, or a
// plausible synthetic one of the recorded length.
func readReply(cmd []byte, s v370.Step) []byte {
	if s.Expect != nil {
		return append([]byte(nil), s.Expect...)
	}
	switch {
	case bytes.Equal(cmd, cmdStatus) && s.Len == 2:
		return []byte{0x18, 0x01}
	case bytes.Equal(cmd, cmdLamp) && s.Len == 1:
		return []byte{0x01}
	}
	return make([]byte, s.Len) // e.g. 1e a1 / 1e 65 → 00 (lamp state, no error)
}

// Synthetic calibration levels (16 bit): the short and the long dark
// reads, and the white reference, brightest in the middle of the line.
const (
	CalDarkShort = 2500
	CalDark      = 1500
	CalWhiteEdge = 20000
	CalWhitePeak = 58000
)

// Calibration returns the data of the profile's calibration read at step
// i: lines × width × RGB u16 from the `1e 41` block before it, plus an
// 8-byte trailer. The first read is a short dark read, the first wide one
// the white reference, later ones dark.
func Calibration(p *v370.Profile, i int) []byte {
	s := p.Steps[i]
	var geom []byte
	white := true // until a wide read was seen before i
	short := true
	cmd := ""
	for j := 0; j < i; j++ {
		st := p.Steps[j]
		switch {
		case st.Kind == v370.StepWrite && len(st.Data) == 2 && (st.Data[0] == 0x1b || st.Data[0] == 0x1e):
			cmd = fmt.Sprintf("%x", st.Data)
		case st.Kind == v370.StepWrite && cmd == "1e41" && len(st.Data) == 22:
			geom = st.Data
		case st.Kind == v370.StepData:
			short = false
			if g := geomWidth(geom); g > 64 {
				white = false
			}
		}
	}
	out := make([]byte, s.Len)
	w, lines := geomWidth(geom), 0
	if geom != nil {
		lines = int(binary.LittleEndian.Uint16(geom[4:]))
	}
	if w <= 0 || lines <= 0 || lines*w*6+8 != s.Len {
		return out // not a calibration read we understand: zeros
	}
	for l := 0; l < lines; l++ {
		for x := 0; x < w; x++ {
			for ch := 0; ch < 3; ch++ {
				v := CalDark
				switch {
				case short:
					v = CalDarkShort
				case white && w > 64:
					v = CalWhite(x, w)
				}
				binary.LittleEndian.PutUint16(out[2*((l*w+x)*3+ch):], uint16(v))
			}
		}
	}
	return out
}

// CalWhite is the synthetic white level of pixel x of w: a parabola from
// CalWhiteEdge at both ends to CalWhitePeak in the middle.
func CalWhite(x, w int) int {
	t := 2*float64(x)/float64(w-1) - 1
	return CalWhiteEdge + int(float64(CalWhitePeak-CalWhiteEdge)*(1-t*t))
}

func geomWidth(geom []byte) int {
	if len(geom) != 22 {
		return 0
	}
	return int(binary.LittleEndian.Uint16(geom[2:])) - int(binary.LittleEndian.Uint16(geom[0:]))
}

// Pattern is the colour of output pixel (x, y) in every synthetic scan.
func Pattern(x, y int) (r, g, b byte) {
	return byte(x), byte(y), byte(x ^ y)
}

// Stream builds the profile's raw image stream so that, after decoding with
// the profile's geometry, every pixel equals Pattern.
func Stream(p *v370.Profile) []byte {
	g, err := p.Geometry()
	if err != nil {
		return make([]byte, p.ImageBytes)
	}
	off := g.ChannelOffsets() // raw line offsets of R, G, B
	line := g.Width * 3
	out := make([]byte, 0, g.StreamSize())
	for l := 0; l < g.Lines; {
		n := min(g.LinesPerBlock, g.Lines-l)
		for i := 0; i < n; i++ {
			raw := l + i
			for x := 0; x < g.Width; x++ {
				r, _, _ := Pattern(x, raw-off[0])
				_, gg, _ := Pattern(x, raw-off[1])
				_, _, b := Pattern(x, raw-off[2])
				out = append(out, r, gg, b)
			}
		}
		out = append(out, rawimg.BlockTrailer(n*line)...)
		l += n
	}
	return out
}

// DummyFirmware is a 64 KiB stand-in for the scanner firmware (not the
// manufacturer's file): the synthetic device accepts any payload with a
// correct checksum, so programs can be tested without the vendor's firmware.
func DummyFirmware() *firmware.Firmware {
	p := make([]byte, firmware.PayloadLen)
	copy(p, "scanSTATION synthetic V370 firmware placeholder")
	fw, err := firmware.FromPayload(p)
	if err != nil {
		panic(err) // fixed size, cannot fail
	}
	return fw
}
