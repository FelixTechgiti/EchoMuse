#!/bin/bash
# Build librespot for the Echo Dot and drop it in ./out.
#
# Usage: ./build.sh [git-ref]
set -euo pipefail

REF="${1:-v0.7.1}"
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/out"
IMAGE=echomuse-librespot

# --no-default-features drops the rodio backend and with it cpal, alsa-sys and
# every other system library. What is LEFT is what this device needs:
#
#   * the pipe backend, which is compiled in unconditionally — PCM to stdout,
#     no ALSA in librespot at all. The device already owns the speaker, and
#     two things opening it is the #80 failure: a blocking open with no
#     timeout and eighteen minutes of a stranded device.
#   * rustls-tls-webpki-roots rather than native-tls, so there is no OpenSSL
#     to cross-compile and no system certificate store to find on a FireOS 5
#     image that does not have one where anything expects it.
#   * with-libmdns, which is how the speaker APPEARS IN THE APP. Pure Rust,
#     needing nothing from the system — unlike with-avahi (a D-Bus daemon
#     Android does not have) and with-dns-sd (Apple's Bonjour).
#
# ⚠ with-libmdns is one of librespot's OWN defaults, and leaving it off this
# list is how a binary reaches a device unable to advertise itself. That
# shipped: measured 2026-09-10, librespot running healthily on a Dot with
# spotifyEnabled on, and the Echo simply never in the Spotify app — nothing
# logged anywhere, because from librespot's side nothing is wrong. Toggling
# the setting could not fix it either; there is no announcement to resend.
# The comment above reasoned only about the AUDIO backend and never mentioned
# discovery, which is the whole of how it was missed.
FEATURES="rustls-tls-webpki-roots,with-libmdns"

# Every default feature this build deliberately drops, with its reason above.
# Checked against upstream's real Cargo.toml below.
DROPPED="native-tls rodio-backend"

# Which of upstream's defaults does this build drop, and did we mean to?
#
# --no-default-features is a blunt instrument: it removes EVERYTHING in
# `default`, including features that have nothing to do with the reason it was
# reached for. Reading the list off the pinned tag and diffing it against what
# we enable and what we deliberately drop turns that silence into a refusal.
#
# Before the build rather than inside it: this costs one HTTP request and
# fails in a second, where the same check after cargo would cost the whole
# cross-compile to tell you the same thing.
echo "Checking features against librespot $REF..."
CARGO_TOML="$(curl -sSf "https://raw.githubusercontent.com/librespot-org/librespot/$REF/Cargo.toml")"
DEFAULTS="$(printf '%s\n' "$CARGO_TOML" \
    | sed -n 's/^default = \[\(.*\)\]/\1/p' | tr -d '"' | tr ',' ' ')"
if [ -z "$DEFAULTS" ]; then
    echo "error: could not read 'default = [...]' from librespot $REF." >&2
    echo "       The guard cannot run, so it must not pass." >&2
    exit 1
fi
for f in $DEFAULTS; do
    case " ${FEATURES//,/ } $DROPPED " in
        *" $f "*) ;;
        *)
            echo "error: librespot $REF has a default feature this build neither" >&2
            echo "       enables nor deliberately drops: $f" >&2
            echo >&2
            echo "Add it to FEATURES, or to DROPPED with the reason. Dropping one" >&2
            echo "by accident is how a librespot that could not advertise itself" >&2
            echo "reached a device (2026-09-10) — it ran perfectly and was simply" >&2
            echo "never in the Spotify app." >&2
            exit 1 ;;
    esac
done
echo "features: $FEATURES   (dropping: $DROPPED)"

echo "Building librespot $REF for armv7a/Android API 22..."
docker build -t "$IMAGE" "$HERE"

mkdir -p "$OUT"
docker run --rm -v "$OUT:/out" "$IMAGE" bash -c "
    set -euo pipefail
    git clone --depth 1 --branch '$REF' https://github.com/librespot-org/librespot /build/librespot
    cd /build/librespot
    cargo build --release --target armv7-linux-androideabi \
        --no-default-features --features '$FEATURES'
    # Stripped: the eMMC is 8GB shared with Android and the symbols are of no
    # use on a device with no debugger on it.
    \"\$NDK/bin/llvm-strip\" target/armv7-linux-androideabi/release/librespot
    cp target/armv7-linux-androideabi/release/librespot /out/librespot
    md5sum /out/librespot > /out/librespot.md5
"

echo
echo "Built: $OUT/librespot"
ls -lh "$OUT/librespot"
cat "$OUT/librespot.md5"
echo
echo "Install it from the dashboard: a device's Updates tab -> Streaming"
echo "endpoints -> Upload binary, then Install on this Echo. It is md5-verified"
echo "on the way, and the toggle on the Config tab goes live without a restart."
echo
echo "By hand, if the device is on USB and not yet talking to the controller:"
echo "  adb push $OUT/librespot /data/local/bin/librespot"
echo "  adb shell chmod 755 /data/local/bin/librespot"
