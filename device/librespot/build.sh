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
# with-libmdns IS NOT OPTIONAL, and leaving it out is how the first build of
# this shipped a librespot that never appeared in the Spotify app.
#
# librespot 0.7.1 defaults to ["native-tls", "rodio-backend", "with-libmdns"].
# --no-default-features is right for the first two — that is the whole point,
# dropping cpal and alsa-sys — but it takes the THIRD as well, and the third
# is how a Spotify Connect device announces itself. Without it librespot
# builds, starts, logs nothing wrong and is invisible: the failure this
# codebase names most often, reached by removing a feature nobody was
# thinking about.
#
# The comment below used to list "what survives" and did not mention
# discovery at all, which is why it went unnoticed. Measured on hardware
# 2026-09-06: binary installed, spotifyEnabled true, no device in the app.
#
# It is the pure-Rust mDNS backend, so it adds no system dependency and
# nothing to cross-compile — which is also why it beats with-avahi (a D-Bus
# daemon Android does not have) and with-dns-sd (Bonjour, or Avahi
# compatibility, same problem).
FEATURES="rustls-tls-webpki-roots,with-libmdns"

echo "Building librespot $REF for armv7a/Android API 22..."
docker build -t "$IMAGE" "$HERE"

mkdir -p "$OUT"
docker run --rm -v "$OUT:/out" -v "$HERE/../compat:/compat:ro" "$IMAGE" bash -c "
    set -euo pipefail

    # getifaddrs, which bionic declares only from API 24 — the SAME gap
    # shairport-sync has, which is why the shim lives in device/compat/ and
    # not under either recipe.
    #
    # It arrives here through with-libmdns: discovery pulls in the \`if-addrs\`
    # crate, which emits a plain undefined reference —
    #
    #   libif_addrs...rlib: undefined reference to 'getifaddrs'
    #
    # — so this is a LINK failure at the very end of a five-minute compile,
    # not something configure or cargo check would have caught.
    #
    # Rust cannot be macro-renamed the way the C sources are, so the archive
    # exports the real symbol names too (see android_ifaddrs.c). RUSTFLAGS is
    # how it reaches the link line: there is no build.rs of ours to put a
    # cargo:rustc-link-lib in, and patching librespot to add one would be a
    # patch to carry across every version bump.
    \"\$NDK/bin/armv7a-linux-androideabi22-clang\" -c -O2 -fPIC \
        /compat/android_ifaddrs.c -o /build/android_ifaddrs.o
    \"\$NDK/bin/llvm-ar\" rcs /build/libemifaddrs.a /build/android_ifaddrs.o

    git clone --depth 1 --branch '$REF' https://github.com/librespot-org/librespot /build/librespot
    cd /build/librespot
    RUSTFLAGS='-L native=/build -l static=emifaddrs' \
    cargo build --release --target armv7-linux-androideabi \
        --no-default-features --features '$FEATURES'

    # Proof rather than assumption. A librespot without discovery builds,
    # runs, and is simply never listed in the Spotify app — which is exactly
    # what shipped the first time. The mDNS responder leaves its service type
    # in the binary, so its absence is checkable here instead of on a phone.
    if ! grep -q '_spotify-connect._tcp' target/armv7-linux-androideabi/release/librespot; then
        echo 'ERROR: no Spotify Connect discovery in the binary — with-libmdns missing' >&2
        exit 1
    fi
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
