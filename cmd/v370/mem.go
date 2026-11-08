package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

// cmdMem reads scanner RAM (read-only `1e 83` type 8), optionally
// repeatedly, printing only changes when repeating.
func cmdMem(args []string) error {
	fs := flag.NewFlagSet("mem", flag.ExitOnError)
	var c common
	c.register(fs)
	repeat := fs.Int("repeat", 1, "read this many times")
	interval := fs.Duration("interval", 50*time.Millisecond, "pause between reads")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: v370 mem [flags] ADDR LEN   e.g. v370 mem 0x450 3 (RAM only, LEN ≤ 512)")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() != 2 || *repeat < 1 {
		fs.Usage()
		os.Exit(2)
	}
	addr, err := strconv.ParseUint(fs.Arg(0), 0, 32)
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(fs.Arg(1))
	if err != nil {
		return err
	}
	c.noSession = true // like the button watcher: Power keeps working
	ctx := context.Background()
	dev, s, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer dev.Close()
	defer s.Close(ctx)
	start := time.Now()
	var last []byte
	for i := 0; i < *repeat; i++ {
		b, err := s.ReadMemory(ctx, uint32(addr), n)
		if err != nil {
			return err
		}
		if i == 0 || string(b) != string(last) {
			fmt.Printf("%8.3f s  0x%x: % x\n", time.Since(start).Seconds(), addr, b)
		}
		last = b
		if i+1 < *repeat {
			time.Sleep(*interval)
		}
	}
	return nil
}
