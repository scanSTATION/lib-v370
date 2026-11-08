# The vendor app and the V370 — knowledge base

Research notes from building scanSTATION (2026-11): how the vendor app works
with the V370, its CLI mode and `.SF2` settings format, the Flatpak, and how
the own USB driver was developed. Used by package `vendorapp` and the
scanSTATION `epsonscan2` driver.


> All facts below were **verified on the live system** (Arch Linux, user `mrw`)
> on 2026-08-05. Earlier guesses that turned out wrong are corrected here.

## 1. Hardware & connection (verified)

| Field | Value | How verified |
|-------|-------|--------------|
| Model | Perfection V37/V370 (GT-S640 / F740 family) | `lsusb` + `epsonscan2 -l` |
| USB vendor:product | `04b8:014a` (vendor ID 04b8) | `lsusb` |
| USB location | `libusb:003:024` (Bus 003 Device 024) | `lsusb` / `sane-find-scanner` |
| Connection | USB, direct (through a USB-C dock hub) | `lsusb` tree |
| OS | Arch Linux | `pacman` output |

> NOTE: An earlier note guessed `04b4:2008` (Cypress). That was **wrong**.
> The real ID is `04b8:014a`.

## 2. What works today (verified)

- The **the vendor app** application runs as a **Flatpak** and scans successfully.
- Flatpak details (verified via `flatpak info` / `flatpak list`):

| Field | Value |
|-------|-------|
| App ID | `net.epson.epsonscan2` |
| Version | 6.7.82.0 |
| Branch | `stable` |
| Arch | `x86_64` |
| Installation | `system` |
| Origin | `flathub` (Collection `org.flathub.Stable`) |
| Ref | `app/net.epson.epsonscan2/x86_64/stable` |
| Commit | `3f4d56b5b603158af5ae970554812abb52b8dcc0b98f1034f58a4bd3298160bf` |
| Installed size | 49.4 MB |
| Runtime | `org.kde.Platform/x86_64/5.15-25.08` |
| SDK | `org.kde.Sdk/x86_64/5.15-25.08` |
| License | LGPL-2.1 AND proprietary (vendor EULA) |

> NOTE: An earlier note guessed a different app ID. The real ID is
> `net.epson.epsonscan2`.

## 3. Flatpak manifest analysis (verified — read `manifest.json`)

The deployed manifest at
`/var/lib/flatpak/app/net.epson.epsonscan2/current/active/files/manifest.json`
reveals exactly what the app bundles and how:

- **Runtime:** `org.kde.Platform` `5.15-25.08` (KDE SDK build).
- **finish-args** (sandbox permissions):
  - `--device=all` → full USB access (this is how it reaches the scanner)
  - `--share=ipc`, `--socket=fallback-x11`, `--socket=wayland` → GUI display
  - `--share=network`, `--system-talk-name=org.freedesktop.Avahi` → network scanner discovery
  - `--filesystem=xdg-pictures`, `--filesystem=xdg-desktop`, `--filesystem=xdg-documents` → where scans can be saved
  - `--env=LD_LIBRARY_PATH=/app/lib/x86_64-linux-gnu/epsonscan2-ocr` → OCR libs
  - `--persist=.epsonscan2` → config persists at `~/.var/app/net.epson.epsonscan2/.epsonscan2`
- **Bundled modules:**
  1. `avahi` (open-source, mDNS for network scanners)
  2. `psmisc` (`killall`)
  3. `epsonscan2-non-free` — the **proprietary** driver plugin, unpacked from
     the vendor's official `epsonscan2-bundle-6.7.82.0.x86_64.deb.tar.gz`
     (sha256 `34e728ab49e245b2a7cff8a0d1cae91c306ce8225e42ddb50eabdb22d4b06b9c`,
     from `download-center.epson.com`)
  4. `boost` (1.68.0, atomic lib)
  5. `epsonscan2` — the **open-source** GUI, built from
     `epsonscan2-6.7.82.0-1.src.tar.gz`
     (sha256 `1dd790dd69dfa72c9aaae78b30196803a8b80117e1561c1675bca9ea37d275f2`)

### Bundled driver files (the actual V370 driver — verified via `find`)

Deployed under `…/files/lib/x86_64-linux-gnu/epsonscan2/`:

| File | Role |
|------|------|
| `libsane-epsonscan2.so` | **SANE backend** (makes `scanimage`-style access possible) |
| `libepsonscan2.so` | Core engine of the vendor app |
| `libepsonscansdk.so` | SDK layer |
| `libes2command.so` | ES2 scanner command layer |
| `libes2fileformat.so` | Output file-format handling |
| `libcommonutility.so` | Utilities |
| `libexec/libiscan-plugin-perfection-v370.so` | **The actual V370 interpreter plugin** |
| `libexec/libcnx-esci-es0282.so` | ESCI connection lib (ES-0282 command level) |
| `libexec/libesci-interpreter-perfection-v330.so` | V330 interpreter (shared ESCI family) |
| `libexec/libesintA1.so` | ES interpreter A1 |
| `non-free-exec/{ddeplugin,dtrplugin,es2intif,es2netif,fitplugin}` | Non-free helper plugins |
| `detectalert` | Status/alert helper |
| `epsonscan2-ocr/libocr-*.so` | OCR engine libraries |

The main GUI binary is `…/files/bin/epsonscan2` (~1.7 MB ELF).

## 4. THE KEY DISCOVERY — `epsonscan2` has a built-in CLI mode (verified)

Running `epsonscan2 --help` inside the Flatpak prints a **command-line mode**:

