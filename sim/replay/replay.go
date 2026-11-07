// Package replay implements a sim.Responder that answers like the real
// scanner did in a recorded trace.
//
// Model: every host *command* — a bulk/interrupt OUT payload or a class/vendor
// control request — opens a response group that holds the IN data the device
// sent afterwards (until the next command). At replay time a command is
// looked up by its exact bytes. Each recorded group is used once:
//
//  1. Normally the first unused group with these bytes (trace order), so
//     repeated commands (status polls, ACKs, firmware chunks) return their
//     recorded answers in order — also when the host polls more or less
//     often than during the capture.
//  2. Context tie-break: if that group was recorded after a *different*
//     previous command than the host's, and an unused group exists that was
//     recorded after the same previous command, that one is used instead.
//     This separates short parameter blocks that follow different commands
//     (e.g. `00` after `1e 87` → data vs. after `1e 93` → ACK), so hosts need
//     not issue every command the vendor app issued.
//  3. Once all are used, the last one is repeated. Standard (chapter 9) control requests are answered by the device
//
// from its descriptors and are not part of the replay.
//
// Some commands carry values that change between runs (the vendor app sends the
// current time as `0b 00 <u32>`). A command without an exact match therefore
// falls back to the next unused recorded command of the same endpoint and
// length that differs in at most MaxFuzzyBytes bytes (and at most a quarter
// of its length). Such matches are counted separately as "fuzzy".
//
// Calibration results computed by the host from the scan's own calibration
// reads (the `1e 42` block after `1e 42`, the shading table after a `1e 84`
// type 5 header) differ from the recorded ones in many bytes. Without an
// exact match such a command takes the next unused recorded command of the
// same endpoint and length; these are counted as "calibrated".
//
// Commands that match neither way are divergences: they are logged,
// counted in Stats, and get no response (control requests stall), so the
// host sees the same symptom as a real device that ignores the command.
package replay

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"

	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
)

const (
	statusEPIPE = -32 // stall
	maxSamples  = 20
	// MaxFuzzyBytes is the most bytes a fuzzy match may differ in.
	MaxFuzzyBytes = 4
	sampleBytes   = 32
	keyBulk       = 'b'
	keyControl    = 'c'
)

type chunk struct {
	data  []byte
	stall bool
}

type group struct {
	seq        int    // trace sequence number of the command
	prev       string // key of the command recorded before this one
	used       bool   // consumed by a host command
	ctrlReply  []byte
	stall      bool // the command itself stalled
	in         map[uint8][]chunk
	commandLen int
}

func newGroup(seq int) *group { return &group{seq: seq, in: map[uint8][]chunk{}} }

// Divergence describes a host command that is not in the trace.
type Divergence struct {
	Kind   string `json:"kind"` // "bulk" or "control"
	Detail string `json:"detail"`
	Len    int    `json:"len"`
	Hex    string `json:"hex"` // first bytes
}

// FuzzyHit records a command matched despite differing bytes.
type FuzzyHit struct {
	Seq      int    `json:"seq"`      // trace seq of the recorded command
	Recorded string `json:"recorded"` // hex
	Received string `json:"received"` // hex
}

// Stats summarises the replay so far.
type Stats struct {
	Groups      int          `json:"groups"`      // distinct recorded commands
	Commands    int          `json:"commands"`    // commands received from the host
	Matched     int          `json:"matched"`     // matched a fresh recorded group
	Repeated    int          `json:"repeated"`    // matched, but recorded occurrences exhausted
	Fuzzy       int          `json:"fuzzy"`       // matched a recorded command with a few bytes differing
	Calibrated  int          `json:"calibrated"`  // computed calibration value matched by position
	Unmatched   int          `json:"unmatched"`   // not in trace
	BytesIn     int64        `json:"bytes_in"`    // bytes served to the host
	Discarded   int          `json:"discarded"`   // recorded IN chunks the host never read
	Divergences []Divergence `json:"divergences"` // first few unmatched commands
	FuzzyHits   []FuzzyHit   `json:"fuzzy_hits"`  // first few fuzzy matches
	LastSeq     int          `json:"last_seq"`    // trace seq of the last matched command
}

// Responder replays a trace. It is safe for concurrent use.
type Responder struct {
	mu      sync.Mutex
	groups  map[string][]*group // by command bytes
	ctx     map[string][]*group // by previous command + command bytes
	prev    string              // key of the host's previous command
	initial *group
	pending map[uint8][]chunk
	stats   Stats
	log     *slog.Logger
}

var _ sim.Responder = (*Responder)(nil)

func bulkKey(ep uint8, data []byte) string {
	return string([]byte{keyBulk, ep}) + string(data)
}

