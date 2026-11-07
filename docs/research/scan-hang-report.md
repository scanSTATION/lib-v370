# V370 scan hang at `1e ff`: firmware analysis

Date 2026-11-07. Static analysis only: firmware disassembly `fw.asm` and the existing traces. No USB access.
Markers: **[V]** verified in code or traces, **[I]** inferred, **[U]** unknown.

## TL;DR

**Cause [V]: `1e ff` is sent while the scanner is still measuring the lamp.**

- The scanner starts the measurement when it receives `1e 46` (8-byte parameter). From then until the
  measurement finishes, it reads host commands itself (`0x0200b416`), but it only answers **`1e 04` and
  `1e 92`**. Every other command header is read and then **thrown away, with no reply and no deferral**
  (`1e ff`, `1b 03`, `1e 85`, `1e 93` included).
- During the measurement, `1e 04` answers **`02` (busy)**. When it is done, `1e 04` gives the result,
  normally `01`.
- **the vendor always polls `1e 04` until it stops answering `02`, and only then sends `1e 92` and `1e ff`**:
  - 13 of 13 scan traces do this, with 2 to 6 polls;
  - the 4th byte of the `1e ff` reply is the number of measurement rounds, and it grows with the number
    of `02` polls: `03` after 1×`02`, `05` after 2×, `07` after 3×, `0a` after 5×.
- Our profile replays exactly the recorded count (2 polls). In the failing scan both answered `02`, so
  our `1e ff` arrived during the measurement and was swallowed. The measurement then ended (it is
  bounded, at most 10 rounds). That is why `1e 93 00` got its ACKs later.

**Fix [V for the mechanism]:** after `1e 46`, poll `1e 04` until the reply is not `02`, with a timeout.
Send nothing else meanwhile. Only then send `1e 92` and `1e ff`.

## Details

### `1e 04` @0x0200b5d8 [V]
Returns RAM byte 0x443. Written only by the `1e 46` handler (`0x0200b20a`):
1. ACK.
2. Read the 8-byte parameter.
3. ACK.
4. Set `[0x443] = 2`.
5. Run the measurement (`0x0200b314`).
6. Set `[0x443]` to the result:
   - `1` = all three levels in `[0x444..0x446]` are above 0x80 (OK);
   - `0` = a level is too low;
   - `3` = when `[0x43E] ≤ 0x1D` [U meaning].

Meaning of 01/00/03 [I]: 01 = OK, 00 and 03 = measurement problem.

### `1e ff` @0x02005e18 [V]
No wait at all:
1. Send 4 bytes from RAM `0x444..0x447` (R, G, B level and the round counter `[0x447]`, which
   `0x200b416` increments).
2. ACK.
3. Clear `[0x447]`.

The handler cannot hang. The command is lost only when it arrives during a busy loop that discards it.

### The measurement loop @0x0200b416 [V]
```
repeat up to 10x:
   measure (0x200b66a); [0x447]++
   if [0x440]==7 (all channels done) -> exit
   if host sent something (0x6b8): read 2-byte header (0x2003e40)
        1e 04 -> answer [0x443] (=02)
        1e 92 -> answer 12 bytes (0x200668c)
        anything else -> DROPPED (no reply, no "pending" flag)
```

The other busy loops are different. They use the dispatcher at `0x02007da6`, which answers
`1b 03/13` and `1e 53 57 77 85 86 87 8E 8F 90 92 93 95 96 97 A1 B0`. All other commands there are
**deferred** (`[0x44E]=1`) and executed later by the main loop. Only this measurement loop drops
commands.

### `1e a1`, `1e a2`, `1e 65` [V code, I meaning]
- **`1e a1`** (0x02006e98): returns `[0x428] & 7`, the **light-source state**.
  - It is set by four functions:
    - 0 = `0x2002bd6`: GPIO `0x37F0DD` bit0 off;
    - 1 = `0x2002ba8`: bit0 on, also clears the sleep flag `[0x43C]` and the sleep counter;
    - 2 = `0x2002bfa`: second source `0x37F0DD` bit2 on, GT-F740 variant only;
    - 3 = `0x2002c3e`: second source off.
  - Bits 3–5 keep the previous state.
  - [I] 0 = flatbed light off, 1 = flatbed light on, 2/3 = TPU (film) light on/off.
  - Sleep entry (`0x2004a18` → `0x2002b88`) turns state 1 into 0.
  - The state is restored from the saved previous state in `0x2002b56` (called from `0x20070f0`).
  - Boot ends with `0x2002ba8` (state 1) unless error bit `[0x42E]&4` is set (`0x20014d0`).
- **`1e a2` + 1 byte** (0x02006da8): ACK, read 1 byte, …, ACK.
  - `00` → light off (state 0);
  - `03` → state 3;
  - `01` → flatbed light on: from state 2 it switches over, from 0 or 3 it calls `0x2002ba8`, **from
    state 1 it does nothing**;
  - `02` → TPU.
  - Error flag `[0x3C1]` bit0 blocks it.
  - The vendor always sees `1e a1 → 00` first, so its `1e a2 01` really switches the light on and the
    measurement starts with a freshly lit lamp.
  - In our failing runs `1e a1 → 01`: the light state was already "on" (left from boot or from a wake
    that restored it), so `1e a2 01` was a no-op. [I] This probably explains why the measurement needed
    more rounds. It is not needed for the hang itself: any measurement longer than our 2 polls hangs.
