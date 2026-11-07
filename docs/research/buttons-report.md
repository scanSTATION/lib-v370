# Perfection V37/V370 (GT-F740) buttons: research report

Date: 2026-11-07. Static analysis only. Nothing was sent to the scanner.

## TL;DR

**The native button query is `1e 85`.** It is a plain query with no parameter
and no ACK. The scanner answers with **1 byte**: `0` means no button, `1`..`4`
is the button number. The vendor's V370 interpreter sends exactly these bytes when
it is asked the ESC/I "push button status" question (`ESC !`). The same code is
in the V330 and V550 interpreters. The vendor's Linux software never asks that
question, which is why no capture contains `1e 85`.

Confidence: high (verified statically, see Q2). It has not been tested on the
device yet.

Side finding: native **`1b 21` is a 16-bit register WRITE**. After the ACK it
waits for a 5-byte parameter: 3-byte address LE + u16 value LE. That is why our
`1b 21` probe hung the scanner.

## Sources obtained

- The vendor app open source **6.7.70.0-1**. The vendor's own URLs (download-center.epson.com,
  support.epson.net) return Akamai "Access Denied". I took the archive from the FreeBSD
  distfile mirror instead: `http://distcache.freebsd.org/ports-distfiles/epsonscan2-6.7.70.0-1.src.tar.gz`.
  Its SHA-256 `e141e66e…967e` matches the FreeBSD ports `graphics/epsonscan2/distinfo`.
  6.7.82 itself was not obtainable. The behaviour matches our 6.7.82 captures.
  Extracted to `src-6.7.70/`.
- iscan 2.30.4-2 (epkowa backend) from Gentoo distfiles: `https://distfiles.gentoo.org/distfiles/ed/iscan_2.30.4-2.tar.gz`.
  Its SHA-512 matches the Gentoo `media-gfx/iscan/Manifest`. Extracted to `src-iscan/`.
- scanbuttond 0.2.3 from SourceForge. Extracted to `src-scanbuttond/`.
- es2button (github.com/janrueth/es2button). A Rust daemon that reads buttons through
  the vendor app's libes2command; written for the ESC/I-2 DS-310. In `dl-es2button/`.
- Closed binaries, read only, from the Flatpak at
  `/var/lib/flatpak/app/net.epson.epsonscan2/x86_64/stable/3f4d…bf/files/lib/x86_64-linux-gnu/epsonscan2/`.
  Disassembly is in `disasm/`. The jump-table decoder is `scripts/jt.py`.

Paths below refer to the analyst's working directory (not part of this
repository); the sources are listed with download links in [README.md](README.md).
`SRC` = `src-6.7.70/epsonscan2-6.7.70.0-1/src`.

## Q1: the vendor app's button code (all VERIFIED from source)

### `RequestButtonStatus`
- `SRC/ES2Command/Src/Command/ESCI/ESCICommand.h:60`: `GET_PUSHBUTTON_STATUS = '!'`.
- `ESCICommand.cpp:832-846`: `SendCommand3(GET_PUSHBUTTON_STATUS, ESC, …)`, then
  `un8OutButtonStatus = data[0]`.
- `SendCommand3` (`ESCICommand.cpp:166-216`) does the standard ESC/I exchange:
  - write `1b 21`;
  - read a 4-byte info block `02 <status> <u16 LE count>` (the first byte must be STX);
  - read `count` data bytes.
  - The button value is data byte 0, used raw as the "button number".
- This is ESC/I as the *interpreter* sees it, not what goes over USB to the V370 (see Q2).

### When button checking runs
- `ESCIAccessor.cpp:727-751`: `StartButtonChecking()` uses an interrupt endpoint
  when there is one and the model is not flagged. Otherwise, on local or interpreter
  connections, it polls `GetButtonStatus()` every `ButtonObservationInterval()` ms
  and calls the delegate when the value is not 0.
- The default interval is 1000 ms (`ESScannerDefs.h:21`), and 200 ms with an interrupt.
- **`CESCIAccessor::StartButtonChecking()` has NO callers anywhere in the source tree.**
  Only `CESCI2Accessor` calls its own version (`ESCI2Accessor.cpp:183`). So ESC/I
  devices, the V370 included, are never polled. This explains capture §7.5.
