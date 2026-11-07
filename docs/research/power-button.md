# Power button, sessions and auto power-off

Static analysis by a research subagent (firmware disassembly, the vendor app
6.7.70 source) on 2026-11-07, then verified on the real scanner by the main
agent. Full report: [`power-button-report.md`](power-button-report.md).
(The corrected S1C33 disassembler — bit-test/set/clear instructions were
mislabelled before — is now `tools/firmware/s1c33dis.py`.)

## Verified on the scanner (2026-11-07)

| Situation | Short press | Hold ~3 s |
|---|---|---|
| host session open (`1e 93 01`) | reported by `1e 85` as **01**, scanner stays on | reported as 01, stays **on** (blocked) |
| no session | stored, reported as 01 on the next read; LED blinks | **switches off** |

- Printer/Email/PDF/Power are reported by `1e 85` **with or without a
  session**; the firmware does not need a session for buttons. Our watcher
  therefore polls without a session and opens one only around scans, so the
  Power button keeps working while our software runs.
- A press must be firm (~0.25–0.5 s: firmware debounce of 10 samples).
- The first press after a long idle time / right after the lamp starts may
  only wake the scanner and not be reported — press again.

## From the firmware (static analysis)

| Finding | Where | Status |
|---|---|---|
| `1e 85` handler reads a latch byte (RAM 0x450): bit 4 → 01 (Power), 5 → 02, 6 → 03, 7 → 04; clears it; no session check | `0x02007344` | verified (code), values verified on device |
| inputs sampled active-low from GPIO 0x37F84E bits 4–6 and 0x37FD05 bit 7; 10 stable samples | `0x02004830` | verified (code) |
| Power-off routine: only called by the auto power-off timer; pressing Power switches off in **hardware** | `0x0200c966`, caller `0x0200c8bc` | inferred |
| `1e 93 01` sets / `1e 93 00` clears bit 7 of 0x37FD00 — a "Power blocked" line to the power circuit | handler `0x02006466` | code verified, meaning inferred — behaviour verified on device |
| **Register 0x0010 = auto power-off time in minutes** (120 = 2 h; 0 = off; > 480 clamped; stored non-volatile). Read `1e 8f` + `10 00`; write `1e 8e` + `10 00 <u32 LE minutes>` | `0x2007512` / `0x2007422` | code verified; read verified on device; **write never sent** |
| `1e 9f` → `10` = "auto power-off supported" | | verified |
| Polling (`1e 85`, `1e 53/65/93`, `1b 03/13/40`) does **not** count as activity: the scanner switches itself off 120 min after the last other command, session or not | | verified (code) |
| Lamp sleep: fixed ~14 min idle timer; woken by a button press or any non-exempt command; not configurable | | verified (code) |

Never send `1e 8e` with functions other than `000b` (clock) / `0010`
(power-off time): 0/1/3/5/6/A overwrite counters, FFFF runs a memory
self-test that overwrites counters. Never send `1b 20`–`1b 25`.

Sources: the vendor app 6.7.70 source (`ESCICommand.h:283` `kLogFunctionPowerOffTime = 0x0010`,
`ESCICommand.cpp:848-971`, `ESCIAccessor.cpp:965`, `PoweroffTimer.hpp:49-71`;
archive as in [README.md](README.md)), firmware SHA-256
`2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954`, V370
plugin SHA-256 `7d0c57d8bd186dbb7a0262b3ce94d28e8c36ec5674457282fc24840d7d35c0ec`.
