# V37/V370 (GT-F740) firmware: which scan modes are supported?

Static analysis of `esfwdd.bin` (payload = file − 256, load address 0x02000000), 2026-11-07.
No USB access. Markers: **[V]** read in code (address given) or in the recorded profiles/traces,
**[I]** inferred, **[U]** unknown. Addresses are load addresses (payload offset = addr − 0x02000000).

## TL;DR

1. **The firmware does not check resolutions at all.** `1e 57` (0x020079FA) copies the 42-byte block
   into RAM 0x3F0–0x418 without any check, and `1e 53` (0x0200788E) just sends it back [V]. The only
   field any other code reads is **byte 0x1A** (RAM 0x40A), and only as a "== 1" test inside the motor
   routines (0x02001DFA, 0x0200229E, GT-F740 variant only; probably film/TPU) [V code, I meaning].
   Resolution, area and mode in `1e 57` are **bookkeeping for the host**. The scan itself is set up by
   the low-level blocks `1e 11`, `1e 21`, `1e 22`, `1e 41`, `1e 01` (plus `1e 42/43/84`). The vendor's plugin
   computes these.
2. **Horizontal resolution comes from the sensor mode `1e 11` (0..5) plus a binning divider in `1e 41`.**
   - `1e 11` (handler 0x020061C0 → 0x0200455A → 0x02009152) loads one of **six CCD timing-generator
     modes** into the analog front end. Each mode has a 64-word waveform at payload 0x8830 + mode×128,
     plus per-mode AFE registers 0x50/0x51/0x61/0x67/0x6B; switch table at 0x91D4 [V].
     Values above 5 skip the per-mode setup [V].
   - Measured unit density per mode (from the recorded `1e 41` start/end vs. the image width) [V traces]:

     | `1e 11` | used by the vendor for | sensor px per inch | waveform at payload |
     |---|---|---|---|
     | 0 | never seen | **[U]** (different, two-phase pattern `a6a6/5353`: likely 2400 or 4800) | 0x8830 |
     | 1 | never seen | **[U]** (pattern `c5f5/0505`: likely 2400 or 4800) | 0x88B0 |
     | 2 | 1200 dpi | 1200 | 0x8930 |
     | 3 | 600 dpi | 600 | 0x89B0 |
     | 4 | 100, 200, 300 dpi | 400 | 0x8A30 |
     | 5 | never seen | 200 [I] (6 sub-periods per line; mode 4 has 3, mode 3 has 2, mode 2 has 1) | 0x8AB0 |

   - `1e 41` byte 8 (handler 0x020066FA):
     `px_out = (end − start) / DIV[(b8 >> 4) & 7] × (b8 & 0x80 ? 3 : 1)`, with
     DIV = `01 02 04 08 10 20 40 80` (payload 0x55C4) [V 0x02006762–0x0200684A]. Checked against all
     profiles [V]:
     - 100 dpi: b8 = 0x21, 3392/4 = 848;
     - 200 dpi: b8 = 0x11, 3360/2 = 1680;
     - 300 dpi: b8 = 0xA1, 3584/4 × 3 = 2688;
     - 600/1200 dpi: b8 = 0x01, /1.

     So the native X resolutions are **1200, 600, 400 (and 200?) and their /2, /4 … /128 and ×3/4
     variants** (e.g. 300 = 400 × 3/4, 150 = 600/4 or 400×3/8, 75 = 600/8 …). The vendor instead scans 75 at
     100 and 150 at 200 and scales on the host. The firmware would accept 150 directly
     (mode 3 + DIV 4, or mode 4 + 0xB1) [I, untested].
   - **4800 dpi: not reachable with any recorded setting.** The highest proven X density is 1200 px/inch
     (mode 2, DIV 1). Modes 0/1 are the only candidates for 2400/4800 [I]. Untested, unknown line
     period, unknown pixel order (a staggered CCD may deliver odd/even rows interleaved) [U].
3. **Vertical resolution = motor steps per line**, `1e 41` byte 10 (u8 → RAM 0x34E, read by the motor
   routine 0x02002742) [V]. The value is always 2400/dpi in the profiles (0x18 = 24 @100, 0x0C @200,
   0x08 @300, 0x04 @600, 0x02 @1200) [V traces]. That gives a **motor base of 2400 steps/inch**.
   Byte 10 = 1 → **2400 dpi vertical** is the finest a u8 allows [I]. Finer steps would need a different
   microstep mode in `1e 01` byte 10 bits 1–3 (handler 0x02006058, modes 0–4, the vendor always uses 4 for
   scans) [V code, I meaning]. Whether that gives 4800 lines/inch is [U].
4. **Bit depth: 16-bit and 1-bit output exist in the firmware.** `1e 41` byte 8 bits 0–1 → ASIC
   0x37F6F0 bits 6–7, and the bytes-per-line calculation [V 0x0200677E, 0x02006852–0x02006864]:
   - `00` = **16 bit/sample** (×2): **used by the vendor for every calibration pass** (b8 = 0x00; data
     sizes match: 3392 px × 3 × 2 = 20 352 B/line = the 1e84 shading table size) [V traces];
   - `01` = 8 bit (all image passes) [V];
   - `02` = 8 bit by size, other meaning [U];
   - `03` = **1 bit/sample** (÷8) [V code, I hardware, never used].