- Per-model data: `SRC/ES2Command/Src/Resource/ModelInfo.json:46-63`.
  `shouldObserveButtonWithCommand` and `customButtonObservationInterval` (250 ms)
  list only **DS-30**.
- `IsPushButtonSupported()` (`ESCIAccessor.cpp:981-984`) checks bit 0 of the
  extended-identity main status.
- Neither `Resources/Models/ES00DD/*/ES00DD.json` nor ES0282 has a button key.
- The GUI ignores button events anyway: `SRC/Controller/Src/Scanner/Engine.cpp:133-136`.
  `Engine::ScannerDidPressButton` only logs "Enter".

### `SetPanelToPushScanReady`
- For ESC/I it does nothing: `ESCIScanner.cpp:154-157` returns `kESErrorNoError`.
- `CESCI2Command::RequestPanelToPushScanReady` (`ESCI2Command.cpp:1613-1620`) sends
  an ESC/I-2 sequence with FourCC `SRDY_ON` / `SRDY_OFF`. It applies only to
  ESC/I-2 devices; the V370 is ESC/I.
- **Conclusion: no "enable panel" step exists for ESC/I devices** in the vendor's code.

### Interrupt path
- `SRC/ES2Command/Src/Interface/ipc/` contains a shared-memory and semaphore
  channel (`event_did_press_button`) from the helper `es2intif`.
- `es2intif`, which loads the V370 interpreter and does the USB I/O, imports no
  `libusb_interrupt_transfer`, `shm*`, `sem*` or `ftok` (checked with `nm -D`).
  So it cannot deliver button events either.
- The V370 has no interrupt endpoint at all.

## Q2: The interpreter translates `ESC !` into native `1e 85` (VERIFIED, static)

File: `libexec/libiscan-plugin-perfection-v370.so.0.0.0`. Symbols are obfuscated
(`libiscan_plugin_perfection_v370_NNN`, abbreviated `L…` below). Disassembly:
`disasm/v.asm`.

### Call chain
- `interpreter_writer` → `L130` → `L138::L307` → **`L328::L193(buf,len)` @ 0x25c20**.
  `interpreter_reader` → `L128` → `L138::L195` → also `L193`.
- So one state machine handles both directions:
  - state byte at `this+0x10`;
  - control code at `+0x11` (`1b`, `1c`, …);
  - command at `+0x12`.

### Dispatch tables (decoded with `scripts/jt.py`)
- **0x3e758** (first write, ESC, index = cmd - 0x21):
  - `!` `?` `F` `I` `S` `f` `i` and others jump to 0x25eb0, which sets state = 1
    ("reply pending"). So ESC `!` is accepted as a **query**.
- **0x3e448** (states 1-3, ESC):
  - `!` → 0x2641a.
    - In state 1 (0x26a24) it builds the info block `02 <L110 status> 01 00`,
      i.e. **1 data byte**, and sets state = 2.
    - In state 2 it calls **`L328::L109` @ 0x13b00** and sets state = 0.

### `L109`: the button query
```text
V370 plugin, button query (function at 0x13b01, pseudocode):
  send native command 0x85 with prefix 1e, no ACK expected
  read exactly 1 byte
  return it if it is 1, 2, 3 or 4, else 0
```

### `L220` and `L196`
- **`L220(cmd, expect_ack)` @ 0x252a0**:
  - builds `{0x1e, cmd}`;
  - writes it with `L218` (2 bytes);
  - only when expect_ack == 1, reads 1 byte and requires `0x06`.
- **`L196`** reads N bytes through the host read callback.

### Cross-check against commands we already know
I listed all `L220` call sites. The ack flag matches our observed protocol exactly:

- ack (flag 1): `93`, `8f`, `8e`, `57`, `a2`, `87`, `46`, `21`, `22`, `41`–`43`, `11`, `31`, `84`, …
- query (flag 0): `a1`, `65`, `9f`, `97`, `86`, `53`, `ff`, `04`, …
- `0x85` is a query (flag 0). It appears **only** in `L109`.

