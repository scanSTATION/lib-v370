# What `esfwdd.bin` is — firmware analysis

**Answer: the complete main firmware of the scanner** — executable code for
a vendor **S1C33 (C33) 32-bit RISC core** (16-bit instructions,
little-endian), compiled from C, linked to run at `0x02000000`. Not a
configuration blob, not an FPGA bitstream, not an unlock/wake-up stub. Before
the upload only a small boot loader in the scanner chip's ROM runs.

Static analysis by a research subagent on 2026-11-07 (no access to the
scanner). Full report with offsets, disassembly excerpts and reproduction:
[`firmware-report.md`](firmware-report.md); its analysis scripts (incl. a
small S1C33 disassembler, written for this because binutils has no C33
target) are in [`tools/firmware/`](tools/firmware/).

## Evidence (verified)

| Finding | Where in the payload (offset from byte 256 of the file) |
|---|---|
| **Vector table** at offset 0: reset → `0x02001000`, NMI → `0x020010BC`, interrupt stubs `0x4A0–0x4E8`. The repeated `2c 01 00 00` words are vectors to the default trap handler at `0x12C` (`nop; jp self; reti`), **not** the value 300 | `0x0000` |
| **C runtime start-up**: clears registers, SP = `0x1000`, r8 = `0x200`, zeroes internal RAM, copies vectors/stubs to RAM, sets the trap-table base, calls `main` (`0x0200140C`) | `0x1000` |
| **Native command dispatch**: prefix byte at RAM `0x44C`, command at `0x44D`; `1b` table (cmd − 3) handles `03, 13, 16, 20–25, 40`; `1e` table (255 entries, cmd − 1) handles every `1e` command we know (`41 47 53 57 84 85 8E 8F 93`) and ~40 more | `0x55DE` / table `0x5638`; `0x578A` / table `0x57F4` |
| **Identity reply** (`1b 13`): chooses the `GT-F740` or `GT-S640` identity by a variant flag; "1.00" is this file's version | handler `0x6F00`, strings `0x558C`, `0x55A8` |
| **Own USB stack and descriptors**: device descriptor VID `04b8` PID `014a` bcdDevice 1.00, bulk EP `0x81`/`0x02` 512 bytes, product strings "… Perfection V37/V370", "… GT-S640/F740"; GET_DESCRIPTOR answered by the payload's code | `0x5480`, copy `0x95B8`; PID `0131` variant `0x96BA` |
| Not compressed/encrypted: entropy 6.1–6.5 bit/byte in code, `0xFF` padding after `0xC9D4`; 1 010 calls to 254 targets, 278 `ret`, ~15 compiler jump tables | whole payload |

Header (256 bytes, identical in all six `esfw*.bin` files): `EDLA`, zeros,
at `0x40` `00 01 00 00 01 00 00 01 00 00 b8 04 01 00 01 00` — the vendor's VID
`04b8` and the 4-byte block `01 00 01 00` that the host sends after `1b 06`.
No model id, size, address or checksum. The trailing file byte (`0xCE`) is
neither a sum, xor nor CRC-8 of anything tested; meaning unknown.

## Inferred

- A mask-ROM loader enumerates as `04b8:014a`, answers status `28 00`,
  accepts `1b 06` + 64 KiB into RAM at `0x02000000` and jumps to
  `0x02001000` on `1b 16 80`; the RAM firmware then re-initialises USB and
  warms up the lamp (the ~2.7 s gap before identity, ~8 s warm-up).
- No flash for firmware in the product: the host driver is the only place
  the firmware lives — and the scanner **cannot be bricked** by a wrong
  upload; a power cycle always returns to the ROM loader.
- Address space: internal RAM `0–0xFFF`, image `0x0200xxxx`, scanner-chip
  registers around `0x37FC00–0x37FFFF`, on-chip peripherals `0x48xxx`. A
  decreasing 16-bit table at `0x5400–0x5474` is probably a motor
  acceleration ramp.
- Two further dispatchers (around `0x7E12`, `0x8046/0x8142`) probably serve
  busy/scanning states. `1b 06` is in no table → handled by the ROM loader.

## Siblings (the vendor app 6.7.82, `share/epsonscan2/`)

`esfwdd` (V37/V370) and `esfwad` (V33/V330) are the same source built per
model (13 213 bytes differ in 63 regions). `esfw0282` (V19 II), `esfw010c`
(V19), `esfwA1` (GT-X820), `esfweb` (V550) are an older code generation
(reset at `0x02000400`) with the same `1b` dispatcher. PID → file mapping:
`epsonscan2.rules` (`014a` → `esfwdd.bin`).

## Sources

| Input | SHA-256 |
|---|---|
| `esfwdd.bin` (V37/V370) | `2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954` |
| `esfwad.bin` (V33/V330, PID 0142) | `319fbaf655b8524c09818132dfe0e7d99580f322cad559f7f320b6e0f3f1d6e0` |
| `esfw0282.bin` (V19 II, PID 013e/013f) | `a5b7116dc25f49962354261f7504f944d855723eff954b9d16ab2796e8c9eb64` |
| `esfw010c.bin` (V19, PID 013c/013d) | `14986e0823fb805f97099605e381686aded9a86f58228c508675b5e42b4a6211` |
| `esfwA1.bin` (GT-X820, PID 013a) | `3fde1787469c5cc3624743da6be2671ee297d07ba3fa8439d9d9408631cd8bc3` |
| `esfweb.bin` (V550, PID 013b) | `6d703b3de3bf2c2d691f12b52008beb99b13296d702a35d3690a74de860aad68` |
| `libiscan-plugin-perfection-v370.so.0.0.0` (host side) | `7d0c57d8bd186dbb7a0262b3ce94d28e8c36ec5674457282fc24840d7d35c0ec` |

All from the vendor app 6.7.82.0 Flatpak (Flathub commit
`3f4d56b5b603158af5ae970554812abb52b8dcc0b98f1034f58a4bd3298160bf`).
Host-side cross-reference: iscan 2.30.4-2 (`https://distfiles.gentoo.org/distfiles/ed/iscan_2.30.4-2.tar.gz`,
SHA-256 `597b9913de42d7a27f95bc82e533cbe778121ba13e3a03bc037f8d09a43447d5`) and
The vendor app 6.7.70 source (`http://distcache.freebsd.org/ports-distfiles/epsonscan2-6.7.70.0-1.src.tar.gz`,
SHA-256 `e141e66e4cd74c06eef0baa163f4cc498c3be4a5db82851c914a1b7f2c50967e`):
both only pass the firmware path to the closed plugin, which reads
`$ISCAN_FIRMWARE_DIR` or `/usr/local/share/iscan` + `esfwdd.bin`.
