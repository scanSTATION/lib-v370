# esfwdd.bin (Perfection V37/V370, GT-F740, 04b8:014a): what it is

## Short answer

**`esfwdd.bin` is the scanner's complete main firmware.** It is executable machine code for an
**the vendor S1C33 (C33) 32-bit RISC core** (16-bit instructions, little-endian), built with a C compiler.
It is linked to run from **0x02000000** (external RAM). It is not a config blob, not an FPGA bitstream
and not an unlock stub. The device can do very little until this code is loaded: it only runs a small
boot loader in ROM.

Strongest evidence (all **verified** by static analysis):

1. **A trap/vector table at payload offset 0, and the C33 startup code at 0x1000.** The reset vector is
   0x02001000. Payload offset 0x1000 does what a C33 C runtime does at startup:
   - clears r0–r15, %psr, %ahr and %alr
   - sets %sp = 0x1000 and the global base pointer r8 = 0x200
   - zeroes internal RAM 0x0–0xFFF
   - copies the vector table (0x200 bytes) and the ISR stubs to internal RAM
   - unlocks TTBR with 0x59 at 0x4812D and sets TTBR (0x48134) = 0
   - calls `main` (0x0200140C)
2. **The firmware handles the native protocol commands itself.** At 0x020055DE it checks
   prefix byte `[0x44C] == 0x1B`, then jumps through a table indexed by `cmd-3`. ESC commands
   handled: **03, 13, 16**, 20–25, 40. At 0x0200578A it checks `[0x44C] == 0x1E`, then a
   255-entry table indexed by `cmd-1`. Every `1e` command we know is there: **41, 47, 53, 57, 84,
   85, 8E, 8F, 93**, plus about 40 more. `1b 06` (the upload command) is *not* in the table, so the
   ROM must handle it.
3. **The identity reply and the full USB descriptor set are inside the payload.** The `1b 13` handler
   (0x02006F00) picks the 28-byte identity string for `GT-F740` or `GT-S640` (version `1.00`) from
   payload 0x558C/0x55A8, based on a variant flag, and sends 0x1C bytes. The device descriptor at
   0x5480 has VID 04b8, PID 014a and bcdDevice 1.00. The string descriptors include the
   product name "… Perfection V37/V370". The code serves these descriptors on GET_DESCRIPTOR (it clamps to
   wLength) and sets wMaxPacketSize to 64 or 512 depending on bus speed.

So the design is the usual one for the vendor's cheap ASIC scanners: a **mask-ROM boot loader inside the
ASIC/SoC** speaks just enough USB and ESC to accept `1b 06` plus 64 KiB. After `1b 16 80` the CPU jumps
into the RAM image. That image then re-enumerates on USB, initialises the hardware (lamp warm-up, etc.)
and implements the whole scanner protocol. RAM is volatile, so the host must upload it again after
every power-on.

---

## Inputs and tools

| File | SHA-256 |
|---|---|
| repo `internal/v370/firmware/esfwdd.bin` (65 793 B) | 2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954 |
| flatpak `share/epsonscan2/esfwdd.bin` (identical) | 2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954 |
| esfwad.bin (V33/V330, PID 0142) | 319fbaf655b8524c09818132dfe0e7d99580f322cad559f7f320b6e0f3f1d6e0 |
| esfw0282.bin (V19 II, PID 013e/013f) | a5b7116dc25f49962354261f7504f944d855723eff954b9d16ab2796e8c9eb64 |
| esfw010c.bin (V19, PID 013c/013d) | 14986e0823fb805f97099605e381686aded9a86f58228c508675b5e42b4a6211 |
| esfwA1.bin (GT-X820, PID 013a) | 3fde1787469c5cc3624743da6be2671ee297d07ba3fa8439d9d9408631cd8bc3 |
| esfweb.bin (Perfection V550, PID 013b) | 6d703b3de3bf2c2d691f12b52008beb99b13296d702a35d3690a74de860aad68 |
| libiscan-plugin-perfection-v370.so.0.0.0 | 7d0c57d8bd186dbb7a0262b3ce94d28e8c36ec5674457282fc24840d7d35c0ec |

