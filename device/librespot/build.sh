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
FEATURES="rustls-tls-webpki-roots"

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
echo "Install it with the dashboard (Updates -> Spotify), or by hand:"
echo "  adb push $OUT/librespot /data/local/bin/librespot"
echo "  adb shell chmod 755 /data/local/bin/librespot"
