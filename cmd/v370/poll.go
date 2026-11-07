package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/scanSTATION/lib-v370/v370"
)

// probe is one read-only query. Only commands that the vendor app itself sent
// as plain queries (no parameters, no side effects seen) are used.
type probe struct {
	name string
	run  func(ctx context.Context, s *v370.Scanner) ([]byte, error)
}

func query(cmd [2]byte, n int) func(context.Context, *v370.Scanner) ([]byte, error) {
	return func(ctx context.Context, s *v370.Scanner) ([]byte, error) { return s.Query(ctx, cmd, n) }
}

func register(addr uint16) func(context.Context, *v370.Scanner) ([]byte, error) {
	return func(ctx context.Context, s *v370.Scanner) ([]byte, error) {
		v, err := s.ReadRegister(ctx, addr)
		if err != nil {
			return nil, err
		}
		return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}, nil
	}
}

var probes = []probe{
	{"status 1b03", query([2]byte{0x1b, 0x03}, 2)},
	{"1ea1", query([2]byte{0x1e, 0xa1}, 1)},
	{"1e65", query([2]byte{0x1e, 0x65}, 1)},
	{"1e9f", query([2]byte{0x1e, 0x9f}, 1)},
	{"1e86", query([2]byte{0x1e, 0x86}, 4)},
	{"1e97", query([2]byte{0x1e, 0x97}, 12)},
	{"1e53", query([2]byte{0x1e, 0x53}, 42)},
	{"reg 0x0010", register(0x0010)},
	{"reg 0x0005", register(0x0005)},
}

// pollRecord is one line of the JSON log.
type pollRecord struct {
	Time    string `json:"time"`
	Elapsed string `json:"elapsed"`
	Probe   string `json:"probe"`
	Value   string `json:"value,omitempty"`
	Error   string `json:"error,omitempty"`
	Changed bool   `json:"changed"`
}

func cmdPoll(args []string) error {
	fs := flag.NewFlagSet("poll", flag.ExitOnError)
	var c common
	c.register(fs)
	interval := fs.Duration("interval", 100*time.Millisecond, "pause between polling rounds")
	duration := fs.Duration("duration", 60*time.Second, "how long to poll")
	logPath := fs.String("log", "", "append every reading as JSON lines to this file")
	only := fs.String("only", "", "poll only the probe with this name (e.g. 1ea1)")
	fs.Parse(args)
	if *interval < 10*time.Millisecond || *duration <= 0 || *duration > time.Hour {
		return errors.New("poll: interval must be ≥10ms and duration in (0, 1h]")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logf *json.Encoder
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		logf = json.NewEncoder(f)
	}

	openCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	dev, s, err := c.open(openCtx)
	cancel()
	if err != nil {
		return err
	}
	defer dev.Close()
	defer s.Close(context.Background())

	active := probes
	if *only != "" {
		active = nil
		for _, p := range probes {
			if p.name == *only {
				active = append(active, p)
			}
		}
		if len(active) == 0 {
			return fmt.Errorf("poll: unknown probe %q", *only)
		}
	}

	fmt.Printf("polling %d probes every %s for %s (Ctrl-C to stop)\n", len(active), *interval, *duration)
	start := time.Now()
	last := map[string]string{}
	rounds := 0
	deadline := time.After(*duration)
	for {
		for _, p := range active {
			v, err := p.run(ctx, s)
			now := time.Now()
			rec := pollRecord{Time: now.UTC().Format(time.RFC3339Nano), Elapsed: now.Sub(start).Round(time.Millisecond).String(), Probe: p.name}
			val := ""
			if err != nil {
				rec.Error = err.Error()
				val = "ERR " + err.Error()
			} else {
				rec.Value = hex.EncodeToString(v)
				val = rec.Value
			}
			prev, seen := last[p.name]
			rec.Changed = seen && prev != val
			if !seen || rec.Changed {
				fmt.Printf("%8s  %-11s %s\n", rec.Elapsed, p.name, val)
				last[p.name] = val
			}
			if logf != nil {
				logf.Encode(rec)
			}
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("probe %s failed: %w", p.name, err)
			}
		}
		rounds++
		select {
		case <-ctx.Done():
			fmt.Printf("stopped after %d rounds\n", rounds)
			return nil
		case <-deadline:
			fmt.Printf("done: %d rounds in %s\n", rounds, time.Since(start).Round(time.Millisecond))
			return nil
		case <-time.After(*interval):
		}
	}
}