The PID-to-file mapping comes from the flatpak's `Resources/Models/epsonscan2.rules`, e.g.
`ATTRS{idProduct}=="014a", … ENV{firmware_file}="esfwdd.bin"` (**verified**).

**Tools.** The local binutils `objdump` only supports x86 and BPF. Capstone (`cstool` 5.0.9) has no
S1C33 target. I wrote a small S1C33 decoder for this job (`scripts/s1c33dis.py`). Classes 0
(branches and control), 1 (register and memory), 3 (immediate ALU) and 6 (`ext`) decode cleanly and
make sense in context. Some class 4 and 5 mnemonics (shift, multiply, sign-extend) are only
approximate. No conclusion below depends on them.

---

## Q1: The 256-byte EDLA header

```
0x00: 45 44 4c 41  "EDLA"   (rest 0x00 except:)
0x40: 00 01 00 00  01 00 00 01  00 00 b8 04  01 00 01 00
```

- **Verified:** the header is byte-for-byte identical in all six esfw*.bin files. It has no model ID,
  version, size, load address or checksum that differs between models.
- **Verified:** bytes 0x4A–0x4B are `b8 04`, which is the vendor's VID 0x04b8 (LE).
- **Verified:** bytes 0x4C–0x4F `01 00 01 00` are exactly the 4-byte block the host sends after `1b 06`.
  *Inferred:* the host copies this from the header. It could mean "1 block of 64 KiB" or a
  format/version word.
- **Inferred:** "EDLA" is probably an "the vendor DownLoad" container tag. The load address is not in the
  header. It is implied by the payload, which is linked for 0x02000000.
- **Unknown:** the meaning of 0x41/0x44/0x47 = 01.
- **Unknown:** the trailing byte at file offset 65 792 (dd 0xCE, ad 0x8B, 0282 0x20, 010c 0xBF,
  A1 0xF7, eb 0x57). For all six files it is none of these: sum8, neg, not or xor over the payload,
  the header or the whole file; 16-bit word sums; any CRC-8 (all 128 odd polynomials, init 00/FF,
  reflected or not, xorout 00/FF). The host does not send it (protocol.md). The 0xAF that is sent is
  simply sum8 of the payload, as already known.

## Q2: Is the payload code? Which CPU?

**Entropy per 4 KiB block (esfwdd):**
1.3–2.6 / 4.8–6.5 (code) / … / 4.8 / 0 / 0 / 0. That is about 6.1–6.5 bits per byte in the code area,
with 0xFF padding after 0xC9D4. The siblings look the same. **Verified: not compressed and not
encrypted.** Packed or encrypted data would be about 8.0 bits per byte everywhere.

**Byte statistics show 16-bit instructions, little-endian (verified).** Over 0x0000–0xC9D4:
- Odd-offset (high) bytes are dominated by 0xC0/0xC1/0xDF (4422/1466/954), 0x6C (1312), 0x1C, 0x34,
  0x24, 0x2E and 0x68.
- Even-offset bytes are dominated by 0x00, 0x8A, 0xBF and 0x0A.
- Splitting halfwords by their top 3 bits gives class 6 (`ext imm13`) at 8369, the biggest group.
  Class 7 (reserved in C33) occurs only as 0xFFFF padding, with 12 other cases.
- The most common halfwords are `c000` (ext 0), `c1bf`/`dff8` (ext), `0200` (pushn %r0) and
  `0640` (ret).

This fingerprint fits the **the vendor S1C33000 core**: 16-bit fixed-length instructions, a 13-bit `ext`
prefix to build 32-bit immediates, `pushn`/`popn` register saves, and `ret`/`reti`. The vendor uses this
core in its own ASICs.

