# Perfection V37/V370 (GT-F740, 04b8:014a): Power button, auto power-off, sleep

Date 2026-11-07. Static analysis only: no USB access, repositories untouched.

Markers: **[V]** verified (code or source read directly; ✅dev = also seen on the device in earlier work),
**[I]** inferred, **[U]** unknown.

## TL;DR

1. **The firmware never switches the scanner off because of a button.** The only code that drives the
   power-off output is the **auto power-off timer** [V]. So the Power button switches power **in hardware**
   (a soft-power controller on the board) [I, strong].
2. **The firmware masks the Power button while a host session is open** [I, strong]. `1e 93 01` sets GPIO
   `0x37FD00` bit 7, and `1e 93 00` clears it [V]. Every other place that touches this bit fits a
   "power-button inhibit / host busy" line to the power controller [V for the code, I for the meaning]:
   - it is set while a button is still held at boot;
   - it is cleared before the main loop starts;
   - it is cleared first thing in the power-off routine.

   Our server keeps the session open permanently (`KeepOpen`, button watcher). That is the most likely
   reason why **Power does nothing while our software runs**. The vendor app's GUI also keeps the session open
   while its window is open (trace `buttons-poweron-gui`: open 34.6 s → close 121.1 s) [V].
3. The firmware samples **four** panel inputs, active-low:
   - `[r8+0x37f64e]` (= `0x37F84E`) bits 4, 5, 6;
   - `[r8+0x37fb05]` (= `0x37FD05`) bit 7.

   They map to `1e 85` values **01, 02, 03, 04** [V]. 02/03/04 = Printer/Email/PDF (✅dev). The fourth
   input (value **01**) is handled specially: when no session is open, it is latched only **on release**.
   Inside a session it is debounced like the others. That fits the **Power button** wired to a GPIO as well
   [I, medium]. If so, a Power press during an open session should show up as `1e 85` → `01`. Our driver
   would then report "button-1". **Test T1 below decides this.**
4. **Auto power-off exists and is active on this unit.** Log/register function `0x0010` = power-off time in
   **minutes**, value 120 = 2 h (✅dev read):
   - read: `1e 8f` + `10 00` → u32 [V];
   - write: `1e 8e` + `10 00 <u32 LE minutes>` [V static; same framing as our verified clock write `0b 00 …`];
   - 0 = off, values above 480 are clamped to 480;
   - stored in non-volatile memory (persistent).

   `1e 9f` → `10` (✅dev) is exactly the "power-off supported" flag (ErP bit) [V].
5. **Polling `1e 85` (and `1b 03`) does not count as activity** [V]. The firmware does not reset the idle
   timers for `1e 45/53/65/85/93`, nor for `1b 03/13/40`. A pure button-watcher session therefore lets the
   scanner **switch itself off 120 min after the last "real" command**, session open or not [V].
6. Lamp sleep: a separate fixed idle timer puts the scanner into a lamp-off sleep state [V]. It fires after
   34 400 ticks ≈ 14.3 min [I: tick = 25 ms]. A button press or any non-exempt command wakes it [V]. There
   is no host command to set the sleep time on this model [V].

## Inputs

| Input | Source | SHA-256 |
|---|---|---|
| `esfwdd.bin` | repo `firmware/esfwdd.bin` | `2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954` |
| `libiscan-plugin-perfection-v370.so.0.0.0` | Flatpak `net.epson.epsonscan2` (commit `3f4d56b5…98160bf`), `files/lib/x86_64-linux-gnu/epsonscan2/libexec/` | `7d0c57d8bd186dbb7a0262b3ce94d28e8c36ec5674457282fc24840d7d35c0ec` |
| The vendor app 6.7.70 source | `http://distcache.freebsd.org/ports-distfiles/epsonscan2-6.7.70.0-1.src.tar.gz`. Not downloaded again: it was copied from the earlier buttons research download into its own directory `dl-es2/` and extracted to `src-es2/`. | `e141e66e4cd74c06eef0baa163f4cc498c3be4a5db82851c914a1b7f2c50967e` (matches) |

