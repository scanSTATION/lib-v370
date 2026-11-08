# Perfection V37/V370 (GT-F740) — USB protocol reference

Everything here was derived from USB captures of the real scanner and,
where marked, verified by our own driver (this library: `v370`, `cmd/v370`)
on the real device. The captures (`captures/*.pcap`, `traces/*.jsonl.gz`,
named below) are kept privately — they contain scanned pages and the vendor's
firmware upload; record your own with `scripts/capture-scan.sh`.
Last updated 2026-11-07.

**Confidence markers**

| Marker | Meaning |
|---|---|
| ✅ verified | our own code sent it to the real scanner and got the documented answer |
| 👁 observed | seen in the vendor app's captures, not yet sent by us |
| 💭 inferred | interpretation of observed values; may be wrong |
| ⚠️ danger | can hang the scanner |

## 1. Transport ✅

| Property | Value |
|---|---|
| USB ID | `04b8:014a`, USB 2.0 high speed |
| Interface | 1 interface (class 0xff vendor), no kernel driver |
| Endpoints | **EP 0x02 bulk OUT** (commands, 512-byte packets), **EP 0x81 bulk IN** (replies, image data) |
| Control | only standard requests (descriptors, SET_CONFIGURATION); everything else is bulk |
| Strings | manufacturer name; product name `… Perfection V37/V370` |
| Exclusivity | one program at a time; claiming fails with EBUSY while the vendor app runs (also after its **window is closed** — the process stays in the background) |

The scanner only sends when the host reads (USB is host-polled); nothing
arrives unsolicited.

## 2. Framing ✅

Every command is **2 bytes** written to EP 0x02, from two families:
`1b xx` (ESC, ESC/I-like) and `1e xx`. The scanner answers on EP 0x81 in one
of three ways:

| Kind | Exchange | Examples |
|---|---|---|
| query | `cmd` → data (fixed length) | `1b 03` → 2 bytes |
| command | `cmd` → `06` (ACK) | `1b 40`, `1e 05`, `1e 9b` |
| command + parameter | `cmd` → `06`, then `param` → `06` **or** data | `1e 93` `01` → `06`; `1e 8f` `1000` → 4 bytes |
| table upload | `1e 84` → `06`, 8-byte header (no reply), payload → `06` | gamma/shading tables |
| data read | `1e 47` → N bulk IN transfers (up to 51 200 bytes each) | calibration and image data |

- `06` = ACK, `15` = NAK (never seen from the V370 so far).
- ⚠️ **After an ACK the scanner waits for the parameter block.** Whatever the
  host sends next is consumed as that parameter. If it is shorter than
  expected, the scanner waits forever and stops accepting OUT data
  (writes time out).
- ⚠️ **A USB port reset does not recover that state**: the scanner drops off
  the bus ("device not accepting address", "unable to enumerate") and needs a
  **power cycle**. (Happened on 2026-11-07 with `1b 21`.)

## 3. Command reference

"param" = parameter block after the ACK.

### 3.1 Session, status, identity