**The vector table (verified).** Payload offset 0x00 holds 32-bit LE entries:

| off | value | C33 meaning |
|---|---|---|
| 0x00 | 0x02001000 | reset |
| 0x04–0x0C | 0x0000012C | reserved (default handler) |
| 0x10 | 0x00000120 | zero-division |
| 0x18 | 0x00000126 | address error |
| 0x1C | 0x020010BC | NMI |
| 0x40–0x4C | 0x4A0, 0x4B8, 0x4D0, 0x4E8 | maskable interrupts (ISR stubs in internal RAM) |
| 0x50 | 0x020010FC | interrupt |
| 0x54… | 0x00000132 | default handler for the remaining interrupts |

The repeated `2c 01 00 00` values are **not "300" values**. They are vector entries that point to
the default trap handler. Each handler at 0x120/0x126/0x12C/0x132 is the C33 idiom
`nop; jp self; reti` (`0000 1eff 04c0`), which halts the CPU on an unexpected trap.

**Startup code at payload 0x1000 = 0x02001000 (verified, excerpt):**
```text
reset handler at 0x02001000 (pseudocode, summarised):
  clear all registers; SP = 0x1000; global base r8 = 0x200
  if (ASIC 0x37FC02 & 1) == 0: jump to 0x02000000          (warm path)
  memset(internal RAM 0x000–0xFFF, 0)
  copy vector table   (0x200 B)  0x02000000 → 0x000
  copy ISR stubs      (0x3D0 B)  0x02000400 → 0x4A0
  unlock + set trap-table base to internal RAM (registers 0x4812D / 0x48134)
  call main (0x0200140C); loop forever if it returns
```
Cross-checks:
- The vectors 0x12C, 0x4A0 and so on point into the copied internal RAM.
- The ISR stub at payload 0x400 (runs at 0x4A0) saves %ahr/%alr, calls a C handler in 0x0200xxxx,
  restores and does `reti`.
- 1010 direct `call` instructions reach 254 distinct targets. 243 of them sit at a function boundary
  (previous instruction is ret/jp/reti/padding, or the target starts with `pushn`).
- There are 278 `ret` and ISR `reti` instructions.
- About 15 gcc-style switch jump tables of 0x0200xxxx pointers exist (`sub; cmp; jrugt default;
  sll 2; ld.w table; jp %r10`).
- Memory map *inferred* from 32-bit constants:
  - 0x0000–0x0FFF: internal RAM (globals at r8+…, SP top 0x1000)
  - 0x02000000–0x0200FFFF: the image itself (96 references)
  - 0x37xxxx: 65 references, likely the scanner-ASIC register block 0x37FC00–0x37FFFF, used as
    absolute constants and as `[r8+0x37fdxx]`
  - 0x48xxx: C33 on-chip peripherals; 0x4812D/0x48134 match the S1C33209 TTBR registers
    (*from memory of the vendor docs, not re-checked online*)
- *Unknown:* why startup jumps to 0x02000000 when bit 0 of 0x37FC02 is set. It may be a soft-reset
  or remap flag.

## Q3: Data inside the payload (verified unless marked)

- **Identity strings:** `GT-F740` version `1.00` @0x558C and `GT-S640` version `1.00`
  @0x55A8. So "1.00" is the firmware version that `1b 13` reports, and it lives in this file.
- **USB descriptors:**
  - @0x5480 device descriptor: `12 01 00 02 ff ff ff 40 b8 04 4a 01 00 01 01 02 00 01`, i.e. USB 2.0,
    vendor class, EP0 64, **VID 04b8 PID 014a**, bcdDevice 1.00, 1 configuration.
  - @0x5492 configuration (wTotalLength 32, self-powered, 2 mA) and interface (2 endpoints, class FF).
  - EP 0x81 bulk-IN 512 and EP 0x02 bulk-OUT 512.
  - @0x54B2 device qualifier.
  - @0x54C0 LANGID 0x0409.
  - UTF-16 strings: manufacturer name and the product names "… Perfection V37/V370", "… GT-S640/F740", "… Scanner".
  - More descriptor copies @0x95B8/0x9638 (a second copy of the PID 0x014A descriptor), plus
    `b8 04 31 01` (PID 0x0131) @0x96BA. *Inferred:* templates or a fallback identity.