Tools (all in this folder):
- `tools/firmware/s1c33dis.py` is the repo disassembler plus a fix: class-5 `op2=0` instructions are bit ops
  (`btst/bclr/bset/bnot [rb(+ext)],imm3`), not "ld.b/mlt". The old tool mislabelled them, which hid all the
  GPIO bit tests.
- `fw.asm` = `python3 -I tools/firmware/s1c33dis.py dd.pl 0 0xC9D4 0x2000000` (`dd.pl` = file bytes 256..65791).
- `v370.asm` = `objdump -d -C` of the plugin.
- `scripts/logcmds.py` extracts `1e 8e/8f/93/85` exchanges from `libv370/testdata/traces/*.jsonl.gz`.

Address note: globals are `[r8+X]` with r8 = 0x200, so RAM = X + 0x200. Peripheral accesses like
`[r8+0x37fb05]` mean absolute 0x37FD05. Two-`ext` immediates are imm26 = ext1<<13 | ext2.

## Q1 Firmware: buttons, `1e 85`, Power

### `1e 85` handler @0x02007344 [V]
Dispatch: 1e table 0x57F4, entry 0x85 → 0x02005CBC → `call 0x2007344`. The latch byte is RAM 0x450.
```text
1e 85 handler (0x02007344, pseudocode):
  latch = RAM 0x450            (bits are active-low)
  reply = 1 if bit 4 low, 2 if bit 5 low (Printer), 3 if bit 6 low (Email),
          4 if bit 7 low (PDF), else 0
  RAM 0x450 = 0xF0             (clear: event semantics)
  send 1 byte (USB bulk-in routine 0x02003D8C)
```
No session check and no ACK. The command does not reset the idle timers (see Q3).

### Sampling and debounce @0x02004830 [V]
The main loop runs it on event `[0x42C]` bit 0, which the timer tick sets about every 25 ms [I], unless
`[0x37A]==1` (probably busy/scanning [I]).
```
raw = ([r8+0x37f64e] & 0x70) | ([r8+0x37fb05] & 0x80)     ; 0xF0 = nothing pressed (active-low)
```
- RAM 0x452 = candidate, 0x451 = stable count, `[0x3C3]` bit7 = "wait for release", 0x476 = session flag
  (set by `1e 93`).
- **Session open (0x476==1):** E0/D0/B0/70 (exactly one button) must be stable for 10 samples (≈250 ms
  [I]). The press is then latched into 0x450, and the button must be released before it can fire again.
  This matches the observed "held 2 s → reported once".
- **No session:** D0/B0/70 behave the same. **E0 (value 1) is never latched while held.** It is latched at
  release time (`0x2004914`: `([0x452]&0xF0)==0xE0` → `[0x450]=[0x452]`). Code:
  `0x20048c8…0x2004908` (E0 skipped in the list) and `0x2004914…0x200492a`.
- Two buttons at once → ignored until release.
- On a latch (return 1), `0x200480e`:
  - wakes the scanner from sleep if `[0x43C]==1`;
  - sets `[0x3C3]` bit6. Nothing reads that bit (grep: only set/cleared) [V].

  So a press has **no local action** apart from waking from sleep. Entering sleep (`0x2004a18`) clears the
  latch (`0x2004a7a`) [V].
- **No long-press timer** exists for any button in the firmware [V: the only readers of these port bits are
  0x20042e6 (boot), 0x2004830 (sampler) and 0x200c98c (wait-for-release helper, no callers found)].

### Which physical button is value 1? [I/U]
Value 2 = Printer, 3 = Email and 4 = PDF are verified on the device. That leaves the Power button as the
only remaining panel button, and the "latch on release when no session" rule only makes sense for a button
the hardware also acts on. So value 1 is very likely Power, but this is **not verified**. It was never
seen, because nobody pressed Power during a `1e 85` poll (PLAN.md P10c: "not Power"). The earlier
"Power switches it off" observations were made without an open session.

