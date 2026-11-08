package v370

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/scanSTATION/lib-v370/rawimg"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

// A Profile is the scan part of a recorded vendor-app session, turned into
// a script that our driver can replay after Open: every command and
// parameter block, the expected reply lengths, and semantic steps where a
// literal replay would be wrong (status polling, live values, data reads,
// standard tables, and calibration values computed from the scan's own
// calibration reads). See docs/protocol.md §3.4.
type Profile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // trace the profile was extracted from

	// From the last scan parameter block (`1e 57`).
	ScanDPI  int `json:"scan_dpi"`
	WidthPx  int `json:"width_px"`
	HeightPx int `json:"height_px"`
	Mode     int `json:"mode"`

	ImageBytes int    `json:"image_bytes"` // size of the final data read
	Steps      []Step `json:"steps"`
}

// StepKind enumerates profile steps.
type StepKind string

const (
	StepWrite StepKind = "write" // send Data
	StepRead  StepKind = "read"  // read a reply of Len bytes (must equal Expect if set)
	StepWait  StepKind = "wait"  // poll status until it equals Until or the scanner is ready
	StepData  StepKind = "data"  // read Len bytes in reads of up to Chunk bytes; pass to the sink as Tag
	StepEcho  StepKind = "echo"  // send bytes derived from the live reply of command Src
	StepCalc  StepKind = "calc"  // send Len bytes computed from this scan's calibration reads (Src, calibration.go)
)

// Step is one element of a profile.
type Step struct {
	Kind   StepKind `json:"k"`
	Data   []byte   `json:"d,omitempty"`
	Len    int      `json:"n,omitempty"`
	Expect []byte   `json:"e,omitempty"`
	Until  []byte   `json:"u,omitempty"`
	Chunk  int      `json:"c,omitempty"`
	Tag    string   `json:"t,omitempty"`
	Src    string   `json:"s,omitempty"`
	Gen    string   `json:"g,omitempty"` // write: generated table (see tables.go) instead of Data
}

// Echo sources: the parameter is derived from the scanner's live reply.
const (
	EchoParamBlock  = "1e53"     // 1e 57 writes back the block read by 1e 53
	EchoCalibration = "1e92"     // 1e 31 sends the 12-byte 1e 92 reply
	EchoLevels      = "1eff:3+0" // 1e 99 sends the first 3 bytes of 1e ff + 00
)

// TagImage marks the final data read (the image); calibration reads are
// tagged "calibration-N".
const TagImage = "image"

const maxProfileStep = 1 << 20

// Validate checks a profile for structural sanity (it may come from a file).
func (p *Profile) Validate() error {
	if p.Name == "" || len(p.Steps) == 0 {
		return errors.New("profile: empty")
	}
	images := 0
	for i, s := range p.Steps {
		switch s.Kind {
		case StepWrite:
			if len(s.Data) == 0 || len(s.Data) > maxProfileStep {
				return fmt.Errorf("profile step %d: bad write length %d", i, len(s.Data))
			}
			if s.Gen != "" {
				want, err := GenerateTable(s.Gen)
				if err != nil {
					return fmt.Errorf("profile step %d: %w", i, err)
				}
				if !bytes.Equal(s.Data, want) {
					return fmt.Errorf("profile step %d: data differs from generated table %q", i, s.Gen)
				}
			}
		case StepRead:
			if s.Len <= 0 || s.Len > replyBuf {
				return fmt.Errorf("profile step %d: bad read length %d", i, s.Len)
			}
		case StepWait:
			if len(s.Until) != 2 {
				return fmt.Errorf("profile step %d: wait needs a 2-byte status", i)
			}
		case StepData:
			if s.Len <= 0 || s.Len > 1<<30 || s.Chunk <= 0 || s.Chunk > maxProfileStep {
				return fmt.Errorf("profile step %d: bad data read %d/%d", i, s.Len, s.Chunk)
			}
			if s.Tag == TagImage {
				images++
			}
		case StepEcho:
			switch s.Src {
			case EchoParamBlock, EchoCalibration, EchoLevels:
			default:
				return fmt.Errorf("profile step %d: unknown echo source %q", i, s.Src)
			}
		case StepCalc:
			switch {
			case (s.Src == CalcWhiteOffsets || s.Src == CalcImageLevels) && s.Len == levelsLen:
			case s.Src == CalcShading && s.Len > 0 && s.Len <= maxProfileStep && s.Len%6 == 0:
			default:
				return fmt.Errorf("profile step %d: bad calc %q of %d bytes", i, s.Src, s.Len)
			}
		default:
			return fmt.Errorf("profile step %d: unknown kind %q", i, s.Kind)
		}
	}
	if images != 1 {
		return fmt.Errorf("profile: %d image reads, want 1", images)
	}
	return nil
}

