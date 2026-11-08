# lib-v370

Go library for the **V37/V370** flatbed scanner (Japanese name GT-F740,
USB `04b8:014a`), talking to it **directly over USB**: no vendor software,
no SANE, no libusb, no cgo. Everything here was reverse
engineered from USB captures and verified on the real scanner
([`docs/protocol.md`](docs/protocol.md)).

It is the V370 driver library of
[scanSTATION-server](https://github.com/scanSTATION/scanSTATION-server)
(included there as a git submodule, see [`scanstation/`](scanstation/README.md)),
but usable on its own.

**Compatibility:** Epson Perfection V37 / V370 (GT-F740). This project is
independent and not affiliated with or endorsed by the manufacturer; product
names are used only to state compatibility.

```go
import (
	"github.com/scanSTATION/lib-v370"
	"github.com/scanSTATION/lib-v370/output"
)

dev, err := libv370.OpenDevice(ctx, libv370.Options{}) // uploads the firmware after power-on
if err != nil { … }
defer dev.Close()

scan, err := dev.Scan(ctx, "a4-300dpi-color")          // see libv370.Profiles()
// scan.Image: uncropped, colour-aligned, untouched 8-bit RGB; scan.Raw: scanner stream
files, err := output.Write("scan", scan.Profile, scan.Raw,
	output.Options{Format: output.PNG}, output.Meta{})  // scan.png + scan.json

b, err := dev.ReadButton(ctx)                          // Printer / Email / PDF / Power (poll while idle)
// Options{NoSession: true}: Power keeps working; Scan opens a session only while scanning
```

## Firmware — not included

The scanner needs the vendor's `esfwdd.bin` after every power-on. It is
proprietary and **not part of this repository**. The library loads it from
**`./firmware/esfwdd.bin` relative to the working directory** of your
program (or from an explicit path). Obtain it from the vendor's own distribution;
every source is checked against pinned SHA-256 values:

```bash
scanstation/firmware.sh /path/to/your/app                      # automatic (as the scanSTATION Docker build does)
scripts/install-firmware.sh --url                              # where to download the vendor's bundle (browser only)
scripts/install-firmware.sh --bundle ~/Downloads/epsonscan2-bundle-6.7.82.0.x86_64.deb.tar.gz /path/to/your/app
scripts/install-firmware.sh /path/to/your/app                  # from an installed the vendor app Flatpak
scripts/install-firmware.sh --flathub /path/to/your/app        # The vendor app app only, official flatpak client, temp dir
scripts/install-firmware.sh --verify FILE                      # check a firmware file
```

`--auto` (used by `scanstation/firmware.sh`) tries the vendor's website (which
currently refuses scripted downloads), then files in `$FW_SOURCES`, an
installed Flatpak, and Flathub. Details: [`firmware/README.md`](firmware/README.md),
licence and safety analysis: [`docs/research/firmware-mod.md`](docs/research/firmware-mod.md).

## Capabilities

| Feature | Status |
|---|---|
| Firmware upload, session, status, identity, registers | ✅ verified |
| Scans with built-in profiles (`v370/profiles`): A4 colour 100/200/300/600 dpi; A5 (148×210 mm) 1200 dpi; 100×50 mm @300, 50×50 mm @600, 25×25 mm @1200 — all real optical resolutions | ✅ verified |
| Image decoding (block trailers, geometry, colour line alignment), PNG with dpi, lossless or JPEG PDF | ✅ verified |
| Panel buttons Printer / Email / PDF / Power (`1e 85`), with or without host session; press duration (`PressDuration`) | ✅ verified |
| Lamp measurement wait, late replies after lamp sleep | ✅ verified |
| The vendor app as an alternative (package `vendorapp`: `.SF2` settings + CLI runner) | ✅ verified |
| Own calibration: offsets and shading computed from every scan's own calibration reads ([`docs/research/calibration.md`](docs/research/calibration.md)) | ✅ tested against all recordings (within the measurement noise) |
| Arbitrary resolution/area | open (profiles replay recorded command sequences; see [`docs/research/improvements.md`](docs/research/improvements.md)) |
| A4 at 1200 dpi | not possible: exceeds the carriage travel (≈ 296 mm at 1200 dpi) |

Output is deliberately unprocessed: full scanned width (not cropped to the
requested area), no tone curve, no resampling, no deskew.

## Packages