`1e 85` (base64 `HoU=`) occurs in **none** of `testdata/traces/*.jsonl.gz`.

### Same code in sibling interpreters
The same `L109` pattern (`mov $0x85,%esi` → `L220(…,0)`) is at:
- 0x14610 in `libesci-interpreter-perfection-v330.so`;
- 0x27550 in `libiscan-plugin-perfection-v550.so`.

So `1e 85` is the button query for the whole family.

### The interpreter advertises a push button
- `L85` @ 0x13fe0 builds the ESC `f` (extended status) reply.
- It starts with `movb $0x1,(%rsi)`, i.e. `EXT_STATUS_PB` = 1 in byte 0.
- So for iscan/epkowa the V370 has a push button.

### Session
- On the very first call, `L193` (0x25db8) runs `L227`, which is `1e 93` with ack:
  the session open.
- So the vendor would always send `1e 85` inside an open session (inferred from the call order).

### Side finding: native `1b xx` commands
Sender `L219` @ 0x25080, same layout with `0x1b`. Call sites:

| Command | ack | Meaning |
|---|---|---|
| `1b 03` | no | status |
| `1b 13` | no | identity |
| `1b 40` | yes | end of job |
| `1b 20` | yes | register write, u8 (`L314`) |
| **`1b 21`** | yes | **register write, u16** (`L316` @ 0x35f70) |
| `1b 22` | yes | register write, u32 (`L315`) |
| `1b 23` / `1b 24` / `1b 25` | – | register read, u8 / u16 / u32 |

- `L316` (`1b 21`): sends `1b 21` → ACK, then a **5-byte** parameter
  `addr[0..2] LE, value u16 LE` → ACK.
- `L204` (`1b 23`): sends `1b 23` → ACK, then a 3-byte address → 1 data byte.
- (inferred) `1b 24` and `1b 25` return 2 and 4 data bytes.
- Our earlier probe left `1b 21` waiting for that 5-byte parameter. **It may also have
  written a register.** Do not use the `1b 2x` commands.

## Q3: iscan / epkowa (VERIFIED from source)

- `src-iscan/iscan-2.30.4/backend/epkowa.c:6178-6220`, `get_push_button_status()`:
  - sends `ESC '!'` (2 bytes);
  - reads the 4-byte info block, then `len = result[3]<<8|result[2]` bytes;
  - `pressed = buf[0] & 0x01`.
  - It is exposed as the SANE option **`monitor-button`** (`epkowa.c:2880-2895`) and
    used by `wait-for-button` (`epkowa.c:4531-4570`).
- It is enabled when ESC `f` byte 0 or FS I `fsi_cap_1` has bit 0 (`EXT_STATUS_PB`)
  set: `command.c:419-420` and `543-544`.
- `hw-data.c:219-240` blacklists only LP-M models, locking devices and network
  devices. `hw-data.c:353-361` sets a 250 ms poll for DS-30 only.
- For the V370 through `epkowa` + `iscan-plugin-perfection-v370`:
  - `monitor-button` should be active (`EXT_STATUS_PB` is set, see Q2);
  - each read becomes `1e 85` on USB.
- **Caveat (inferred):** epkowa tests only bit 0. Button codes 2 and 4 would read as
  "not pressed" and only 1 and 3 as pressed. Our driver must use the full byte.

## Q4: scanbuttond / scanbd / others

- **scanbuttond** 0.2.3 (`src-scanbuttond/*/backends/epson.c:258-285`):
  - sends raw `1b 21` over libusb, reads the 4-byte header plus data, and returns byte 0;
  - works only for classic ESC/I devices (USB IDs 0x0101-0x011E and 0x0802);
  - **does not list 04b8:014a**.
  - On the V370 this is exactly the command that hangs (native register write).
- **scanbd**: polls SANE options such as `monitor-button`. In theory it would work with
  epkowa + V370 plugin. I found no public report of this working.
