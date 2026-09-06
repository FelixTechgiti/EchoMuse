#!/bin/bash
# Build shairport-sync (classic AirPlay) for the Echo Dot.
#
# ⛔ THIS DOES NOT CURRENTLY PRODUCE A BINARY, and the reason is in the
# platform rather than in this script. configure completes — every library
# below is found — and the compile then fails on pthread cancellation, which
# bionic does not implement at any API level (pthread_cancel appears zero
# times in its pthread.h). shairport-sync has 164 cancellation call sites in
# 4.3.7 and 113 in 3.3.9, so no version pin escapes it; rewriting those
# teardown paths is a port of the threading model. See README.md before
# spending time here.
#
# The script is kept, and run, because it now reaches that obstacle instead
# of stopping five corrections earlier at the first configure check.
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

    # EMPTY librt.a and libpthread.a, and they are not hacks to be tidied
    # away later.
    #
    # configure.ac requires both unconditionally — line 43
    # AC_CHECK_LIB([rt],[clock_gettime]) and line 65
    # AC_CHECK_LIB([pthread],[pthread_create], each with an AC_MSG_ERROR
    # behind it — under with_os=linux, which is what Android is. bionic has
    # NEITHER library: clock_gettime and the whole pthread API live in libc,
    # where glibc moved them in 2.17 and 2.34 respectively. Every distro
    # keeps stub archives around so exactly these checks keep passing;
    # Android does not, so both checks fail on functions the platform HAS.
    #
    # An empty archive makes -lrt / -lpthread resolvable and lets libc answer
    # the symbols, which is the same thing a glibc stub does. Patching
    # configure.ac would mean carrying a patch against every future 4.3.x;
    # this survives a version bump untouched.
    #
    # They have to persist into the real link, not just the probe:
    # AC_CHECK_LIB prepends the -l to LIBS on success, so the archives stay
    # in /build/prefix/lib, which is on LDFLAGS.
    #
    # Two and only two. -lm resolves against a real bionic libm.so, and every
    # other AC_CHECK_LIB in that file is either behind a --with flag we turn
    # off or is a library built for real below. An unexplained archive is
    # worse than a missing one.
    mkdir -p /build/prefix/lib
    : > /build/stub.c
    "$CC" -c /build/stub.c -o /build/stub.o
    llvm-ar rcs /build/prefix/lib/librt.a /build/stub.o
    llvm-ar rcs /build/prefix/lib/libpthread.a /build/stub.o

    # libpopt and libconfig are HARD requirements of classic shairport-sync
    # — configure aborts without either — and this recipe built neither
    # until it was first run. Both are cross-compiled here rather than
    # apt-installed, for the reason the whole image exists: a host library
    # is a second opinion about what FireOS 5 provides.
    #
    # Static, so the device carries one file rather than a binary plus two
    # shared objects it would have to find at exec time.

    # popt from the release tarball rather than a git tag: the tarball ships
    # a generated `configure`, while the git tree needs autopoint (gettext),
    # which this image does not carry and which exists only to build the
    # translations --disable-nls then throws away. Pinned by sha256, the same
    # treatment the controller image gives its vendored assets.
    #
    # ac_cv_header_glob_h=no is the load-bearing part. The NDK ships glob.h
    # but declares glob() only for __ANDROID_API__ >= 28, so the header check
    # passes and the LINK fails — undefined reference to glob/globfree, in
    # popt'"'"'s config-file reader. Everything that uses it is behind
    # HAVE_GLOB_H, so denying the header compiles the non-glob path and the
    # library is correct rather than merely linkable. Nothing here reads
    # /etc/popt.d.
    curl -sSLO http://ftp.rpm.org/popt/releases/popt-1.x/popt-1.19.tar.gz
    echo "c25a4838fc8e4c1c8aacb8bd620edb3084a3d63bf8987fdad3ca2758c63240f9  popt-1.19.tar.gz" \
        | sha256sum -c -
    tar xzf popt-1.19.tar.gz
    cd popt-1.19
    ac_cv_header_glob_h=no ./configure --host=armv7a-linux-androideabi \
        --prefix=/build/prefix --disable-shared --enable-static --disable-nls
    make -j"$(nproc)"
    make install
    cd /build

    # libconfig has no release tarballs on GitHub, so it comes from the tag
    # and is bootstrapped here.
    #
    # SUBDIRS=lib builds and installs the library alone. The default target
    # descends into doc/, which runs makeinfo — texinfo is not in this image
    # and adding a documentation toolchain to a cross-compiler to produce a
    # manual nobody on the device can read is the wrong trade.
    git clone --depth 1 --branch v1.8.2 https://github.com/hyperrealm/libconfig
    cd libconfig
    autoreconf -fi
    ./configure --host=armv7a-linux-androideabi --prefix=/build/prefix \
        --disable-shared --enable-static --disable-cxx --disable-examples
    make SUBDIRS=lib -j"$(nproc)"
    make SUBDIRS=lib install
    cd /build

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
    #
    # --without-pkg-config sends popt and libconfig through AC_CHECK_LIB
    # instead of PKG_CHECK_MODULES. It is the safer half of a cross build:
    # pkg-config answers from the HOST by default, and a .pc file found there
    # hands back host include and library paths that link an x86 object into
    # an ARM binary — a failure that arrives at the linker with no mention of
    # architecture. PKG_CONFIG_LIBDIR below confines it as well, so neither
    # alone is trusted.
    # The option names are read off the AC_ARG_WITH list in configure.ac,
    # not guessed, because autoconf ACCEPTS AN UNKNOWN --with SILENTLY. Two
    # were wrong here and only one of them announced itself:
    #
    #   --with-mbedtls        the option is `ssl`, taking a value. This one
    #                         failed loudly — "specify one of --with-ssl=..."
    #                         — because nothing had selected a backend.
    #   --without-pipewire    the option is `pw`. This one did NOTHING, and
    #                         would have gone on doing nothing forever: the
    #                         backend is off by default, so the flag read as
    #                         a deliberate exclusion while excluding nothing.
    #                         The day that default changes is the day it
    #                         matters, and by then nobody is looking here.
    # LIBS seeds the dependencies of a STATIC mbedtls, and without it the
    # mbedtls check fails on a library that is present and correct.
    # AC_CHECK_LIB([mbedtls],[mbedtls_ssl_init]) links `-lmbedtls $LIBS` and
    # nothing else — which is enough for the shared library every distro
    # ships, and not enough for an archive: libmbedtls.a calls into
    # libmbedx509 and libmbedcrypto, so the probe ends in undefined symbols
    # and configure reports the library as missing. Naming them in LIBS puts
    # them AFTER -lmbedtls on every link line, which is the order a static
    # link needs.
    PKG_CONFIG_LIBDIR=/build/prefix/lib/pkgconfig \
    ./configure --host=armv7a-linux-androideabi \
        --with-stdout --with-tinysvcmdns --with-ssl=mbedtls --without-pkg-config \
        --without-alsa --without-pa --without-pw --without-soxr \
        CFLAGS="-I/build/prefix/include -O2" \
        LDFLAGS="-L/build/prefix/lib -static-libgcc" \
        LIBS="-lmbedx509 -lmbedcrypto"
    make -j"$(nproc)"

    "$NDK/bin/llvm-strip" shairport-sync
    cp shairport-sync /out/shairport-sync
    md5sum /out/shairport-sync > /out/shairport-sync.md5
'

echo
echo "Built: $OUT/shairport-sync"
ls -lh "$OUT/shairport-sync"
echo
echo "Install it from the dashboard: a device's Updates tab -> Streaming"
echo "endpoints -> Upload binary, then Install on this Echo. It is md5-verified"
echo "on the way, and the toggle on the Config tab goes live without a restart."
echo
echo "By hand, if the device is on USB and not yet talking to the controller:"
echo "  adb push $OUT/shairport-sync /data/local/bin/shairport-sync"
echo "  adb shell chmod 755 /data/local/bin/shairport-sync"