// ParseProfile decodes and validates a JSON profile.
func ParseProfile(b []byte) (*Profile, error) {
	var p Profile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	for i := range p.Steps {
		if s := &p.Steps[i]; s.Kind == StepWrite && s.Gen != "" && len(s.Data) == 0 {
			b, err := GenerateTable(s.Gen)
			if err != nil {
				return nil, fmt.Errorf("profile step %d: %w", i, err)
			}
			s.Data = b
		}
	}
	return &p, p.Validate()
}

// --- extraction --------------------------------------------------------------

type xfer struct {
	in   bool
	data []byte
}

func isCmd(b []byte) bool { return len(b) == 2 && (b[0] == 0x1b || b[0] == 0x1e) }

// ExtractProfile builds a profile from a trace of a vendor app scan: all
// bulk traffic after the identity reply, up to the session close.
func ExtractProfile(tr *trace.Trace, name, description string) (*Profile, error) {
	var xs []xfer
	for _, t := range tr.Transfers {
		if t.Type != trace.TypeBulk || t.Status != 0 {
			continue
		}
		xs = append(xs, xfer{in: t.IsIn(), data: t.Data})
	}
	// Start after the identity reply.
	start := -1
	for i := 0; i+1 < len(xs); i++ {
		if !xs[i].in && bytes.Equal(xs[i].data, cmdIdentity) && xs[i+1].in && len(xs[i+1].data) == 28 {
			start = i + 2
			break
		}
	}
	if start < 0 {
		return nil, errors.New("profile: no identity query in trace")
	}
	// End before the last session close (1e 93, 00).
	end := len(xs)
	for i := len(xs) - 3; i >= start; i-- {
		if !xs[i].in && bytes.Equal(xs[i].data, cmdSession) && !xs[i+2].in && bytes.Equal(xs[i+2].data, []byte{0x00}) {
			end = i
			break
		}
	}

	p := &Profile{Name: name, Description: description, Source: tr.Meta.Source}
	lastReply := map[string][]byte{}
	cmd := ""
	var dataSteps []int
	for i := start; i < end; {
		x := xs[i]
		if x.in {
			return nil, fmt.Errorf("profile: unexpected IN without command at transfer %d", i)
		}
		// Status polling run → one wait step.
		if bytes.Equal(x.data, cmdStatus) {
			var last []byte
			for i < end && !xs[i].in && bytes.Equal(xs[i].data, cmdStatus) && i+1 < end && xs[i+1].in {
				last = xs[i+1].data
				i += 2
			}
			if len(last) != 2 {
				return nil, fmt.Errorf("profile: status poll without 2-byte reply at transfer %d", i)
			}
			p.Steps = append(p.Steps, Step{Kind: StepWait, Until: last})
			cmd = ""
			continue
		}
		// Echo rules for parameter blocks.
		echo := ""
		switch {
		case cmd == "1e57" && bytes.Equal(x.data, lastReply["1e53"]):
			echo = EchoParamBlock
		case cmd == "1e31" && bytes.Equal(x.data, lastReply["1e92"]):
			echo = EchoCalibration
		case cmd == "1e99" && len(x.data) == 4 && len(lastReply["1eff"]) >= 3 &&
			bytes.Equal(x.data[:3], lastReply["1eff"][:3]) && x.data[3] == 0:
			echo = EchoLevels
		}
		if echo != "" {
			p.Steps = append(p.Steps, Step{Kind: StepEcho, Src: echo})
		} else {
			p.Steps = append(p.Steps, Step{Kind: StepWrite, Data: x.data})
		}
		if isCmd(x.data) {
			cmd = fmt.Sprintf("%x", x.data)
		}
		i++
		// Replies.
		var reply []byte
		if cmd == "1e47" && isCmd(x.data) {
			total, chunk := 0, 0
			for i < end && xs[i].in {
				total += len(xs[i].data)
				chunk = max(chunk, len(xs[i].data))
				reply = append(reply, xs[i].data...)
				i++
			}
			if total == 0 {
				return nil, errors.New("profile: data command without data")
			}
			// Round the read size up to whole 512-byte packets.
			chunk = (chunk + 511) / 512 * 512
			p.Steps = append(p.Steps, Step{Kind: StepData, Len: total, Chunk: chunk})
			dataSteps = append(dataSteps, len(p.Steps)-1)
		} else {
			for i < end && xs[i].in {
				d := xs[i].data
				st := Step{Kind: StepRead, Len: len(d)}
				if bytes.Equal(d, []byte{ack}) {
					st.Expect = []byte{ack}
				}
				p.Steps = append(p.Steps, st)
				reply = append(reply, d...)
				i++
			}
		}
		if isCmd(x.data) {
			lastReply[cmd] = reply
		} else if cmd != "" && len(reply) > 0 {
			lastReply[cmd] = reply // reply to the parameter (e.g. 1e 8f, 1e 87)
		}
	}
	if len(dataSteps) == 0 {
		return nil, errors.New("profile: no data read in trace")
	}
	// The largest data read is the image.
	img := dataSteps[0]
	for _, d := range dataSteps {
		if p.Steps[d].Len > p.Steps[img].Len {
			img = d
		}
	}
	n := 0
	for _, d := range dataSteps {
		if d == img {
			p.Steps[d].Tag = TagImage
		} else {
			n++
			p.Steps[d].Tag = fmt.Sprintf("calibration-%d", n)
		}
	}
	p.ImageBytes = p.Steps[img].Len
	// Scan geometry from the last 1e 57 parameter block.
	for i := 0; i+1 < len(p.Steps); i++ {
		s := p.Steps[i]
		if s.Kind == StepWrite && bytes.Equal(s.Data, cmdSetParams) {
			for j := i + 1; j < len(p.Steps); j++ {
				if p.Steps[j].Kind == StepWrite && len(p.Steps[j].Data) == 42 {
					b := p.Steps[j].Data
					p.ScanDPI = int(binary.LittleEndian.Uint32(b[0:]))
					p.WidthPx = int(binary.LittleEndian.Uint32(b[16:]))
					p.HeightPx = int(binary.LittleEndian.Uint32(b[20:]))
					p.Mode = int(binary.LittleEndian.Uint32(b[24:]))
					break
				}
				if p.Steps[j].Kind == StepEcho {
					break
				}
			}
		}
	}
	p.MarkGenerated()
	p.MarkCalibration()
	return p, p.Validate()
}

