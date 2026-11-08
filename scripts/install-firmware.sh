#!/bin/bash
#
# install-firmware.sh — obtain the vendor's esfwdd.bin (V37/V370 firmware) from an
# official vendor source, verify it and install it as DIR/firmware/esfwdd.bin
# (default DIR: the current directory, i.e. where your program will be
# started). libv370 does not ship the firmware; this script fetches it from
# The vendor's own distribution instead.
#
# Usage:
#   scripts/install-firmware.sh [--flatpak] [DIR]       from the installed Flatpak of the vendor app (default)
#   scripts/install-firmware.sh --download [DIR]        download the vendor's official bundle from the vendor's website
#   scripts/install-firmware.sh --bundle FILE [DIR]     from the vendor's official bundle downloaded in a browser
#   scripts/install-firmware.sh --file FILE [DIR]       from an esfwdd.bin copied from elsewhere
#   scripts/install-firmware.sh --flathub [DIR]         fetch the vendor app (no runtime) from Flathub
#                                                       with the flatpak client into a temporary directory
#   scripts/install-firmware.sh --auto [DIR]            try, in this order: --download, bundles and
#                                                       esfwdd.bin files in $FW_SOURCES, --flatpak, --flathub
#   scripts/install-firmware.sh --verify FILE           only check a firmware file
#   scripts/install-firmware.sh --url                   print where to download the bundle
#
# The vendor's download center currently answers scripted downloads with HTTP 403
# (nothing here imitates a browser); then download the bundle in a browser
# (see --url) or let --auto fall back. All sources are checked against
# SHA-256 values pinned below:
#   - the bundle: the value Flathub's build manifest pins for the same URL;
#   - the Flatpak: its build manifest must name that bundle with that value;
#   - the firmware: the file every source must yield.
#
# What it needs: sha256sum; for --download also curl; for --bundle/--download
# also bsdtar (libarchive) — or ar, tar and xz/zstd; for --flatpak/--flathub
# also flatpak (python3 optional, for the build-manifest check).
#
set -euo pipefail

FW_SHA256=2fec0d33ff64a98c84fa8c4e17fd64d8121ec10cab39333a53d49d21743dd954
FW_SIZE=65793
BUNDLE_NAME=epsonscan2-bundle-6.7.82.0.x86_64.deb.tar.gz
BUNDLE_URL=https://download-center.epson.com/f/module/19dc56b2-fbdc-4569-a8c1-ccc7224e4573/$BUNDLE_NAME
BUNDLE_SHA256=34e728ab49e245b2a7cff8a0d1cae91c306ce8225e42ddb50eabdb22d4b06b9c
FLATPAK_ID=net.epson.epsonscan2

# Temporary directories, removed on exit.
CLEANUP=()
trap '[ ${#CLEANUP[@]} -eq 0 ] || rm -rf "${CLEANUP[@]}"' EXIT
# newtmp VAR — create a temporary directory and store its path in VAR.
newtmp() { local _newtmp; _newtmp=$(mktemp -d); CLEANUP+=("$_newtmp"); printf -v "$1" %s "$_newtmp"; }

die() { echo "Error: $*" >&2; exit 1; }
sha() { sha256sum "$1" | awk '{print $1}'; }

# verify_fw FILE — size and SHA-256 must match the known firmware.
verify_fw() {
    local f=$1 got size
    [ -f "$f" ] || die "$f not found"
    size=$(stat -c %s "$f")
    got=$(sha "$f")
    [ "$got" = "$FW_SHA256" ] || die "$f has sha256 $got ($size bytes), expected $FW_SHA256 ($FW_SIZE bytes) — different the vendor app version or a modified file"
    echo "verified $f: sha256 $got, $size bytes"
}

install_fw() {
    local src=$1 dir=$2
    verify_fw "$src"
    mkdir -p "$dir/firmware"
    install -m 0644 "$src" "$dir/firmware/esfwdd.bin"
    echo "installed $(realpath "$dir/firmware/esfwdd.bin")"
}

