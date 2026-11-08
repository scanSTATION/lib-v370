# Firmware patches

The scanner has no flash memory for its firmware: the host uploads it
after every power-on, and it runs from RAM. lib-v370 can change it on the
way. A **patch** is a short list of our own bytes at payload offsets
(`firmware.Patch`). It contains none of the vendor's code, and no modified
firmware file is ever written or distributed. The patch is applied in
memory just before the upload:

- only to the one vendor build it was made for (SHA-256 of the file is
  pinned in `Patch.Base`), and
- only if the result has the pinned SHA-256 (`Patch.Result`).

Any other firmware file is uploaded unchanged. A power cycle always
restores the original.

```mermaid
flowchart LR
  F[firmware file<br/>from the vendor app] --> H{SHA-256 =<br/>Patch.Base?}
  H -->|no| U[upload unchanged]
  H -->|yes| A[apply our bytes<br/>in memory]
  A --> R{result SHA-256 =<br/>Patch.Result?}
  R -->|no| E[error: not uploaded patched]
  R -->|yes| P[upload patched]
  S[StockFirmware] -.-> U
```

Switching it off: `v370.Options.StockFirmware` (library),
`-stock-firmware` (tool `v370`), `--v370-stock-firmware`
(scanSTATION server). The upload log names the applied patches
(`patches=[led]`).

## Summary of improvements

| | Stock firmware | With the `led` patch |
|---|---|---|
| LED states the host can set (`1e de`) | green, orange | green, orange, **off** |
| Buttons while the LED is orange | **not detected** (the orange state skips button sampling and the idle timer) | detected in every LED state |
| LED while the scanner is busy (scanning, carriage return, warm-up) | always blinks green | blinks in the **selected colour** (green or orange) |
| Colour after a scan job (`1b 40`) | reset to green | **kept** |

What the host makes of it (scanSTATION server, button scans): off as
soon as a press is registered; green blinking for a single page; orange
flash at 1 s (release = cancel); orange blinking and orange for a
multi-page document; off → green when Power finishes it. Details:
scanSTATION-server README, "Multi-page PDFs".

Verified on the scanner on 2026-11-08: the patch loads, all three LED
states, buttons while orange, the busy blink in orange during document
pages, the colour kept across pages, and the full button sequence above.

## `led` — LED off state, buttons active in every LED state, coloured busy blink

### Findings (static analysis, verified on the scanner)

- The panel LED is a two-colour LED on ASIC register `0x37FD00` bits 4/5:
  `10` green, `01` orange, `11` off. The 25 ms timer tick (`0x020011B6`)
  sets it 40 times a second: error states blink orange (off ↔ orange,
  0.5 s), start-up blinks green, otherwise RAM `0x3C2` bit 2 selects
  orange (set) or green.
- `1e de` + 1 byte (handler `0x02006A12`, not used by the vendor app) sets
  `0x3C2` bit 2 to bit 0 of the byte: green ↔ orange works with the stock
  firmware. But the stock orange branch jumps over the tick's button
  sampling and idle timer, so buttons are not detected while the LED is
  orange, and there is no command for off.
- While the scanner is busy (scanning, carriage return, warm-up), the tick
  ignores the selection and blinks green itself (`0x02001312` ff.). The
  re-initialisation `0x02001786`, which `1b 40` calls at the end of every
  job, clears `0x3C2` bit 2 (back to green).

### Tick after the patch

```mermaid
flowchart TD
  T[timer tick, every 25 ms] --> ERR{error state?}
  ERR -->|yes| EB[blink orange<br/>stock]
  ERR -->|no| BUSY{busy?<br/>scan, return, warm-up}
  BUSY -->|yes| BB[blink: on phase in the<br/>selected colour<br/>patch routine 2]
  BUSY -->|no| SEL[patch routine 1]
  SEL -->|0x3C2 bit 3| OFF[off]
  SEL -->|0x3C2 bit 2| OR[orange]
  SEL -->|else| GR[green]
  OFF --> BTN[button sampling,<br/>idle timer]
  OR --> BTN
  GR --> BTN
```

