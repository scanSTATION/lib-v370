#!/bin/bash
#
# test-install-firmware.sh — self-test of install-firmware.sh with synthetic
# The vendor-style bundles (tar.gz → .deb → data.tar.xz → esfwdd.bin). Needs a
# genuine firmware file (not part of this repository):
#
#   scripts/test-install-firmware.sh PATH/TO/esfwdd.bin
#
# Network: --auto tries the vendor's website first; --flathub downloads the vendor
# Scan 2 app (~27 MB). SKIP_NETWORK=1 skips the Flathub test.
#
set -euo pipefail
FW=$(realpath "${1:?usage: $0 PATH/TO/esfwdd.bin}")
SCRIPT=$(realpath "$(dirname "$0")/install-firmware.sh")
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0

# bundle NAME FIRMWARE — build a vendor-like bundle containing FIRMWARE.
bundle() {
    local w="$T/build-$1"
    mkdir -p "$w/data/usr/share/epsonscan2" "$w/ctl" "$w/b/epsonscan2-bundle/plugins"
    cp "$2" "$w/data/usr/share/epsonscan2/esfwdd.bin"
    echo "Package: epsonscan2-non-free-plugin" > "$w/ctl/control"
    echo "2.0" > "$w/debian-binary"
    tar -cJf "$w/data.tar.xz" -C "$w/data" .
    tar -cJf "$w/control.tar.xz" -C "$w/ctl" .
    (cd "$w" && ar rc b/epsonscan2-bundle/plugins/epsonscan2-non-free-plugin_1.0.0.6-1_amd64.deb debian-binary control.tar.xz data.tar.xz)
    tar -czf "$T/$1.tar.gz" -C "$w/b" .
}

# expect ok|fail NAME CMD... — run CMD, check the outcome.
expect() {
    local want=$1 name=$2 got
    shift 2
    if "$@" >"$T/out" 2>&1; then got=ok; else got=fail; fi
    if [ "$got" = "$want" ]; then
        echo "PASS $name"
    else
        echo "FAIL $name (want $want, got $got):"; sed 's/^/    /' "$T/out"; fail=1
    fi
}

bundle good "$FW"
cp "$FW" "$T/tampered.bin"
printf '1' | dd of="$T/tampered.bin" bs=1 seek=$((0x56a7)) conv=notrunc status=none
bundle tampered "$T/tampered.bin"
mkdir -p "$T/empty" && echo x > "$T/empty/readme" && tar -czf "$T/nodeb.tar.gz" -C "$T/empty" .

expect ok   "bundle with genuine firmware installs" "$SCRIPT" --bundle "$T/good.tar.gz" "$T/dest"
expect ok   "installed file verifies"               "$SCRIPT" --verify "$T/dest/firmware/esfwdd.bin"
expect fail "bundle with modified firmware"          "$SCRIPT" --bundle "$T/tampered.tar.gz" "$T/dest2"
[ ! -e "$T/dest2/firmware/esfwdd.bin" ] && echo "PASS modified firmware not installed" || { echo "FAIL modified firmware installed"; fail=1; }
expect fail "bundle without packages"                "$SCRIPT" --bundle "$T/nodeb.tar.gz" "$T/dest3"
expect fail "missing bundle"                         "$SCRIPT" --bundle "$T/missing.tar.gz"
expect fail "modified file does not verify"          "$SCRIPT" --verify "$T/tampered.bin"
expect ok   "--url"                                  "$SCRIPT" --url
expect ok   "--file with genuine firmware"           "$SCRIPT" --file "$FW" "$T/dest5"
expect fail "--file with modified firmware"          "$SCRIPT" --file "$T/tampered.bin" "$T/dest6"
mkdir -p "$T/src-bundle" "$T/src-bad" "$T/src-mixed"
cp "$T/good.tar.gz" "$T/src-bundle/"
cp "$T/tampered.tar.gz" "$T/src-bad/"
cp "$T/tampered.tar.gz" "$T/src-mixed/a.tar.gz"; cp "$FW" "$T/src-mixed/esfwdd.bin"
expect ok   "--auto falls back to a bundle in FW_SOURCES" env FW_SOURCES="$T/src-bundle" "$SCRIPT" --auto "$T/dest7"
expect ok   "--auto skips a modified bundle, takes esfwdd.bin" env FW_SOURCES="$T/src-mixed" "$SCRIPT" --auto "$T/dest8"
if command -v flatpak >/dev/null && flatpak info net.epson.epsonscan2 >/dev/null 2>&1; then
    expect ok "installed Flatpak" "$SCRIPT" --flatpak "$T/dest4"
    grep -q "built from the vendor's" "$T/out" && echo "PASS Flatpak manifest names the vendor's bundle" || { echo "FAIL Flatpak manifest check"; fail=1; }
fi
if command -v flatpak >/dev/null && [ "${SKIP_NETWORK:-}" != 1 ]; then
    expect ok "--flathub (network, official flatpak client, temporary dir)" "$SCRIPT" --flathub "$T/dest9"
fi
exit $fail
