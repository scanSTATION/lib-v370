# scanSTATION driver contract

lib-v370 is a scanner library for [scanSTATION-server](https://github.com/scanSTATION/scanSTATION-server),
included there as a git submodule. This folder tells the server's build what
the library provides.

| File | Purpose |
|---|---|
| `drivers` | drivers in this library: name (= Go build tag in the server) and the firmware script, or `-` |
| `firmware.sh DEST` | downloads and verifies the firmware the driver needs into `DEST/firmware/` |

Drivers of this library:

| Driver | Scanner / software | Firmware |
|---|---|---|
| `v370` | Perfection V37/V370 (USB `04b8:014a`), own USB driver (packages `usb`, `v370`, `rawimg`, `output`) | The vendor's `esfwdd.bin`, uploaded after every power-on; fetched by `firmware.sh` from the vendor's distribution (never stored in this repository) |
| `epsonscan2` | The vendor app (Flatpak or native) as a fallback (package `vendorapp`) | none (the vendor app brings its own; must be installed on the host) |

The server builds only the drivers named in its `DRIVERS` build argument
(e.g. `docker build --build-arg DRIVERS="v370" .`) and runs the firmware
scripts of exactly those drivers. A library for another scanner provides the
same two files.
