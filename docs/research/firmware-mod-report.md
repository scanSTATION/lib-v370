# Can the V37/V370 (GT-F740, 04b8:014a) firmware be modified — e.g. to scan faster?

Static analysis only. No scanner was contacted; nothing was uploaded; no repo
was modified. Target: `firmware/esfwdd.bin`
(SHA-256 `2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954`).
Payload = file bytes `[256:65792]` (65 536 B, SHA-256
`f790a2dfdf5a120e56122bae77d6d68cf8a573ca97b487afcaf262d4b28525f6`),
sum8 = `0xaf`, trailing file byte `0xce`.

Builds on `libv370/docs/research/firmware-report.md` and `protocol.md`.
Scripts (stdlib Python, run with `python3 -I`):
`crypto_scan.py`, `xref.py`, `s1c33dis.py` (copied from the repo tools).
Confidence markers: **verified** (seen directly in this analysis),
**inferred** (reasoned from evidence), **unknown**.

---

## Q1 — Integrity protection of the upload

**There is no cryptographic or structural integrity check beyond the 1-byte
sum. Verified.**

- **No crypto constants anywhere in the payload (verified).**
  `crypto_scan.py` found *none* of: SHA-256 IV/K, SHA-1 IV, MD5 IV/T, AES
  S-box (`63 7c 77 7b …`) or Te-table, CRC-32 table entries
  (`77073096`, `ee0e612c`) or the `04c11db7` polynomial, in either byte order.
  No 64-byte window has entropy > 7.0 (excluding the `0xFF` pad region), so
  there is **no RSA modulus, ECC key, or packed/encrypted blob** hiding in the
  image. A signature/hash verifier would need one of these and none exists.
- **The transmitted checksum is only `sum(payload) mod 256` (verified).**
  Computed `0xaf`, matching the byte the host sends (`protocol.md §3.2`). This
  is the *only* integrity value in the upload.
- **The firmware does not verify itself at start-up (verified).** Full
  disassembly of the reset path (`s1c33dis.py payload.bin 0x1000 0x10c0`):
  it zeroes r0–r15/%psr/%ahr/%alr, sets SP=`0x1000` and base r8=`0x200`,
  clears internal RAM `0..0xFFF`, copies the `0x200`-byte vector table and
  `0x3d0` bytes of ISR stubs into RAM, unlocks and writes TTBR
  (`0x4812d`←`0x59`, `0x48134`←0), then `call 0x0200140c` (main). **No loop
  reads the image range or accumulates a sum/CRC; the 0xCE byte is never
  referenced.** There is no self-checksum.
- **What the ROM loader checks (inferred).** `1b 06` is in no RAM-firmware
  dispatch table, so the mask-ROM loader handles the upload. Given that the
  only value on the wire is the sum8 and the loader ACKs with `06`, the loader
  almost certainly checks **only the 1-byte sum** (and the fixed `01 00 01 00`
  block it expects). It cannot check a hash/signature it never receives.
- **EDLA header / `01 00 01 00` (verified + inferred).** The 256-byte `EDLA`
  header is **not transmitted** during upload; only its 4-byte field at `0x4c`
  (`01 00 01 00`) is sent after `1b 06`. The header has no checksum, size,
  address or model field (identical across all six `esfw*.bin`). It is a
  host-side container tag, irrelevant to on-device integrity.
- **Trailing byte `0xCE` (verified not-a-checksum + unknown meaning).** Prior
  work ruled out sum/xor/neg/not and all CRC-8 variants; it is **not sent to
  the scanner** (`protocol.md §3.2`). It plays no role in device-side
  integrity. Meaning still unknown (likely a host container artifact).

**Bottom line (Q1):** the only barrier to a modified image is recomputing the
1-byte sum. No signature, hash, MAC, CRC, or self-check — on either the ROM or
the firmware side (sum8 verified; absence of crypto verified; ROM check
inferred).

---

## Q2 — Does the firmware authenticate the host?

**No. A correctly-summed modified image will run and answer normally. Inferred
(from absence), consistent with all captures.**

