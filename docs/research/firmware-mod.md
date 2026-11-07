# Can the firmware be modified, and is it safe?

Static analysis by a research subagent on 2026-11-07 (nothing was uploaded,
no scanner access). Full report: [`firmware-mod-report.md`](firmware-mod-report.md);
scripts: `tools/firmware/crypto_scan.py`, `tools/firmware/xref.py`.

## Answer

**No anti-modification protection, and the scanner cannot be bricked by a
bad upload.** The real risks of modifying are mechanical/optical.

| Question | Finding | Status |
|---|---|---|
| Integrity check of the upload | only the 1-byte sum (payload bytes mod 256) — the only value on the wire | **verified on the device** (modified image accepted, see below) |
| Signatures, hashes, CRC, MAC | none: no SHA/MD5 IVs, AES S-box, CRC tables or high-entropy key material anywhere in the 64 KiB | verified |
| Self-check at start-up | none: the reset path clears RAM, copies vectors, calls `main`; never reads its own image | verified |
| Host authentication | none (plain vendor-class USB, no challenge/response) | inferred |
| Bricking | impossible by upload: firmware lives in RAM, every cold start returns to the ROM loader | verified by design + observed |
| Physical risks | the firmware drives motor, lamp and CCD via ASIC registers `0x37xxxx`: carriage into the end stop / stalls, lost steps, lamp overdriven or stuck on, CCD over-integration; no thermal cut-off found | inferred |

Low-risk to change: identity strings (`0x558C`), USB descriptors (`0x5480`).
High-risk: motor ramp tables (payload `0x4BF4`, `0x4CF4`, `0x4EF4`, `0x4FF4`,
`0x51F4`, `0x53F4`), handlers writing `0x37xxxx` (`1e 41` at `0x66FA`, `1e 01`,
`1e 05`, `1e 21/22`), home-sensor and lamp warm-up logic, start-up/vectors/USB.

## Speed

The line rate is a **host parameter**, not locked in firmware: `1e 41` word 5
(`0x80xx`, xx = 2400/dpi) is written directly to ASIC register `0x37F460`
(handler `0x66FA`). So faster scanning needs no firmware change — but it is
bounded physically (CCD exposure, lamp brightness: faster = darker/noisier).

| Rank | Speed-up | Kind | Gain | Risk |
|---|---|---|---|---|
| 1 | lower resolution / smaller area | host parameter | largest | none |
| 2 | faster line rate (`1e 41` word 5) | host parameter | moderate | image quality |
| 3 | more lines per USB transfer (`1e 41` word 9) | host parameter | small | low |
| 4 | fewer calibration passes | host/driver | ~1–3 s | quality |
| 5 | shorter lamp warm-up (~8 s) | firmware change | up to ~8 s | high (colour stability, lamp) |
| 6 | faster carriage return (~10 s) | firmware change (ramp tables) | a few s | high (stall/jam) |

## Experiment: modified firmware runs (verified 2026-11-07)

Changed one byte, file offset `0x56A7` (`0x30` → `0x31`, version string
"1.00" → "1.01"); payload sum `0xAF` → `0xB0` (computed by the driver);
SHA-256 of the modified file
`4aa3727aca5076dff4f021f23033f3e7a071bfbdb81a7904733c8d5e5ceb98b2`.

```bash
python3 -I -c 'import sys;b=bytearray(open(sys.argv[1],"rb").read());assert b[0x56a4:0x56a8]==b"1.00";b[0x56a7]=0x31;open(sys.argv[2],"wb").write(b)' \
  firmware/esfwdd.bin /tmp/esfwdd-1.01.bin
# power cycle the scanner, then:
v370 info -firmware /tmp/esfwdd-1.01.bin -allow-unknown-firmware
```

Result on the real scanner: upload accepted, identity
**GT-F740 version 1.01**, status `18 01`, registers readable as usual. After
a power cycle the stock firmware was uploaded again → "1.00". This confirms
on the device that the 1-byte sum is the only check. The modified file is
not kept in any repository.
