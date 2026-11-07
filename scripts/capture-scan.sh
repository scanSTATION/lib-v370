#!/bin/bash
#
# capture-scan.sh — ONE-TIME recording of a real scan for the USB simulator.
#
# Run this on the machine where the V370 is connected. It records all
# USB traffic of one scan COMMAND with usbmon, converts it into a replay
# trace (testdata/traces/<name>.jsonl.gz) and prints a summary. Afterwards
# the scanner is no longer needed: `v370-usbsim serve -trace ...` replays it.
#
# Usage:
#   scripts/capture-scan.sh [-n NAME] [--no-replug] -- COMMAND [ARGS...]
#
# COMMAND performs one scan and writes its result into the directory given
# by the literal argument @OUT@ (replaced by a temporary directory); the
# first file found there is stored as the reference output.
#
# Examples:
#   # The vendor app's traffic, via the application's CLI:
#   scripts/capture-scan.sh -n a4-300dpi-jpeg -- \
#       ../bin/scanstation --driver epsonscan2 --dpi 300 --size a4 --format jpeg -o @OUT@ --prefix capture
#   # Our own driver's traffic:
#   scripts/capture-scan.sh -n direct-a4-100dpi --no-replug -- \
#       go run ./cmd/v370 scan -profile a4-100dpi-color -o @OUT@/capture.png
#
# What it needs: lsusb (usbutils), dumpcap (wireshark-cli) or tcpdump, go,
# root for the usbmon capture (sudo, or V370_ROOT=docker — see
# scripts/lib/asroot.sh), and whatever COMMAND needs.
#
# PRIVACY: the trace contains the scanned image. Scan a blank or
# non-sensitive page.
#
# Why replug: the V370 needs its firmware uploaded after every power-up, and
# The vendor app does that on first use. Power-cycling the scanner during the
# capture records the firmware upload and the USB enumeration (descriptors),
# so the simulator behaves like a freshly plugged-in scanner.
#
set -euo pipefail

VID=04b8
PID=014a
NAME="a4-300dpi-jpeg"
REPLUG=1
CMD=()

while [ $# -gt 0 ]; do
    case "$1" in
        -n) NAME="$2"; shift 2 ;;
        --no-replug) REPLUG=0; shift ;;
        --) shift; CMD=("$@"); break ;;
        -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