- The firmware implements a plain USB 2.0 vendor-class device: it answers
  GET_DESCRIPTOR and a bulk command interpreter (`1b`/`1e` dispatch tables).
  There is **no challenge/response, key exchange, session token, or nonce** in
  the protocol captures or in the command set (`protocol.md §§2–8`). The only
  "session" is `1e 93 01` (open) / exclusivity, which is a mutex, not auth.
- No crypto primitives exist in the image (Q1), so runtime cryptographic
  authentication is impossible.
- The host never proves identity; the scanner replies to any client that
  claims the interface. Our own `v370` driver already drives it.

**Bottom line (Q2):** a modified image is **not** refused at runtime for
authentication reasons. It will fail only if the modification breaks the code
that answers USB/commands.

---

## Q3 — Safety: can a bad image brick the scanner? Physical risks?

### Bricking: effectively impossible (verified by design + observation)
- Firmware lives **only in volatile RAM at `0x02000000`**; it is re-uploaded
  by the host on every cold start (`protocol.md §3.2`; status `28 00` when
  firmware is missing). There is no on-board flash for firmware.
- A **power cycle always returns to the mask-ROM loader**, which re-accepts an
  upload. This is confirmed indirectly: `scripts/hwsim-test.sh --disable-real`
  (`authorized=0→1`) made the real scanner drop firmware and reload it on the
  next session (`protocol.md §8`).
- Therefore a broken/crashing image is a *soft* failure: reset, upload a good
  image, done. **You cannot permanently brick it with a bad upload.**
  (The scan-counter register `0x0005` is non-volatile, so *something*
  persists, but it is not firmware.)

### Physical risks while a bad image runs (inferred — this is where real damage is possible)
The ASIC drives the stepper motor, lamp and CCD/CIS from registers in the
`0x37xxxx` block (65 code references; e.g. the `1e41` handler writes
`0x37eec6/0x37eec8` (line start/width) and `0x37f460` (speed/mode), verified).
The firmware — not the ROM loader — enforces the operating envelope, so a
wrong image can physically misdrive the mechanism:

| Hazard | Mechanism | Where limits live (inferred) |
|---|---|---|
| **Carriage into the end stop / stall** | motor commanded past travel or not homed | home-sensor read + step counting in the motor/home routines; startup reads a strap/home-style bit at `0x37fc02` (verified read, purpose inferred). A wrong motor routine can skip steps or crash the carriage. |
| **Motor overcurrent / lost steps** | step period set too short (ramp floor too low) | acceleration ramp tables at payload `0x4bf4/0x4cf4/0x4ef4/0x4ff4/0x51f4/0x53f4…` (referenced by code, verified) + the host's `1e41` word-5 rate written to `0x37f460`. |
| **Lamp over-driven / left on** | lamp PWM/enable held high, warm-up/ timeout bypassed | lamp control register in `0x37xxxx` + the warm-up wait (see Q4). A stuck-on lamp risks heat; an LED/CCFL has finite life. |
| **CCD/CIS over-integration** | integration time / gain set out of range | per-line timing in `1e41` + ASIC; physically bounded by the sensor. |
| **Overheating** | any of the above sustained (motor stalled, lamp stuck) | no thermal cutoff seen in firmware; relies on correct timing constants. |

These are **mechanical/optical risks, not bricking**: worst realistic outcome
of a careless change is a jammed carriage (needs manual freeing / power cycle),
a buzzing stalled motor, or accelerated lamp wear — not a dead board.

### Risk ranking of firmware regions to touch
- **Low risk** (pure data, no actuation):
  - Identity strings `GT-F740/GT-S640 "1.00"` @payload `0x558c/0x55a8`
    (verified; handler `0x6f00`).
  - USB descriptors @`0x5480` (strings, bcdDevice). Breaking these only breaks
    enumeration (recoverable by re-upload), not hardware.
  - Pure host-reported constants (e.g. `1e 9f`=`0x10`).
- **Medium risk** (timing that the hardware tolerates over a range):
  - Warm-up wait / idle timeouts (Q4) — too short = poor color; not dangerous.
  - USB transfer chunking.
- **High risk** (directly move the mechanism / drive the lamp):
  - Motor ramp tables (`0x4bf4…0x53f4`) and anything writing `0x37xxxx`.
  - The `1e41/1e01/1e05` motor/pass handlers (`0x66fa`, `0x6058`, …).
  - Home/end-stop sensor logic and lamp control.

