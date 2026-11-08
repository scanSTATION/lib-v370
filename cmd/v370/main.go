// Command v370 talks to the Perfection V37/V370 directly over
// USB (pure Go usbfs) — no vendor app, no Flatpak, no libusb.
//
// Usage:
//
//	v370 info [-sim] [-firmware PATH] [-allow-unknown-firmware] [-v]
//	v370 scan [-profile NAME] [-o FILE.png] [-keep-raw] [-keep-calibration] [-sim] [-v]
//	v370 decode [-profile NAME] [-o OUT.png] RAW
//	v370 buttons [-interval 200ms] [-duration D] [-once]
//	v370 poll [-interval 100ms] [-duration 60s] [-log FILE] [-sim] [-v]
//
// info opens a session (uploading the firmware if the scanner was just
// switched on), prints status, identity and known registers, and closes it.
// -sim selects a simulated scanner attached by v370-usbsim instead of the
// real one.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/scanSTATION/lib-v370/firmware"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/usb/usbfs"
	"github.com/scanSTATION/lib-v370/v370"
)

const usage = `v370 — direct USB access to the V37/V370 (no vendor software)

Commands:
  info    open a session, print status/identity/registers, close
  scan    scan with a built-in profile → PNG (uncropped, channel-aligned) + JSON
  decode  convert a raw stream (scan -keep-raw) into PNG + JSON
  buttons watch the panel buttons (Printer, Email, PDF) and print presses
  poll    open a session and poll read-only queries, logging every change
  mem     read scanner RAM (read-only 1e 83 type 8), e.g. button state
  raw     send 2-byte commands (1b xx / 1e xx) and print the replies (exploration)
  reset   USB port reset (WARNING: a hung V370 drops off the bus instead of
          recovering; only a power cycle helps — see docs/protocol.md §2)

Run 'v370 <command> -h' for flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "info":
		err = cmdInfo(os.Args[2:])
	case "scan":
		err = cmdScan(os.Args[2:])
	case "decode":
		err = cmdDecode(os.Args[2:])
	case "poll":
		err = cmdPoll(os.Args[2:])
	case "buttons":
		err = cmdButtons(os.Args[2:])
	case "mem":
		err = cmdMem(os.Args[2:])
	case "raw":
		err = cmdRaw(os.Args[2:])
	case "reset":
		err = cmdReset(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type common struct {
	sim          bool
	fwPath       string
	allowUnknown bool
	verbose      bool
	noSession    bool
	vendorStart  bool
}

func (c *common) register(fs *flag.FlagSet) {
	fs.BoolVar(&c.sim, "sim", false, "use the simulated scanner (v370-usbsim via vhci-hcd)")
	fs.StringVar(&c.fwPath, "firmware", "", "firmware file (default: embedded esfwdd.bin)")
	fs.BoolVar(&c.allowUnknown, "allow-unknown-firmware", false, "accept a firmware file with an unknown SHA-256")
	fs.BoolVar(&c.verbose, "v", false, "verbose logging")
	fs.BoolVar(&c.vendorStart, "vendor-start", false, "start scans where the vendor app does (≈ 1.2 mm after the glass corner)")
}

// open finds, claims and initialises the scanner.
func (c *common) open(ctx context.Context) (*usbfs.Device, *v370.Scanner, error) {
	level := slog.LevelInfo
	if c.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	opt := v370.Options{Log: log, NoSession: c.noSession, VendorStart: c.vendorStart}
	if c.fwPath != "" {
		fw, err := firmware.Load(c.fwPath, c.allowUnknown)
		if err != nil {
			return nil, nil, err
		}
		opt.Firmware = fw
	}
	devs, err := usbfs.Find("", usb.VendorV370, usb.ProductV370)
	if err != nil {
		return nil, nil, err
	}
	sel := usbfs.OnlyReal
	if c.sim {
		sel = usbfs.OnlySimulated
	}
	info, err := usbfs.Select(devs, sel)
	if err != nil {
		return nil, nil, fmt.Errorf("scanner not found: %w", err)
	}
	dev, err := usbfs.Open(info)
	if err != nil {
		return nil, nil, err
	}
	log.Debug("opened USB device", "device", info.String())
	s, err := v370.Open(ctx, dev, opt)
	if err != nil {
		dev.Close()
		return nil, nil, err
	}
	return dev, s, nil
}

func cmdReset(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	sim := fs.Bool("sim", false, "reset the simulated scanner")
	risk := fs.Bool("i-accept-the-risk", false, "required: a hung V370 disappears from USB after a reset")
	fs.Parse(args)
	if !*risk {
		return fmt.Errorf("reset: refusing without -i-accept-the-risk (a hung V370 dropped off the bus after a reset; " +
			"power-cycling the scanner is the reliable recovery)")
	}
	devs, err := usbfs.Find("", usb.VendorV370, usb.ProductV370)
	if err != nil {
		return err
	}
	sel := usbfs.OnlyReal
	if *sim {
		sel = usbfs.OnlySimulated
	}
	info, err := usbfs.Select(devs, sel)
	if err != nil {
		return err
	}
	dev, err := usbfs.Open(info)
	if err != nil {
		return err
	}
	if err := dev.Reset(); err != nil {
		return err
	}
	fmt.Printf("reset %s\n", info)
	return nil
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	var c common
	c.register(fs)
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	start := time.Now()
	dev, s, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer dev.Close()
	fmt.Printf("device:     %s\n", dev.Info())
	fmt.Printf("identity:   %s\n", s.Identity())
	fmt.Printf("firmware:   uploaded now: %v\n", s.FirmwareUploaded)
	fmt.Printf("open took:  %s\n", time.Since(start).Round(time.Millisecond))
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("status:     %s\n", st)
	for _, reg := range []uint16{0x0010, 0x0005} {
		v, err := s.ReadRegister(ctx, reg)
		if err != nil {
			return err
		}
		fmt.Printf("register 0x%04x: %d\n", reg, v)
	}
	return errors.Join(s.Close(ctx))
}
