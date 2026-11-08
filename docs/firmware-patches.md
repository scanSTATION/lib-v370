# Firmware patches

The scanner's firmware is uploaded by the host after every power-on and
lives in RAM only. lib-v370 can change it on the way: a **patch** is a
list of our own bytes at payload offsets (`firmware.Patch`). It contains
none of the vendor's code. It is applied in memory just before the upload,
and only

- to the one vendor build it was made for (SHA-256 of the file, pinned), and
- if the result has the pinned SHA-256 (`Patch.Result`).

Any other file is uploaded unchanged. A power cycle always restores the
original. `v370.Options.StockFirmware` (tool: `-stock-firmware`, server:
`--v370-stock-firmware`) disables patching.

## `led` — LED off state, buttons active in every LED state, coloured busy blink

**Findings (static analysis, verified on the scanner 2026-11-08):**

- The panel LED is a two-colour LED on ASIC register `0x37FD00` bits 4/5:
  `10` green, `01` orange, `11` off. The 25 ms timer tick (`0x020011B6`)
  sets it 40 times a second: error states blink orange (off ↔ orange,
  0.5 s), start-up blinks green, otherwise RAM `0x3C2` bit 2 selects
  orange (set) or green.
- `1e de` + 1 byte (handler `0x02006A12`, not used by the vendor app) sets
  `0x3C2` bit 2 to bit 0 of the byte: green ↔ orange works with the stock
  firmware. But the stock orange branch jumps over the tick's button
  sampling and idle timer, so **buttons are not detected while the LED is
  orange**, and there is no command for off.

- While the scanner is busy (scanning, carriage return, warm-up) the tick
  ignores the selection and blinks green itself (`0x02001312`ff.), and the
  re-initialisation `0x02001786`, which `1b 40` calls at the end of every
  job, clears `0x3C2` bit 2 (back to green).

**The patch** (`firmware.LEDPatch`, 6 changes):

| Load address | Change |
|---|---|
| `0x0200C9D4` (free area, `0xFF` before) | new LED routine: `0x3C2` bit 3 → off; else bit 2 → orange; else green; then continue at `0x02001392` (button sampling, idle timer) in every case |
| `0x02001374` | jump to the new routine (the stock selection behind it becomes unreachable) |
| `0x0200CA06` (free area) | busy blink "on" phase: orange while `0x3C2` bit 2 is set, else green; continue at `0x02001336` |
| `0x02001332` | busy blink "on" phase jumps there |
| `0x020019D0` | re-initialisation: `ext`/`ext`/`bclr [0x3C2],2` → three `nop` (the colour survives `1b 40`) |
| `0x02006A2A`, `0x02006A32` | `1e de` keeps two parameter bits (`and 0x3`) and clears both RAM bits first (`and 0xf3`) |

New routine (our code):

```text
ld.w  %r11,0x37fd00
btst  [%r8+0x1c2],3 ; jreq L1      ; RAM 0x3C2 bit 3: off
bset  [%r11],4 ; bset [%r11],5 ; jp 0x02001392
L1: btst [%r8+0x1c2],2 ; jreq L2   ; bit 2: orange
bclr  [%r11],4 ; bset [%r11],5 ; jp 0x02001392
L2: bset [%r11],4 ; bclr [%r11],5 ; jp 0x02001392    ; green

; busy blink "on" phase (entered from 0x02001332, %r11 = LED)
btst  [%r8+0x1c2],2 ; jreq G                         ; orange selected?
bclr  [%r11],4 ; bset [%r11],5 ; jp 0x02001336        ; orange
G: bset [%r11],4 ; bclr [%r11],5 ; jp 0x02001336     ; green
```

`0x3C2` bit 3 is otherwise unused (other code only sets bits 5/6 or tests
bits 1, 2, 4–6), and RAM is cleared at reset.

**Use:** `v370.Scanner.SetLED(LEDGreen | LEDOrange | LEDOff)` (`1e de` 00 /
01 / 02); `LEDPatched` reads the routine's first bytes back with the
read-only memory read. With stock firmware `LEDOff` shows green.

Pinned values: input file `2fec0d33…dd954` (vendor app 6.7.82.0), patched
payload `5abb7f4b…0e94`. Tests: `firmware/patch_test.go` (with the real
file via `LIBV370_FIRMWARE`), `sim/synth` (`Options.LEDPatch`).

## Adding a patch

1. Find the code to change with the disassembler (`docs/research/tools/firmware`,
   listings are regenerated from your own copy of the firmware) and keep
   away from motor, lamp and CCD code (`docs/research/improvements.md`).
2. Put new code into the free area (payload `0xC9D4`–`0xFFFF`), assemble
   with `s1c33asm.py`, check the disassembly.
3. Define a `firmware.Patch` with the bytes and both SHA-256 values; add a
   marker if the host needs to detect it.
4. Test on the scanner (power cycle = undo).