func controlKey(setup []byte, data []byte) string {
	return string([]byte{keyControl}) + string(setup) + string(data)
}

// New builds a responder from a trace.
func New(t *trace.Trace, log *slog.Logger) (*Responder, error) {
	if log == nil {
		log = slog.Default()
	}
	r := &Responder{
		groups: map[string][]*group{},
		ctx:    map[string][]*group{},
		log:    log,
	}
	r.initial = newGroup(0)
	cur := r.initial
	prev := ""
	add := func(key string, g *group) {
		g.prev = prev
		r.groups[key] = append(r.groups[key], g)
		r.ctx[ctxKey(prev, key)] = append(r.ctx[ctxKey(prev, key)], g)
		prev = key
		cur = g
	}
	for _, tr := range t.Transfers {
		switch {
		case tr.Type == trace.TypeControl:
			s, err := usb.ParseSetup(tr.Setup)
			if err != nil {
				return nil, fmt.Errorf("replay: transfer %d: %w", tr.Seq, err)
			}
			if s.IsStandard() {
				continue
			}
			g := newGroup(tr.Seq)
			g.stall = tr.Status == statusEPIPE
			var out []byte
			if s.IsIn() {
				g.ctrlReply = tr.Data
			} else {
				out = tr.Data
			}
			g.commandLen = len(out)
			add(controlKey(tr.Setup, out), g)
		case !tr.IsIn():
			g := newGroup(tr.Seq)
			g.stall = tr.Status == statusEPIPE
			g.commandLen = len(tr.Data)
			add(bulkKey(tr.Endpoint, tr.Data), g)
		default: // bulk/interrupt IN
			switch tr.Status {
			case 0:
				cur.in[tr.Endpoint] = append(cur.in[tr.Endpoint], chunk{data: tr.Data})
			case statusEPIPE:
				cur.in[tr.Endpoint] = append(cur.in[tr.Endpoint], chunk{stall: true})
			default:
				// Cancelled / timed-out URBs (-ENOENT, -ECONNRESET, ...) carry
				// no device data; the host simply waited in vain.
			}
		}
	}
	r.stats.Groups = len(r.groups)
	r.pending = cloneIn(r.initial.in)
	return r, nil
}

func cloneIn(m map[uint8][]chunk) map[uint8][]chunk {
	c := make(map[uint8][]chunk, len(m))
	for k, v := range m {
		c[k] = append([]chunk(nil), v...)
	}
	return c
}

// pickLocked returns the response group for key, or nil.
func ctxKey(prev, key string) string {
	return fmt.Sprintf("%d:%s%s", len(prev), prev, key)
}

func firstUnused(list []*group) *group {
	for _, g := range list {
		if !g.used {
			return g
		}
	}
	return nil
}

func (r *Responder) pickLocked(key string) *group {
	r.stats.Commands++
	list := r.groups[key]
	if g := firstUnused(list); g != nil {
		if g.prev != r.prev {
			if c := firstUnused(r.ctx[ctxKey(r.prev, key)]); c != nil {
				g = c
			}
		}
		g.used = true
		r.stats.Matched++
		return g
	}
	if len(list) == 0 {
		return nil
	}
	r.stats.Repeated++
	return list[len(list)-1]
}

// takeLocked consumes the first unused group of key (which must have one).
func (r *Responder) takeLocked(key string) *group {
	g := firstUnused(r.groups[key])
	g.used = true
	return g
}

// fuzzyKeyLocked finds the recorded command closest in trace order after the
// last matched one that has the same kind, endpoint and length as key, still
// has an unused occurrence, and differs in at most MaxFuzzyBytes bytes.
func (r *Responder) fuzzyKeyLocked(key string) (string, bool) {
	payload := len(key) - 2
	limit := min(MaxFuzzyBytes, payload/4)
	if limit == 0 {
		return "", false
	}
	best, bestSeq := "", 0
	for k, list := range r.groups {
		if len(k) != len(key) || k[:2] != key[:2] {
			continue
		}
		g := firstUnused(list)
		if g == nil {
			continue
		}
		seq := g.seq
		if seq <= r.stats.LastSeq || (best != "" && seq >= bestSeq) {
			continue
		}
		diff := 0
		for i := 2; i < len(k) && diff <= limit; i++ {
			if k[i] != key[i] {
				diff++
			}
		}
		if diff <= limit {
			best, bestSeq = k, seq
		}
	}
	return best, best != ""
}

// calibrationContext reports whether the host's previous command (key) is
// followed by a computed calibration value: `1e 42` or a `1e 84` type 5
// table header.
func calibrationContext(prev string) bool {
	if len(prev) < 2 || prev[0] != keyBulk {
		return false
	}
	d := prev[2:]
	return d == "\x1e\x42" || (len(d) == 8 && d[0] == 5 && d[1] == 0 && d[2] == 8 && d[3] == 1)
}