| Cmd | Kind | Request / reply | Meaning | Status |
|---|---|---|---|---|
| `1b 03` | query | → 2 bytes | **status**, see §4 | ✅ |
| `1b 13` | query | → 28 bytes | **identity**: vendor (8), model (16, `GT-F740`), firmware version (4, `1.00`), space padded | ✅ |
| `1e 93` | cmd+param | `01` → `06` / `00` → `06` | **open / close session** (~0.1–1 s). **While a session is open the Power button is blocked** (verified); buttons work without a session | ✅ |
| `1e 8e` | cmd+param | `0b 00 <u32 LE>` → `06` | **clock**: whole hours since 2024-01-01 00:00 UTC (0x6199 = 24985 on 2026-11-07 01:xx UTC); ACK takes ~1 s | ✅ |
| `1e 8f` | cmd+param | `<u16 LE addr>` → u32 LE | **register read**, see §5 | ✅ |
| `1e 83` | cmd+param | `08 <u32 LE addr> <u24 LE len>` → len bytes | **memory read** (type 8, read-only; RAM only: 0x000–0xFFF, 0x02000000–0x0200FFFF), e.g. button input 0x452 (§7); `v370 mem` | ✅ |
| `1e 85` | query | → 1 byte | **panel button** pressed since the last read: `00` none, `02` Printer, `03` Email, `04` PDF (§7) | ✅ |
| `1b 20` / `1b 21` / `1b 22` | cmd+param | `06`, then **4/5/7** bytes (3-byte address + 8/16/32-bit value; lengths from the firmware, [research/firmware-map.md](research/firmware-map.md)) | 💭 **register writes** (from the vendor's V370 plugin, static analysis) — ⚠️ never send | — |
| `1b 23` / `1b 24` / `1b 25` | cmd+param | `06`, then 3-byte address → 1/2/4 bytes | 💭 register reads (same source) — not used | — |
| `1b 40` | cmd | → `06` | end of scan job (sent after the last data read) | 👁 |

### 3.2 Firmware ✅

| Step | Host → scanner | Reply |
|---|---|---|
| begin | `1b 06` | `06` |
| header | `01 00 01 00` | none |
| payload | 65 536 bytes = `esfwdd.bin[256:65792]` | none |
| checksum | 1 byte = sum of payload bytes mod 256 (`af`) | `06` |
| restart | `1b 16`, then `80` | `06`, `06` |
| settle | wait ~2.5 s (the vendor app: 2.7 s) | — |

Needed only when status byte 0 has 0x20 set (`28 00`), i.e. after the scanner
was **switched off** (a USB replug alone keeps the firmware). Afterwards the
scanner warms up for ~8 s; total time to ready ≈ 13.6 s (measured twice).
Our upload is byte-identical to the vendor's (`testdata/traces/direct-poweron-open`).

**What the file is** (static analysis, [`research/firmware.md`](research/firmware.md)):
the scanner's complete main firmware — S1C33 (C33) RISC code linked at
`0x02000000`, with its own USB stack/descriptors, the identity strings and
the dispatch tables for every native `1b xx` / `1e xx` command. Before the
upload only a ROM boot loader runs; since the firmware lives only in RAM,
a power cycle always restores the loader (the scanner cannot be bricked by
an upload).
File provenance and SHA-256: `firmware/README.md`.

### 3.3 Read-only queries (purpose partly unknown)

Values from the idle scanner unless noted.

| Cmd | Reply | Value(s) seen | Interpretation | Status |
|---|---|---|---|---|
| `1e 53` | 42 bytes | see §6 | **read scan parameter block** (defaults when idle) | ✅ |
| `1e 9f` | 1 byte | `10` | "auto power-off supported" flag | ✅ (meaning from firmware analysis) |
| `1e a1` | 1 byte | `00` | unknown; polled by the vendor app before each step; **not** a button register (§7). light-source state (0 off, 1 flatbed lamp on; firmware analysis, [research/scan-hang.md](research/scan-hang.md)). `01` is normal when the lamp is already on | ✅ |
| `1e 65` | 1 byte | `00` | same as `1e a1` | ✅ |
| `1e 86` | 4 bytes | idle `00000000`; in scans `14000000`, `08000800`, `1c000800` | unknown (per-scan state?) | ✅ idle |
| `1e 97` | 12 bytes | `6a270000 6c270000 6c270000` = 10090, 10092, 10092 | 💭 three per-channel (R/G/B?) values, constant | ✅ |
| `1e 92` | 12 bytes | e.g. `87008c008a00 0000 3a31…` | 💭 per-channel calibration result (varies per scan) | 👁 |
| `1e ff` | 4–5 bytes | e.g. `e5e7ee03` | 💭 per-channel levels + count; host sends `1e 99` with the first 3 bytes + `00` afterwards | 👁 |
| `1e 04` | 1 byte | `02` / `01` | unknown; polled twice before calibration | 👁 |

### 3.4 Scan setup and data (observed in every scan, order as in the captures)

| Cmd | Kind | Param / reply | Interpretation |
|---|---|---|---|
| `1e 57` | cmd+param | 42-byte block (§6) → `06` | **write scan parameter block** (resolution, area, mode) |
| `1e 9b` | cmd | → `06` | 💭 apply parameters |
| `1e a2` | cmd+param | `01` → `06` | unknown |
| `1e 87` | cmd+param | `00` → 10 bytes, e.g. `01004f009000f000e800` | 💭 measurement (lamp/white level?) |
| `1e 46` | cmd+param | 8 bytes, e.g. `04 01 000d 00000000` → `06` | 💭 pass setup (pass type, length) |
| `1e 99` | cmd+param | 4 bytes (from `1e ff` reply) → `06` | 💭 set levels |
| `1e 31` | cmd+param | 12 bytes (from `1e 92` reply) → `06` | 💭 set calibration result |
| `1e 11` | cmd+param | 1 byte (`02`/`03`/`04`) → `06` | unknown |
| `1e 21` | cmd+param | 28 bytes → `06` | 💭 pass geometry (first u32 varies with resolution: 0x0d00, 0x21c0, 0x1140) |
| `1e 22` | cmd+param | 12 bytes → `06` | 💭 same first value as `1e 21` |
| `1e 41` | cmd+param | 22 bytes → `06` | 💭 motor / line parameters |
| `1e 42` | cmd+param | 18 bytes → `06` | ✅ 3 × u16 base K (shading reference) + 3 × 2 × u16 dark offsets; zero for the dark reads (`docs/research/calibration.md`) |
| `1e 43` | cmd+param | 12 bytes → `06` | gains: `0080 0080 0080` (unity) for calibration reads, constant `0679 ca7a 407b` for the image |
| `1e 01` | cmd+param | 12 bytes → `06` | 💭 start motor/pass |
| `1e 05` | cmd | → `06` | 💭 start / carriage return |
| `1e 84` | upload | 8-byte header + table → `06` | **table upload**, see below |
| `1e 47` | data | → N × ≤ 51 200 bytes | **read data**: calibration lines or image |

`1e 84` headers seen (payload length in brackets):

| Header | Payload | Interpretation |
|---|---|---|
| `08 00 fc 1f 02 00 01 00`, `…fd1f…`, `…fe1f…` | 256 bytes `00 01 02 … ff` | identity 8-bit LUTs, one per channel; generated by the driver (`ramp-8`) |
| `09 00 00 00 00 00 01 00` | 256 bytes | unknown table |
| `02 00 00 38 00 00 20 00`, `…2038…`, `…4038…` | 8 192 bytes each | ✅ tone curve per channel: 4096 × uint16 LE, `int(65535·(i/4095)^(1/1.8)) >> 4` (gamma 1.8, 12-bit); identical in every recorded scan, generated by the driver (`gamma-1.8`) |
| `04 00 00 01 02 00 08 00` | 2 048 bytes | motor table (type 4), identical for equal speeds |
| `05 00 08 01 02 80 4f 00` / `…02 00 54 00` | calibration width × 6 bytes | ✅ per-pixel shading table, computed from the scan's calibration reads (`docs/research/calibration.md`) |
| `04 00 00 01 02 02 00 00` | 2 bytes (`2001` / `d000`) | motor table (type 4), varies with resolution |
| `04 00 00 01 02 00 03 00` | 768 bytes | motor table (type 4) |

**Scan structure**: calibration reads (`1e 47`: a short dark read, the
white reference, 1–3 dark reads; 16 bit), host computes offsets, bases and
the shading table (`1e 42`, `1e 84` type 5; `docs/research/calibration.md`),
final pass (`1e 47` returns the image), `1b 40`. During the scan status is `58 01` (busy).

## 4. Status (`1b 03`) ✅ / 💭

| Byte 0 | Byte 1 | Seen when |
|---|---|---|
| `28` | `00` | power-on, firmware missing |
| `1a` | `01` | firmware running, warming up (≈ 8 s) |
| `5a` | `01` | warming up + carriage moving |
| `58` | `01` | scanning / busy |
| `18` | `01` | **ready** |

Inferred bits of byte 0: `0x20` no firmware · `0x10` firmware running ·
`0x02` warming up · `0x40` busy. `v370.Status` implements exactly this.

## 5. Registers (`1e 8f` + u16 address → u32) ✅

| Address | Value | Meaning | Evidence |
|---|---|---|---|
| `0x0005` | 849, 850, 880 | **scan counter** (lifetime number of scans) | +1 per scan: 850 at 01:59, 880 after exactly 30 scans; unchanged by sessions, firmware uploads, power cycles |
| `0x0010` | 120 | **auto power-off time in minutes** (0 = off, max 480, non-volatile; write: `1e 8e` + `10 00 <u32 LE>` — never sent so far) | firmware analysis ([research/power-button.md](research/power-button.md)); read by the vendor app three times per session |

Other addresses have **not** been probed (reading an unknown register may not
be harmless — see §2).

## 6. Scan parameter block (`1e 53` read / `1e 57` write) 👁 / 💭

42 bytes: ten u32 LE + one u16.

| # | Offset | Meaning | Idle default (`1e 53`) | Examples written with `1e 57` |
|---|---|---|---|---|
| 0 | 0 | X resolution (dpi) | 100 | 100 (for 75 dpi!), 200 (for 150!), 300, 600, 1200 |
| 1 | 4 | Y resolution | 100 | same as X |
| 2 | 8 | X offset (px) | 0 | 0 |
| 3 | 12 | Y offset (px) | 0 | 0 |
| 4 | 16 | width (px at this resolution) | **848** (= 215.4 mm, whole glass) | 2480 (A4 @300), 1181 (100 mm @300) |
| 5 | 20 | height (px) | **1170** (= 297.2 mm) | 3507 (A4 @300), 591 (50 mm @300) |
| 6 | 24 | 💭 mode | 256 | `0x0813` colour, `0x0800` gray and line-art |
| 7 | 28 | unknown | 768 | 1040–1128, varies with mode/area |
| 8 | 32 | unknown | 32768 | 0 |
| 9 | 36 | unknown | 0 | 0 |
| 10 | 40 (u16) | unknown | 0 | 0 |

Findings:
- **Not every resolution is native**: for 75 dpi the vendor app scans at 100 dpi,
  for 150 at 200, and scales on the host. Seen native values: 100, 200, 300,
  600, 1200.
- **Gray and line-art** still transfer colour-sized data (30.4 MB at 300 dpi
  A4, like colour); conversion happens on the host.
- The idle defaults give the scannable area: 848 × 1170 px @ 100 dpi.

## 7. Buttons (Printer / Email / PDF / Power) ✅

**`1e 85`** (query, no parameter, no ACK) returns **1 byte**: the button
pressed since the last read.

| Value | Button | Verified |
|---|---|---|
| `00` | none | ✅ |
| `02` | Printer (copy) | ✅ |
| `03` | Email | ✅ |
| `04` | PDF | ✅ |
| `01` | **Power** (also while it is blocked by a session) | ✅ |

- A press is an **event**: reported once, cleared by the read — also when
  the button is held for 2 s. Polling every 200 ms lost no press
  (`testdata/buttons/1e85-watch.log`, `v370-buttons.out`).
- Works **with or without** a session; without a session the Power button
  keeps working (hold ~3 s to switch off) — our watcher polls session-less
  and opens a session only for scans ([research/power-button.md](research/power-button.md)).
  **Never poll during a scan** (it would interleave with the `1e 47` reads).
- A press must be firm (~0.25–0.5 s); the first press after a long idle
  time may only wake the scanner (lamp sleep, ~14 min) and not be reported.
- `v370.Scanner.ReadButton` (high level: `libv370.Device.ReadButton`), tool `v370 buttons [-once]`.
- **Press duration** (verified 2026-11-08): the firmware keeps the current
  button inputs in RAM 0x452 (same active-low bits, `f0` = nothing held) and
  latches a press after 10 samples (≈ 0.25 s). After a `1e 85` event the host
  reads 0x452 with the read-only `1e 83` type 8 until the button is released:
  held 1.7 s, 4.5 s and 2.0 s were measured to ±50 ms (`v370 mem -repeat 2400
  0x450 3`). `v370.Scanner.PressDuration`. Power is latched only at release
  without a session, so its duration is not available.

How it was found (2026-11-07):
1. Polling every known query while Printer/Email/PDF were pressed, three
   times (slow, fast, after the vendor's GUI start-up from power-on): nothing
   changed. The vendor app's Linux GUI never polls buttons for this model.
2. `1b 21` (classic ESC/I "push button status") is answered with ACK here
   and hung the scanner (power cycle needed; a USB reset made it drop off the
   bus). Static analysis later showed `1b 20`–`1b 22` are **register writes**
   in this dialect — the probe may have written a register.
3. A research subagent analysed the vendor app's open source (6.7.70), iscan /
   epkowa and the vendor's closed V370 interpreter plugin
   (`libiscan-plugin-perfection-v370.so`): the vendor's ESC/I button request
   (`ESC !`) is translated by the plugin into the native **`1e 85`**, 1-byte
   reply; the vendor app simply never starts button polling for ESC/I devices
   (`CESCIAccessor::StartButtonChecking` is never called; the GUI's
   `ScannerDidPressButton` is empty). **Sources with URLs, checksums,
   file/line and binary addresses: [`docs/research/README.md`](research/README.md)**;
   full report: [`docs/research/buttons-report.md`](research/buttons-report.md).
4. Verified on the real scanner: single probe `1e 85` → `00`, status still
   `18 01`; then the button mapping above.

## 8. Scanning with a profile ✅ and the raw image data 💭

Our driver replays the scan part of a recorded the vendor session
(`v370/profile.go`, built-in profiles in `v370/profiles/`)
after its own `Open`:

- Status-poll runs become "poll until the recorded status or ready";
  every run in the captures ends at `18 01` (the last one after the image
  waits ~10 s while the carriage returns: `58 01` → `1a 01` → `18 01`).
- Three parameters are **live echoes** of earlier replies and must not be
  replayed literally: `1e 57` ← `1e 53` reply (read-modify-write of the
  parameter block), `1e 99` ← first 3 bytes of `1e ff` + `00`, `1e 31` ←
  `1e 92` reply.
- Data reads (`1e 47`) are read until the recorded byte count; transfers are
  up to 51 200 bytes (A4 300 dpi: 587 transfers of 48 392 / 24 200 bytes).
- Calibration values (`1e 42` before the white read and before the image,
  the `1e 84` type 5 shading table) are computed from the scan's own
  calibration reads (`calc` steps); standard tables are generated.

Verified on the real scanner (2026-11-07): A4 100 dpi in 18.5 s, A4 300 dpi
in 24.8 s; register `0x0005` counted both scans.

### Raw image data of the final pass ✅ (verified on all 9 same-dpi recordings)

| Property | Finding |
|---|---|
| Transfer structure | each bulk transfer = **N whole lines + 8-byte trailer** `02 <u24 LE payload length> 00 00 80 00` (e.g. `02 c0c600 00008000` = 50 880 bytes) |
| Lines | `1e 41` block, u16 word 2 (1172 @100 dpi A4, 3519 @300 dpi A4) |
| Lines per transfer | `1e 41` block, u16 word 9 (20 @100, 10 @200, 6 @300 A4, 3 @1200 A6, …) |
| Width | payload of a full transfer ÷ (lines per transfer × 3); **rounded up** by the scanner: 826→848 (100), 1653→1680 (200), 2480→2688 / 1181→1536 (300), 2480→2512 / 1181→1200 (600), 4960→5008 / 1181→1200 (1200) |
| `1e 41` words 0/1 | start/end in sensor units; (end − start) = width × (400/dpi for ≤ 300 dpi, 1 for 600/1200) |
| `1e 41` word 5 | `0x80xx`, xx = 2400 / dpi |
| Pixel format | 8-bit **RGB, pixel-interleaved**; same orientation as the vendor's output |
| Colour line offsets | output line *y* = **B** raw line *y*, **G** *y* + 2·dpi/100, **R** *y* + 4·dpi/100; the scanner sends exactly 4·dpi/100 extra lines for this |
| Values | scanner-calibrated, **before** the vendor's host tone curve (the vendor: raw 192→220, 224→233, 240→240) |
| Gray / line-art | the scanner still sends RGB (mode 0x0800 changes nothing visible in the data size); conversion on the host |
| 75 / 150 dpi | scanned at 100 / 200 dpi, downscaled on the host |
| Horizontal crop | the vendor keeps the requested width a few pixels after the line start (5–10 px at 300–1200 dpi, matched against its output) and all scanned lines; it does not rescale |
| Scan origin | across: the first column is at the frame edge (a sheet in the corner starts ≈ 0.4–1.5 mm under the frame; moving the sensor window left only shows the frame edge and unlit pixels). Along: with the vendor's pre-move (`1e 01` before the image pass: 568/556/752/748/812 steps at 100/200/300/600/1200 dpi) the first line is ≈ 1.2 mm after the corner; the driver omits 100/110/113/116/117 steps so the first line is the corner ±0.1 mm (sheet of known length, lid open; ≈ 93.4 steps/mm). `Options.VendorStart` keeps the vendor values |
| Real resolution | along the glass **300.2 dpi** at nominal 300 (ruler, mm and 1/16-inch scales over 25 cm, ±0.01 %). Across about +1.0–1.5 % (card 304.4 dpi; ruler face 606 at 600 dpi), depending on the object's height above the glass (lens magnification); a raised ruler scale gave +0.2 % |
| Sheet edges | with the lid closed a sheet's edges show as a shadow line (bottom) or a brighter lid strip (right); with the lid open as a sharp paper/black edge (best for measuring) |

The shear and colour fringes in `docs/first-direct-scan-raw-100dpi.png` are
exactly the trailers (rendered as pixels) and the missing channel alignment.
Decoded with `rawimg` (trailer check, geometry from `1e 41`,
channel alignment) the result is clean: `docs/direct-scan-300dpi-detail.png`
(1:1 crop of a real 300 dpi scan made by our driver).

Side observation: switching the real scanner off in software
(`authorized=0` → `1`, as `scripts/hwsim-test.sh --disable-real` does) made
it lose its firmware; the next session uploaded it again.

## 9. Sources

| Trace | What it shows |
|---|---|
| `a4-75dpi-poweron` | firmware upload + full scan (the vendor) |
| `a4-300dpi-jpeg`, `a4-150dpi-color`, `a4-300dpi-gray`, `a4-300dpi-lineart` | A4 scans, mode/resolution variants |
| `user-100x50mm-300dpi`, `user-50x50mm-600dpi`, `user-25x25mm-1200dpi` | custom areas |
| `a6-*-userbug` | A6 scans (recorded before the size fix) |
| `direct-poweron-open` | **our** firmware upload and session |
| `buttons-vendorgui` | The vendor GUI idle while buttons were pressed |
| `direct-a4-100dpi` | **our** first real scan (profile `a4-100dpi-color`) |

Tools: `v370-usbsim inspect TRACE`, `v370 info|poll|raw|reset`.

## Lamp measurement (`1e 46` … `1e 04` … `1e ff`)

After `1e 46` the firmware measures the lamp and, until it is done, answers
**only `1e 04` (`02` = busy) and `1e 92`** — every other command is dropped
without a reply. Poll `1e 04` until it is not `02` before sending anything
else ([research/scan-hang.md](research/scan-hang.md)).

## Never send

From the firmware analysis ([research/firmware-map.md](research/firmware-map.md)):
`1b 20`–`1b 25` (register access), `1e 45` (writes NV), `1e 96` (overwrites
factory constants), `1e 98` (rewrites the whole NV memory with a default
image: USB identity, counters, factory calibration), and `1e 8e` with any
function other than `0b 00` (clock) / `10 00` (auto power-off minutes).

## Resolutions (verified on the scanner, 2026-11-07)

- Horizontal = sensor mode (`1e 11`: 4 = 400/in, 3 = 600, 2 = 1200) ÷
  divider in `1e 41` byte 8 (×¾ with bit 7); vertical = 2400 / `1e 41`
  byte 10. The vendor rounds other values up and scales on the host (400 dpi →
  450 × 400, 800 dpi → 1200 × 800). Real, unscaled: 100, 200, 300, 600,
  1200 dpi ([research/supported-modes.md](research/supported-modes.md)).
- **Carriage travel at 1200 dpi** ≈ 14 038 lines: A4 (14 031 + 48 colour
  offset lines) does not fit — the stream ends early and the carriage stays
  at the far end with the LED blinking until a power cycle. A5 at 1200 dpi
  and A4 at 600 dpi work with our driver.