| Package | Purpose |
|---|---|
| `libv370` | high-level API: `OpenDevice`, `Device.Scan/Status/ReadButton/Close`; openers `RealScanner`, `SimulatedScanner`, `SyntheticScanner` |
| `v370` | protocol: framing, status, identity, firmware upload, clock, session, registers, buttons, profiles |
| `firmware` | load/validate `esfwdd.bin` (`./firmware/esfwdd.bin`), `FromPayload` for tests |
| `rawimg` | decode the scanner's image stream; PNG with resolution |
| `output` | PNG/JPEG/PDF/raw + JSON sidecar |
| `usb`, `usb/usbfs` | transport interface; pure-Go Linux usbfs transport |
| `sim/synth` | **synthetic V370**: implements the protocol (firmware upload, profiles with a test pattern, buttons) — tests without hardware or recordings |
| `sim/...` | USB simulator: traces, replay of recordings, usbmon pcap converter, USB/IP server, vhci attach |
| `vendorapp`, `vendorapp/fake` | drive the vendor app (Flatpak/native); a stand-in binary for tests |

## Tools

| Command | Purpose |
|---|---|
| `cmd/v370` | `info`, `scan`, `decode`, `buttons [-once] [-no-session]`, `poll`, `mem` (read-only RAM read, e.g. live button state); `raw`/`reset` (exploration, need `-i-accept-the-risk`) |
| `cmd/v370-usbsim` | `convert` (pcap → trace), `inspect`, `profile` (trace → scan profile), `serve [-attach]`, `attach`, `detach`, `list` |
| `cmd/vendorapp-fake` | stand-in for the vendor app's `epsonscan2` binary (tests) |
| `scanstation/firmware.sh` | scanSTATION contract: fetch + verify the firmware |
| `scripts/install-firmware.sh` | firmware from the vendor's website / a browser-downloaded bundle / a Flatpak / Flathub, verified (self-test: `scripts/test-install-firmware.sh FW`) |
| `scripts/capture-scan.sh` | record a scan's USB traffic (usbmon) and convert it into a trace |
| `scripts/hwsim-test.sh` | run hwsim tests against the simulator attached via vhci-hcd |
| `scripts/vendorapp-setup-native.sh` | extract the vendor app from the Flatpak for native use |

Root steps (usbmon, vhci) use sudo, or `V370_ROOT=docker`
(`scripts/lib/asroot.sh`). USB access for users: `udev/60-v370.rules`.

## Tests

| Tier | Command | Needs |
|---|---|---|
| unit + synthetic scanner | `go test ./...` | nothing |
| replay of real recordings | `SCANSTATION_TRACES=/path/to/recordings go test ./...` | private recordings (not published: they contain scans and the firmware upload) |
| usbfs/protocol/scan through the kernel | `scripts/hwsim-test.sh $SCANSTATION_TRACES/TRACE.jsonl.gz --run TestUsbfsReplay\|TestDirectOpen\|TestDirectScan` | root (or `V370_ROOT=docker`), `vhci-hcd`, recordings |
| real scanner | `go test -tags hardware -count=1 -v ./integration/hardware/` | scanner connected, no vendor software running |

## Documentation

- [`docs/protocol.md`](docs/protocol.md) — protocol reference (commands, status, registers, parameter blocks, image format, buttons, never-send list)
- [`docs/vendorapp.md`](docs/vendorapp.md) — the vendor app: CLI mode, `.SF2` format, Flatpak, native setup
- [`docs/research/`](docs/research/) — how the button command was found, what the firmware is ([`firmware.md`](docs/research/firmware.md)), whether it can be modified ([`firmware-mod.md`](docs/research/firmware-mod.md)), the Power button / sessions / auto power-off ([`power-button.md`](docs/research/power-button.md)), the scan hang at the lamp measurement ([`scan-hang.md`](docs/research/scan-hang.md)), the per-scan calibration ([`calibration.md`](docs/research/calibration.md)), a full firmware map ([`firmware-map.md`](docs/research/firmware-map.md)), supported modes ([`supported-modes.md`](docs/research/supported-modes.md)) and improvements ([`improvements.md`](docs/research/improvements.md)), with sources
- [`v370/profiles/README.md`](v370/profiles/README.md) — built-in scan profiles

This repository contains no recorded scans and no files from the
manufacturer; analysis tools regenerate their listings from your own copy of
the firmware (git-ignored).