- **Code that uses them:**
  - 0x02003476 sends the device descriptor with length `min(desc[0], wLength@[r8+0x1BA])`.
  - 0x02003528 does the same for string descriptors.
  - 0x02003482–0x020034E2 builds the config descriptor in RAM (0x388/0x389) and writes
    wMaxPacketSize as 0x0040 or 0x0200 depending on bus speed.
  - So the RAM firmware runs the full USB device stack itself.
- **Variant select:** `[r8+0x1BE] & 7 == 1` picks GT-F740, else GT-S640. *Inferred:* hardware strap
  or TPU detection, because V37 and V370 share one image.
- **Tables:** @0x5400–0x5474 a monotonically decreasing u16 sequence (0x0603, 0x05B9, 0x0579 … 0x024B).
  *Inferred:* a stepper-motor acceleration ramp (step periods). Other dense u16/byte tables
  (@0x4C74, 0x8860 …) are *unknown*; likely gamma, shading or timing tables.
- Apart from these strings, the payload has no readable strings (58 runs of 5+ chars, almost all
  opcode noise). There are no debug or log messages.

## Q4: Native command dispatch (verified)

Globals: r8 = 0x200. The received command header is at 0x44C (prefix) and 0x44D (command).

| Location | Guard | Index | Command cases with their own handler |
|---|---|---|---|
| 0x020055DE / 0x02005620, table 0x5638 (62) | `[0x44C]==0x1B` | cmd−3 | **03, 13, 16**, 20, 21, 22, 23, 24, 25, 40 |
| 0x0200578A / 0x020057DC, table 0x57F4 (255) | `[0x44C]==0x1E` | cmd−1 | 01 04 05 11 18 21 22 25 31 **41** 42 43 45 46 **47** 48 **53 57** 65 68 77 83 **84 85** 86 87 89 8A 8C **8E 8F** 90 92 **93** 95 96 97 98 99 9A 9B 9F A1 A2 B0 B1 DE FA FF |
| 0x02007DDC→0x02007E12, table 0x7E2C | 1B (03/13 inline) / 1E, cmd−0x53 | | 1E: 53 57 77 85 86 87 8E 8F 90 92 93 95 96 97 A1 B0 (likely the subset allowed while busy/scanning, *inferred*) |
| 0x02008046→0x0200804C / 0x02008142 | 1B cmd−3 / 1E cmd−0x11 | | 1B: 03 13 20–25; 1E: 11 18 21 31 41 42 43 53 57 77 83–87 8E 8F 90 92 93 95 96 97 A1 B0 (second state-machine dispatcher) |

- **`1b 13` handler (verified):** loads the identity string (see Q3) and calls the send routine
  0x02003D8C with length 0x1C.
- **`1b 06` (upload) is not in any RAM-firmware table.** Together with the observed behaviour
  (status `28 00` and nothing else useful before the upload), this puts the loader in ROM
  (*inferred*). The restart `1b 16` *is* in the RAM table (case 0x16), so a running image can reboot
  itself.

## Sibling comparison

- All six files use the same container (same header). Every payload starts with a C33 vector table
  and is linked at 0x02000000 (**verified**).
- There are two code generations:
  - **dd (V37) / ad (V33):** reset at 0x02001000, the same layout and dispatchers, and identical
    offsets for strings and descriptors. 13 213 bytes differ in 63 regions, mostly 2-byte call
    displacements and data, i.e. the same source rebuilt per model.
  - **0282 (V19 II), 010c (V19), A1 (GT-X820), eb (V550):** reset at 0x02000400, NMI 0x020004BC,
    default handler 0x0560.
