package v370_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/scanSTATION/lib-v370/internal/testtraces"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/v370"
)

// recordedHour is the clock value in both captures (0x6199 hours).
var recordedHour = v370.ClockEpoch.Add(24985*time.Hour + 30*time.Minute)

func noSleep(context.Context, time.Duration) error { return nil }

func simulator(t *testing.T, name string) (*sim.Transport, *replay.Responder) {
	t.Helper()
	tr, err := trace.Load(testtraces.Path(t, name))
	if err != nil {
		t.Fatal(err)
	}
	r, err := replay.New(tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := sim.NewDevice(sim.DescriptorsFromTrace(tr), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return sim.NewTransport(dev), r
}

func open(t *testing.T, tp usb.Transport) *v370.Scanner {
	t.Helper()
	return openAt(t, tp, recordedHour)
}

// openAt opens a session with the clock pinned to at.
func openAt(t *testing.T, tp usb.Transport, at time.Time) *v370.Scanner {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := v370.Open(ctx, tp, v370.Options{Now: func() time.Time { return at }, Sleep: noSleep, Firmware: testFirmware(t)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func checkExact(t *testing.T, r *replay.Responder) {
	t.Helper()
	st := r.Stats()
	if st.Unmatched != 0 || st.Fuzzy != 0 {
		t.Fatalf("replay diverged: %+v", st)
	}
}

// Fresh power-on: status 28 00 → firmware upload → warm-up polling → ready.
func TestOpenUploadsFirmware(t *testing.T) {
	tp, r := simulator(t, "a4-75dpi-poweron")
	s := open(t, tp)
	if !s.FirmwareUploaded {
		t.Fatal("firmware was not uploaded")
	}
	if id := s.Identity(); id.Model != "GT-F740" || id.Version != "1.00" {
		t.Fatalf("identity = %+v", id)
	}
	ctx := context.Background()
	// Register reads and the resolution query as recorded right after Open.
	if v, err := s.ReadRegister(ctx, 0x0010); err != nil || v != 120 {
		t.Fatalf("register 0x10 = %d, %v", v, err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	checkExact(t, r)
	if st := r.Stats(); st.Commands < 70 {
		t.Fatalf("expected the warm-up polling to be replayed, stats %+v", st)
	}
}

// Firmware already loaded (USB replug only): no upload.
func TestOpenWithoutFirmwareUpload(t *testing.T) {
	tp, r := simulator(t, "a4-300dpi-jpeg")
	s := open(t, tp)
	if s.FirmwareUploaded {
		t.Fatal("firmware must not be uploaded when already present")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkExact(t, r)
}

func TestStatusBits(t *testing.T) {
	cases := map[v370.Status][4]bool{ // loaded, warming, busy, ready
		{0x28, 0x00}: {false, false, false, false},
		{0x1a, 0x01}: {true, true, false, false},
		{0x5a, 0x01}: {true, true, true, false},
		{0x58, 0x01}: {true, false, true, false},
		{0x18, 0x01}: {true, false, false, true},
	}
	for st, want := range cases {
		got := [4]bool{st.FirmwareLoaded(), st.WarmingUp(), st.Busy(), st.Ready()}
		if got != want {
			t.Errorf("%s: got %v want %v", st, got, want)
		}
	}
}

// fakeTransport answers every OUT with a fixed reply.
type fakeTransport struct{ reply []byte }

func (f *fakeTransport) Control(context.Context, usb.Setup, []byte) ([]byte, error) { return nil, nil }
func (f *fakeTransport) BulkOut(_ context.Context, _ uint8, b []byte) (int, error) {
	return len(b), nil
}
func (f *fakeTransport) BulkIn(context.Context, uint8, int) ([]byte, error) { return f.reply, nil }
func (f *fakeTransport) Close() error                                       { return nil }

func TestRejectsUnexpectedReplies(t *testing.T) {
	ctx := context.Background()
	opt := v370.Options{Sleep: noSleep}
	// Status of the wrong length.
	if _, err := v370.Open(ctx, &fakeTransport{reply: []byte{0x18}}, opt); !errors.Is(err, v370.ErrUnexpectedReply) {
		t.Fatalf("short status: %v", err)
	}
	// Ready status, but a NAK (0x15) instead of ACK for the session command.
	ft := &fakeTransport{reply: []byte{0x18, 0x01}}
	_, err := v370.Open(ctx, ft, opt)
	if !errors.Is(err, v370.ErrUnexpectedReply) {
		t.Fatalf("NAK: %v", err)
	}
}

func TestWaitReadyTimesOut(t *testing.T) {
	// Always warming up.
	ft := &fakeTransport{reply: []byte{0x1a, 0x01}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := v370.Open(ctx, ft, v370.Options{ReadyTimeout: 50 * time.Millisecond, PollInterval: time.Millisecond})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// clockOf returns a time that encodes to the clock value recorded in tr.
func clockOf(t *testing.T, tr *trace.Trace) time.Time {
	t.Helper()
	for _, x := range tr.Transfers {
		if x.Type == trace.TypeBulk && !x.IsIn() && len(x.Data) == 6 && x.Data[0] == 0x0b && x.Data[1] == 0 {
			h := binary.LittleEndian.Uint32(x.Data[2:])
			return v370.ClockEpoch.Add(time.Duration(h)*time.Hour + 30*time.Minute)
		}
	}
	t.Fatal("no clock command in trace")
	return time.Time{}
}

func TestReadButton(t *testing.T) {
	ctx := context.Background()
	for reply, want := range map[byte]v370.Button{0x00: v370.ButtonNone, 0x02: v370.ButtonPrinter, 0x03: v370.ButtonEmail, 0x04: v370.ButtonPDF} {
		// Session open needs "ready" + ACKs; use a scripted transport.
		tp := &scriptTransport{replies: [][]byte{{0x18, 0x01}, {0x06}, {0x06}, {0x06}, {0x06}, {0x18, 0x01},
			[]byte("SIM     GT-F740         1.00"), {reply}}}
		s, err := v370.Open(ctx, tp, v370.Options{Sleep: noSleep})
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.ReadButton(ctx)
		if err != nil || b != want {
			t.Fatalf("reply %02x: got %v, %v want %v", reply, b, err, want)
		}
		if got := tp.lastWrite; !bytes.Equal(got, []byte{0x1e, 0x85}) {
			t.Fatalf("sent %x", got)
		}
	}
	// An ACK instead of a button value is an error (never observed).
	tp := &scriptTransport{replies: [][]byte{{0x18, 0x01}, {0x06}, {0x06}, {0x06}, {0x06}, {0x18, 0x01},
		[]byte("SIM     GT-F740         1.00"), {0x06}}}
	s, _ := v370.Open(ctx, tp, v370.Options{Sleep: noSleep})
	if _, err := s.ReadButton(ctx); !errors.Is(err, v370.ErrUnexpectedReply) {
		t.Fatalf("ACK reply: %v", err)
	}
	if v370.ButtonEmail.String() != "email" || v370.Button(1).String() != "power" || v370.Button(9).String() != "button-9" {
		t.Fatal("names")
	}
}

// scriptTransport returns the scripted replies in order.
type scriptTransport struct {
	replies   [][]byte
	lastWrite []byte
}

func (f *scriptTransport) Control(context.Context, usb.Setup, []byte) ([]byte, error) {
	return nil, nil
}
func (f *scriptTransport) BulkOut(_ context.Context, _ uint8, b []byte) (int, error) {
	f.lastWrite = append([]byte(nil), b...)
	return len(b), nil
}
func (f *scriptTransport) BulkIn(context.Context, uint8, int) ([]byte, error) {
	if len(f.replies) == 0 {
		return nil, errors.New("script exhausted")
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, nil
}
func (f *scriptTransport) Close() error { return nil }

var (
	fwOnce sync.Once
	fwTest *firmware.Firmware
	fwErr  error
)

// testFirmware returns the firmware payload recorded in the power-on trace
// (the vendor's esfwdd.bin is not part of this repository); the test is skipped
// without the private recordings.
func testFirmware(t *testing.T) *firmware.Firmware {
	t.Helper()
	path := testtraces.Path(t, "a4-75dpi-poweron")
	fwOnce.Do(func() {
		tr, err := trace.Load(path)
		if err != nil {
			fwErr = err
			return
		}
		for _, x := range tr.Transfers {
			if x.Type == trace.TypeBulk && !x.IsIn() && len(x.Data) == firmware.PayloadLen {
				fwTest, fwErr = firmware.FromPayload(x.Data)
				return
			}
		}
		fwErr = errors.New("no firmware payload in the power-on trace")
	})
	if fwErr != nil {
		t.Fatal(fwErr)
	}
	return fwTest
}

// slowTransport answers like scriptTransport but delays the reply that
// follows a given command (models the scanner answering 1e ff only after
// the lamp warmed up).
type slowTransport struct {
	scriptTransport
	slowAfter []byte
	delay     time.Duration
	slow      bool
}

func (f *slowTransport) BulkOut(ctx context.Context, ep uint8, b []byte) (int, error) {
	f.slow = bytes.Equal(b, f.slowAfter)
	return f.scriptTransport.BulkOut(ctx, ep, b)
}

func (f *slowTransport) BulkIn(ctx context.Context, ep uint8, n int) ([]byte, error) {
	if f.slow {
		f.slow = false
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.scriptTransport.BulkIn(ctx, ep, n)
}

// A profile reply that arrives later than the normal I/O timeout (but within
// ProfileReplyTimeout) does not fail the scan.
func TestProfileToleratesLateReply(t *testing.T) {
	ctx := context.Background()
	tp := &slowTransport{
		scriptTransport: scriptTransport{replies: [][]byte{{0x18, 0x01}, {0x06}, {0x06}, {0x06}, {0x06}, {0x18, 0x01},
			[]byte("SIM     GT-F740         1.00"),
			{0xe5, 0xe7, 0xee, 0x03}, // late reply to 1e ff
			{0x01, 0x02, 0x03, 0x04}, // image data
		}},
		slowAfter: []byte{0x1e, 0xff}, delay: 150 * time.Millisecond,
	}
	s, err := v370.Open(ctx, tp, v370.Options{Sleep: noSleep, IOTimeout: 50 * time.Millisecond, ProfileReplyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	p := &v370.Profile{Name: "late", Steps: []v370.Step{
		{Kind: v370.StepWrite, Data: []byte{0x1e, 0xff}},
		{Kind: v370.StepRead, Len: 4},
		{Kind: v370.StepWrite, Data: []byte{0x1e, 0x47}},
		{Kind: v370.StepData, Len: 4, Chunk: 512, Tag: v370.TagImage},
	}}
	if err := s.RunProfile(ctx, p, nil); err != nil {
		t.Fatalf("late reply must be tolerated: %v", err)
	}
	// With a reply timeout shorter than the delay it fails clearly.
	tp2 := &slowTransport{scriptTransport: scriptTransport{replies: tp.scriptTransport.replies}, slowAfter: tp.slowAfter, delay: tp.delay}
	tp2.replies = [][]byte{{0x18, 0x01}, {0x06}, {0x06}, {0x06}, {0x06}, {0x18, 0x01}, []byte("SIM     GT-F740         1.00"), {0xe5, 0xe7, 0xee, 0x03}}
	s2, _ := v370.Open(ctx, tp2, v370.Options{Sleep: noSleep, IOTimeout: 50 * time.Millisecond, ProfileReplyTimeout: 60 * time.Millisecond})
	if err := s2.RunProfile(ctx, p, nil); err == nil {
		t.Fatal("expected a timeout")
	}
}

// recTransport records every write (to check what is sent during the lamp
// measurement).
type recTransport struct {
	scriptTransport
	writes [][]byte
}

func (f *recTransport) BulkOut(ctx context.Context, ep uint8, b []byte) (int, error) {
	f.writes = append(f.writes, append([]byte(nil), b...))
	return f.scriptTransport.BulkOut(ctx, ep, b)
}

// A profile recorded with 2 `1e 04` polls still works when the measurement
// needs 4: the runner polls until the reply is not 02 and sends nothing else
// in between (the firmware drops other commands while measuring).
func TestProfileWaitsForLampMeasurement(t *testing.T) {
	ctx := context.Background()
	tp := &recTransport{scriptTransport: scriptTransport{replies: [][]byte{{0x18, 0x01}, {0x06}, {0x06}, {0x06}, {0x06}, {0x18, 0x01},
		[]byte("SIM     GT-F740         1.00"),
		{0x02}, {0x02}, {0x02}, {0x01}, // 1e 04: measuring ×3, then done
		{0x86, 0, 0x8c, 0, 0x8a, 0, 0, 0, 0x38, 0x2f, 0x34, 0}, // 1e 92
		{0x01, 0x02, 0x03, 0x04},                               // image data
	}}}
	s, err := v370.Open(ctx, tp, v370.Options{Sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	p := &v370.Profile{Name: "lamp", Steps: []v370.Step{
		{Kind: v370.StepWrite, Data: []byte{0x1e, 0x04}},
		{Kind: v370.StepRead, Len: 1},
		{Kind: v370.StepWrite, Data: []byte{0x1e, 0x04}},
		{Kind: v370.StepRead, Len: 1},
		{Kind: v370.StepWrite, Data: []byte{0x1e, 0x92}},
		{Kind: v370.StepRead, Len: 12},
		{Kind: v370.StepWrite, Data: []byte{0x1e, 0x47}},
		{Kind: v370.StepData, Len: 4, Chunk: 512, Tag: v370.TagImage},
	}}
	tp.writes = nil
	if err := s.RunProfile(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range tp.writes {
		got = append(got, fmt.Sprintf("%x", w))
	}
	want := "1e04 1e04 1e04 1e04 1e92 1e47"
	if strings.Join(got, " ") != want {
		t.Fatalf("writes %v, want %s", got, want)
	}
}
