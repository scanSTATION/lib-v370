package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// cmdRaw sends exploratory 2-byte commands inside a session. Only the
// ESC (1b) and FS-like (1e) families are allowed; unknown commands may
// still have side effects, so this is for protocol exploration only.
func cmdRaw(args []string) error {
	fs := flag.NewFlagSet("raw", flag.ExitOnError)
	var c common
	c.register(fs)
	timeout := fs.Duration("timeout", 2*time.Second, "how long to wait for a reply")
	repeat := fs.Int("repeat", 1, "send the command list this many times")
	interval := fs.Duration("interval", 200*time.Millisecond, "pause between repeats")
	risk := fs.Bool("i-accept-the-risk", false, "required: unknown commands can hang the scanner until it is power-cycled (docs/protocol.md §2)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: v370 raw [flags] HEX [HEX...]   e.g. v370 raw 1b21")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() == 0 || *repeat < 1 || *repeat > 10000 {
		fs.Usage()
		os.Exit(2)
	}
	if !*risk {
		return fmt.Errorf("raw: refusing without -i-accept-the-risk: a command answered with ACK waits for a parameter, " +
			"and the scanner then hangs until it is switched off (happened with 1b21, see docs/protocol.md §2)")
	}
	var cmds [][2]byte
	for _, a := range fs.Args() {
		b, err := hex.DecodeString(strings.TrimPrefix(a, "0x"))
		if err != nil || len(b) != 2 || (b[0] != 0x1b && b[0] != 0x1e) {
			return fmt.Errorf("raw: %q is not a 2-byte 1bxx/1exx command", a)
		}
		cmds = append(cmds, [2]byte{b[0], b[1]})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	openCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	dev, s, err := c.open(openCtx)
	cancel()
	if err != nil {
		return err
	}
	defer dev.Close()
	defer s.Close(context.Background())

	start := time.Now()
	for i := 0; i < *repeat && ctx.Err() == nil; i++ {
		for _, cmd := range cmds {
			r, err := s.Raw(ctx, cmd, *timeout)
			el := time.Since(start).Round(time.Millisecond)
			switch {
			case err != nil:
				return fmt.Errorf("%x: %w", cmd, err)
			case r == nil:
				fmt.Printf("%8s  %x -> (no reply within %s)\n", el, cmd, *timeout)
			case len(r) == 1 && r[0] == 0x15:
				fmt.Printf("%8s  %x -> 15 (NAK)\n", el, cmd)
			default:
				fmt.Printf("%8s  %x -> %x (%d bytes) %q\n", el, cmd, r, len(r), printable(r))
			}
		}
		if i+1 < *repeat {
			time.Sleep(*interval)
		}
	}
	return nil
}

func printable(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c < 0x7f {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}
