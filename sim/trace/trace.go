// Package trace defines the on-disk format of a recorded USB session.
//
// A trace is a JSON-lines file (optionally gzip-compressed, detected by magic
// bytes) where every line is one Record. Byte payloads are base64 (Go's
// default []byte JSON encoding). Line order is significant: transfers are
// stored in completion order, which is the order the device produced them.
//
//	{"kind":"meta","meta":{...}}
//	{"kind":"descriptor","value":256,"index":0,"data":"EgEAAv..."}
//	{"kind":"transfer","seq":1,"type":"bulk","ep":2,"data":"G0A="}
//
// Traces come from untrusted files, so Read validates every record.
package trace

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Record kinds.
const (
	KindMeta       = "meta"
	KindDescriptor = "descriptor"
	KindTransfer   = "transfer"
)

// Transfer types.
const (
	TypeControl   = "control"
	TypeBulk      = "bulk"
	TypeInterrupt = "interrupt"
)

// Limits applied when reading traces.
const (
	MaxRecordData = 32 << 20 // one transfer payload
	MaxTotalData  = 4 << 30  // whole trace
	maxLineBytes  = 48 << 20 // base64 of MaxRecordData + JSON overhead
)

// Meta describes where a trace came from.
type Meta struct {
	VendorID   uint16   `json:"vendor_id"`
	ProductID  uint16   `json:"product_id"`
	Bus        uint16   `json:"bus,omitempty"`
	Addresses  []uint8  `json:"addresses,omitempty"` // device addresses seen (changes on re-enumeration)
	Source     string   `json:"source,omitempty"`    // capture file name
	CapturedAt string   `json:"captured_at,omitempty"`
	Scenario   []string `json:"scenario,omitempty"` // arguments of the scan command used during capture
	Note       string   `json:"note,omitempty"`
	// OutputSHA256 is the hash of the image file the real scanner produced
	// during capture; Tier-2 tests compare against it.
	OutputSHA256 string `json:"output_sha256,omitempty"`
}

// Descriptor is a GET_DESCRIPTOR response captured from the device.
type Descriptor struct {
	Value uint16 `json:"value"` // (type << 8) | index
	Index uint16 `json:"index"` // language ID for strings
	Data  []byte `json:"data"`
}

// Transfer is one completed USB transfer.
type Transfer struct {
	Seq      int    `json:"seq"`
	Type     string `json:"type"`
	Endpoint uint8  `json:"ep"`              // address incl. direction bit 0x80
	Setup    []byte `json:"setup,omitempty"` // control transfers only (8 bytes)
	Data     []byte `json:"data,omitempty"`
	Status   int32  `json:"status,omitempty"` // 0 = ok, negative errno otherwise
	TimeUS   int64  `json:"time_us,omitempty"`
}

// IsIn reports whether the transfer carried data device → host.
func (t Transfer) IsIn() bool {
	if t.Type == TypeControl && len(t.Setup) == 8 {
		return t.Setup[0]&0x80 != 0
	}
	return t.Endpoint&0x80 != 0
}

// Record is one line of the file (flat, so field names never collide).
type Record struct {
	Kind     string `json:"kind"`
	Meta     *Meta  `json:"meta,omitempty"`
	Value    uint16 `json:"value,omitempty"`
	Index    uint16 `json:"index,omitempty"`
	Seq      int    `json:"seq,omitempty"`
	Type     string `json:"type,omitempty"`
	Endpoint uint8  `json:"ep,omitempty"`
	Setup    []byte `json:"setup,omitempty"`
	Data     []byte `json:"data,omitempty"`
	Status   int32  `json:"status,omitempty"`
	TimeUS   int64  `json:"time_us,omitempty"`
}

// Trace is a fully loaded recording.
type Trace struct {
	Meta        Meta
	Descriptors []Descriptor
	Transfers   []Transfer
}