5. **Channels:** `1e 41` byte 13 bit 0: 0 → 3 channels (all the vendor scans, including "gray" and
   "line-art"); 1 → **1 channel** (sets r2 = 1 for the size calculation and 0x80 instead of 0x90 in
   ASIC 0x37F660) [V 0x0200670C–0x0200675A]. That would be **true gray with 1/3 of the data** [I]. Which
   colour/lamp channel it delivers is [U]: probably chosen by `1e 41` byte 12 (→ 0x37F650 & 0xC3) or
   byte 17 [U].
6. **No clamping of area or dpi** in any of the handlers [V]. The only masks/limits are:

   | Field | Limit | Where |
   |---|---|---|
   | `1e 41` start, end (u16, sensor units) | 16 bit; + offset RAM 0x470 → ASIC 0x37F0C6/0x37F0C8 | 0x0200672E–0x02006740 [V] |
   | `1e 41` lines (bytes 4–7) | `& 0x1FFFF` (≤ 131 071 lines); 0 → treated as 1 + flag 0x3C0 bit 3 | 0x020067AC [V] |
   | `1e 41` lines per transfer (bytes 18–19) | clamped to ≤ total lines | 0x0200686E [V] |
   | number of transfers | `& 0x1FFF` → 0x37F9B0 (≤ 8191 transfers) | 0x020068C6 [V code, I meaning] |
   | bytes per transfer | **not checked**; + 8-byte trailer → RAM 0x35C, ASIC 0x37F9A0 | 0x02006894–0x020068BC [V]. Hardware buffer limit [U]; the vendor stays ≤ 51 200 B |
   | `1e 21` line period (u24) | `& 0x1FFFFF` | 0x02006216 [V] |
   | `1e 22` lamp on-times (u24 ×3) | `& 0x1FFFFF`; 0 = lamp off; ≥ period = always on | 0x0200631A–0x02006350 [V] |
   | `1e 11` sensor mode | `& 7`; > 5 → no per-mode setup | 0x0200455A, 0x020091C0 [V] |
   | `1e 01` microstep mode | `(b10 >> 1) & 7`, cases 0–4, > 4 → raw value | 0x02006128 [V] |

7. **Maximum area** [I]: the glass is 8.48 in × 11.70 in (the idle `1e 53` defaults 848 × 1170 @ 100,
   set at boot by 0x02001A4E — only a default, not enforced). In sensor units the widest recorded line
   is 0x51…0xE51 (mode 4, 3584 units = 8.96 in). At 1200 px/inch that is ≈ 10 200 units, which fits the
   u16 registers. At 1200 dpi colour 8-bit, a full-width line is ≈ 30 KB; 16-bit ≈ 61 KB. Above
   ≈ 51 200 B per transfer, lines per transfer must be 1 and the hardware buffer limit is untested [U].
   The real limit is the travel set by the host's `1e 01` step count (u32; the vendor ≈ 28 250 steps for A4
   = 11.77 in at 2400 steps/inch) [V traces].

## `1e 41` block layout (22 bytes; firmware view, handler 0x020066FA) [V unless marked]

| Byte | Meaning (firmware) | 300 dpi example |
|---|---|---|
| 0–1 | start, sensor units (u16) → 0x37F0C6 (+[0x470]) | 0x0051 |
| 2–3 | end (u16) → 0x37F0C8 (+[0x470]) | 0x0E51 |
| 4–7 | lines (`& 0x1FFFF`) → 0x37F834, RAM 0x370 | 3519 |
| 8 | bits 0–1 depth (00 = 16, 01 = 8, 11 = 1 bit); bits 4–6 binning index; bit 7 = ×3; whole byte >> 4 → 0x37F660 (\|0x80, \|0x10 for 3 channels) | 0xA1 |
| 9 | not read [V] | 01 |
| 10 | motor steps per line (2400/dpi) → RAM 0x34E | 0x08 |
| 11 | bits 6–7 → 0x37F640 (0x80 = image pass: flag 0x3BF bit 5); bits 0–1 → 0x37F680 [I: pass type / shading] | 0x80 |
| 12 | `& 0xC3` → 0x37F650 [U] | 0x80 |
| 13 | bit 0: 1 = single channel | 0x00 |
| 14 | not read [V] | 00 |
| 15 | → 0x37F690 [U; 0x82/0x83 in mode 4, 0x72 in modes 2/3] | 0x83 |
| 16 | → high byte of 0x37F670 [U] | 00 |
| 17 | `& 0xF0` → RAM 0x3C0 (flags) [U] | 0xA0 |
| 18–19 | lines per transfer (u16) | 6 |
| 20–21 | not read [V] | 0000 |

(Earlier research said word 5 is written to 0x37F460. That is wrong: byte 10 goes to RAM 0x34E (motor),
and 0x37F660 receives byte 8 >> 4 [V].)

## Related handlers asked about