- **es2button**: uses the ESC/I-2 interrupt path in libes2command (DS-310). It would not
  work on the V370, because `StartButtonChecking` is never called for ESC/I and es2intif
  has no interrupt path.
- I found no public evidence of V37/V370/GT-F740 buttons working under Linux.

## Q5: Captures or docs of the vendor Event Manager with the V370

- None found. Searches for V370 / V37 / GT-F740 with "Event Manager" and USB returned
  nothing relevant.
- The vendor's FAQ for the button events (Copy/Email/PDF/Scan to PC) only says that the
  number of buttons depends on the model.
- The static result in Q2 makes a Windows capture unnecessary, unless 1e 85 turns out
  to need an enable step.

## Candidate command sequences (ranked)

### C1: poll `1e 85` (confidence HIGH)
```
(session already open: 1e 93 01 -> 06, as our driver does today)
loop every 100-250 ms while idle:
    write 1e 85
    read  1 byte  -> 00 = no button; 01..04 = button number
```

What it does:
- Kind: query, no parameter, no ACK, 1-byte reply. Verified from the interpreter code
  for this exact firmware family.
- The vendor's interpreter maps any value other than 1..4 to 0.

Inferred:
- The value is probably latched until it is read, like classic ESC/I push-button
  status. So short taps should be caught even at 250-1000 ms polling.
- The button-to-number mapping is unknown. The panel has Copy (printer icon),
  E-mail, PDF and a 4th button. **Power is very likely not reported**; it is a power
  switch. Map the numbers by experiment.

**Risk: low.**
- The vendor's own code sends it bare and reads exactly 1 byte.
- The only bad case: the reply is `06` (ACK). That would mean this firmware treats
  0x85 as command+param, which would contradict the interpreter.

### C2: `1e 85` inside a fresh session after power-on (fallback if C1 always returns 0)
- Same exchange.
- Run it right after firmware upload + `1e 93 01`, before any scan. This mirrors the
  interpreter's own order: session open first, then any command.
- Also try pressing a button *before* the poll starts, to test latching.
- Risk: same as C1.

### C3 (low confidence, only if C1/C2 never report)
- An enable step is not supported by any code path for ESC/I: `SetPanelToPushScanReady`
  does nothing for ESC/I.
- The next step would be a Windows USB capture of the vendor Event Manager. I did not
  find one publicly.
- Do NOT guess other 1e/1b commands.

### Never send
- `1b 21`, `1b 20`, `1b 22`: register writes.
- `1b 23` / `1b 24` / `1b 25`: register reads with a 3-byte address parameter.
- Any `1e` command from the ack list without its exact parameter.

## Safe test procedure for the implementing agent

1. Pre-check (no device):
   - Add `1e 85` to `docs/protocol.md` as a query with a 1-byte reply.
   - Add a usbsim replay or unit test for the exchange.
   - Use a read buffer ≥ 512 bytes. Expect exactly 1 byte back.
2. Device idle, our driver as the only USB user (the vendor app fully exited):
   1. `1b 03`: expect 2 bytes, ready `18 01`.
   2. `1e 93 01`: expect `06` (session open).
   3. Send `1e 85` **once**:
      - read with a short timeout (≤ 1 s);
      - log the raw reply.
   4. Immediately `1b 03` to confirm the device still answers.
   5. If step 3 returned **`06`**: STOP. Send nothing else (the device would be waiting
      for a parameter) and report. Recovery is a power cycle. This outcome is not
      expected from the code.
   6. If step 3 returned 1 byte other than `06`: poll `1e 85` every ~200 ms for a few
      minutes, with a `1b 03` sanity check every N rounds. Meanwhile press each button
      in turn (short tap, then 2 s hold) and record the values. Also test whether a
      value stays latched when you press, then start polling.
   7. Close the session (`1e 93 00`).
3. Never poll during a scan. Interleaving with `1e 47` data reads would corrupt the
   stream. Stop polling and claim the device exclusively for scanning, then resume.
4. Integration: map the codes to `copy` / `email` / `pdf` / `start`, emit events, and
   debounce, treating a repeated non-zero value as one press.
