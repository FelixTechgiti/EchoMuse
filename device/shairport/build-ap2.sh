#!/bin/bash
# Build shairport-sync WITH AIRPLAY 2, plus nqptp, for the Echo Dot.
#
# Separate from build.sh rather than a mode inside it. The two recipes share
# four library builds and differ in everything else — a different SSL
# arrangement, six more dependencies, a replaced mDNS backend and a second
# binary — and build.sh is the path currently being proven on hardware (#16).
# A mode flag would put a branch into a script nobody can run in CI, on the one
# file whose failure mode is "the device refuses to exec it".
#
# Needs a machine with a Docker daemon. Claude Code cloud sessions have none.
#
# Usage: ./build-ap2.sh [shairport-ref] [nqptp-ref]
set -euo pipefail

SPS_REF="${1:-4.3.7}"
NQPTP_REF="${2:-1.2.8}"

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/out"
IMAGE=revoice-shairport-ap2

echo "Building shairport-sync $SPS_REF (AirPlay 2) and nqptp $NQPTP_REF"
echo "for armv7a/Android API 22..."
docker build -f "$HERE/Dockerfile.ap2" -t "$IMAGE" "$HERE"

mkdir -p "$OUT"
docker run --rm \
    -v "$OUT:/out" \
    -v "$HERE/compat:/compat:ro" \
    -v "$HERE/ap2:/ap2:ro" \
    -e SPS_REF="$SPS_REF" \
    -e NQPTP_REF="$NQPTP_REF" \
    "$IMAGE" bash /ap2/in-container.sh

cat <<EOF

Built:
  $OUT/shairport-sync-ap2
  $OUT/nqptp

AirPlay 2 needs BOTH. nqptp is a second process: it must be running, it needs
UDP 319 and 320 to itself, and it needs to be able to bind them — which on a
rooted Echo it can. shairport-sync reads the clock nqptp publishes through a
shared-memory object; on bionic that is a file under /dev (a tmpfs), not POSIX
shared memory, which is what compat/android_shm.c exists for.

Install by hand while the device supervision for this is being written:

  adb push $OUT/shairport-sync-ap2 /data/local/bin/shairport-sync
  adb push $OUT/nqptp              /data/local/bin/nqptp
  adb shell chmod 755 /data/local/bin/shairport-sync /data/local/bin/nqptp

And note FireOS drops every inbound port it was not told about, so a device
that advertises AirPlay 2 perfectly can still be unreachable. See
internal/netfilter and the "Advertised is not reachable" section in
device/CLAUDE.md — AirPlay 2 needs TCP 7000 and the UDP range, and nqptp needs
319/320 inbound, none of which the classic rules cover.
EOF
