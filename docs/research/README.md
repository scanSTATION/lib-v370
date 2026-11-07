# Research sources — button command `1e 85`

Where the button findings (docs/protocol.md §7) come from. The research was
done on 2026-11-07 by a research subagent (static analysis only — it never
talked to the scanner); the result was then verified on the real V370 by the
main agent. Full report: [`buttons-report.md`](buttons-report.md) (its
relative paths such as `src-6.7.70/…`, `disasm/…` refer to the temporary
research folder, which is not kept; everything can be re-obtained with the
URLs and checksums below).

## Downloaded sources

| Source | Version | Obtained from | Checksum (SHA-256 of the archive) |
|---|---|---|---|
| The vendor app open source | 6.7.70.0-1 | `http://distcache.freebsd.org/ports-distfiles/epsonscan2-6.7.70.0-1.src.tar.gz` (the vendor's own download URLs returned "Access Denied"; 6.7.82 was not obtainable) | `e141e66e4cd74c06eef0baa163f4cc498c3be4a5db82851c914a1b7f2c50967e` — matches FreeBSD ports `graphics/epsonscan2/distinfo` |
| iscan ("Image Scan! for Linux", SANE backend `epkowa`) | 2.30.4-2 | `https://distfiles.gentoo.org/distfiles/ed/iscan_2.30.4-2.tar.gz` | `597b9913de42d7a27f95bc82e533cbe778121ba13e3a03bc037f8d09a43447d5` (SHA-512 matches Gentoo `media-gfx/iscan/Manifest`) |
| scanbuttond | 0.2.3 | SourceForge project *scanbuttond* | `93b060a363fef069548a49cb151da53ac6071dc0569f9a07d68751a28dffebee` |
| es2button (ESC/I-2 button daemon) | git `624df7ac087cd7e3ca5ca79d7e811c68e5fca8d5` | `https://github.com/janrueth/es2button` | — |
| The vendor app Debian builder (consulted) | git `12dc38c514432336b42542e65cc0a291c7300003` | `https://github.com/janrueth/epsonscan2` | — |
| AUR vendor-app package mirror (consulted) | git `b9a73e2d48197b90731be5ba394f2bf1a24b955b` | `https://github.com/duck7000/aur_epsonscan2` | — |

## Closed binaries analysed (read-only, from the installed Flatpak)

The vendor app 6.7.82.0, Flatpak `net.epson.epsonscan2`, commit
`3f4d56b5b603158af5ae970554812abb52b8dcc0b98f1034f58a4bd3298160bf`,
directory `files/lib/x86_64-linux-gnu/epsonscan2/`:

| File | SHA-256 |
|---|---|
| `libexec/libiscan-plugin-perfection-v370.so.0.0.0` | `7d0c57d8bd186dbb7a0262b3ce94d28e8c36ec5674457282fc24840d7d35c0ec` |
| `libexec/libesci-interpreter-perfection-v330.so.0.0.0`, `libexec/libiscan-plugin-perfection-v550.so.0.0.0` | compared (same button function) |
| `libes2command.so` | symbol names (`RequestButtonStatus`, `StartButtonChecking`, …) |

Reproduce the disassembly and the command-table decoding:

```bash
LIB=$(flatpak info --show-location net.epson.epsonscan2)/files/lib/x86_64-linux-gnu/epsonscan2/libexec
objdump -d "$LIB/libiscan-plugin-perfection-v370.so.0.0.0" > v370.asm
# ESC command jump table at 0x3e758 (index = command byte - 0x21):
python3 -I docs/research/tools/jt.py "$LIB/libiscan-plugin-perfection-v370.so.0.0.0" 3e758 21 94
```

## Which finding comes from where

| Finding | Source | Location |
|---|---|---|
| The vendor's button request is ESC/I `ESC !` (`GET_PUSHBUTTON_STATUS = '!'`) | The vendor app 6.7.70 source | `src/ES2Command/Src/Command/ESCI/ESCICommand.h:60`, `ESCICommand.cpp:832-846` (exchange: `SendCommand3`, `ESCICommand.cpp:166-216`) |
| Button polling is never started for ESC/I devices | The vendor app 6.7.70 source | `CESCIAccessor::StartButtonChecking` `ESCIAccessor.cpp:727-751` has no caller; only `ESCI2Accessor.cpp:183` starts polling (ESC/I-2) |
| Button support flag = bit 0 of the extended status | The vendor app 6.7.70 source | `IsPushButtonSupported()` `ESCIAccessor.cpp:981-984` |
| The GUI ignores button events | The vendor app 6.7.70 source | `src/Controller/Src/Scanner/Engine.cpp:133-136` (`ScannerDidPressButton` empty) |
| No "push-scan ready" step for ESC/I | The vendor app 6.7.70 source | `ESCIScanner.cpp:154-157`; ESC/I-2 only: `ESCI2Command.cpp:1613-1620` |
| Per-model button polling only for DS-30 | The vendor app 6.7.70 source | `ModelInfo.json:46-63`; ES00DD/ES0282 model files have no button keys |
| **`ESC !` is translated into native `1e 85`, 1-byte reply, values 1–4 passed through** | V370 interpreter plugin (closed) | state machine `0x25c20`; ESC jump table `0x3e758` (`!` grouped with queries F, I, S, f, i); button function **`0x13b00`** sends `{1e, 85}` without ACK and reads 1 byte |
| Native command sender and its ACK flag | V370 interpreter plugin | `0x252a0` (`1e xx`); ACK pattern matches all commands in our captures |
| Same button function in sibling models | V330 interpreter `0x14610`, V550 plugin `0x27550` | — |
| Plugin reports "has push button" | V370 interpreter plugin | ESC `f` reply builder `0x13fe0` sets bit 0 |
| **`1b 20`/`1b 21`/`1b 22` are register writes, `1b 23`–`1b 25` reads** (why `1b 21` hung the scanner) | V370 interpreter plugin | `1b xx` sender `0x25080`; `1b 21` = u16 write via `0x35f70` (3-byte address + u16) |
| iscan/epkowa would poll the button via `ESC !` (SANE options `monitor-button`, `wait-for-button`), testing only bit 0 | iscan 2.30.4 | `backend/epkowa.c:6178-6220`, `2880-2895`, `4531-4570`; flag `command.c:419-420`, `543-544`; blacklist `hw-data.c:219-240` |
| scanbuttond does not support 04b8:014a and sends raw `1b 21` | scanbuttond 0.2.3 | `backends/epson.c:258-285` |
| No public evidence of working V370 buttons on Linux, no public Event-Manager capture | web search (2026-11-07) | — |

## Verification on the real scanner (main agent, 2026-11-07)

`1e 85` → `00`, status unchanged; then Printer `02`, Email `03`, PDF `04`
(`testdata/buttons/1e85-watch.log`, `testdata/buttons/v370-buttons.out`).

## Firmware map and improvements (2026-11-07)

Static analysis by research subagents, partly verified on the scanner:

- [`firmware-map.md`](firmware-map.md) — memory map, RAM variables, ASIC
  registers, NV memory, full command table with risk classes, how to change
  the firmware (verified steps only), how to rebuild the listings.
- [`supported-modes.md`](supported-modes.md) — resolutions, bit depths,
  channels, limits; real-hardware results of the resolution experiment.
- [`improvements.md`](improvements.md) — host-side improvements ranked by
  benefit and risk; risky internals (motor, lamp, CCD) explained, not to be
  changed.
- Tools in [`tools/firmware/`](tools/firmware/); generated listings go to
  `tools/firmware/out/` (git-ignored: they contain disassembled the vendor code).
