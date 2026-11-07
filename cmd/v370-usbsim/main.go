// Command v370-usbsim simulates the V370 at the USB level by replaying
// a capture of the real scanner, so the scanner does not have to be
// connected for development and tests.
//
// Usage:
//
//	v370-usbsim convert -in capture.pcap -out testdata/traces/scan.jsonl.gz
//	v370-usbsim inspect testdata/traces/scan.jsonl.gz
//	v370-usbsim serve   -trace testdata/traces/scan.jsonl.gz [-attach] [-status-addr 127.0.0.1:3241]
//	v370-usbsim attach  -server 127.0.0.1:3240      (root)
//	v370-usbsim detach  -port 0                      (root)
//	v370-usbsim list    -server 127.0.0.1:3240
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/scanSTATION/lib-v370/sim"
	"github.com/scanSTATION/lib-v370/sim/pcapusb"
	"github.com/scanSTATION/lib-v370/sim/replay"
	"github.com/scanSTATION/lib-v370/sim/trace"
	"github.com/scanSTATION/lib-v370/sim/usbip"
	"github.com/scanSTATION/lib-v370/sim/vhci"
	"github.com/scanSTATION/lib-v370/usb"
	"github.com/scanSTATION/lib-v370/v370"
)

const usage = `v370-usbsim — USB-level simulator for the Perfection V37/V370

Commands:
  convert   convert a usbmon pcap capture into a replay trace
  inspect   summarise a trace
  profile   extract a scan profile (script for v370) from a trace
  serve     export the simulated scanner over USB/IP (optionally attach it locally)
  attach    attach a USB/IP-exported device via vhci-hcd (root)
  detach    detach a vhci port (root)
  list      list devices exported by a USB/IP server

Run 'v370-usbsim <command> -h' for flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{
		"convert": cmdConvert,
		"inspect": cmdInspect,
		"profile": cmdProfile,
		"serve":   cmdServe,
		"attach":  cmdAttach,
		"detach":  cmdDetach,
		"list":    cmdList,
	}
	fn, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := fn(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newLogger(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q", level)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})), nil
}

func warnIfExposed(log *slog.Logger, what, addr string) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return
	}
	log.Warn(what+" is reachable from the network; it has no authentication", "addr", addr)
}

// --- convert -----------------------------------------------------------------

func cmdConvert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	in := fs.String("in", "", "usbmon capture (classic pcap)")
	out := fs.String("out", "", "output trace (.jsonl or .jsonl.gz)")
	bus := fs.Uint("bus", 0, "USB bus number (default: auto-detect)")
	dev := fs.Uint("dev", 0, "USB device address (default: auto-detect from descriptors)")
	allowTrunc := fs.Bool("allow-truncated", false, "keep transfers truncated by the capture")
	scenario := fs.String("scenario", "", "arguments of the scan command used during capture (stored in meta)")
	sha := fs.String("output-sha256", "", "sha256 of the image the real scanner produced (stored in meta)")
	note := fs.String("note", "", "free-text note stored in meta")
	fs.Parse(args)
	if *in == "" || *out == "" {
		return errors.New("convert: -in and -out are required")
	}
	if *bus > 0xffff || *dev > 127 {
		return errors.New("convert: -bus/-dev out of range")
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()
	t, st, err := pcapusb.Convert(f, pcapusb.Options{
		Bus: uint16(*bus), Address: uint8(*dev), AllowTruncated: *allowTrunc,
	})
	if err != nil {
		return err
	}
	t.Meta.Source = *in
	t.Meta.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	if fi, err := f.Stat(); err == nil {
		t.Meta.CapturedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}
	if *scenario != "" {
		t.Meta.Scenario = strings.Fields(*scenario)
	}
	t.Meta.OutputSHA256 = *sha
	t.Meta.Note = *note
	if err := t.Save(*out); err != nil {
		return err
	}
	fmt.Printf("events=%d transfers=%d descriptors=%d addresses=%v unpaired=%d truncated=%d iso=%d other-devices=%d\n",
		st.Events, st.Transfers, st.Descriptor, st.Addresses, st.Unpaired, st.Truncated, st.Iso, st.OtherDevs)
	fmt.Println("wrote", *out)
	return nil
}

// --- inspect -----------------------------------------------------------------

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	n := fs.Int("commands", 30, "number of host commands to print")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("inspect: expected one trace file")
	}
	t, err := trace.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	m, _ := json.MarshalIndent(t.Meta, "", "  ")
	fmt.Printf("meta: %s\n", m)
	desc := sim.DescriptorsFromTrace(t)
	if dd, err := desc.Device(); err == nil {
		fmt.Printf("device: %04x:%04x bcdDevice=%04x class=%02x\n", dd.VendorID, dd.ProductID, dd.BcdDevice, dd.DeviceClass)
	}
	if ci, err := desc.Config(); err == nil {
		for _, it := range ci.Interfaces {
			fmt.Printf("interface %d class=%02x:", it.Number, it.Class)
			for _, ep := range it.Endpoints {
				fmt.Printf(" ep=0x%02x/%d", ep.Address, ep.MaxPacketSize)
			}
			fmt.Println()
		}
	}
	fmt.Printf("captured descriptors: %d (placeholders are used for missing ones)\n", len(t.Descriptors))

	type agg struct{ n, bytes int }
	per := map[string]*agg{}
	for _, tr := range t.Transfers {
		dir := "out"
		if tr.IsIn() {
			dir = "in"
		}
		k := fmt.Sprintf("%-9s ep=0x%02x %-3s status=%d", tr.Type, tr.Endpoint, dir, tr.Status)
		if per[k] == nil {
			per[k] = &agg{}
		}
		per[k].n++
		per[k].bytes += len(tr.Data)
	}
	keys := make([]string, 0, len(per))
	for k := range per {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("transfers: %d\n", len(t.Transfers))
	for _, k := range keys {
		fmt.Printf("  %s  count=%d bytes=%d\n", k, per[k].n, per[k].bytes)
	}

	fmt.Printf("first %d host commands (non-standard control + OUT):\n", *n)
	shown := 0
	for _, tr := range t.Transfers {
		if shown >= *n {
			break
		}
		if tr.Type == trace.TypeControl {
			s, _ := usb.ParseSetup(tr.Setup)
			if s.IsStandard() {
				continue
			}
			fmt.Printf("  #%d control %s data=%s\n", tr.Seq, s, hexPrefix(tr.Data))
			shown++
		} else if !tr.IsIn() {
			fmt.Printf("  #%d %s ep=0x%02x len=%d %s\n", tr.Seq, tr.Type, tr.Endpoint, len(tr.Data), hexPrefix(tr.Data))
			shown++
		}
	}
	return nil
}

func hexPrefix(b []byte) string {
	const max = 24
	if len(b) > max {
		return hex.EncodeToString(b[:max]) + "…"
	}
	return hex.EncodeToString(b)
}

// --- profile -----------------------------------------------------------------

func cmdProfile(args []string) error {
	fs := flag.NewFlagSet("profile", flag.ExitOnError)
	tracePath := fs.String("trace", "", "trace of a vendor app scan")
	name := fs.String("name", "", "profile name")
	desc := fs.String("description", "", "description")
	out := fs.String("out", "", "output JSON file")
	fs.Parse(args)
	if *tracePath == "" || *name == "" || *out == "" {
		return errors.New("profile: -trace, -name and -out are required")
	}
	t, err := trace.Load(*tracePath)
	if err != nil {
		return err
	}
	p, err := v370.ExtractProfile(t, *name, *desc)
	if err != nil {
		return err
	}
	p.Source = filepath.Base(*tracePath)
	p.Compact() // generated tables are stored by name
	b, err := json.MarshalIndent(p, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d steps, scan %d dpi %dx%d px, mode %#x, image %d bytes\n",
		p.Name, len(p.Steps), p.ScanDPI, p.WidthPx, p.HeightPx, p.Mode, p.ImageBytes)
	return nil
}

// --- serve -------------------------------------------------------------------

type statusDoc struct {
	Trace        trace.Meta   `json:"trace"`
	Listen       string       `json:"listen"`
	BusID        string       `json:"bus_id"`
	AttachedPort *int         `json:"attached_port,omitempty"`
	Replay       replay.Stats `json:"replay"`
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	tracePath := fs.String("trace", "", "replay trace recorded from the real scanner (required)")
	listen := fs.String("listen", "127.0.0.1:3240", "USB/IP listen address")
	busID := fs.String("busid", "1-1", "exported bus id")
	statusAddr := fs.String("status-addr", "", "optional HTTP status endpoint, e.g. 127.0.0.1:3241")
	attach := fs.Bool("attach", false, "attach the device locally via vhci-hcd (root)")
	logLevel := fs.String("log-level", "info", "debug, info, warn, error")
	fs.Parse(args)

	log, err := newLogger(*logLevel)
	if err != nil {
		return err
	}
	if *tracePath == "" {
		return errors.New("serve: -trace is required (record one with scripts/capture-scan.sh)")
	}
	t, err := trace.Load(*tracePath)
	if err != nil {
		return err
	}
	resp, err := replay.New(t, log)
	if err != nil {
		return err
	}
	dev, err := sim.NewDevice(sim.DescriptorsFromTrace(t), resp, log)
	if err != nil {
		return err
	}
	srv, err := usbip.NewServer(dev, *busID, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	warnIfExposed(log, "USB/IP server", *listen)
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	log.Info("serving simulated scanner", "listen", ln.Addr().String(), "busid", *busID,
		"trace", *tracePath, "transfers", len(t.Transfers), "commands", resp.Stats().Groups)

	var attachedPort atomic.Int64
	attachedPort.Store(-1)
	if *statusAddr != "" {
		warnIfExposed(log, "status endpoint", *statusAddr)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
			doc := statusDoc{Trace: t.Meta, Listen: ln.Addr().String(), BusID: *busID, Replay: resp.Stats()}
			if p := int(attachedPort.Load()); p >= 0 {
				doc.AttachedPort = &p
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(doc)
		})
		hs := &http.Server{Addr: *statusAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("status endpoint failed", "err", err)
			}
		}()
		defer hs.Close()
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ctx, ln) }()

	var ctrl vhci.Controller
	if *attach {
		ctrl, err = vhci.Default()
		if err != nil {
			stop()
			<-errc
			return err
		}
		port, err := ctrl.Attach(ln.Addr().String(), *busID)
		if err != nil {
			stop()
			<-errc
			return err
		}
		attachedPort.Store(int64(port))
		log.Info("attached via vhci-hcd", "port", port)
	}

	<-ctx.Done()
	if p := int(attachedPort.Load()); p >= 0 {
		if err := ctrl.Detach(p); err != nil {
			log.Warn("detach failed", "err", err)
		} else {
			log.Info("detached", "port", p)
		}
	}
	stop()
	err = <-errc
	st := resp.Stats()
	log.Info("replay summary", "commands", st.Commands, "matched", st.Matched, "repeated", st.Repeated,
		"unmatched", st.Unmatched, "bytes_in", st.BytesIn)
	return err
}

// --- attach / detach / list --------------------------------------------------

func cmdAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	server := fs.String("server", "127.0.0.1:3240", "USB/IP server address")
	busID := fs.String("busid", "1-1", "bus id to import")
	fs.Parse(args)
	ctrl, err := vhci.Default()
	if err != nil {
		return err
	}
	port, err := ctrl.Attach(*server, *busID)
	if err != nil {
		return err
	}
	fmt.Println(port)
	return nil
}

func cmdDetach(args []string) error {
	fs := flag.NewFlagSet("detach", flag.ExitOnError)
	port := fs.Int("port", -1, "vhci port printed by attach")
	fs.Parse(args)
	ctrl, err := vhci.Default()
	if err != nil {
		return err
	}
	return ctrl.Detach(*port)
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	server := fs.String("server", "127.0.0.1:3240", "USB/IP server address")
	fs.Parse(args)
	conn, err := net.DialTimeout("tcp", *server, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	devs, err := usbip.DevList(conn)
	if err != nil {
		return err
	}
	for _, d := range devs {
		fmt.Printf("%s  %04x:%04x  bus=%d dev=%d speed=%d interfaces=%d\n",
			d.BusID, d.VendorID, d.ProductID, d.BusNum, d.DevNum, d.Speed, len(d.Interfaces))
	}
	return nil
}
