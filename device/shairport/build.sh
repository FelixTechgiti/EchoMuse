#!/bin/bash
# Build shairport-sync (classic AirPlay) for the Echo Dot.
#
# Usage: ./build.sh [git-ref]
set -euo pipefail

REF="${1:-4.3.7}"
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/out"
IMAGE=echomuse-shairport

echo "Building shairport-sync $REF for armv7a/Android API 22..."
docker build -t "$IMAGE" "$HERE"

mkdir -p "$OUT"
docker run --rm -v "$OUT:/out" "$IMAGE" bash -c '
    set -euo pipefail
    cd /build

    # mbedtls rather than OpenSSL. shairport-sync supports both, and OpenSSL
    # is the harder cross-compile by a wide margin — its Configure has no
    # Android/armv7 target that matches this NDK without patching, while
    # mbedtls is plain CMake with a toolchain file.
    git clone --depth 1 --branch v2.28.8 https://github.com/Mbed-TLS/mbedtls
    cmake -S mbedtls -B mbedtls/build \
        -DCMAKE_TOOLCHAIN_FILE="$NDK_ROOT/build/cmake/android.toolchain.cmake" \
        -DANDROID_ABI=armeabi-v7a -DANDROID_PLATFORM=android-22 \
        -DENABLE_TESTING=OFF -DENABLE_PROGRAMS=OFF \
        -DCMAKE_INSTALL_PREFIX=/build/prefix
    cmake --build mbedtls/build --target install -j"$(nproc)"

    git clone --depth 1 --branch "'"$REF"'" https://github.com/mikebrady/shairport-sync
    cd shairport-sync
    autoreconf -fi

    # --with-stdout is the whole point: PCM on stdout, no ALSA in
    # shairport-sync at all. The device already owns the speaker, and two
    # things opening it is the #80 failure — a blocking open with no timeout
    # and eighteen minutes of a stranded device.
    #
    # --with-tinysvcmdns uses the bundled responder instead of Avahi, which
    # Android does not have and cannot get without D-Bus.
    ./configure --host=armv7a-linux-androideabi \
        --with-stdout --with-tinysvcmdns --with-mbedtls \
        --without-alsa --without-pa --without-pipewire --without-soxr \
        CFLAGS="-I/build/prefix/include -O2" \
        LDFLAGS="-L/build/prefix/lib -static-libgcc"
    make -j"$(nproc)"

    "$NDK/bin/llvm-strip" shairport-sync
    cp shairport-sync /out/shairport-sync
    md5sum /out/shairport-sync > /out/shairport-sync.md5
'

echo
echo "Built: $OUT/shairport-sync"
ls -lh "$OUT/shairport-sync"
echo
echo "Install it by hand for now:"
echo "  adb push $OUT/shairport-sync /data/local/bin/shairport-sync"
echo "  adb shell chmod 755 /data/local/bin/shairport-sync"
