package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/scanSTATION/lib-v370/v370"
)

// cmdButtons polls the panel buttons (`1e 85`) inside one session and prints
// every press. A press is reported once by the scanner (cleared on read).
func cmdButtons(args []string) error {
	fs := flag.NewFlagSet("buttons", flag.ExitOnError)
	var c common
	c.register(fs)
	interval := fs.Duration("interval", 200*time.Millisecond, "polling interval (50ms–5s)")
	duration := fs.Duration("duration", 0, "stop after this time (0 = until Ctrl-C)")
	once := fs.Bool("once", false, "exit after the first press and print only the button name (for scripts)")
	fs.BoolVar(&c.noSession, "no-session", false, "poll without a host session (the Power button keeps working: hold ~3 s)")
	fs.Parse(args)
	if *interval < 50*time.Millisecond || *interval > 5*time.Second {
		return errors.New("buttons: -interval must be between 50ms and 5s")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	openCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	dev, s, err := c.open(openCtx)
	cancel()
	if err != nil {
		return err
	}
	defer dev.Close()
	defer s.Close(context.Background())

	if !*once {
		fmt.Fprintf(os.Stderr, "watching buttons (Printer, Email, PDF, Power) every %s (session: %v) — Ctrl-C to stop\n", *interval, !c.noSession)
	}
	t := time.NewTicker(*interval)
	defer t.Stop()
	for {
		b, err := s.ReadButton(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if b != v370.ButtonNone {
			if *once {
				fmt.Println(b)
				return nil
			}
			fmt.Printf("%s  %s\n", time.Now().Format("15:04:05.000"), b)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