```
=== the vendor app Command line Mode Help===
Usage: epsonscan2 [OPTION]...

-h, --help            display this help message and exit
-l, --list            show available scanner devices
-i, --set-ip          set manual ip address
                      e.g.) $ epsonscan2 --set-ip 192.168.xx.x
-s, --scan [device ID or IP address](optional) [setting file]
                      start scan with user setting file.
                      e.g.1) $ epsonscan2 --scan DS-XX0 UserSettingFile.SF2
                      e.g.2) $ epsonscan2 --scan DS-XX0:001:004 UserSettingFile.SF2
-c, --create          create a default user setting file in current directory
-e, --edit [full path of setting file] : create a user setting file
                      e.g.) $ epsonscan2 --edit /home/user1/UserSettingFile.SF2
-gs, --get-status     Get device status.
```

This means the scanner **can be driven headlessly** — no GUI required — using
the bundled driver, invoked through the Flatpak sandbox.

### Device listing (verified — `epsonscan2 -l`)

```
=== List of available devices ==
device ID :Perfection V37/V370/GT-S640/F740
ModelID:ES00DD
```

- **Device ID string:** `Perfection V37/V370/GT-S640/F740`
- **ModelID:** `ES00DD`
- (Note: the ESCI connection lib is `libcnx-esci-es0282.so`; the model reports
  `ES00DD`.)

### Headless scan workflow (the native-without-UI method)

```bash
# 1. List devices (get the device ID)
flatpak run --command=epsonscan2 net.epson.epsonscan2 -l

# 2. Create a default settings file in the current dir
flatpak run --command=epsonscan2 net.epson.epsonscan2 -c
#   -> creates UserSettingFile.SF2 (JSON)

# 3. Edit UserSettingFile.SF2 (see §5 for the format) to set 300 DPI / A4 / color / output

# 4. Scan (device ID is optional; give it explicitly to be safe)
flatpak run --command=epsonscan2 net.epson.epsonscan2 \
  -s "Perfection V37/V370/GT-S640/F740" /path/to/setting.SF2

# Get device status (2-way comms check). The device ID is optional and must
# be ONE argument (quote it). Prints nothing when the scanner is fine —
# see §14 "--get-status semantics".
flatpak run --command=epsonscan2 net.epson.epsonscan2 --get-status "Perfection V37/V370/GT-S640/F740"
```

Filesystem notes: the sandbox exposes `xdg-documents`, `xdg-desktop`,
`xdg-pictures`. A host path like `/home/mrw/Documents/foo.SF2` is visible at the
same path inside the sandbox. Output location is controlled by the `Folder` enum
and `UserDefinePath` in the `.SF2` file (see §5).

## 5. The `.SF2` setting file format (verified — it's JSON)

The files in `~/Documents/` (`epsonscan2-*.SF2`, `test_fmt0.SF2`, `test_fmt1.SF2`)
are **JSON scan-setting presets**, NOT images and NOT the vendor-internal binaries.

Structure:
```json
{
  "Preset": [
    { "0": [ { <setting>: {"int": <n>} | {"string": "<s>"}, ... } ] }
  ]
}
```

### Verified relevant keys (from `test_fmt0.SF2`, which is already 300 DPI A4)

| Key | Type | Value in test_fmt0 | Meaning |
|-----|------|--------------------|---------|
| `Resolution` | int | `300` | DPI |
| `ScanAreaWidth` | int | `2480` | width in px (210 mm × 300 dpi ≈ 2480) |
| `ScanAreaHeight` | int | `3508` | height in px (297 mm × 300 dpi ≈ 3508) |
| `FixedDocumentSize` | int | `3` | document-size enum (3 = A4) |
| `FunctionalUnit` | int | `0` | 0 = Flatbed (1 = ADF, presumably) |
| `ColorType` | int | `0` | color mode enum (exact mapping TBD) |
| `ImageFormat` | int | `0` | output format enum (exact mapping TBD; test_fmt0 vs test_fmt1 differ here) |
| `Folder` | int | `2` | output-folder enum (2 = Documents, presumably) |
| `UserDefinePath` | string | `""` | custom output path (used when Folder = UserDefine) |
| `FileNamePrefix` | string | `"fmttest0"` | output filename prefix |
| `JpegQuality` | int | `85` | JPEG quality |
| `TransferCompression` | int | `1` | transfer compression |
| `Gamma` | int | `22` | gamma |
| `Brightness` | int | `0` | brightness offset |
| `Contrast` | int | `0` | contrast offset |
| `documentType` | int | `0` | document type |
| `PaperDeskew` | int | `1` | auto deskew on |
| `DoubleFeedDetection` | int | `1` | DFD on (ADF) |
| `Threshold` | int | `110` | line-art threshold |
| `Orientation` | int | `0` | orientation |
| `ContinuousAutoFeedingMode` | int | `0` | ADF continuous mode |
| `focusPosition` | int | `0` | focus position |

### Other keys seen (full list in the files)
`AFMMode`, `AFMTimeout`, `AddPages`, `AutoColorPixelLevel`, `AutoColorPixelType`,
`AutoCroppingInscribed`, `AutoSize`, `BackgroundColor`, `BackgroundRemoval`,
`BlankPageSkip`, `BlankPageSkipLevel`, `ColorType`, `DeficiencyCorrection`,
`DoubleFeedDetectionAreaLength/Min/Level`, `DropoutColor`, `DuplexType`,
`FileNameCounter`, `FileNameOverWrite`, `ImageOption`, `JpegQualityForJpeg/Pdf`,
`MultiTiffCompression/Enabled`, `NearDurationAlert`, `OFDEnabled`, `OverDurationAlert`,
`PDFAllPages`, `PDFSelectPage`, `PagesTobeScanned`, `PaperEndDetection(Memory)`,
`RemovePunchHole`, `ScanAreaOffsetX/Y`, `textEnhance*`, `thresholdAdjustment`,
`UserScanAreaWidth/Height`, `jpegProgressive`.

### `FixedDocumentSize` — measured on the real scanner (2026-11-07)
Presets **override** `ScanAreaWidth/Height`; only non-preset values use the
explicit area. Measured with 75 DPI scans and a 100×50 mm area set:

| Value | Scanned area (mm) | Meaning |
|---|---|---|
| 0, 1, -1, 99 | 100 × 50 | explicit area (`ScanAreaWidth/Height`) ← **we always send 0** |
| 2, 11 | 215.7 × 297.0 | whole glass |
| 3 | 209.6 × 296.7 | A4 |
| 4 | 215.7 × 209.6 | unknown |
| 5 / 6 | 147.7 × 209.6 / landscape | A5 |
| 7 / 8 | 104.6 × 147.7 / landscape | A6 |
| 9 / 10 | 51.8 × 73.5 / landscape | A8 |
| 12 | 215.7 × 256.4 | unknown |

Until 2026-11-07 the code sent 4 for Letter, 5 for Legal and 7 for "user"
(guessed values) → those scans came out 216×210 mm, A5 and A6.
`UserScanAreaWidth/Height` have no visible effect. Legal (355.6 mm) does not
fit the 297 mm glass and is now rejected.

### A4 at 300 DPI reference values
- Width px = round(210 mm / 25.4 × 300) = **2480**
- Height px = round(297 mm / 25.4 × 300) = **3508**
- Raw RGB size ≈ 2480 × 3508 × 3 ≈ 26 MB; JPEG much smaller.

## 6. Native SANE situation (verified — V370 is NOT natively supported)

| Item | Status |
|------|--------|
| `sane` package | installed `1.4.0-4` |
| `sane-airscan` | installed `0.99.38-1` (network eSCL/WSD only — **not** for USB V370) |
| `scanimage` / `sane-find-scanner` | present at `/usr/bin/` |
| `scanimage -L` result | only `v4l:/dev/video0` and `v4l:/dev/video2` (webcams) — **no vendor** |
| Native `libsane-epsonscan2.so` | **NOT installed** |
| `/etc/sane.d/dll.conf` | enables `epson2`, `epsonds`, `escl`, etc. — **not** `vendorapp` |
| `/etc/sane.d/epson2.conf` | someone added `usb 0x04b8 014a` (modified Jan 25 2026) — but epson2 still doesn't claim the V370 |
| `/usr/lib/udev/rules.d/65-sane.rules` | does **not** list `04b8:014a` (no udev match for the V370) |
| AUR helper (`yay`/`paru`) | **not installed** |
| `utsushi` / `imagescan` | not installed |
| `imagemagick` | installed `7.1.2.29-1` (good for PNM→PNG conversion) |

### Why native SANE doesn't see the V370
`sane-find-scanner` **does** detect the scanner at `libusb:003:024`
(`vendor=0x04b8`, `product=0x014a` "… Perfection V37/V370"), proving the
USB layer works. But no installed SANE backend speaks the V370's protocol
(ESCI ES-0282 / ES00DD). The V370 requires the vendor's **proprietary** interpreter
(`libiscan-plugin-perfection-v370.so` + `libcnx-esci-es0282.so`), which only
ships inside the `epsonscan2-non-free` package — i.e. the Flatpak, or the AUR
`epsonscan2` / `epsonscan2-non-free-plugin` packages built from the vendor's `.deb`.

### User / permissions (verified)
```
id: uid=1000(mrw) ... groups=...,96(scanner),963(docker),990(lp),998(wheel),1001(plugdev)
```
User `mrw` is already in the `scanner`, `lp`, and `plugdev` groups.
`/etc/udev/rules.d/` is empty (no custom rules). The standard `65-sane.rules`
doesn't match the V370, so for a *native* install you'd add:
```
# /etc/udev/rules.d/55-v370.rules
SUBSYSTEM=="usb", ATTRS{idVendor}=="04b8", ATTRS{idProduct}=="014a", MODE="0666", GROUP="scanner"
```
(reload: `sudo udevadm control --reload-rules && sudo udevadm trigger`)
—but this is only needed for a native backend; the Flatpak uses `--device=all`.

## 7. Paths that matter

| Path | What |
|------|------|
| `/var/lib/flatpak/app/net.epson.epsonscan2/current/active/files/` | deployed app tree |
| `…/files/bin/epsonscan2` | GUI + CLI binary |
| `…/files/lib/x86_64-linux-gnu/epsonscan2/` | driver libs + `libsane-epsonscan2.so` |
| `…/files/lib/x86_64-linux-gnu/epsonscan2/libexec/` | ESCI plugins incl. V370 |
| `…/files/manifest.json` | flatpak build manifest |
| `~/.var/app/net.epson.epsonscan2/` | persisted app data (config, cache, `.epsonscan2`) |
| `~/.var/app/net.epson.epsonscan2/.epsonscan2/` | The vendor app config/presets |
| `~/Documents/*.SF2` | scan-setting preset files (JSON) |

## 8. Flatpak backup / offline export (so the program is always available)

The app is **proprietary** and could be pulled from Flathub / the vendor's servers at
any time. Back it up.

### Installed runtimes the app depends on (verified via `flatpak list --runtime`)