// MarkCalibration turns the writes that carry calibration results into calc
// steps (their bytes are dropped): the `1e 42` block before the white read,
// the `1e 42` block before the image read and the `1e 84` type 5 table.
func (p *Profile) MarkCalibration() {
	// Data reads in order, and the first wide calibration read (white).
	var reads []int
	white := -1
	var geom []byte
	cmd := ""
	for i, s := range p.Steps {
		switch {
		case s.Kind == StepWrite && isCmd(s.Data):
			cmd = fmt.Sprintf("%x", s.Data)
		case s.Kind == StepWrite && cmd == "1e41" && len(s.Data) == 22:
			geom = s.Data
		case s.Kind == StepData:
			reads = append(reads, i)
			if white < 0 && isCalibrationTag(s.Tag) && len(reads) > 1 && geom != nil &&
				int(binary.LittleEndian.Uint16(geom[2:]))-int(binary.LittleEndian.Uint16(geom[0:])) > 64 {
				white = i
			}
		}
	}
	nextRead := func(i int) int {
		for _, r := range reads {
			if r > i {
				return r
			}
		}
		return -1
	}
	cmd = ""
	var header []byte
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.Kind != StepWrite {
			continue
		}
		if isCmd(s.Data) {
			cmd, header = fmt.Sprintf("%x", s.Data), nil
			continue
		}
		switch {
		case cmd == "1e42" && len(s.Data) == levelsLen:
			switch r := nextRead(i); {
			case r >= 0 && r == white:
				*s = Step{Kind: StepCalc, Src: CalcWhiteOffsets, Len: levelsLen}
			case r >= 0 && p.Steps[r].Tag == TagImage:
				*s = Step{Kind: StepCalc, Src: CalcImageLevels, Len: levelsLen}
			}
		case cmd == "1e84" && header == nil && len(s.Data) == 8:
			header = s.Data
		case cmd == "1e84" && header != nil && header[0] == 5 && s.Gen == "":
			*s = Step{Kind: StepCalc, Src: CalcShading, Len: len(s.Data)}
		}
	}
}