# extract_member ARCHIVE PATTERN OUTDIR — unpack matching members of a tar
# or ar (.deb) archive; bsdtar reads both, otherwise ar/tar.
extract_member() {
    local a=$1 pat=$2 out=$3
    if command -v bsdtar >/dev/null; then
        bsdtar -xf "$a" -C "$out" "$pat" 2>/dev/null || true
    elif [[ $a == *.deb ]]; then
        (cd "$out" && ar x "$(realpath "$a")" 2>/dev/null) || true
    else
        tar -xf "$a" -C "$out" --wildcards "$pat" 2>/dev/null || true
    fi
}

from_bundle() {
    local bundle=$1 dir=$2 got debs deb fw=""
    [ -f "$bundle" ] || die "$bundle not found (download it in a browser: $0 --url)"
    got=$(sha "$bundle")
    if [ "$got" = "$BUNDLE_SHA256" ]; then
        echo "bundle $bundle: sha256 matches the vendor's $BUNDLE_NAME as pinned by Flathub"
    else
        echo "warning: bundle sha256 $got is not the pinned $BUNDLE_NAME ($BUNDLE_SHA256); continuing — the firmware itself is still verified" >&2
    fi
    local TMP
    newtmp TMP
    mkdir -p "$TMP/b"
    tar -xzf "$bundle" -C "$TMP/b" || die "cannot unpack $bundle"
    mapfile -t debs < <(find "$TMP/b" -name '*.deb' | sort)
    [ ${#debs[@]} -gt 0 ] || die "no .deb packages in $bundle (expected the vendor's .deb bundle)"
    for deb in "${debs[@]}"; do
        local d="$TMP/$(basename "$deb" .deb)"
        mkdir -p "$d/data"
        extract_member "$deb" 'data.tar*' "$d"
        for data in "$d"/data.tar*; do
            [ -f "$data" ] || continue
            extract_member "$data" '*esfwdd.bin' "$d/data"
        done
        fw=$(find "$d/data" -name esfwdd.bin -type f | head -1)
        if [ -n "$fw" ]; then
            echo "found ${fw#"$d/data/"} in $(basename "$deb")"
            install_fw "$fw" "$dir"
            return
        fi
    done
    die "esfwdd.bin not found in any package of $bundle"
}

from_download() {
    local dir=$1 code d
    command -v curl >/dev/null || die "curl not found"
    newtmp d
    code=$(curl -sS -L --max-time 600 -o "$d/$BUNDLE_NAME" -w '%{http_code}' "$BUNDLE_URL") || code=000
    [ "$code" = 200 ] || die "the vendor's website answered HTTP $code for $BUNDLE_URL (it refuses scripted downloads; download it in a browser and use --bundle)"
    from_bundle "$d/$BUNDLE_NAME" "$dir"
}

# from_flathub DIR — install only the vendor app (no runtime, nothing
# is run) with the official flatpak client into a temporary user
# installation, then take the firmware from it.
from_flathub() {
    local dir=$1
    command -v flatpak >/dev/null || die "flatpak not found"
    local d
    newtmp d
    export FLATPAK_USER_DIR="$d"
    flatpak remote-add --user --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo ||
        die "cannot add the Flathub remote"
    # bwrap may complain inside containers (post-install triggers); the files
    # are installed regardless and checked below.
    flatpak install --user --noninteractive --no-deps --no-related flathub "$FLATPAK_ID//stable" >/dev/null 2>&1 || true
    from_flatpak "$dir" --user
}

# from_flatpak DIR [--user] — firmware of the installed Flatpak of the vendor app.
from_flatpak() {
    local dir=$1 scope=${2:-} loc manifest
    command -v flatpak >/dev/null || die "flatpak not found; install the vendor app ($FLATPAK_ID) or use --bundle"
    loc=$(flatpak info $scope --show-location "$FLATPAK_ID" 2>/dev/null) ||
        die "Flatpak of the vendor app not installed: sudo flatpak install flathub $FLATPAK_ID (or use --bundle)"
    manifest="$loc/files/manifest.json"
    if [ -f "$manifest" ] && command -v python3 >/dev/null; then
        # The build manifest records the sources the Flatpak was built from.
        if python3 -I - "$manifest" "$BUNDLE_URL" "$BUNDLE_SHA256" <<'EOF'
import json, sys
manifest, url, want = sys.argv[1:]
def sources(mods):
    for m in mods:
        if isinstance(m, dict):
            yield from (s for s in m.get("sources", []) if isinstance(s, dict))
            yield from sources(m.get("modules", []))
found = [s for s in sources(json.load(open(manifest)).get("modules", [])) if s.get("url") == url]
sys.exit(0 if found and found[0].get("sha256") == want else 1)
EOF
        then
            echo "Flatpak build manifest: built from the vendor's $BUNDLE_NAME (sha256 $BUNDLE_SHA256)"
        else
            echo "warning: the Flatpak build manifest does not name $BUNDLE_NAME with the pinned sha256 (other version?)" >&2
        fi
    fi
    install_fw "$loc/files/share/epsonscan2/esfwdd.bin" "$dir"
}

# auto DIR — try every source in order of preference; the first one that
# yields the verified firmware wins.
auto() {
    local dir=$1 f
    try() { echo "== trying $*"; "$0" "$@" "$dir" && exit 0; echo "   (not available)"; }
    try --download
    if [ -n "${FW_SOURCES:-}" ] && [ -d "$FW_SOURCES" ]; then
        for f in "$FW_SOURCES"/*.tar.gz; do [ -f "$f" ] && try --bundle "$f"; done
        for f in "$FW_SOURCES"/esfwdd.bin "$FW_SOURCES"/*/esfwdd.bin; do [ -f "$f" ] && try --file "$f"; done
    fi
    command -v flatpak >/dev/null && flatpak info "$FLATPAK_ID" >/dev/null 2>&1 && try --flatpak
    command -v flatpak >/dev/null && try --flathub
    die "no source yielded the verified firmware (download the vendor's bundle in a browser: $0 --url)"
}

case "${1:-}" in
--url)
    echo "Download in a browser (the vendor's server refuses scripted downloads):"
    echo "  $BUNDLE_URL"
    echo "expected sha256: $BUNDLE_SHA256"
    echo "then: $0 --bundle ~/Downloads/$BUNDLE_NAME [DIR]"
    ;;
--verify)
    [ $# -eq 2 ] || die "usage: $0 --verify FILE"
    verify_fw "$2"
    ;;
--file)
    [ $# -ge 2 ] && [ $# -le 3 ] || die "usage: $0 --file FILE [DIR]"
    install_fw "$2" "${3:-.}"
    ;;
--download)
    [ $# -le 2 ] || die "usage: $0 --download [DIR]"
    from_download "${2:-.}"
    ;;
--flathub)
    [ $# -le 2 ] || die "usage: $0 --flathub [DIR]"
    from_flathub "${2:-.}"
    ;;
--auto)
    [ $# -le 2 ] || die "usage: $0 --auto [DIR]"
    auto "${2:-.}"
    ;;
--bundle)
    [ $# -ge 2 ] && [ $# -le 3 ] || die "usage: $0 --bundle FILE [DIR]"
    from_bundle "$2" "${3:-.}"
    ;;
--flatpak)
    [ $# -le 2 ] || die "usage: $0 --flatpak [DIR]"
    from_flatpak "${2:-.}"
    ;;
-h | --help)
    sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'
    ;;
-*)
    die "unknown option $1 (see --help)"
    ;;
*)
    [ $# -le 1 ] || die "usage: $0 [--flatpak] [DIR]"
    from_flatpak "${1:-.}"
    ;;
esac
