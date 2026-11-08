package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/scanSTATION/lib-v370/v370"
)

// cmdLED runs a visible test sequence of the LED (`1e de`): wait, orange
// (or off), green, quick changes, green.
func cmdLED(args []string) error {
	fs := flag.NewFlagSet("led", flag.ExitOnError)
	var c common
	c.register(fs)
	wait := fs.Duration("wait", 5*time.Second, "pause before the sequence")
	hold := fs.Duration("hold", 3*time.Second, "how long the alternative state is held")
	blinks := fs.Int("blinks", 3, "quick changes after the hold")
	period := fs.Duration("period", 300*time.Millisecond, "duration of each quick on/off phase")
	off := fs.Bool("off", false, "use the off state (needs the LED patch) instead of orange")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: v370 led [flags]   (test sequence of the LED's alternative state, 1e de)")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	c.noSession = true
	ctx := context.Background()
	dev, s, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer dev.Close()
	defer s.Close(ctx)
	start := time.Now()
	say := func(msg string) { fmt.Printf("%6.2f s  %s\n", time.Since(start).Seconds(), msg) }
	alt := v370.LEDOrange
	if *off {
		alt = v370.LEDOff
	}
	patched, err := s.LEDPatched(ctx)
	if err != nil {
		return err
	}
	say(fmt.Sprintf("LED patch running: %v", patched))
	set := func(on bool) error {
		l := v370.LEDGreen
		if on {
			l = alt
		}
		if err := s.SetLED(ctx, l); err != nil {
			s.SetLED(ctx, v370.LEDGreen) // best effort: back to normal
			return err
		}
		return nil
	}
	say("waiting")
	time.Sleep(*wait)
	say("alternative state ON")
	if err := set(true); err != nil {
		return err
	}
	time.Sleep(*hold)
	say("back to normal")
	if err := set(false); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	say(fmt.Sprintf("%d quick changes", *blinks))
	for i := 0; i < *blinks; i++ {
		if err := set(true); err != nil {
			return err
		}
		time.Sleep(*period)
		if err := set(false); err != nil {
			return err
		}
		time.Sleep(*period)
	}
	say("done (normal)")
	return nil
}