var cmdSetParams = []byte{0x1e, 0x57}

// cmdImagePass (`1e 47`) starts a data read; before the image read the LED
// is switched to fast blinking (transferBlink).
var cmdImagePass = []byte{0x1e, 0x47}

// --- execution ---------------------------------------------------------------

// DataSink receives data reads: tag is "image" or "calibration-N".
type DataSink func(tag string, chunk []byte) error

// RunProfile executes a profile on an open session. Data reads are passed to
// sink in the order received.
func (s *Scanner) RunProfile(ctx context.Context, p *Profile, sink DataSink) error {
	if err := p.Validate(); err != nil {
		return err
	}
	replies := map[string][]byte{}
	cmd := ""
	cal := &calibration{widthPx: p.WidthPx, scanDPI: p.ScanDPI}
	for i := 0; i < len(p.Steps); i++ {
		st := p.Steps[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		fail := func(err error) error { return fmt.Errorf("profile %s step %d (%s): %w", p.Name, i, st.Kind, err) }
		switch st.Kind {
		case StepWrite:
			if n := lampPollRun(p.Steps, i); n > 0 {
				// The recorded number of 1e 04 polls is only what the
				// measurement needed in that session; poll until it is done.
				if err := s.waitLampMeasurement(ctx); err != nil {
					return fail(err)
				}
				i += n - 1
				cmd = ""
				continue
			}
			if bytes.Equal(st.Data, cmdImagePass) && i+1 < len(p.Steps) && p.Steps[i+1].Kind == StepData && p.Steps[i+1].Tag == TagImage {
				if err := s.transferBlink(ctx); err != nil {
					return fail(err)
				}
			}
			data := st.Data
			if cmd == "1e01" && !s.opt.VendorStart {
				data = cornerStart(data, p.ScanDPI)
			}
			if err := s.c.write(ctx, data); err != nil {
				return fail(err)
			}
			switch {
			case isCmd(st.Data):
				cmd = fmt.Sprintf("%x", st.Data)
				replies[cmd] = nil
			case cmd == "1e41" && len(st.Data) == 22:
				cal.last41 = st.Data
			case cmd == "1e11" && len(st.Data) == 1:
				cal.sensorMode = st.Data[0]
			}
		case StepCalc:
			b, err := cal.value(st.Src, st.Len)
			if err != nil {
				return fail(err)
			}
			if len(b) != st.Len {
				return fail(fmt.Errorf("calc %s: %d bytes, want %d", st.Src, len(b), st.Len))
			}
			if err := s.c.write(ctx, b); err != nil {
				return fail(err)
			}
		case StepEcho:
			b, err := echoBytes(st.Src, replies)
			if err != nil {
				return fail(err)
			}
			if err := s.c.write(ctx, b); err != nil {
				return fail(err)
			}
		case StepRead:
			// Replies inside a scan can be late (lamp warm-up after sleep).
			r, err := s.c.readWithin(ctx, replyBuf, s.opt.ProfileReplyTimeout)
			if err != nil {
				return fail(err)
			}
			if len(r) != st.Len || (st.Expect != nil && !bytes.Equal(r, st.Expect)) {
				return fail(fmt.Errorf("%w: got %s, want %d bytes %x", ErrUnexpectedReply, hexShort(r), st.Len, st.Expect))
			}
			if cmd != "" {
				replies[cmd] = append(replies[cmd], r...)
			}
		case StepWait:
			if err := s.waitStatus(ctx, Status{st.Until[0], st.Until[1]}); err != nil {
				return fail(err)
			}
			cmd = ""
		case StepData:
			if !isCalibrationTag(st.Tag) {
				if err := s.readData(ctx, st, sink); err != nil {
					return fail(err)
				}
				break
			}
			// Keep calibration reads for the calc steps.
			var buf []byte
			collect := func(tag string, b []byte) error {
				buf = append(buf, b...)
				if sink != nil {
					return sink(tag, b)
				}
				return nil
			}
			if err := s.readData(ctx, st, collect); err != nil {
				return fail(err)
			}
			cal.reads = append(cal.reads, calRead{geom: cal.last41, data: buf})
		}
	}
	return nil
}

// cmdLampResult (`1e 04`) answers 02 while the lamp measurement started by
// `1e 46` runs. During the measurement the firmware answers only `1e 04` and
// `1e 92` and silently drops every other command (no reply, not queued) —
// sending `1e ff` too early hangs the scan (firmware handler 0x0200b416,
// docs/research/scan-hang.md).
var cmdLampResult = []byte{0x1e, 0x04}

// lampMeasureTimeout bounds the lamp measurement (the vendor: up to 5 polls).
const lampMeasureTimeout = 60 * time.Second

// lampPollRun returns the number of steps of a run of recorded `1e 04`
// polls (write + 1-byte read pairs) starting at i, or 0.
func lampPollRun(steps []Step, i int) int {
	n := 0
	for i+n+1 < len(steps) && steps[i+n].Kind == StepWrite && bytes.Equal(steps[i+n].Data, cmdLampResult) &&
		steps[i+n+1].Kind == StepRead && steps[i+n+1].Len == 1 {
		n += 2
	}
	return n
}

// waitLampMeasurement polls `1e 04` until the measurement is finished
// (reply ≠ 02), sending nothing else in between.
func (s *Scanner) waitLampMeasurement(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, lampMeasureTimeout)
	defer cancel()
	for polls := 1; ; polls++ {
		if err := s.c.write(ctx, cmdLampResult); err != nil {
			return err
		}
		r, err := s.c.read(ctx, replyBuf)
		if err != nil {
			return err
		}
		if len(r) != 1 {
			return fmt.Errorf("%w: lamp result %s", ErrUnexpectedReply, hexShort(r))
		}
		if r[0] != 0x02 {
			if r[0] != 0x01 && s.opt.Log != nil {
				// 01 = all channel levels OK; 00/03 likely mean too dark
				// (inferred from the firmware) — the vendor scans on, so do we.
				s.opt.Log.Warn("v370: lamp measurement result", "result", fmt.Sprintf("%02x", r[0]), "polls", polls)
			}
			return nil
		}
		if err := s.opt.Sleep(ctx, lampPollInterval); err != nil {
			return fmt.Errorf("waiting for the lamp measurement (%d polls): %w", polls, err)
		}
	}
}