if [ ${#CMD[@]} -eq 0 ]; then
    echo "Error: give the scan command after --, e.g. -- ../bin/scanstation --driver epsonscan2 -o @OUT@ --prefix capture" >&2
    exit 2
fi
if ! [[ "$NAME" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "Error: name may only contain letters, digits, . _ -" >&2
    exit 2
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=lib/asroot.sh
. "$ROOT/scripts/lib/asroot.sh"
CAPDIR="$ROOT/testdata/captures"   # raw pcaps (gitignored, large)
TRACEDIR="$ROOT/testdata/traces"
BIN="$ROOT/bin"
mkdir -p "$CAPDIR" "$TRACEDIR" "$BIN"

need() { command -v "$1" >/dev/null 2>&1 || { echo "Error: '$1' not found ($2)" >&2; exit 1; }; }
need lsusb "install usbutils"
need go "install Go 1.22+"
if command -v dumpcap >/dev/null 2>&1; then
    CAPTOOL=dumpcap
elif command -v tcpdump >/dev/null 2>&1; then
    CAPTOOL=tcpdump
else
    echo "Error: neither dumpcap (wireshark-cli) nor tcpdump found" >&2
    exit 1
fi

find_bus() {
    lsusb -d "${VID}:${PID}" 2>/dev/null | awk '{print $2}' | sed 's/^0*//' | head -n1
}

BUS="$(find_bus)"
if [ -z "$BUS" ]; then
    echo "Error: scanner ${VID}:${PID} not found — connect and power on the V370" >&2
    exit 1
fi
echo "==> Scanner found on USB bus $BUS"

echo "==> Building tools"
go build -o "$BIN/v370-usbsim" ./cmd/v370-usbsim

echo "==> Loading usbmon"
as_root modprobe usbmon

PCAP="$CAPDIR/$NAME.pcap"
OUTDIR="$(mktemp -d)"
PIDFILE="$CAPDIR/.$NAME.capture.pid"
trap 'rm -rf "$OUTDIR" "$PIDFILE"' EXIT

echo "==> Starting capture on usbmon$BUS ($CAPTOOL) → $PCAP"
if [ "$CAPTOOL" = dumpcap ]; then
    as_root_bg_out "$PIDFILE" "$PCAP" dumpcap -q -i "usbmon$BUS" -F pcap -s 0 -w -
else
    as_root_bg_out "$PIDFILE" "$PCAP" tcpdump -q -i "usbmon$BUS" -s 0 -U -w -
fi
sleep 2

stop_capture() {
    as_root_kill INT "$PIDFILE"
    wait 2>/dev/null || true
}

if [ "$REPLUG" = 1 ]; then
    echo
    echo ">>> Now POWER-CYCLE the scanner: unplug its USB cable, wait 3 s, plug it back in."
    read -r -p ">>> Press Enter once it is plugged in again... "
    for _ in $(seq 1 30); do
        [ -n "$(find_bus)" ] && break
        sleep 1
    done
    NEWBUS="$(find_bus)"
    if [ "$NEWBUS" != "$BUS" ]; then
        stop_capture
        echo "Error: scanner came back on bus '$NEWBUS' (was $BUS) — use the same USB port and retry" >&2
        exit 1
    fi
    sleep 2
fi

RUNCMD=()
for a in "${CMD[@]}"; do RUNCMD+=("${a//@OUT@/$OUTDIR}"); done
echo "==> Scanning: ${RUNCMD[*]}"
set +e
"${RUNCMD[@]}"
RC=$?
set -e
sleep 2
stop_capture
as_root chown "$(id -u):$(id -g)" "$PCAP"

if [ $RC -ne 0 ]; then
    echo "Error: scan failed (exit $RC); the capture is kept in $PCAP for analysis" >&2
    exit 1
fi

OUTFILE="$(find "$OUTDIR" -maxdepth 1 -type f ! -name '*.json' ! -name '*.raw' | sort | head -n1)"
SHA=""
if [ -n "$OUTFILE" ]; then
    SHA="$(sha256sum "$OUTFILE" | awk '{print $1}')"
    EXT="${OUTFILE##*.}"
    cp "$OUTFILE" "$TRACEDIR/$NAME.expected.$EXT"
    echo "==> Reference output: $TRACEDIR/$NAME.expected.$EXT (sha256 $SHA)"
else
    echo "Warning: scan produced no output file" >&2
fi

echo "==> Converting capture to trace"
DEVARGS=()
if [ "$REPLUG" = 0 ]; then
    # Without a replug no device descriptor is captured, so address the
    # scanner explicitly.
    DEV="$(lsusb -d "${VID}:${PID}" | awk '{print $4}' | tr -d ':' | sed 's/^0*//' | head -n1)"
    DEVARGS=(-bus "$BUS" -dev "$DEV")
fi
"$BIN/v370-usbsim" convert -in "$PCAP" -out "$TRACEDIR/$NAME.jsonl.gz" "${DEVARGS[@]}" \
    -scenario "${CMD[*]:1}" -output-sha256 "$SHA" \
    -note "captured with scripts/capture-scan.sh on $(hostname) (replug=$REPLUG), command: ${CMD[0]}"

"$BIN/v370-usbsim" inspect "$TRACEDIR/$NAME.jsonl.gz" | head -n 60

cat <<EOF

==> Done.
    Trace:     testdata/traces/$NAME.jsonl.gz
    Raw pcap:  testdata/captures/$NAME.pcap (not committed; keep it for analysis)

    Replay it without the scanner (Tier-2 test):
      scripts/hwsim-test.sh testdata/traces/$NAME.jsonl.gz
EOF