- **`1e 65`** (0x02007876): returns error flags `[0x42E]`. `00` = no error. The firmware tests bits 0, 2
  and 4. Bit 2 = error found at boot or in a carriage check (`0x20022c4`), which skips the lamp-on at
  boot [I].

### Lamp sleep and wake [V]
- Lamp sleep starts after about 14 min idle (see the earlier report).
- Any non-exempt command wakes the scanner (`0x200494a`). Exempt are `1e 45/53/65/85/93` and
  `1b 03/13/40`. So `1e 93 01` does **not** wake it, but the following `1e 8f 1000` does. Our sequence
  sends one, so the wake-up happens in both our sequence and the vendor's.
- The vendor's order (session → clock → status polls `1a01` → identity) is the power-on warm-up, not a
  condition for the lamp. The warm-up runs from firmware start whatever the host does. The lamp is
  switched on by `1e a2 01` [V].

### Power "dead" in that state [I]
- While the measurement loop runs, the main loop does not run, so buttons are **not sampled**: no `01`
  and no other button.
- With the session open (`1e 93 01` was sent at scan start), GPIO `0x37FD00` bit7 is set, so the
  hardware ignores the Power button (see the power report).
- Once `1e 93 00` is processed, bit 7 is cleared and Power should work again. If it still does not,
  check whether that `1e 93 00` really got its ACK **after** the measurement ended. During the loop it
  would have been dropped as well.
- Nothing in `1e a2`, `1e 46` or `1e 01` touches bit 7 [V: the only writers are the `1e 93` handler,
  boot, main init and power-off].

## Host rules (to never hang)

1. **After `1e 46` + its 8 bytes [V]:**
   - poll `1e 04` (1-byte reply) about every 200–500 ms until the reply is not `02`, with a timeout of
     about 60 s (the measurement is at most 10 rounds);
   - send **no other command** meanwhile: no `1b 03`, `1e 85` or `1e 93`;
   - expect `01`; treat `00` or `03` as an error [I];
   - only then send `1e 92` (12 bytes) and `1e ff` (4 data bytes + `06` = 5 bytes).
   - Apply the same rule wherever a profile contains a run of `1e 04` polls: turn it into "poll until
     not 02", like the status-poll runs.
2. Before the scan:
   - `1e 65` must be `00` [V meaning "no error flags"];
   - `1b 03` must be `18 01` (ready).
3. Optional [I, untested]: if `1e a1` is not `00` before `1e a2 01`, send `1e a2` + `00`, then
   `1e a2` + `01`. Each step is `1e a2` → `06`, then 1 byte → `06`. This reproduces the vendor's starting
   state (light switched on fresh). `1e a2 00` is not in any capture, but the handler accepts it (it
   calls the light-off function).
4. Do not poll buttons (`1e 85`) while a scan is running. This was already a rule; it is now also
   proven necessary, because the poll is silently dropped during the measurement.

## Recovery without a power cycle [V for the mechanism]

The measurement ends by itself after at most 10 rounds; the dropped `1e ff` leaves nothing pending.
- Wait about 5 s.
- `1e 04` → 1 byte. Expect `01`/`00`/`03`; if it is `02`, keep polling.
- Then either:
  - continue: `1e 92` → 12 bytes, `1e ff` → 5 bytes, and the rest of the scan; or
  - abort: `1e 93` → `06`, `00` → `06` (this is what worked on the device).

If the USB read was cancelled after a timeout, nothing arrives late. The command was discarded, not
queued.

## Evidence commands
- Firmware: `python3 -I tools/firmware/s1c33dis.py dd.pl 0 0xC9D4 0x2000000 > fw.asm`. Then
  `awk '$1>="0200b20a:" && $1<"0200b2e0:"' fw.asm` (1e 46) and the same for `0200b416`–`0200b480`
  (loop), `02005e18` (1e ff), `0200b5d8` (1e 04), `02006da8`/`02006e98` (1e a2/1e a1).
- Dispatch tables: 1e at 0x57F4 (cmd−1); busy subset at 0x7E2C (cmd−0x53, default `0x2008004` sets
  `[0x44E]=1` = deferred).
- Traces: `python3 -I scripts/seq.py <trace> 100000 | grep -oE '>1e46 <06 >[0-9a-f]+ <06( >1e04 <[0-9a-f]+)+( >1e92 <[0-9a-f]+)?( >1eff <[0-9a-f]+)?'`
  over `libv370/testdata/traces/*.jsonl.gz`.

| Trace | `1e 04` replies | `1e ff` reply |
|---|---|---|
| a4-150dpi-color | 02 ×5 → 01 | `…0a` |
| a4-300dpi-gray | 02 ×2 → 01 | `…05` |
| a4-300dpi-lineart | 02 ×4 → 01 | `…09` |
| a6-300dpi | 02 ×3 → 01 | `…07` |
| 9 other scans | 02 ×1 → 01 | `…03` |