// lampPollInterval between `1e 04` polls (the vendor: ~130 ms).
const lampPollInterval = 130 * time.Millisecond

func echoBytes(src string, replies map[string][]byte) ([]byte, error) {
	switch src {
	case EchoParamBlock:
		if r := replies["1e53"]; len(r) == 42 {
			return r, nil
		}
	case EchoCalibration:
		if r := replies["1e92"]; len(r) == 12 {
			return r, nil
		}
	case EchoLevels:
		if r := replies["1eff"]; len(r) >= 3 {
			return []byte{r[0], r[1], r[2], 0}, nil
		}
	}
	return nil, fmt.Errorf("no live reply for echo %s", src)
}

// waitStatus polls until the status equals target or the scanner is ready.
func (s *Scanner) waitStatus(ctx context.Context, target Status) error {
	ctx, cancel := context.WithTimeout(ctx, s.opt.ReadyTimeout)
	defer cancel()
	for {
		st, err := s.Status(ctx)
		if err != nil {
			return err
		}
		if st == target || st.Ready() {
			return nil
		}
		if !st.FirmwareLoaded() {
			return fmt.Errorf("scanner lost its firmware (status %s)", st)
		}
		if err := s.opt.Sleep(ctx, s.opt.PollInterval); err != nil {
			return fmt.Errorf("waiting for status %s (last %s): %w", target, st, err)
		}
	}
}