| Runtime | Branch | Size |
|---------|--------|------|
| org.kde.Platform | 5.15-25.08 | 974.3 MB (app's direct runtime) |
| org.freedesktop.Platform | 25.08 | 659.3 MB |
| org.freedesktop.Platform.GL.default | 25.08 | 457.0 MB |
| org.freedesktop.Platform.GL.default | 25.08-extra | 457.1 MB |
| org.freedesktop.Platform.VAAPI.Intel | 25.08 | 45.9 MB |
| org.freedesktop.Platform.codecs-extra | 25.08-extra | 43.4 MB |
| org.gtk.Gtk3theme.Breeze | 3.22 | 649.7 kB |
| (org.kde.Platform 6.10 also installed but NOT used by this app) | | |

### Method A — `flatpak build-bundle` (single .flatpak files)

```bash
mkdir -p ~/backups/vendorapp

# ESSENTIAL: the proprietary app itself
sudo flatpak build-bundle /var/lib/flatpak/repo \
  ~/backups/vendorapp/net.epson.epsonscan2-6.7.82.0.flatpak \
  net.epson.epsonscan2 stable

# RECOMMENDED: runtimes for fully-offline restore
sudo flatpak build-bundle --runtime /var/lib/flatpak/repo \
  ~/backups/vendorapp/org.kde.Platform-5.15-25.08.flatpak \
  org.kde.Platform 5.15-25.08
sudo flatpak build-bundle --runtime /var/lib/flatpak/repo \
  ~/backups/vendorapp/org.freedesktop.Platform-25.08.flatpak \
  org.freedesktop.Platform 25.08
sudo flatpak build-bundle --runtime /var/lib/flatpak/repo \
  ~/backups/vendorapp/org.freedesktop.Platform.GL.default-25.08.flatpak \
  org.freedesktop.Platform.GL.default 25.08
sudo flatpak build-bundle --runtime /var/lib/flatpak/repo \
  ~/backups/vendorapp/org.freedesktop.Platform.VAAPI.Intel-25.08.flatpak \
  org.freedesktop.Platform.VAAPI.Intel 25.08
sudo flatpak build-bundle --runtime /var/lib/flatpak/repo \
  ~/backups/vendorapp/org.freedesktop.Platform.codecs-extra-25.08-extra.flatpak \
  org.freedesktop.Platform.codecs-extra 25.08-extra

# User data (settings/presets)
tar -czf ~/backups/vendorapp/userdata.tar.gz -C ~/.var/app net.epson.epsonscan2

# Record checksums + commit
sha256sum ~/backups/vendorapp/*.flatpak > ~/backups/vendorapp/sha256sums.txt
# working commit: 3f4d56b5b603158af5ae970554812abb52b8dcc0b98f1034f58a4bd3298160bf
```

**Restore (runtimes first, then app — order matters):**
```bash
sudo flatpak install ~/backups/vendorapp/org.freedesktop.Platform-25.08.flatpak
sudo flatpak install ~/backups/vendorapp/org.freedesktop.Platform.GL.default-25.08.flatpak
sudo flatpak install ~/backups/vendorapp/org.freedesktop.Platform.VAAPI.Intel-25.08.flatpak
sudo flatpak install ~/backups/vendorapp/org.freedesktop.Platform.codecs-extra-25.08-extra.flatpak
sudo flatpak install ~/backups/vendorapp/org.kde.Platform-5.15-25.08.flatpak
sudo flatpak install ~/backups/vendorapp/net.epson.epsonscan2-6.7.82.0.flatpak
mkdir -p ~/.var/app && tar -xzf ~/backups/vendorapp/userdata.tar.gz -C ~/.var/app
```

### Method B — `flatpak create-usb` (one-shot, grabs the WHOLE dep tree)

Destination must be a **mount point** (USB stick, or a loopback image):
```bash
truncate -s 3G ~/backups/vendorapp-offline.img
mkfs.ext4 ~/backups/vendorapp-offline.img
mkdir -p /mnt/backup
sudo mount -o loop ~/backups/vendorapp-offline.img /mnt/backup
flatpak create-usb /mnt/backup net.epson.epsonscan2
sudo umount /mnt/backup
# Restore: mount the image, then:
#   flatpak install /mnt/backup/net.epson.epsonscan2.flatpakref
```

### Pin against unwanted updates
```bash
flatpak mask net.epson.epsonscan2   # prevents the app from being auto-updated
```

### Also grab the original the vendor source archives (vendor URLs can vanish)
- `epsonscan2-bundle-6.7.82.0.x86_64.deb.tar.gz` — sha256 `34e728ab49e245b2a7cff8a0d1cae91c306ce8225e42ddb50eabdb22d4b06b9c`
- `epsonscan2-6.7.82.0-1.src.tar.gz` — sha256 `1dd790dd69dfa72c9aaae78b30196803a8b80117e1561c1675bca9ea37d275f2`

These let you rebuild a native (non-Flatpak) install later via the AUR
`epsonscan2` / `epsonscan2-non-free-plugin` packages.

## 9. Direct-communication analysis (scan WITHOUT the vendor app — verified 2026-08-05)

Question: what is required to scan with no vendor app at all — direct USB
or our own Go app? Findings below are all verified on the live system.

### USB transport (verified via `lsusb -v`)
- Device `04b8:014a`, USB 2.0 High Speed, Vendor Specific class (0xFF).
- **1 interface, 2 bulk endpoints:**
  - `EP 0x02 OUT` (512-byte) — host → scanner commands
  - `EP 0x81 IN`  (512-byte) — scanner → host responses + image data
- No kernel driver claims it → libusb can own it directly.

### Firmware upload is MANDATORY (verified via `epsonscan2.rules` line 47)
```
ATTRS{idProduct}=="014a", ENV{epsonscan2_driver}="esci", ENV{firmware_file}="esfwdd.bin"
```
- Scanner is non-functional until `esfwdd.bin` (65 793 bytes) is uploaded over
  USB on each power-up. **Confirmed by USB capture on 2026-11-07** (§14,
  "Power-on capture"): sent on the first access after power-on, not after a
  USB-only replug; payload = file bytes 256..65791 + byte-sum checksum.
- Located in Flatpak at `files/share/epsonscan2/esfwdd.bin` (also `esfw0282.bin`,
  `esfwA1.bin`, `esfwad.bin`, `esfweb.bin`, `esfw010c.bin` — one per device family).
- It is a proprietary **data blob** (redistributable), not code we execute.

### ESCI protocol (the core work)
- The V370 speaks **ESC/I** at the **ES-0282** command level; device reports
  ModelID `ES00DD`.
- Commands = short frames on EP 0x02 (ESC `0x1b` + letter + params); responses on
  EP 0x81 with a 4-byte status header (`STX`+status+flags) before payload.
- Scan session: identity query → firmware load → set params (resolution, color
  mode, scan area, bit depth, gamma) → start scan → read image data line-by-line
  (ESCI IMG format: per-line header + pixel bytes + footer) → cancel/end.
- Public references: the vendor's ESC/I reference manual; SANE `epson2` backend source;
  `utsushi` source; **our own USB capture** (the ground truth).

### Proprietary plugin does NOT touch USB (verified via `readelf -d` / `nm -D`)
- `libiscan-plugin-perfection-v370.so` exports `interpreter_ctor/_dtor/
  _reader/_writer` (standard utsushi/iscan plugin interface) and NEEDED only
  `libstdc++/libm/libc/libgcc_s` — **no USB library**.
- It only generates/parses ESCI command buffers. USB transport is entirely the
  host's job → we can replace it with our own (gousb).
- `libsane-epsonscan2.so` exports the full standard SANE API
  (`sane_epsonscan2_init/open/start/read/...`) and dlopens the device plugin.
- `libes2command.so` has a `CESScanner` C++ class + `ModelInfo` behavior flags
  + `stESRect/stESSize` scan-geometry structs.

### Device capabilities (verified — `Resources/Models/ES00DD/WW/ES00DD.json`)
| Field | Value |
|---|---|
| ESModelID / ESProductID | `00DD` / `014A` |
| ESProductName | GT-S640 (V37/V370 family) |
| ESCommandType / ESKind | 3 / 12 (ESCI) |
| ESFlatbedType | 10 (flatbed, **no ADF**) |
| ESBitDepth | 19 |
| ESResolution | min 50 / max 6400 / optical 4800 |
| ESGamma | 2.2 |
| ICC profiles | PERV370R.icc (reflective), PERV370F.icc (film) |
| ESInterpreterName | EsintDD.dll → libiscan-plugin-perfection-v370.so |
- Shared ESCI-level table: `ES0282/ALL/ES0282.json` (ModelID 0282, vender 04B8,
  res 50–4800, interpreter ESINT00/esimp2).
- `ES00DD.dti` / `ES0282.dti` are binary capability descriptors (the JSON is the
  readable equivalent). Resolution step tables: `Resources/Tables/ESResolution0*.json`
  (~180 files); V370 uses 0135/0137/0141/0143 (main), 0132/0137/0141/0142 (draft).
- `esfwdd.bin` and all `esfw*.bin` are 65 793 bytes each.

### Toolchain available (verified)
- Go 1.26.5, libusb 1.0.30, **tshark + wireshark + dumpcap** installed,
  `usbmon` kernel module available (not loaded).
- `gousb` NOT in module cache (needs `go get`; network available).
- The project (private development repository) already had
  Flatpak-wrapper code (`cmd/scan`, `internal/runner`, `internal/scanconfig`).
- `epsonscan2_6-7-82-0.tar.gz` present is the **Flatpak bundle** (backup), NOT the
  C++ source. The open-source source (`epsonscan2-6.7.82.0-1.src.tar.gz`) URL is
  in the Flatpak manifest if needed for Tier B.

### Three implementation tiers
- **A — Pure Go direct USB + ESCI** (only proprietary dep = `esfwdd.bin` data).
  Target. Most independent, most work.
- **B — Hybrid Go + plugin (cgo)**: our USB transport + load
  `libiscan-plugin-perfection-v370.so` for command generation. Fallback.
- **C — Native SANE (iscan AUR epkowa) + Go SANE client**: easiest, needs AUR,
  not "direct".

### Plan
See **`plan-direct.md`** for the full phased plan (Phase 0: USB capture →
Phase 1: USB+firmware+identity → Phase 2: first image by replaying captured
bytes → Phase 3: generalized ESCI engine → Phase 4: Docker + integration tests).

## 10. Open items / next steps (updated 2026-11-07)

Done since the original list:
- Headless scans work end to end (CLI, REST API, multi-page PDF);
  `Folder=2` writes to `~/Desktop`, enum values are documented in
  `internal/scanconfig`.
- Real USB captures exist (`testdata/traces/`), the simulator replays them
  byte-identically, firmware upload sequence decoded (§14).

Open:
1. `plan-direct.md` Phase 0.4/0.5: annotate the remaining ESC/I commands of
   the captures (identity, parameters, start, image readout, end).
2. Capture more scenarios for regression tests (PDF, grayscale, multi-page)
   with `scripts/capture-scan.sh -n NAME -- ARGS`.
3. Native SANE wiring (optional, fragile — the Flatpak CLI path is preferred).

## 11. Network isolation — Flatpak internet access blocked (2026-08-05)

The Flatpak manifest grants `--share=network` and
`--system-talk-name=org.freedesktop.Avahi` for network scanner discovery
(Avahi/mDNS). The V370 is a **USB scanner** (`04b8:014a`), so network access
is unnecessary and was removed from the Go application's execution path.

**Implementation:**
- `runner.Config.NoNetwork` (default `true`) adds `--unshare=network` to every
  `flatpak run` command. This strips the network namespace from the sandbox for
  that invocation — no outbound or inbound network I/O is possible.
- The CLI flag `--allow-network` (default `false`) is the escape hatch for the
  rare case where a network scanner is used; it sets `NoNetwork = false`.
- Native mode is unaffected (no sandbox involved).

This is per-invocation isolation: it applies to every scan triggered through
`scanstation` and does not depend on any system-wide Flatpak override.

## 12. JPEG quality + multi-page scanning (2026-08-06)

### JPEG quality

Previously hardcoded at 85 for all three SF2 parameters (`JpegQuality`,
`JpegQualityForJpeg`, `JpegQualityForPdf`). Now configurable via the
`--quality` flag (1–100, default 85). Set to 100 for maximum quality.
The `JpegQuality` field on `scanconfig.ScanRequest` feeds all three SF2 keys.

### Multi-page scanning

The V370 is a flatbed scanner without an ADF, so multi-page scanning requires
user interaction: scan one page, place the next page on the glass, scan again.

**Implementation:** The `--multi` flag enables an interactive loop:
1. Each page is scanned as a single-page PDF via `epsonscan2 --scan`
   (`ImageFormat = 6`).
2. After each scan, the user is prompted with a single keypress (no Enter
   needed — terminal raw mode via `stty raw -echo`): `n` = next page,
   `e` = end session.
3. On `e`, all individual page PDFs are merged into a single multi-page PDF
   using **`pdfunite`** (poppler-utils). Individual pages are cleaned up
   unless `--keep-pages` is set.

**Additional flags:**
- `--timestamp`: appends `_<unix_timestamp>` to the output filename prefix.
- `--keep-pages`: retains individual page PDFs in the output directory.
- `--multi` always outputs PDF regardless of `--format`; a warning is printed
  if `--format` is set to something other than `pdf`.

**No Go dependencies added.** `pdfunite` is a system tool (same pattern as
`flatpak`). Single keypress uses `stty` (system command), not `golang.org/x/term`.

## 13. REST API server (2026-08-06)

A REST HTTP server (today `scanstation-server`) exposes the scanner via JSON
endpoints. Uses Go 1.22+ `http.ServeMux` method patterns and path wildcards —
no router framework dependency.

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/health` | Health check |
| GET | `/api/devices` | List scanner devices |
| GET | `/api/status` | Get scanner status |
| POST | `/api/scan` | Single-page scan |
| POST | `/api/scan/multi` | Start multi-page session (scans page 1) |
| POST | `/api/scan/multi/{id}/next` | Scan next page |
| POST | `/api/scan/multi/{id}/finish` | Combine pages into one PDF |
| GET | `/api/scan/multi/{id}` | Session status |
| DELETE | `/api/scan/multi/{id}` | Cancel session (cleanup) |
| GET | `/api/files/{filename}` | Download a scanned file |

### Multi-page flow

1. `POST /api/scan/multi` with `ScanParams` JSON → scans page 1, returns
   `{session_id, page, status}`.
2. `POST /api/scan/multi/{id}/next` → scans next page, returns
   `{session_id, page, status}`.
3. Repeat step 2 for each additional page.
4. `POST /api/scan/multi/{id}/finish` → combines all pages via `pdfunite`,
   returns `{file, filename, download_url, pages}`.

### Key design points

- **Scanner exclusivity**: `sync.Mutex` held only during the actual
  `epsonscan2 --scan` call. Between multi-page scans, the scanner is free.
- **Sessions in-memory**: `SessionStore` with `map[string]*Session`. No TTL;
  `DELETE` endpoint allows explicit cleanup.
- **Multi-page always PDF**: warning returned if request specifies a
  different format.
- **Path traversal prevention**: `GET /api/files/{filename}` uses
  `filepath.Base()` to strip directory components.
- **No authentication**: suitable for local/network use.
- **Package**: `internal/api/` with `types.go`, `session.go`, `server.go`,
  `handlers.go`.

## 14. USB simulator + integration tests (2026-11-07)

Goal: develop and test without the physical V370 attached.

### Decisions
- **Capture first**: the simulator only replays what the real scanner did
  (no guessed ESC/I model). Record once with `scripts/capture-scan.sh` on the
  USB host; the trace goes to `testdata/traces/<name>.jsonl.gz`.
- **USB/IP**: `v370-usbsim serve -attach` exports the simulated device and
  attaches it through the kernel's `vhci-hcd` (sysfs `attach` file, no `usbip`
  tool needed). The unmodified `epsonscan2` then sees a real `04b8:014a`.
- **In-process transport**: `usbsim.NewTransport` implements `usb.Transport`
  for the future direct Go driver (`plan-direct.md`), no kernel/root needed.

### Replay model (`internal/usbsim/replay`)
Each host *command* (bulk OUT payload, or class/vendor control request) owns
the IN data the device sent until the next command. Lookup is by exact bytes;
repeated commands (status polls, ACKs, firmware chunks) return their recorded
responses in order, then repeat the last one. Standard requests
(GET_DESCRIPTOR, SET_CONFIGURATION, …) are answered by `usbsim.Device` from
the captured descriptors. Unknown commands are divergences: logged, counted,
visible at `GET /status` of `serve -status-addr`, and get no response.
Consequence: replay is exact only for the **same scenario** as the capture
(same `scanstation` arguments). Capture one trace per scenario you want to test.

### Capture caveats
- Capture with classic pcap (`dumpcap -F pcap` / `tcpdump`), link type 220.
- The trace contains the scanned image — scan a blank page.
- Power-cycle the scanner during capture (script prompts) so the firmware
  upload (`esfwdd.bin`) and enumeration descriptors are recorded.
- usbmon's binary interface limits the captured bytes per URB (~240 KB with
  the maximum ring size). `v370-usbsim convert` refuses truncated transfers.
  If that happens, a USB/IP *recording proxy* (usbip-host on the real device
  ↔ v370-usbsim ↔ vhci) would capture full payloads — not built yet.

### Test tiers
| Tier | What runs | Needs | Command |
|---|---|---|---|
| unit | all packages, `-race` | Go (gcc for race) | `docker compose -f docker-compose.test.yml run --rm unit` |
| 1 | real CLI + REST server, `vendorapp-fake` | nothing special | `go test -tags integration ./integration/` or `docker compose -f docker-compose.test.yml run --rm tests` |
| 2 | real `epsonscan2` → vhci → simulator | root, `vhci-hcd`, the vendor app, a trace | `scripts/hwsim-test.sh testdata/traces/<name>.jsonl.gz` |

`vendorapp-fake` (`internal/fakescan`) parses the generated SF2 and writes a
real JPEG/PDF/raw of the requested pixel size; `VENDORAPP_FAKE_LOG` records
each call, `VENDORAPP_FAKE_CONTROL` injects failures.

### Fixes found by the integration tests
- `prefix` (CLI/API) was not validated: `../x` wrote outside the output
  directory, `*` was used as a glob. Now rejected (`scanconfig.ValidatePrefix`).
- API: out-of-range `dpi`/`quality`/user size returned 500 after a scan
  attempt; now 400 before scanning (`scanconfig.Validate`).
- Dockerfile: `groupadd scanner` failed because `sane-utils` already creates
  the group, so the image never built.

### First real capture (2026-11-07, USB host, the vendor app 6.7.82.0 Flatpak)
- Scenario `--dpi 300 --size a4 --format jpeg`, physical replug during the
  capture, page on the glass. Trace: `testdata/traces/a4-300dpi-jpeg.jsonl.gz`
  (24 MB), reference output `a4-300dpi-jpeg.expected.jpg` (2480×3507).
- 2706 usbmon events, 0 dropped, **0 truncated** (largest IN transfers fit
  the usbmon limit), 1313 transfers, 345 host commands, 30.1 MB image data IN.
- No firmware upload in *this* capture: only the USB cable was replugged,
  and the scanner kept its firmware (see the power-on capture below).
- Commands start with `1b 03`, `1e xx`, `1b 13`, … (see `v370-usbsim inspect`);
  basis for the protocol analysis in `plan-direct.md` Phase 0.
- **Tier 2 passed**: real vendor app → vhci → simulator produced a
  **byte-identical** JPEG; 345/345 commands matched, 0 divergences. Replay
  takes ~15 s vs ~29 s on the real scanner (no lamp warm-up/carriage).
- Root without sudo password: `V370_ROOT=docker` (scripts/lib/asroot.sh)
  runs the root steps in a privileged container in the host namespaces.
  dumpcap refuses to write files when started that way, so the capture is
  written to stdout and redirected by the root shell.
- The real scanner does not need to be unplugged for Tier 2:
  `scripts/hwsim-test.sh TRACE --disable-real` sets `authorized=0` on it
  during the test and restores it afterwards.

### Power-on capture — firmware upload confirmed (2026-11-07)
Scanner powered off, unplugged, powered on, plugged in; capture started
before any the vendor app access. Trace `a4-75dpi-poweron` (75 DPI A4 JPEG).
- The upload **is** sent on the first access after power-on (§9 holds; it is
  *not* triggered by a USB-only replug, the scanner keeps its firmware then).
- Sequence (bulk EP 0x02 / 0x81):
  `1b 03` → `28 00` (status: firmware **not** loaded; `18 01` = loaded) ·
  `1b 06` → `06` (ACK) · `01 00 01 00` (4-byte header) ·
  **65 536 bytes = `esfwdd.bin[256:65792]`** (the 256-byte "EDLA" file
  header and the final file byte are not sent) · `af` = **checksum: byte sum
  of the payload mod 256** → `06` · `1b 16` → `06` · `80` …
- Clock command: every session sends `0b 00 <u32 LE>`, value = **hours since
  2024-01-01 00:00 UTC** (0x6199 = 24985 at 01:5x UTC, 24986 after 02:00).
  Replay matches it fuzzily (≤ 4 differing bytes, `replay.MaxFuzzyBytes`).
- Tier 2 passes on both traces with byte-identical output; the power-on trace
  replays the full firmware upload.

### More captures (2026-11-07, all `--no-replug`, 0 truncated)
| Trace | Notes |
|---|---|
| `a4-150dpi-color` | 13.6 MB pcap |
| `a4-300dpi-gray`, `a4-300dpi-lineart` | same USB traffic size as colour (30.4 MB): **the scanner always scans colour; gray/line-art conversion is done on the host** |
| `user-100x50mm-300dpi`, `user-50x50mm-600dpi`, `user-25x25mm-1200dpi` | custom areas (after the `FixedDocumentSize` fix), 3.9–5.1 MB each |
| `a6-{300,600,1200}dpi-userbug` | recorded before the fix: requested custom areas, scanner did A6; kept as valid A6 recordings |

### `--get-status` semantics (the vendor app 6.7.82.0, verified)
- Scanner OK: **prints nothing**, exit 0.
- Scanner unreachable: `ERROR : An unexpected error occurred. The vendor app
  will close.`, exit 255 (plus libusb warnings).
- Invalid options (e.g. the device ID split into two arguments by missing
  quotes): `ERROR : Option format invalid.`, but exit **0**.
- `runner.GetStatus` therefore parses the output: empty → ready, `ERROR` →
  error, other text → caution (not ready). CLI `--status` prints
  `Scanner status: …` (exit 1 if not ready); `GET /api/status` returns
  `{"ready":true,"status":"ready"}` or 503 on scanner errors.

## 15. Direct USB access without the vendor app (2026-11-07)

`internal/usb/usbfs` talks to the scanner through the kernel's usbfs
(`/dev/bus/usb/BBB/DDD` + `USBDEVFS_*` ioctls): pure Go, no cgo, no libusb,
no vendor binary. ioctl numbers are computed from the struct sizes
(64-bit: CONTROL `0xC0185500`, BULK `0xC0185502`, CLAIMINTERFACE
`0x8004550F`).

Verified:
- Through vhci-hcd against the simulator, the complete host side of the
  power-on trace (64 KiB firmware upload included) and of the 300 DPI trace
  replays with every answer byte-identical (~270 MB/s,
  `scripts/hwsim-test.sh TRACE --disable-real --run TestUsbfsReplay`).
- Real scanner (read-only, `go test -tags hardware ./integration/hardware/`):
  GET_DESCRIPTOR ok; `1b 03` → `18 01` (firmware loaded); `1b 13` →
  28 bytes: model `GT-F740` (Japanese model name), version `1.00`.
- Interface 0 has no kernel driver; claiming fails with EBUSY while the vendor
  software holds it (reported as "is the vendor app running?").
- Bulk IN uses 2 s usbfs timeouts in a loop until the context ends; a
  timed-out URB could drop a partial packet, hence the long slice.

### Protocol layer `internal/v370` (P2, 2026-11-07)
Session as implemented (all steps verified against the captures and the
real scanner):

| Step | Host → scanner | Scanner → host |
|---|---|---|
| status | `1b 03` | 2 bytes, see below |
| firmware (only if status has 0x20) | `1b 06` · `01 00 01 00` · 64 KiB payload · checksum | `06` after `1b 06` and after the checksum |
| restart after firmware | `1b 16` · `80` (then ~2.5 s pause) | `06`, `06` |
| open session | `1e 93` · `01` | `06`, `06` (~0.1–1 s) |
| clock | `1e 8e` · `0b 00 <u32 LE hours since 2024-01-01 UTC>` | `06` (~1 s), `06` |
| wait ready | `1b 03` every 80 ms | until `18 01` (≈ 8 s after power-on) |
| identity | `1b 13` | 28 bytes: vendor, model `GT-F740`, version `1.00` |
| register | `1e 8f` · `<u16 LE addr>` | `06`, then u32 LE |
| close session | `1e 93` · `00` | `06`, `06` |

Status byte 0 (inferred): 0x20 no firmware, 0x10 set once firmware runs,
0x02 warming up, 0x40 busy (moving/scanning); byte 1 = 01 with firmware.
Observed sequence after power-on: `28 00` → `1a 01` → `5a 01` → `1a 01` →
`18 01`; during a scan `58 01`.

Registers: `0x0010` = 120 (constant so far); **`0x0005` = scan counter**
(850 at 01:59, 880 after 30 more scans, unchanged by sessions without a scan).

Real-hardware results: `v370 info` opens a session in ~4 s when the
firmware is loaded; after a power cycle it uploads the firmware itself and
is ready after 13.6 s. Our upload was captured
(`testdata/traces/direct-poweron-open`) and is byte-identical to the vendor's
except for the clock value.

Replay detail: identical parameter bytes after different commands (`00`
after `1e 87` → 10 data bytes, after `1e 93` → ACK) are told apart by the
preceding command; recorded order still has priority, so hosts that poll
more or less often than the vendor app get the answers in recorded order.

Note: traces recorded before the size fix (A4 used preset 3) make the
current CLI ask the vendor app for a 1-row-larger area, so its replay shows 4
fuzzy parameters and a slightly different image; not a replay bug.

## 16. Protocol reference, registers, buttons (2026-11-07)

The consolidated, maintained protocol description is now
**[`docs/protocol.md`](docs/protocol.md)**: every command seen in the
captures with request/reply formats, status bits, registers (`0x0005` = scan
counter, `0x0010` = 120), the 42-byte scan parameter block (`1e 53` read /
`1e 57` write: resolution, offset, size in px, mode), table uploads, and the
button experiment. New findings go there first.

Highlights of this session:
- Native resolutions seen: 100, 200, 300, 600, 1200 dpi. The vendor app scans
  75 dpi at **100** and 150 dpi at **200** and scales on the host.
- Idle `1e 53` reports the scannable area: 848 × 1170 px at 100 dpi
  (215.4 × 297.2 mm).
- Buttons: not reported by any known command; the vendor's Linux GUI does not poll
  them for this model (`testdata/buttons/`).
- ⚠️ Danger: a command answered with ACK waits for a parameter; sending
  something else hangs the scanner (OUT transfers time out). A USB reset then
  makes it drop off the bus; only a power cycle recovers it. Closing the
  the vendor app window leaves the program running and holding the scanner.

## 17. First scans without the vendor app (2026-11-07)

`v370 scan -profile a4-100dpi-color|a4-300dpi-color` scanned the
real page with only our code (usbfs + `internal/v370` + a profile extracted
from the vendor's capture). Details, profile rules and the first look at the raw
image format: `docs/protocol.md` §8. Raw outputs: `testdata/direct/`.

## 18. Raw image decoding (P4, 2026-11-07)

Analysis before implementation (user request) established the format on all
recordings: block trailers, geometry in `1e 41`, RGB pixel-interleaved,
colour line offsets B 0 / G 2·dpi/100 / R 4·dpi/100, values before the vendor's
tone curve, horizontal crop offset not determinable with the grid page.
Decision: store **uncropped, channel-aligned, untouched** as PNG + JSON
(`v370 scan`). Details: `docs/protocol.md` §8, `PLAN.md` P4.

## 19. Backends — direct is the default (P8, 2026-11-07)

`internal/backend`: `Direct` (own driver), `the vendor` (the vendor app), `Auto`
(direct, the vendor fallback only for `ErrUnsupported`). User decision: direct is
the default, the vendor app the fallback or explicit choice (`--backend`).
Verified on the real scanner: CLI and API scan directly by default; a 75 dpi
API request fell back to the real vendor app Flatpak. Behaviour change for
default scans: uncropped, no vendor tone curve. See README "Backends".

## 20. Buttons work (2026-11-07)

`1e 85` → 1 byte: Printer 02, Email 03, PDF 04 (event, cleared on read).
Found by static analysis of the vendor's V370 interpreter plugin (research
subagent, `docs/research/buttons-report.md`), verified on the scanner.
The vendor's Linux software never polls buttons for ESC/I devices — that is why
none of our captures contained it. `1b 20`–`1b 22` are register writes in
this dialect (never send). Details: `docs/protocol.md` §7.

## 21. Scanner library `libv370` (git submodule, 2026-11-07)

Everything that talks to the scanner moved to the module
`github.com/mrw/libv370` in the submodule `libv370/` (own repository):
driver (`usb`, `usb/usbfs`, `v370`, `firmware`, `rawimg`, `output`),
high-level API (`libv370.OpenDevice`, `Device.Scan/ReadButton`), simulator
(`sim/...`), tools (`v370`, `v370-usbsim`), scripts, protocol
reference and traces. The app keeps the backends, the vendor app path, API,
CLI and raw captures. Firmware: the library contains no vendor file and loads
`./firmware/esfwdd.bin` relative to the working directory; this repository
keeps it at `firmware/esfwdd.bin`.

Firmware analysis (research subagent): `esfwdd.bin` is the scanner's complete
main firmware (S1C33 RISC code, own USB stack, all command handlers); only a
ROM loader runs before the upload. See `libv370/docs/research/firmware.md`.