### Power-button inhibit: `0x37FD00` bit 7 [V code, I meaning]
| Where | What |
|---|---|
| `1e 93` handler 0x02006466 | parameter → `[0x476]`; ==1 → `[0x37FD00] |= 0x80`, else `&= 0x7F`; then idle timers reset, ACK |
| boot 0x020042e6 | writes `[0x37FD00]=0x10` (bit7 = 0). If a button is held at boot (the Power button the user just pressed): sets bit 7 and loops until all buttons are released (`0x2004348…0x2004374`) |
| main 0x020014ea | clears bit 7 before the main loop |
| power-off 0x0200c966 | clears bit 7 **first**, then drives the power-off GPIO |

Bits 4/5 of the same port blink in the tick handler (LEDs [I]). Bit 7 is never toggled for display. The
pattern (set during host session, set while the boot press is still held, released just before
self-power-off) is what a "do not let the power button cut power now" line to a soft-power controller
would look like. Hence: **with a session open, the Power button is (very likely) ignored by the hardware**,
and the firmware only sees it as input value 1.

## Q2 Who switches power off? [V for the firmware part]

There is a software power latch:
- `0x0200c8fa` (init): `[0x48C] = [0x37F0E8] bit0` (hardware present). If `[0x48D]==1` (NV word 0x26
  magic `0x33340000`/`0x7C340000`, set in `0x200c92e`): clear `[0x37F0D9]` bit0 and enable outputs
  `[0x37F0D8] |= 0x11`.
- **`0x0200c966` = power-off**, which never returns:
```text
power-off routine (0x0200C966, pseudocode):
  disable interrupts
  clear bit 7 of GPIO 0x37FD00     (release the power-button inhibit)
  set bit 0 of 0x37F0D9            (power-off request)
  set bits 0x11 in 0x37F0D8
  wait until power drops
```
- Its **only caller** is the timer tick `0x0200c8bc` (`[0x494]` ≥ `[0x490]` → power off). No button path
  calls it. So **button power-off is pure hardware** [I]. The firmware cannot block it, except through the
  inhibit line above [I].
- Nothing in the power-off path checks for an open session, carriage position or USB activity [V]. The
  timer fires whatever state the scanner is in, and only "real" commands reset it.

## Q3 Auto power-off and sleep [V unless marked]

Firmware timer variables: `[0x490]` limit (ticks), `[0x494]` counter, `[0x488]` enable.
- `0x200c856` (read, log function 0x10): if `[0x48C..0x48D]==0x0101`:
  - minutes = NV(0x2A);
  - limit = minutes × **2400** (⇒ 40 ticks/s, tick = 25 ms [I]);
  - returns minutes.

  Otherwise it returns 0 and sets limit = 0.
- `0x200c812` (write): if `0x0101`: m = min(value, **480**); limit = m × 2400; if changed, NV(0x2A) = m;
  then it re-arms the timer (`0x200c89a`). Otherwise the value is silently ignored (still ACKed).
- `0x200c890` = counter reset. It is called by:
  - the 1e dispatcher for **every 1e command except 45, 53, 65, 85, 93** (`0x2005790…0x20057d4`);
  - the 1b dispatcher for every 1b command **except 03, 13, 40** (`0x20055e6…0x2005618`);
  - the `1e 93` handler itself (`0x20064a8`), the wake-up routine, and two other internal paths.

  A button press does **not** reset it (unless it wakes the scanner from sleep).
- Init `0x200c804` loads the limit at boot. Main `0x200150e` arms the counter. The tick `0x20011b6` calls
  `0x200c8bc`.

Log (register) commands, from the firmware handlers [V]:
- `1e 8f` @0x2007512: ACK, 2-byte function, 4-byte reply. Functions:
  - 0, 1, 3, 5, 6, 0xA, 0xB → NV words 0x2C, 0x2E, 0x30, 0x32, 0x34, 0x36, 0x3E;
  - 0x10 → power-off minutes;
  - anything else → 0.
- `1e 8e` @0x2007422: ACK, **6-byte parameter** `<u16 LE function><u32 LE value>`, then ACK. Functions:
  - 0, 1, 3, 5, 6, 0xA → write those NV counters (**could overwrite lamp/scan counters**);
  - 0xB = clock (written once per boot, `[0x47A]` flag);
  - 0x10 = power-off minutes;
  - **0xFFFF = NV write test that overwrites counters** (`0x2009a6a`).