### Changes (`firmware.LEDPatch`, 6 changes)

| Load address | Change |
|---|---|
| `0x0200C9D4` (free area, `0xFF` before) | routine 1: `0x3C2` bit 3 → off; else bit 2 → orange; else green; then continue at `0x02001392` (button sampling, idle timer) in every case |
| `0x02001374` | jump to routine 1 (the stock selection behind it becomes unreachable) |
| `0x0200CA06` (free area) | routine 2, busy blink "on" phase: orange while `0x3C2` bit 2 is set, else green; continue at `0x02001336` |
| `0x02001332` | the busy blink's "on" phase jumps to routine 2 |
| `0x020019D0` | re-initialisation: `ext`/`ext`/`bclr [0x3C2],2` → three `nop` (the colour survives `1b 40`) |
| `0x02006A2A`, `0x02006A32` | `1e de` keeps two parameter bits (`and 0x3`) and clears both RAM bits first (`and 0xf3`) |

Our code in the free area:

```text
; routine 1: normal LED selection (entered from 0x02001374)
ld.w  %r11,0x37fd00
btst  [%r8+0x1c2],3 ; jreq L1                        ; RAM 0x3C2 bit 3: off
bset  [%r11],4 ; bset [%r11],5 ; jp 0x02001392
L1: btst [%r8+0x1c2],2 ; jreq L2                     ; bit 2: orange
bclr  [%r11],4 ; bset [%r11],5 ; jp 0x02001392
L2: bset [%r11],4 ; bclr [%r11],5 ; jp 0x02001392    ; green

; routine 2: busy blink "on" phase (entered from 0x02001332, %r11 = LED)
btst  [%r8+0x1c2],2 ; jreq G                         ; orange selected?
bclr  [%r11],4 ; bset [%r11],5 ; jp 0x02001336        ; orange
G: bset [%r11],4 ; bclr [%r11],5 ; jp 0x02001336     ; green
```

`0x3C2` bit 3 is otherwise unused (other code only sets bits 5/6 or tests
bits 1, 2, 4–6), and RAM is cleared at reset.

### Safety

- Only the LED output bits (`0x37FD00` bits 4/5) and two RAM bits are
  touched. Motor, lamp, CCD, USB and NV code is unchanged, and the button
  sampling and idle timer (auto power-off) now run in every LED state.
- Every branch of the new code ends in a jump back to the stock code; the
  free area was `0xFF` (unused) before.
- The scanner accepts any payload with the right 1-byte sum, and the
  driver computes it; a power cycle loads the original again (see
  `research/firmware-mod.md`).

### Use

- Library: `v370.Scanner.SetLED(LEDGreen | LEDOrange | LEDOff)` (`1e de`
  00 / 01 / 02). With stock firmware `LEDOff` shows green.
- `v370.Scanner.LEDPatched` reads routine 1's first bytes back from the
  scanner's RAM (read-only memory read `1e 83` type 8): true if the patched
  firmware is running.
- Tool: `v370 led` (test sequence: orange, green, quick changes;
  `-off` uses the off state); `v370 mem` reads RAM, e.g. `0x3C2`.

Pinned values: input file `2fec0d33…dd954` (vendor app 6.7.82.0), patched
payload `5abb7f4b…0e94`. Tests: `firmware/patch_test.go` (with the real
file via `LIBV370_FIRMWARE`), `sim/synth` (`Options.LEDPatch`), the
server's LED sequence tests.

## Adding a patch

1. Find the code to change with the disassembler
   (`docs/research/tools/firmware`; listings are generated from your own
   copy of the firmware and not kept in git). Keep away from motor, lamp
   and CCD code (`docs/research/improvements.md` §C).
2. Put new code into the free area (payload `0xC9D4`–`0xFFFF`; `led` uses
   `0xC9D4`–`0xCA1F`), assemble with `s1c33asm.py`, check the disassembly.
3. Define a `firmware.Patch` with the bytes and both SHA-256 values; add a
   marker if the host needs to detect it.
4. Test on the scanner (power cycle = undo).
