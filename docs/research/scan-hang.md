# Scan hang at `1e ff` (lamp measurement)

Observed on the real scanner on 2026-11-07, analysed statically from the
firmware by a research subagent, fixed and re-verified the same day. Full
report: [`scan-hang-report.md`](scan-hang-report.md) ([V] = read in code or
traces, [I] = inferred); sequence tool `tools/firmware/seq.py`.

## Cause [V]

- `1e 46` + 8-byte parameter starts the lamp measurement (up to 10
  rounds). While it runs, a loop in the firmware (0x0200b416) reads host
  commands itself and answers **only `1e 04` (→ `02` = busy) and `1e 92`**.
  Every other command is **dropped without a reply** and is not queued
  (`1e ff`, `1b 03`, `1e 85`, `1e 93`, …).
- The vendor app polls `1e 04` until the reply is not `02` (1–5 polls in all
  13 traces), then sends `1e 92` and `1e ff`. Our replayed profiles sent
  exactly the recorded number of polls (2), so a longer measurement made
  `1e ff` vanish → the scan waited forever.
- While the loop runs, buttons aren't sampled at all, and the open session
  blocks Power — that's why Power seemed dead.

## Fix

`RunProfile` replaces a recorded run of `1e 04` polls with "poll `1e 04`
every 130 ms until the reply is not `02`" (timeout 60 s), sending nothing
else in between. Result `01` = levels OK; `00`/`03` [I] = too dark, logged
as a warning. Verified on the scanner: power off → on → firmware upload →
two PDF button scans, both completed.

## Other findings

| Command | Meaning | Status |
|---|---|---|
| `1e 04` | lamp measurement result (RAM 0x443): 02 busy, 01 OK, 00 level too low, 03 unknown counter condition | V (code), meaning partly I |
| `1e a1` | light-source state (RAM 0x428 low 3 bits): 0 off, 1 flatbed lamp on, 2/3 TPU | V code, I meaning |
| `1e a2` + 1 byte | set the light source (00 off, 01 flatbed on, 02/03 TPU); from state 1, `01` does nothing | V code; `00` never seen in a capture |
| `1e 65` | error flags (RAM 0x42e), 00 = no error | V |
| `1e ff` | sends 4 stored bytes (RAM 0x444–0x447; 4th = rounds needed) + `06`; never waits | V |
| lamp wake | any command except `1e 45/53/65/85/93`, `1b 03/13/40` | V |

Recovery without a power cycle [V mechanism]: wait ~5 s, poll `1e 04`
until it is not `02`, then continue (`1e 92`, `1e ff`) or end the session
(`1e 93` + `00`). No late reply arrives.