- **`1e 87`** (0x0200739A): ACK, reads 1 byte (ignored), sends 10 bytes = `[0x43D]`, `00`, then four
  u16 from RAM 0x45A/0x45C/0x45E/0x460 [V]. These are written by 0x0200A900 (boot/home-position routine,
  reads ASIC 0x37F818) [V]. Seen: `01 00 4f00 9000 f000 e800`. [I] carriage home/white-strip
  calibration positions measured at start-up. No limits.
- **`1e 97`** (0x0200772C): sends three u32 read from NV words 0x38, 0x3A, 0x3C (via 0x02009A32) [V].
  Seen 10090/10092/10092. [I] factory per-channel constants. Read-only, no limits.
- **`1e 21`** (0x020061E0, 28 B): u24 line period → 0x0200C73C (ASIC 0x37F0A0/0x37F830), RAM 0x304.
  Three u16 at bytes 3, 6, 9 → per-channel exposure `period − value` → RAM 0x308/0x30C/0x310. Bytes
  13–24: three u24, passed to empty stubs 0x0200C77E/80/82 (unused in this model). Byte 25 flags, bytes
  26/27 → RAM 0x478/0x479 [V].
  Line periods used: mode 4: 0x1200 (100 dpi), 0x0C00 (200), 0x0D00 (300); mode 3: 0x1140 (600);
  mode 2: 0x21C0 (1200) [V traces].
- **`1e 22`** (0x020062EC, 12 B): lamp PWM per line, three u24 (`& 0x1FFFFF`). Value 0 → lamp output
  off; value ≥ period → always on; otherwise on-time ×2 → 0x37F0A0 (lamp 1) / 0x37F0A8 (lamp 2, TPU)
  [V]. The vendor sends the period itself (= always on).
- **`1e 46`** 8 B: byte 0 = sensor mode (as `1e 11`), byte 1 = 1, bytes 2–5 = line period [V traces].

## How to test safely (suggested order, all host-side, firmware unchanged)

1. **True gray** (lowest risk): copy a known-good profile (e.g. 300 dpi) and, in the *final* `1e 41`
   only, set byte 13 = 0x01 and expect a third of the data per line. Adjust the expected byte count;
   the per-transfer size changes. Keep everything else the same.
2. **16-bit image**: final `1e 41` byte 8 low bits 01 → 00, double the expected bytes, lower lines per
   transfer so that one transfer stays ≤ 51 200 B. The vendor already uses 16-bit for calibration, so the
   hardware can do it. Whether the shading tables must change for a 16-bit image pass is [U].
3. **1-bit**: byte 8 low bits → 11, expect ÷8 [I].
4. **2400 dpi vertical**: byte 10 = 1 with mode 2 (1200 px/inch horizontal), small area, and the vendor's
   1200 dpi `1e 21` line period. The motor then makes 1 step per line period instead of 2, i.e. **half**
   the 1200 dpi step rate, so it is not stressed [I]. The `1e 01` step count must cover the area
   (2400 steps/inch as before). The colour line offsets (2·dpi/100 and 4·dpi/100 lines) double [I].
5. **Modes 0/1 (possible 2400/4800 X):** experimental. They change CCD clocking (no mechanical risk),
   but line period and pixel order are unknown. Use a tiny area, mode 2 settings otherwise, and look at
   the raw data.

Never use `1b 20`–`1b 25` or the NV register writes for any of this.

## Addendum: new vendor-app captures (main agent, 2026-11-07) [V]

| Requested | 1e 57 block X×Y | `1e 11` | `1e 41` b8 | b10 | Matches the rule |
|---|---|---|---|---|---|
| 400 dpi | 450 × 400 | 3 (600/in) | 0xA1 (/4 ×3 = ¾) | 6 | 450 = 600·¾, 400 = 2400/6 |
| 800 dpi | 1200 × 800 | 2 (1200/in) | 0x01 | 3 | 800 = 2400/3 |
| A4 600 dpi | 600 × 600, 4961 × 7016 | 3 | 0x01 | 4 | 600 = 2400/4 |

These confirm the model:
- X = mode density / DIV (×¾ with bit 7);
- Y = 2400 / byte 10, independent of X;
- The vendor rounds X up to the next native value and scales on the host.

## Real-hardware results of the resolution experiment (main agent, 2026-11-07) [V]

| Scan | Our driver | Result |
|---|---|---|
| A4 600 dpi (5008 × 7016 raw px) | 53 s (the vendor 64 s) | clean: sharp, colours aligned |
| A5 148 × 210 mm, 1200 dpi (7056 × 9921 px) | 113 s | clean: single print dots resolved |
| A4 1200 dpi (needs 14 079 lines = 14 031 + 48 colour offset) | — | **exceeds the carriage travel** (vendor-app recording: `testdata/traces/a4-1200dpi-color.jsonl.gz`, 352 MB): the vendor's own session got only 14 038 lines (stream ended early); our replay left the carriage at the far end with the LED blinking (error) and the scanner answering every command with image data → power cycle needed (carriage homed after the next firmware upload, no damage). Usable height at 1200 dpi ≈ 14 038 − 48 lines ≈ 296 mm. |