// Validate checks structural invariants.
func (t *Transfer) Validate() error {
	switch t.Type {
	case TypeControl:
		if len(t.Setup) != 8 {
			return fmt.Errorf("control transfer %d: setup must be 8 bytes", t.Seq)
		}
		if t.Endpoint&0x0f != 0 {
			return fmt.Errorf("control transfer %d: endpoint must be 0", t.Seq)
		}
	case TypeBulk, TypeInterrupt:
		if t.Endpoint&0x0f == 0 || t.Endpoint&0x70 != 0 {
			return fmt.Errorf("transfer %d: invalid endpoint 0x%02x", t.Seq, t.Endpoint)
		}
		if len(t.Setup) != 0 {
			return fmt.Errorf("transfer %d: setup on non-control transfer", t.Seq)
		}
	default:
		return fmt.Errorf("transfer %d: unknown type %q", t.Seq, t.Type)
	}
	if len(t.Data) > MaxRecordData {
		return fmt.Errorf("transfer %d: payload too large", t.Seq)
	}
	return nil
}

// Read parses a trace from r (plain or gzip).
func Read(r io.Reader) (*Trace, error) {
	br := bufio.NewReader(r)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("trace: gzip: %w", err)
		}
		defer gz.Close()
		br = bufio.NewReader(gz)
	}

	sc := bufio.NewScanner(br)
	sc.Buffer(make([]byte, 64<<10), maxLineBytes)
	t := &Trace{}
	var total int64
	seenMeta := false
	line := 0
	for sc.Scan() {
		line++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		var rec Record
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rec); err != nil {
			return nil, fmt.Errorf("trace line %d: %w", line, err)
		}
		switch rec.Kind {
		case KindMeta:
			if seenMeta || rec.Meta == nil {
				return nil, fmt.Errorf("trace line %d: duplicate or empty meta", line)
			}
			seenMeta = true
			t.Meta = *rec.Meta
		case KindDescriptor:
			if len(rec.Data) < 2 || len(rec.Data) > 4096 {
				return nil, fmt.Errorf("trace line %d: invalid descriptor length", line)
			}
			t.Descriptors = append(t.Descriptors, Descriptor{Value: rec.Value, Index: rec.Index, Data: rec.Data})
			total += int64(len(rec.Data))
		case KindTransfer:
			tr := Transfer{Seq: rec.Seq, Type: rec.Type, Endpoint: rec.Endpoint, Setup: rec.Setup,
				Data: rec.Data, Status: rec.Status, TimeUS: rec.TimeUS}
			if err := tr.Validate(); err != nil {
				return nil, fmt.Errorf("trace line %d: %w", line, err)
			}
			total += int64(len(tr.Data))
			t.Transfers = append(t.Transfers, tr)
		default:
			return nil, fmt.Errorf("trace line %d: unknown kind %q", line, rec.Kind)
		}
		if total > MaxTotalData {
			return nil, errors.New("trace: total payload exceeds limit")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	return t, nil
}

// Load reads a trace file.
func Load(path string) (*Trace, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t, err := Read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// Write encodes the trace as JSON lines.
func (t *Trace) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	meta := t.Meta
	if err := enc.Encode(Record{Kind: KindMeta, Meta: &meta}); err != nil {
		return err
	}
	for _, d := range t.Descriptors {
		if err := enc.Encode(Record{Kind: KindDescriptor, Value: d.Value, Index: d.Index, Data: d.Data}); err != nil {
			return err
		}
	}
	for _, tr := range t.Transfers {
		rec := Record{Kind: KindTransfer, Seq: tr.Seq, Type: tr.Type, Endpoint: tr.Endpoint,
			Setup: tr.Setup, Data: tr.Data, Status: tr.Status, TimeUS: tr.TimeUS}
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// Save writes the trace to path; a ".gz" suffix enables gzip compression.
// The file is written to a temporary name first and renamed on success.
func (t *Trace) Save(path string) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	var w io.Writer = f
	var gz *gzip.Writer
	if strings.HasSuffix(path, ".gz") {
		gz = gzip.NewWriter(f)
		w = gz
	}
	err = t.Write(w)
	if gz != nil {
		if cerr := gz.Close(); err == nil {
			err = cerr
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