// dataTimeout bounds a single data read; the first read of a pass may wait
// for the lamp and carriage.
const dataTimeout = 60 * time.Second

func (s *Scanner) readData(ctx context.Context, st Step, sink DataSink) error {
	got := 0
	for got < st.Len {
		cx, cancel := context.WithTimeout(ctx, dataTimeout)
		b, err := s.c.tp.BulkIn(cx, usb.EndpointIn, st.Chunk)
		cancel()
		if err != nil {
			return fmt.Errorf("data read at %d/%d bytes: %w", got, st.Len, err)
		}
		if got+len(b) > st.Len {
			return fmt.Errorf("data read overran: %d > %d bytes", got+len(b), st.Len)
		}
		got += len(b)
		if sink != nil {
			if err := sink(st.Tag, b); err != nil {
				return err
			}
		}
	}
	return nil
}

// Geometry derives the raw image geometry of the profile's final pass: lines
// and lines per block from the last `1e 41` parameter block before the image
// read, width from the image size (must divide exactly).
func (p *Profile) Geometry() (rawimg.Geometry, error) {
	img := -1
	for i, s := range p.Steps {
		if s.Kind == StepData && s.Tag == TagImage {
			img = i
		}
	}
	if img < 0 {
		return rawimg.Geometry{}, errors.New("profile: no image read")
	}
	var block []byte
	for i := 0; i+1 < img; i++ {
		if p.Steps[i].Kind == StepWrite && bytes.Equal(p.Steps[i].Data, cmdLineParams) {
			for j := i + 1; j < img; j++ {
				if p.Steps[j].Kind == StepWrite {
					if len(p.Steps[j].Data) == 22 {
						block = p.Steps[j].Data
					}
					break
				}
			}
		}
	}
	if block == nil {
		return rawimg.Geometry{}, errors.New("profile: no 1e41 line parameters before the image read")
	}
	g := rawimg.Geometry{
		DPI:           p.ScanDPI,
		Lines:         int(binary.LittleEndian.Uint16(block[4:])),
		LinesPerBlock: int(binary.LittleEndian.Uint16(block[18:])),
	}
	if g.Lines <= 0 || g.LinesPerBlock <= 0 {
		return g, fmt.Errorf("profile: invalid line parameters %x", block)
	}
	blocks := (g.Lines + g.LinesPerBlock - 1) / g.LinesPerBlock
	payload := p.ImageBytes - blocks*8
	if payload <= 0 || payload%(g.Lines*3) != 0 {
		return g, fmt.Errorf("profile: image size %d does not fit %d lines in %d blocks", p.ImageBytes, g.Lines, blocks)
	}
	g.Width = payload / (g.Lines * 3)
	return g, g.Validate()
}

var cmdLineParams = []byte{0x1e, 0x41}
