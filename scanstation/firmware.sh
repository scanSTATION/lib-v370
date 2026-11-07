#!/bin/bash
#
# firmware.sh DEST — scanSTATION contract: obtain and verify the firmware
# the v370 driver needs and install it as DEST/firmware/esfwdd.bin.
#
# The firmware is the vendor's proprietary file and is not part of this
# repository. Sources, in order (each verified by SHA-256; see
# scripts/install-firmware.sh --help):
#   1. The vendor's website (currently refuses scripted downloads)
#   2. bundles / esfwdd.bin files in $FW_SOURCES (e.g. downloaded in a browser)
#   3. an installed the vendor app Flatpak
#   4. Flathub, with the official flatpak client (only the vendor app app)
#
set -euo pipefail
DEST=${1:?usage: $0 DEST}
exec "$(dirname "$0")/../scripts/install-firmware.sh" --auto "$DEST"
