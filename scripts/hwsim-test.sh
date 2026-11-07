#!/bin/bash
#
# hwsim-test.sh — Tier-2 integration tests against the simulated scanner,
# NO real scanner needed.
#
# Starts the USB simulator with a recorded trace, attaches it to the local
# kernel via vhci-hcd (the scanner appears as a real USB device), runs one
# hwsim test and detaches.
#
# Usage:
#   scripts/hwsim-test.sh TRACE [--disable-real] [--run REGEX] [--pkg DIR]
#
#   --pkg DIR       Go test package to run (default: this library's
#                   integration/hwsim). Applications pass their own package;
#                   environment variables they need are passed through.
#   --run REGEX     which test to run (default TestDirectScan). Each test
#                   consumes the simulator session, so run them one at a time:
#                     TestUsbfsReplay   our pure-Go usbfs transport → simulator
#                     TestDirectOpen    our protocol layer (v370.Open) → simulator
#                     TestDirectScan    v370 scan (built-in profile) → simulator
#
#   --disable-real  if the real V370 is plugged in, switch it off in software
#                   (sysfs authorized=0) for the test and back on afterwards
#
# Needs: root for the vhci attach (sudo, or V370_ROOT=docker — see
# scripts/lib/asroot.sh), the vhci-hcd kernel module, and the udev rule that
# grants the user access to 04b8:014a (udev/60-v370.rules).
# LIBV370_FIRMWARE=/path/to/esfwdd.bin is needed for TestDirectScan with a
# trace that contains a firmware upload.
# A real V370 must not be active at the same time (unplug it, or use
# --disable-real).
#
set -euo pipefail

TRACE="${1:-}"
[ -n "$TRACE" ] || { sed -n '2,16p' "$0"; exit 2; }
[ -f "$TRACE" ] || { echo "Error: trace '$TRACE' not found (record one with scripts/capture-scan.sh)" >&2; exit 1; }
shift
EXTRA=("$@")

CALLER="$PWD"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=lib/asroot.sh
. "$ROOT/scripts/lib/asroot.sh"
TRACE="$(cd "$CALLER" && realpath "$TRACE")"
BIN="$ROOT/bin"
mkdir -p "$BIN"
STATUS_ADDR="127.0.0.1:3241"
LISTEN="127.0.0.1:3240"


# real_scanners prints sysfs paths of physical (non-vhci) 04b8:014a devices.
real_scanners() {
    local d
    for d in /sys/bus/usb/devices/*; do
        [ -f "$d/idVendor" ] || continue
        [ "$(cat "$d/idVendor")" = 04b8 ] && [ "$(cat "$d/idProduct")" = 014a ] || continue
        readlink -f "$d" | grep -q vhci_hcd && continue
        echo "$d"
    done
}
sim_scanner_present() {
    local d
    for d in /sys/bus/usb/devices/*; do
        [ -f "$d/idVendor" ] || continue
        [ "$(cat "$d/idVendor")" = 04b8 ] && [ "$(cat "$d/idProduct")" = 014a ] || continue
        readlink -f "$d" | grep -q vhci_hcd && return 0
    done
    return 1
}

DISABLE_REAL=0
RUN=TestDirectScan
PKG="$ROOT/integration/hwsim"
for ((i = 0; i < ${#EXTRA[@]}; i++)); do
    case "${EXTRA[$i]}" in
        --disable-real) DISABLE_REAL=1 ;;
        --run) RUN="${EXTRA[$((i + 1))]:-}"; i=$((i + 1)) ;;
        --pkg) PKG="$(cd "$CALLER" && realpath "${EXTRA[$((i + 1))]:-}")"; i=$((i + 1)) ;;
    esac
done
[[ "$RUN" =~ ^[A-Za-z0-9_|.*^$]+$ ]] || { echo "Error: invalid --run regex" >&2; exit 2; }
DISABLED=()
for d in $(real_scanners); do
    [ "$(cat "$d/authorized")" = 1 ] || continue
    if [ "$DISABLE_REAL" = 1 ]; then
        echo "==> Disabling real scanner $(basename "$d") for the test"
        as_root sh -c "echo 0 > $(readlink -f "$d")/authorized"
        DISABLED+=("$(readlink -f "$d")")
    else
        echo "Error: the real V370 ($(basename "$d")) is active — unplug it or pass --disable-real" >&2
        exit 1
    fi
done
restore_real() {
    local d
    for d in "${DISABLED[@]}"; do
        echo "==> Re-enabling real scanner $(basename "$d")"
        as_root sh -c "echo 1 > $d/authorized"
    done
}
trap restore_real EXIT

echo "==> Building"
go build -o "$BIN/v370-usbsim" ./cmd/v370-usbsim

echo "==> Loading vhci-hcd"
as_root modprobe vhci-hcd

LOG="$(mktemp)"
PIDFILE="$BIN/.usbsim.pid"
echo "==> Starting simulator (log: $LOG)"
as_root_bg "$PIDFILE" "$BIN/v370-usbsim" serve -trace "$TRACE" -listen "$LISTEN" -attach \
    -status-addr "$STATUS_ADDR" >"$LOG" 2>&1
cleanup() {
    as_root_kill TERM "$PIDFILE"
    wait 2>/dev/null || true
    rm -f "$PIDFILE"
    restore_real
    echo "==> Simulator log tail:"
    tail -n 20 "$LOG"
}
trap cleanup EXIT

for _ in $(seq 1 50); do
    if curl -fsS "http://$STATUS_ADDR/status" 2>/dev/null | grep -q '"attached_port"'; then
        break
    fi
    sleep 0.2
done
curl -fsS "http://$STATUS_ADDR/status" | grep -q '"attached_port"' || { echo "Error: simulator did not attach" >&2; exit 1; }
for _ in $(seq 1 25); do
    sim_scanner_present && break
    sleep 0.2
done
sim_scanner_present || { echo "Error: simulated scanner did not appear in sysfs" >&2; exit 1; }
echo "==> Simulated scanner attached:"
lsusb -d 04b8:014a || true

echo "==> Running Tier-2 tests"
export USBSIM_TRACE="$TRACE" USBSIM_STATUS_URL="http://$STATUS_ADDR/status"
(cd "$PKG" && go test -tags integration,hwsim -count=1 -v -timeout 10m -run "$RUN" .)