Host side [V]:
- The vendor app: `ESCICommand.h:283` `kLogFunctionPowerOffTime = 0x0010`. `ESCICommand.cpp:848-855`
  `RequestSet/GetPowerOffTime` → `RequestWrite/ReadLogForFunction` (`:940-971`), i.e. ESC `0xE1`
  (u16 + u32) / ESC `0xE2` (u16 → u32).
- V370 plugin:
  - ESC E1 → jump table 0x3e448 → 0x26627 → `L61` @0x137f0: `1e 8e` with ACK, **6 host bytes passed
    through**, 1-byte ACK;
  - `L62` @0x13880: `1e 8f` with ACK, 2 bytes, 4-byte reply;
  - `L114` @0x29ed0: clock = `1e 8e` with function 0x000b.
- `IsPowerOffSupported()` (`ESCIAccessor.cpp:965`) tests ext-identity MainStatus2 bit4
  (`kIDMainStatusErPSupport`, `:43`). The plugin fills offset 0x2d (= MainStatus2 in
  `ST_ESCI_EXTENDED_IDENTITY`, `ESCICommand.h:153-176`) with the `1e 9f` reply (`L98` @0x15fb9). The
  firmware `1e 9f` handler @0x20085a6 returns `0x10` iff `[0x48C]==1 && [0x48D]==1`. The device answers
  `10` (✅dev), so the feature is enabled.
- The GUI never offers the setting for this model:
  - `PoweroffTimer.hpp:49-71` requires model key `ESPowerOffTimer`;
  - `ES00DD.json` (6.7.70 and the 6.7.82 Flatpak) has no `ESPowerOffTimer` and no Sleep key;
  - preset list `{0,30,60,120,240,480,720}` minutes (`:73`).

  Its `GetCapability()` still calls `GetValueForKey(kESPowerOffTime)` → that is the **three `1e 8f 1000`
  reads per session** in the captures.
- Traces: `1e 8e` is only ever sent with function `0b00` (17×). `0x0010` is never written [V,
  `scripts/logcmds.py`].
- Sleep timer: `SleepTimer.hpp` uses `kESPowerSaveTime`, which ESC/I does not implement. ESC `P` is unmapped
  in the plugin's tables, and no firmware log function sets it [V].
- Lamp sleep in the firmware: `[0x456]++` per tick while awake. At `0x8660` (34 400 ticks ≈ 14 min 20 s
  [I]) it raises event `[0x42C]` bit6 → `0x2004a18` (sleep: lamp/ASIC off, `[0x43C]=1`, button latch
  cleared). Wake-up via `0x200494a` happens on any non-exempt command or a button press. In the error state
  it goes to sleep immediately.

## Q4 Recommendations for our driver

(a) **Detect a Power press:** probably yes, but only **while a session is open** (value `01` from `1e 85`)
[I, test T1]. Our code already passes it on as `Button(1)` ("button-1"); `WatchButtons` → `onPress`
(`internal/backend/direct.go:178-223`). If T1 confirms it, name it `ButtonPower = 1`. On that event:
- end any scan cleanly, send `1e 93 00`, release the device and stop polling;
- tell the user "press Power again to switch off". Once the session is closed, Power works in hardware.

There is **no host command that powers the scanner off directly**. The only software path is the
auto-off timer. Setting it to 1 min and going idle is technically possible, but it costs an NV write and
the old value must be restored after the next power-on. Not recommended.

(b) **Do not keep the session open while idle.** That is what masks Power [I]. The firmware does not check
the session for `1e 85` (no reference to `[0x476]` in the dispatch path) [V]. So the button watcher can
poll `1e 85` **without** `1e 93 01`, and open the session only for scans/status:
- Printer/Email/PDF are still debounced and latched;
- Power then switches off in hardware;
- value 01 may arrive on release just before power drops.

The protocol.md rule "poll only inside an open session" comes from the vendor's call order, not from the
firmware.

