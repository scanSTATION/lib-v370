# V370 driver improvements (from the firmware analysis)

Markers: **[V]** read in the code, traces or on the device; **[I]** inferred; **[U]** unknown. Command
details are in `firmware-map.md` §5; resolutions and limits are in `supported-modes.md`.

## A. Host-side improvements (firmware unchanged), ranked by benefit and risk

| # | Improvement | Benefit | Risk | |
|---|---|---|---|---|
| 1 | Generate the scan blocks ourselves instead of replaying profiles | any dpi/area, no stale tables | low if built from the rules below | [V rules] |
| 2 | Single-channel gray (`1e 41` byte 13 = 01) | 1/3 of the data → about 3× faster transfer for gray/line-art | low | [V code, I result] |
| 3 | Real 400/450/800/2400-line resolutions | true dpi instead of host scaling | low | [V captures] |
| 4 | 16-bit output (`1e 41` byte 8 bits 0–1 = 00) | 48-bit colour / 16-bit gray | low | [V hardware: the vendor's calibration passes] |
| 5 | Larger transfers (`1e 41` bytes 18–19) | fewer USB round trips | low (stay ≤ 51 200 B) | [V] |
| 6 | Read-only diagnostics: `1e 95`, `1e 97`, `1e 8f`, `1e 83` type 8 | NV/counter/lamp-hour display, upload check | very low | [V code] |
| 7 | Auto power-off setting (`1e 8e` function 0x0010) | user control of the 120 min ErP timer | low (one NV write) | [V code] |
| 8 | Lamp handling (`1e a1`/`1e a2`, sleep) | no needless re-warm, documented sleep | low | [V] |

### 1. Building the low-level blocks (rules from the firmware and 13+3 vendor-app captures)

| Setting | Rule | |
|---|---|---|
| sensor mode `1e 11` (and `1e 46` byte 0) | 4 → 400 px/inch, 3 → 600, 2 → 1200 (5 probably 200, 0/1 unknown) | [V] |
| X pixels | `(end − start) / DIV[(b8 >> 4) & 7] × (b8 & 0x80 ? 3/4 : 1)`, DIV = 1, 2, 4 … 128. The "×3" in the code, together with the 4 from index 2, gives 3/4 of the mode density, e.g. 0xA1 = ¾ | [V] |
| Y lines | `1e 41` byte 10 = 2400 / dpi_y (motor steps per line) | [V] |
| line period `1e 21` | mode 4: 0x1200 (100), 0x0C00 (200), 0x0D00 (300); mode 3: 0x1140 (600); mode 2: 0x21C0 (1200) | [V traces] |

New vendor-app captures (main agent, 2026-11-07) confirm the rules, and that X and Y may differ:

| Requested | Block (X×Y) | `1e 11` | b8 | b10 | Check |
|---|---|---|---|---|---|
| 400 dpi | 450 × 400 | 3 | 0xA1 | 6 | 600 × ¾ = 450; 2400/6 = 400 |
| 800 dpi | 1200 × 800 | 2 | 0x01 | 3 | 1200 / 1; 2400/3 = 800 |
| A4 600 | 600 × 600 (4961 × 7016) | 3 | 0x01 | 4 | 600 / 1; 2400/4 = 600 |

So the reachable native grid is:
- X: {1200, 600, 400, 200?} / 2ⁿ, and ¾ of each (900, 450, 300, 225, 150 …);
- Y: 2400 / n, n = 1…255 (2400, 1200, 800, 600, 480, 400, 300 …).

The vendor picks the next native X ≥ the request and scales on the host. Our driver can do the same, and
can offer exact native values. The colour line offsets (G +2·dpi_y/100, R +4·dpi_y/100 lines) follow
the Y resolution [V for 100–1200].

- **Benefit:** any resolution and area without recording a profile.
- **Risk:** low as long as the line period and the speed table are the vendor's values for the same Y
  step count.
- **Test:** reproduce each recorded profile from the rules and byte-compare it with the recording in
  the simulator (`go test ./v370/`); then one real scan at 400 dpi.

### 2. Single-channel gray
- Final-pass `1e 41` byte 13 = 01. Expect `px × 1` bytes per line (8-bit).
- Which channel or lamp mix it delivers is [U]: try it with a colour test chart.
- **Test:** copy the 300 dpi profile, change only the final `1e 41` and the expected byte counts, and
  scan a small area (e.g. 50 × 50 mm).

### 3. Real resolutions
Use the rules in §1. 2400 dpi vertical (b10 = 1) with mode 2 and the 1200 dpi line period moves the
motor at **half** the 1200-dpi step rate, so the mechanics are not stressed [I]. Test on a 10 × 10 mm
area.

### 4. 16-bit
- Final-pass byte 8 low bits 00. Twice the data; lines per transfer must be lowered to stay ≤ 51 200 B.
- Whether the shading/gamma uploads must change for a 16-bit image pass is [U].
- **Test:** small area, compare it with the 8-bit scan.

### 5. Bytes per transfer
- Bytes per transfer = lines per transfer × bytes per line; the firmware adds an 8-byte trailer.
- The firmware does not check it, and the hardware buffer size is [U]. The vendor never exceeds
  51 200 B.
- Keep ≤ 51 200 B; raising lines per transfer up to that limit is safe.

### 6. Read-only diagnostics (none used by the vendor)
- `1e 95` → 128 bytes = the whole NV memory: USB identity, power-off minutes, counters, clock,
  factory constants. Back it up once; it is a useful record of the unit.
- `1e 8f` functions 1/3/5 → flatbed lamp minutes [I] / TPU lamp minutes [I] / scan counter [V].
- `1e 83` + `08 <addr u32 LE> <len u24 LE>` → reads `len` bytes of memory directly.
  - Use it only for internal RAM 0x000–0xFFF and external RAM 0x02000000–0x0200FFFF (e.g. compare
    the uploaded image with the file, or read status variables from firmware-map.md §3).
  - Do not read register areas: reads may have side effects.
  - Start with len ≤ 512.
  - [V code; verified on the device 2026-11-08]
- `1e 90` → 256 bytes of front-end registers [I].

### 7. Auto power-off
- Read: `1e 8f` + `10 00` → u32 minutes (120 now).
- Write: `1e 8e` → `06`, then `10 00 <u32 LE minutes>` → `06`. Range 0–480; 0 = off. Stored
  persistently, only when the value changes.
- `1e 85` and `1e 93` polling do **not** reset the timer, so an idle button watcher lets the scanner
  switch itself off after 120 min [V].
- **Test:** read, write 120 again (no-op), read back; only then try another value with the owner's OK.

### 8. Lamp
- Lamp sleep after ≈ 14.3 min idle [V]; any non-exempt command wakes it.
- Before a scan:
  - check `1e a1` (00 off, 01 on);
  - if it is 01, sending `1e a2 00` and then `1e a2 01` reproduces the vendor's freshly-lit start;
  - after `1e 46`, poll `1e 04` until it is not 02 (already in our driver).

## B. Firmware-side changes

Verified on the device: data-only changes (version string), and the **LED patch** in use since
2026-11-08 ([`../firmware-patches.md`](../firmware-patches.md)): LED off state, buttons working while
the LED is orange, busy blink in the selected colour, colour kept across jobs. It touches only the LED
output bits and two RAM bits and is applied in memory to the pinned vendor build (no firmware file is
distributed). Other low-risk candidates: identity and USB strings. Nothing that touches motor, lamp or
CCD code is recommended (§C). All gains in §A are reachable without firmware changes.

## C. Risky internals: explained, **do not change**

| Area | How it works | Where |
|---|---|---|
| **Motor speed table** (`1e 84` type 4) | u16 step periods. The vendor uploads its own table to external RAM 0x02010000 (768 B or 2048 B, or 2 B = one constant period for the image pass, e.g. 0x0120). The firmware points motor register 0x37F810 at it and puts its length into 0x37F814. For internal moves the firmware copies its own tables (payload 0x4CF4 + 0x4EF4: 2500 → 222) to the same place (0x02001D1E). Smaller value = faster step. | 0x02006B42 type 4, 0x02001CA8, tables payload 0x4CF4–0x5473 [V] |
| **Carriage return / homing** | firmware moves with the built-in tables, home sensor 0x37F822 bit 3 [I]; at boot 0x0200261E | [V code, I names] |
| **Motor set-up `1e 01`** | u32 step count, two u16 acceleration/deceleration lengths, byte 10 = direction + microstep mode (0–4) → motor current tables 0x37F880… | 0x02006058 [V] |
| **Lamp on-time `1e 22`** | per line; 0 = off, ≥ line period = always on (the vendor's value); in between = PWM | 0x020062EC [V] |
| **Line period / exposure `1e 21`** | u24 period → line timer; three exposures = period − u16 | 0x020061E0 [V] |
| **CCD modes `1e 11`** | 64-word clock waveform per mode + front-end registers; modes 0/1 never used | 0x02009152, payload 0x8830 [V] |
| **Lamp measurement `1e 46`** | adjusts front-end gains until levels > 0x80; drops other commands while running | 0x0200B20A–0x0200BE1A [V] |
| **Warm-up** | 0x0200AB64 and the idle lamp check 0x0200A08C; status bit 0x02 while it runs | [V names I] |
| **NV writes** | 1e 45, 1e 96, 1e 98, 1e 8e (other than 0B/10) can wipe counters, factory values or the USB identity | **never send** |

Changing the speed tables, step counts or periods risks stalls, lost steps or a carriage hitting the end
stop. Changing lamp/exposure settings risks over-driving the lamp. Use only the vendor's recorded values for
these.

## D. Unknown / next steps
- What `1e 41` bytes 11/12/15/16/17 do.
- Sensor modes 0, 1 and 5.
- Which channel the single-channel mode delivers.
- The hardware limit on bytes per transfer.
- Whether 16-bit image passes need different shading uploads.
- What `1e 68`, `1e fa` and `1e 89/8a` are for (`1e de` = LED, see `../firmware-patches.md`).