- **Model-specific parts:** identity string ("GT-S660 1.00", "GT-S650 1.42", "GT-X820 1.10",
  "Perfection V550 1.02", "GT-F730/S630 1.10"), USB PID and strings, and tables.
- **Common to all:** a 1B dispatcher with cmd−3 over 03, 13, (16), 20–25, 40.

## Q5: Conclusion

- **Verified:** the 64 KiB payload is the scanner's entire runtime firmware. It is C-compiled S1C33
  code, linked at 0x02000000, with its own trap table, C runtime start-up, ISRs, USB device stack and
  descriptors, ESC (`1b`) and native (`1e`) command interpreter, identity/version string, and
  motor/scan tables.
- **Inferred:** what runs before it is a **small mask-ROM boot loader in the vendor scanner ASIC**,
  which has an embedded S1C33 core and a USB 2.0 device controller. The loader:
  - enumerates as 04b8:014a
  - answers a minimal status (`28 00`)
  - accepts `1b 06` + `01 00 01 00` + 65 536 bytes + a sum8 byte into external RAM at 0x02000000
  - on `1b 16 80`, jumps to the image's reset vector 0x02001000
- **Inferred:** the image then sets TTBR to internal RAM, re-initialises and re-enumerates USB, and
  starts the hardware (the ~8 s lamp warm-up). This explains the 2.7 s gap and the identity string
  that appears afterwards.
- **Inferred:** the per-power-on upload means the product has no flash for firmware (a cost-reduced
  design). The host driver is therefore the firmware's only store. Our own driver must always carry
  and upload this exact image. "Updating firmware" just means shipping a new .bin.
- **Inferred:** the scanner itself has no persistent firmware, so a bad image cannot brick it; a
  power cycle brings back the ROM loader.
- **Unknown:** what the 0x37FCxx–0x37FFxx registers do, beyond motor/lamp/CCD control by the ASIC;
  what the trailing file byte means; what the remaining header fields mean.

## Host side (cross-reference)

- The Flatpak udev rules map PIDs to firmware files. The V370 interpreter plugin contains the strings
  `ISCAN_FIRMWARE_DIR`, `/usr/local/share/iscan` and `esfwdd.bin` (`strings -t x`: 0x36724, 0x3673B,
  0x36750). It imports only `fopen`/`fread`/`fseek`, so it reads the file itself. Its symbols are
  obfuscated (`libiscan_plugin_perfection_v370_NNN`), so the upload code was not traced.
- iscan 2.30.4 (`backend/cfg-obj.c:856`, the config syntax
  `interpreter usb <vid> <pid> <library> [firmware-file]`) only passes the firmware path to the
  closed interpreter. There is no open upload code or format comment in it, nor in the vendor app
  6.7.70 (only `kPrvHRD_FWDriverName` in `ModelInfo.hpp`).

## Downloads (stored in their own directories, never executed)

- https://distfiles.gentoo.org/distfiles/ed/iscan_2.30.4-2.tar.gz:
  SHA-256 597b9913de42d7a27f95bc82e533cbe778121ba13e3a03bc037f8d09a43447d5
- http://distcache.freebsd.org/ports-distfiles/epsonscan2-6.7.70.0-1.src.tar.gz:
  SHA-256 e141e66e4cd74c06eef0baa163f4cc498c3be4a5db82851c914a1b7f2c50967e (matches the given hash)

## Reproduction

Scratch files of the analysis are not part of this repository.

- `in/`: copies of the inputs
- `*.pl`: the 65 536-byte payloads (`dd if=… bs=1 skip=256 count=65536`)
- `scripts/stats.py`: checksums, entropy and byte frequencies
- `scripts/lastbyte.py`: trailing-byte search
- `scripts/s1c33dis.py <payload> <start> <end> 0x2000000`: disassembler
- `scripts/switches.py <payloads…>`: finds the switch/dispatch tables

No USB device was accessed. The repository was not modified.