---

## Q4 — Where time is spent, and what controls it

Timing budget from captures (`protocol.md §8`): power-on→ready ≈ **13.6 s**
(≈ 8 s lamp warm-up after firmware boot); a 300-dpi A4 scan ≈ **25 s** =
calibration passes + ~12 s final pass (28 MB) + ~10 s carriage return.

| Time sink | Controlled by | Evidence | Change needed | Est. gain | Risk |
|---|---|---|---|---|---|
| **Lamp warm-up (~8 s)** | firmware wait / lamp-stabilization (inferred); likely tied to lamp level, not a clean fixed constant | no single ~8 s timer constant stood out; large constants are mostly `0x37xxxx` regs & code addrs (`xref.py`) | firmware change (shorten/skip wait) | up to ~8 s per cold start | **high** – under-warmed lamp = color/brightness errors; physically bounded by lamp stabilization |
| **Carriage return (~10 s)** | firmware motor ramp/speed tables | return uses the same motor ramps `0x4bf4…0x53f4` (referenced by code, verified); status `58 01→1a 01→18 01` | firmware change (raise return speed = lower ramp floor) | few seconds | **high** – stall / end-stop crash if floor too low |
| **Scan motor speed per line** | **mostly host parameter** `1e41` word 5 = `0x80xx`, `xx = 2400/dpi` | verified: `1e41` handler `0x66fa` ORs this with `0x80`/`0x90` and writes it to ASIC reg `0x37f460`; word 0/1 → `0x37eec6/0x37eec8` | **no firmware change** — change the host-sent `1e41` block | bounded | lower xx = faster line rate, but **physically bounded by CCD integration time / lamp brightness** (darker, noisier) |
| **Calibration passes (~3 s + tables)** | **host-driven** | host issues `1e46` pass setups, reads `1e47`, computes shading/gamma, uploads via `1e84` (`protocol.md §3.4`) | **host/driver change** (fewer/smaller passes) | ~1–3 s | medium – skipping shading = stripes/color cast |
| **USB chunking (lines/transfer)** | **host parameter** `1e41` word 9 | verified word meaning (`protocol.md §8`); 20@100, 6@300 dpi | **no firmware change** — raise lines/transfer | minor (overhead only) | low |
| **Final-pass data volume** | host resolution/area (`1e53/1e57` block) + fixed sensor width rounding | `protocol.md §6, §8` | host params | large if you lower dpi/area | none (quality trade) |

Key finding: **the per-line scan rate is a host parameter** (`1e41` word 5),
not a firmware constant — the firmware just forwards it to the motor/timing
register. So "scan the pixels faster" needs **no firmware change**; it is
limited by physics (exposure), not by a software lock.
The genuinely *firmware*-bound costs are **lamp warm-up** and **carriage
return speed**, and both are the risky ones.

---

## Q5 — A minimal, reversible experiment (NOT executed here)

**Goal: prove you can run a self-built image, with zero hardware risk.**
Change only the **identity version string** and watch the `1b 13` reply change.

1. In a *copy* of the payload, change the GT-F740 version `"1.00"`:
   - payload offset **`0x55a4`** (= file offset **`0x56a4`**), 4 bytes
     `31 2e 30 30` → e.g. `31 2e 30 31` (`"1.01"`). Verified these are the
     bytes the identity handler `0x6f00` sends (28 B from `0x558c`, selected
     when `[r8+0x1be]&7==1`, which is the V37/V370 variant).
   - Only the F740 string matters for this device (the S640 copy at `0x55a8`
     is for the sibling variant and is not sent).
2. **Recompute the checksum byte.** The upload trailer must equal
   `sum(modified 65 536-byte payload) mod 256`. For `"1.00"→"1.01"` (one byte
   `0x30→0x31`, +1) the sum8 goes `0xaf → 0xb0` (verified by recompute). Send
   `0xb0`, not `0xaf`. (The host always recomputes this; there is no need to
   keep `0xaf`.)
3. Upload exactly as `protocol.md §3.2`: `1b 06`→`06`, `01 00 01 00`, 65 536 B,
   checksum byte→`06`, `1b 16`+`80`→`06`,`06`, wait ~2.7 s.