// positionalKeyLocked finds the first unused recorded command after the last
// matched one with the same kind, endpoint and length as key.
func (r *Responder) positionalKeyLocked(key string) (string, bool) {
	best, bestSeq := "", 0
	for k, list := range r.groups {
		if len(k) != len(key) || k[:2] != key[:2] {
			continue
		}
		g := firstUnused(list)
		if g == nil || g.seq <= r.stats.LastSeq || (best != "" && g.seq >= bestSeq) {
			continue
		}
		best, bestSeq = k, g.seq
	}
	return best, best != ""
}

func (r *Responder) activateLocked(g *group) {
	for _, q := range r.pending {
		r.stats.Discarded += len(q)
	}
	if g == nil {
		r.pending = map[uint8][]chunk{}
		return
	}
	r.stats.LastSeq = g.seq
	r.pending = cloneIn(g.in)
}

func (r *Responder) divergeLocked(kind, detail string, data []byte) {
	r.stats.Unmatched++
	h := data
	if len(h) > sampleBytes {
		h = h[:sampleBytes]
	}
	d := Divergence{Kind: kind, Detail: detail, Len: len(data), Hex: hex.EncodeToString(h)}
	if len(r.stats.Divergences) < maxSamples {
		r.stats.Divergences = append(r.stats.Divergences, d)
	}
	r.log.Warn("replay: command not in trace", "kind", kind, "detail", detail, "len", len(data), "hex", d.Hex,
		"after_seq", r.stats.LastSeq)
}

// Control implements sim.Responder.
func (r *Responder) Control(setup usb.Setup, data []byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []byte
	if !setup.IsIn() {
		out = data
	}
	key := controlKey(setup.Bytes(), out)
	g := r.pickLocked(key)
	r.prev = key
	r.activateLocked(g)
	if g == nil {
		r.divergeLocked("control", setup.String(), out)
		return nil, usb.ErrStall
	}
	if g.stall {
		return nil, usb.ErrStall
	}
	return append([]byte(nil), g.ctrlReply...), nil
}

// BulkOut implements sim.Responder.
func (r *Responder) BulkOut(ep uint8, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := bulkKey(ep, data)
	g := r.pickLocked(key)
	if g == nil && calibrationContext(r.prev) {
		if ck, ok := r.positionalKeyLocked(key); ok {
			g = r.takeLocked(ck)
			key = ck
			r.stats.Calibrated++
		}
	}
	if g == nil {
		if fk, ok := r.fuzzyKeyLocked(key); ok {
			g = r.takeLocked(fk)
			key = fk // continue the context as recorded
			r.stats.Fuzzy++
			hit := FuzzyHit{Seq: g.seq, Recorded: hex.EncodeToString([]byte(fk[2:])), Received: hex.EncodeToString(data)}
			if len(r.stats.FuzzyHits) < maxSamples {
				r.stats.FuzzyHits = append(r.stats.FuzzyHits, hit)
			}
			r.log.Info("replay: fuzzy match", "seq", hit.Seq, "recorded", hit.Recorded, "received", hit.Received)
		}
	}
	r.prev = key
	r.activateLocked(g)
	if g == nil {
		r.divergeLocked("bulk", fmt.Sprintf("ep=0x%02x", ep), data)
		return nil
	}
	if g.stall {
		return usb.ErrStall
	}
	return nil
}

// TryBulkIn implements sim.Responder.
func (r *Responder) TryBulkIn(ep uint8, max int) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.pending[ep]
	if len(q) == 0 {
		return nil, false, nil
	}
	c := q[0]
	if c.stall {
		r.pending[ep] = q[1:]
		return nil, false, usb.ErrStall
	}
	if len(c.data) > max {
		out := append([]byte(nil), c.data[:max]...)
		q[0].data = c.data[max:]
		r.stats.BytesIn += int64(max)
		return out, true, nil
	}
	r.pending[ep] = q[1:]
	r.stats.BytesIn += int64(len(c.data))
	return append([]byte(nil), c.data...), true, nil
}

// Reset implements sim.Responder. Recorded-occurrence counters are kept,
// because a real scanner keeps its state (e.g. loaded firmware) across a
// USB reset; only undelivered IN data is dropped.
func (r *Responder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activateLocked(nil)
}

// Stats returns a snapshot of the replay statistics.
func (r *Responder) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stats
	s.Divergences = append([]Divergence(nil), r.stats.Divergences...)
	s.FuzzyHits = append([]FuzzyHit(nil), r.stats.FuzzyHits...)
	return s
}
