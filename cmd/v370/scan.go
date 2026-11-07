package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/scanSTATION/lib-v370/output"
	"github.com/scanSTATION/lib-v370/v370"
)

// writeScan decodes a raw stream and writes BASE.png + BASE.json (and
// BASE.raw with keepRaw) via the shared scanout package.
func writeScan(out string, p *v370.Profile, stream []byte, keepRaw bool, meta output.Meta) error {
	base := strings.TrimSuffix(out, ".png")
	files, err := output.Write(base, p, stream, output.Options{Format: output.PNG, KeepRaw: keepRaw}, meta)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", strings.Join(files, ", "))
	return nil
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	var c common
	c.register(fs)
	profile := fs.String("profile", "a4-300dpi-color", "built-in profile: "+strings.Join(v370.ProfileNames(), ", "))
	out := fs.String("o", "scan.png", "output PNG (metadata goes to the same name with .json)")
	keepRaw := fs.Bool("keep-raw", false, "also keep the raw scanner stream (.raw) for later re-decoding")
	keepCalib := fs.Bool("keep-calibration", false, "also save the calibration reads (.calibration-N)")
	fs.Parse(args)

	p, err := v370.LoadProfile(*profile)
	if err != nil {
		return err
	}
	if _, err := p.Geometry(); err != nil {
		return err
	}
	base := strings.TrimSuffix(*out, ".png")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	start := time.Now()
	dev, s, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer dev.Close()
	fmt.Fprintf(os.Stderr, "scanning with profile %s (%d dpi, %.1f MB)…\n", p.Name, p.ScanDPI, float64(p.ImageBytes)/1e6)

	stream := make([]byte, 0, p.ImageBytes)
	calib := map[string][]byte{}
	lastPct := -1
	sink := func(tag string, b []byte) error {
		if tag != v370.TagImage {
			if *keepCalib {
				calib[tag] = append(calib[tag], b...)
			}
			return nil
		}
		stream = append(stream, b...)
		if pct := len(stream) * 100 / p.ImageBytes; pct/10 != lastPct/10 {
			lastPct = pct
			fmt.Fprintf(os.Stderr, "  image %3d%%\n", pct)
		}
		return nil
	}
	runErr := s.RunProfile(ctx, p, sink)
	closeErr := s.Close(context.Background())
	if err := errors.Join(runErr, closeErr); err != nil {
		return err
	}
	elapsed := time.Since(start)
	for tag, b := range calib {
		if err := os.WriteFile(base+"."+tag, b, 0o644); err != nil {
			return err
		}
	}
	return writeScan(base, p, stream, *keepRaw, output.Meta{
		Identity: s.Identity().String(), Time: start.UTC(), Seconds: elapsed.Seconds(),
	})
}

// legacyRawMeta is the sidecar written by the P3 version of `scan`.
type legacyRawMeta struct {
	Profile string    `json:"profile"`
	Time    time.Time `json:"time"`
	Seconds float64   `json:"seconds"`
	Ident   string    `json:"identity"`
}

// cmdDecode converts a raw stream (from `scan -keep-raw` or the P3 version
// of scan) into PNG + JSON.
func cmdDecode(args []string) error {
	fs := flag.NewFlagSet("decode", flag.ExitOnError)
	profile := fs.String("profile", "", "profile the raw stream was scanned with (default: from RAW.json)")
	out := fs.String("o", "", "output PNG (default: RAW with .png)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: v370 decode [-profile NAME] [-o OUT.png] RAW")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	rawPath := fs.Arg(0)
	meta := output.Meta{}
	if b, err := os.ReadFile(rawPath + ".json"); err == nil {
		var lm legacyRawMeta
		if json.Unmarshal(b, &lm) == nil {
			if *profile == "" {
				*profile = lm.Profile
			}
			meta.Time, meta.Seconds, meta.Identity = lm.Time, lm.Seconds, lm.Ident
		}
	}
	if *profile == "" {
		return errors.New("decode: -profile is required (no RAW.json with a profile name)")
	}
	p, err := v370.LoadProfile(*profile)
	if err != nil {
		return err
	}
	g, err := p.Geometry()
	if err != nil {
		return err
	}
	fi, err := os.Stat(rawPath)
	if err != nil {
		return err
	}
	if fi.Size() != int64(g.StreamSize()) {
		return fmt.Errorf("decode: %s has %d bytes, profile %s expects %d", rawPath, fi.Size(), p.Name, g.StreamSize())
	}
	stream, err := os.ReadFile(rawPath)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = strings.TrimSuffix(rawPath, ".raw") + ".png"
	}
	if meta.Time.IsZero() {
		meta.Time = fi.ModTime().UTC()
	}
	return writeScan(*out, p, stream, false, meta)
}