4. **Verify acceptance:** issue `1b 13`; a ready scanner must reply the new
   28-byte identity containing `"1.01"`. Status `1b 03` should reach `18 01`.
   If the string changed, your modified image is running — integrity is sum8
   only, confirmed on the real device. (Reversible: re-upload the stock image,
   or just power-cycle; firmware is RAM-only.)

This experiment touches **zero** actuation code, so it carries no mechanical
risk and is fully reversible.

### What must NEVER be changed (without the real scanner in front of you and a willingness to risk a jam)
- Motor acceleration ramp tables `0x4bf4/0x4cf4/0x4ef4/0x4ff4/0x51f4/0x53f4…`
  and ramp floors.
- Any code writing the `0x37xxxx` ASIC registers (motor/lamp/CCD):
  `1e41` (`0x66fa`), `1e01`, `1e05`, `1e21/1e22` pass/geometry handlers.
- Home/end-stop sensor handling and the lamp warm-up/timeout logic.
- The C-runtime start-up (`0x1000`), vector/trap table (`0x0`), TTBR writes,
  and the USB device stack (`~0x3476–0x35xx`, descriptors `0x5480`) — breaking
  these stops enumeration/boot (recoverable by re-upload, but no scanning).

---

## Risk matrix (change → risk → reversible?)

| Change | Hardware risk | Functional risk | Reversible? |
|---|---|---|---|
| Identity version/string (`0x55a4`/`0x558c`) | none | none (cosmetic) | yes (RAM-only; re-upload) |
| USB descriptor strings / bcdDevice (`0x5480`) | none | enumeration may break → no scanning | yes |
| `1e41` word 5 (line rate) **via host**, no fw edit | low→med (faster motor, dimmer lines) | darker/noisier image if too fast | yes (just send stock params) |
| Lines/transfer `1e41` word 9 **via host** | none | stalls/timeouts if absurd | yes |
| Fewer calibration passes **via host** | none | stripes / color cast | yes |
| Shorten lamp warm-up (firmware) | **high** (color/brightness; lamp) | washed-out scans | yes (re-upload) but risks bad scans |
| Raise carriage-return speed / lower ramp floor (firmware) | **high** (stall, end-stop crash, jam) | failed scans | yes electrically; a jam needs manual clearing |
| Edit motor/ASIC handlers or ramp tables | **high** (motor/lamp/CCD misdrive, overheating) | broken scans | firmware yes; mechanical damage maybe not |
| Edit start-up / vectors / USB stack | none mechanical | device won't boot/enumerate | yes (re-upload / power cycle) |

## Ranked speed-up opportunities

1. **Lower-resolution / smaller-area scanning** — *host params* (`1e53/1e57`).
   Biggest wins, no risk, no firmware change. (Already how dpi is chosen.)
2. **Faster per-line rate** — *host param* `1e41` word 5 (`0x80xx`,
   `xx=2400/dpi`). No firmware change. Bounded by CCD exposure/lamp: push `xx`
   down and images get darker/noisier; modest safe headroom, low mechanical
   risk. **Best "make scans faster" lever that needs no firmware edit.**
3. **More lines per USB transfer** — *host param* `1e41` word 9. Trims
   per-transfer overhead only; small gain, low risk.
4. **Fewer/one calibration pass** — *host/driver change*. Saves ~1–3 s at the
   cost of shading/color quality. No firmware change.
5. **Shorter lamp warm-up** — *firmware change*, saves up to ~8 s per cold
   start, **high risk** to color fidelity and lamp; physically bounded.
6. **Faster carriage return** — *firmware change* (ramp floor), saves a few
   seconds, **high risk** of stall/jam.

**Overall:** the scanner firmware has **no anti-modification protection**
(only a 1-byte sum, no signature/hash/self-check), and **cannot be bricked**
(RAM-only, ROM loader always returns). The real danger of modding is
**mechanical/optical** (motor/lamp/CCD), concentrated in the `0x37xxxx`
handlers and the motor ramp tables. For speed, **start with host parameters**
(`1e41` rate, lines/transfer, resolution) — they give most of the achievable
gain with no firmware risk; firmware-only levers (warm-up, carriage return)
are small and dangerous.