Opening and closing the session around every poll is **not** a good idea:
- each `1e 93` resets the auto-off counter, so the scanner would never auto-off;
- `1e 93` takes 0.1–1 s;
- the switches would race with a Power press.

(c) **Auto power-off is on (120 min) and our polling does not stop it** [V]. Pick a policy and document it:
- accept it (ErP behaviour; the watcher already retries with back-off), or
- let the user set it with a new, explicit call:
  - `SetPowerOffTime(min)` = `1e 8e` → expect `06`, then exactly 6 bytes `10 00 <u32 LE>` → expect `06`;
  - `PowerOffTime()` = existing register read `1e 8f` + `10 00` → u32 (✅dev);
  - range 0..480, 0 = off.

  It is persistent: it writes NV word 0x2A, but only when the value changes. Same framing as our verified
  clock write [V static]. The write itself has **not** been sent to the device yet.

  A keep-alive with a non-exempt query (e.g. register read `1e 8f 0500`) would also stop the auto-off, but
  it wakes the lamp from sleep. Not recommended.

Commands that are **safe** (exact lengths known):
- `1e 85` (no parameter, 1 byte);
- `1e 93` + 1 byte;
- `1e 8f` + 2 bytes → 4 bytes;
- `1e 8e` + 6 bytes with function `000b` (clock) or `0010` (power-off time).

**Never send:**
- `1e 8e` with any other function (0/1/3/5/6/A overwrite counters; FFFF is an NV test);
- `1b 20`–`1b 25`;
- any ACK command without its full parameter.

## Safe test procedure (main agent with the user at the scanner)

Preconditions: the vendor app fully exited (the background process too); our server/watcher stopped; scanner
on and ready.

- **T0 baseline:** nothing running, no session. Tap Power (≈0.5 s). Expected: the scanner switches off
  (known). Switch it on again.
- **T1 session open:** run `v370 buttons` (opens the session = `1e 93 01`, polls `1e 85` every
  200 ms, prints unknown values as `button-1`).
  1. Tap Printer once (sanity check, expect `printer`).
  2. Tap Power ≈0.5 s. Record whether the scanner stays on and whether `button-1` is printed.
  3. Hold Power 1 s, 3 s, then up to 10 s. Stop as soon as it switches off. Record the time (hardware
     forced-off by long press? [U]).
- **T2 after the session:** Ctrl-C (the tool sends `1e 93 00`), then tap Power. Expected: switches off
  [I].
- **T3 (optional, needs a small tool change):** add `-no-session` to `v370 buttons`: upload the
  firmware if needed, but skip `1e 93 01`; poll `1e 85` with `1b 03` as a sanity check every N rounds.
  1. Tap Printer → expect `printer` (confirms session-less polling).
  2. Tap Power → expect it switches off, possibly after printing `button-1` on release.
- **T4 (optional, persistent change, only with the user's OK):**
  1. Read `1e 8f 1000` (expect 120).
  2. Write `1e 8e` → `06`, `10 00 02 00 00 00` → `06`; read back (expect 2).
  3. Leave it idle with no commands, or only `1e 85` polls. Expect self-power-off about 2 min after the
     last non-exempt command.
  4. Switch on, open a session, and **immediately restore** with `10 00 78 00 00 00`; read back 120.
  5. Needs a parameter-capable helper: `raw` only sends 2-byte commands.

Interpretation:
- T1 stays on and prints `button-1`, T2 switches off → mask + value-1 = Power confirmed. Implement (a) and (b).
- T1 switches off at once → there is no mask. Then the "no function" report needs another cause, e.g. the
  scanner was in lamp sleep or had already switched itself off. Check the logs.
- T1 stays on with no `button-1` → mask confirmed, value 1 belongs to something else. Use (b) only.

## Not done / open
- The real tick frequency (2400/min is taken from the code that converts minutes to ticks).
- What the board-level power controller does with a long press [U].
- The 0x37F0D8/D9 and 0x37FD00 pin assignments (only the firmware side was read).
- `.aiwa` tags were not updated: no repository writes were allowed for this task.
