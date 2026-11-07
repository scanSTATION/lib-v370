# V370 firmware (`esfwdd.bin`)

The Perfection V37/V370 loses its firmware when it is switched off. On
the first access after power-on the host must upload `esfwdd.bin`; the
scanner reports this with status `28 00` (docs/protocol.md §3.2). As long
as the scanner stays powered, no file is needed.

**`esfwdd.bin` is the vendor's proprietary file and is not part of this
library.** The library loads it from

    ./firmware/esfwdd.bin      (relative to the working directory the program is started in)

or from an explicit path (`v370.Options.Firmware`, `firmware.Load`,
`v370 -firmware PATH`). When the file is needed but missing, the
error names the absolute path that was searched. Services (systemd,
containers) therefore need a defined working directory, or an explicit path.

Only the known file is accepted (SHA-256 check); other files require an
explicit opt-in (`allowUnknown` / `-allow-unknown-firmware`), because a
wrong firmware could leave the scanner unusable until it is switched off.

| Property | Value |
|---|---|
| Size | 65 793 bytes |
| SHA-256 | `2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954` |
| Origin | The vendor app 6.7.82.0 (Flatpak `net.epson.epsonscan2`, Flathub commit `3f4d56b5b603158af5ae970554812abb52b8dcc0b98f1034f58a4bd3298160bf`), `files/share/epsonscan2/esfwdd.bin` |
| Layout | 256-byte header (starts with `EDLA`), 65 536-byte payload, 1 trailing byte |
| Sent to the scanner | payload only (bytes 256 … 65 791) + checksum byte = sum of the payload mod 256 (`0xaf`) |

## Installing it

```bash
# 1. Install the vendor app (Flathub), if not yet installed
sudo flatpak install flathub net.epson.epsonscan2

# 2. Copy and verify (into ./firmware/ of the directory you start your program from)
scripts/install-firmware.sh /path/to/your/app

# or by hand:
cp "$(flatpak info --show-location net.epson.epsonscan2)/files/share/epsonscan2/esfwdd.bin" firmware/
sha256sum firmware/esfwdd.bin
```

Other the vendor models use other files from the same directory (`esfw0282.bin`,
`esfwA1.bin`, …); they are not compatible with the V370.

Tests and the simulator do not need the file: `firmware.FromPayload` takes
the 64 KiB payload as recorded in a USB trace.
