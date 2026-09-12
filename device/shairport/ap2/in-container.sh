#!/bin/bash
# The AirPlay 2 build, as it runs INSIDE the container. See ../build-ap2.sh.
#
# A file rather than a `bash -c` string, unlike the classic recipe. That one
# carries a warning in three places that an apostrophe in a comment ends the
# single-quoted string and breaks the build — a trap that has nothing to teach
# and costs a cycle every time somebody forgets. This script is mounted and
# run, so it can be read, linted and quoted normally.
#
# Everything it fetches is pinned. Where the classic recipe pins a tarball by
# sha256 it is repeated verbatim here; where it cannot, the pin is a git tag,
# which is what the classic recipe already does for mbedtls and libconfig.
#
# NOTHING HERE HAS EVER BEEN RUN. The classic recipe needed seven corrections
# the first time it was executed, and there is no reason to expect better of a
# larger one. What it encodes is the decisions and the traps that were read off
# the sources rather than guessed — see ../README.md.

set -euo pipefail

SPS_REF="${SPS_REF:-4.3.7}"
NQPTP_REF="${NQPTP_REF:-1.2.8}"

PREFIX=/build/prefix
JOBS="$(nproc)"

mkdir -p "$PREFIX/lib" "$PREFIX/include"
cd /build

say() { echo; echo "=== $* ==="; }

# ---------------------------------------------------------------------------
say "librt.a and libpthread.a — empty, and not a hack to be tidied away"
# configure.ac requires both unconditionally (AC_CHECK_LIB([rt],[clock_gettime])
# and AC_CHECK_LIB([pthread],[pthread_create], each with an AC_MSG_ERROR behind
# it) under with_os=linux, which is what Android is. bionic has NEITHER library:
# both APIs live in libc, where glibc moved them in 2.17 and 2.34. Every distro
# keeps stub archives so exactly these checks keep passing; Android does not, so
# both checks fail on functions the platform HAS.
: > /build/stub.c
"$CC" -c /build/stub.c -o /build/stub.o
llvm-ar rcs "$PREFIX/lib/librt.a" /build/stub.o
llvm-ar rcs "$PREFIX/lib/libpthread.a" /build/stub.o

# ---------------------------------------------------------------------------
say "popt"
# From the release tarball, not the git tag: the tarball ships a generated
# `configure`, while the git tree needs autopoint to build translations that
# --disable-nls then throws away. sha256 is the classic recipe's, unchanged.
#
# ac_cv_header_glob_h=no is the load-bearing part. The NDK ships glob.h but
# declares glob() only for __ANDROID_API__ >= 28, so the header check passes
# and the LINK fails. Everything using it is behind HAVE_GLOB_H.
curl -sSLO http://ftp.rpm.org/popt/releases/popt-1.x/popt-1.19.tar.gz
echo "c25a4838fc8e4c1c8aacb8bd620edb3084a3d63bf8987fdad3ca2758c63240f9  popt-1.19.tar.gz" \
    | sha256sum -c -
tar xzf popt-1.19.tar.gz
(
    cd popt-1.19
    ac_cv_header_glob_h=no ./configure --host=armv7a-linux-androideabi \
        --prefix="$PREFIX" --disable-shared --enable-static --disable-nls
    make -j"$JOBS"
    make install
)

# ---------------------------------------------------------------------------
say "libconfig"
# SUBDIRS=lib builds the library alone; the default target descends into doc/
# and runs makeinfo on a manual nobody on the device can read.
git clone --depth 1 --branch v1.8.2 https://github.com/hyperrealm/libconfig
(
    cd libconfig
    autoreconf -fi
    ./configure --host=armv7a-linux-androideabi --prefix="$PREFIX" \
        --disable-shared --enable-static --disable-cxx --disable-examples
    make SUBDIRS=lib -j"$JOBS"
    make SUBDIRS=lib install
)

# ---------------------------------------------------------------------------
say "mbedtls"
# Still mbedtls for shairport-sync's own crypto, exactly as the classic build.
# It is NOT displaced by libgcrypt below: those are two different consumers.
git clone --depth 1 --branch v2.28.8 https://github.com/Mbed-TLS/mbedtls
cmake -S mbedtls -B mbedtls/build \
    -DCMAKE_TOOLCHAIN_FILE="$NDK_ROOT/build/cmake/android.toolchain.cmake" \
    -DANDROID_ABI=armeabi-v7a -DANDROID_PLATFORM=android-22 \
    -DENABLE_TESTING=OFF -DENABLE_PROGRAMS=OFF \
    -DCMAKE_INSTALL_PREFIX="$PREFIX"
cmake --build mbedtls/build --target install -j"$JOBS"

# ---------------------------------------------------------------------------
say "libgpg-error"
# libgcrypt needs it, and libgcrypt is not optional — see the shairport step.
#
# --host=arm-unknown-linux-androideabi, and NOT the armv7a spelling used
# everywhere else in this file. That is not carelessness and it is the single
# trap in this library:
#
# For a host matching *-gnu*/*-musl*, configure.ac generates the lock object
# header by running objdump over a probe. Android matches neither, so it falls
# to `force_use_syscfg=yes` and src/mkheader.c:621 includes
# `syscfg/lock-obj-pub.$host.h` — a file that must ALREADY EXIST for the exact
# canonicalised triplet. The tree ships lock-obj-pub.arm-unknown-linux-
# androideabi.h; it does not ship an armv7a one, and `armv7a-linux-androideabi`
# canonicalises to armv7a-unknown-linux-androideabi, which would not match.
# CC is unchanged, so the objects are still armv7a.
git clone --depth 1 --branch libgpg-error-1.61 https://github.com/gpg/libgpg-error
(
    cd libgpg-error
    ./autogen.sh
    ./configure --host=arm-unknown-linux-androideabi --prefix="$PREFIX" \
        --disable-shared --enable-static --disable-nls --disable-doc \
        --disable-tests --enable-install-gpg-error-config
    make -j"$JOBS"
    make install
)

# ---------------------------------------------------------------------------
say "libgcrypt"
# NOT a choice, and not a replacement for mbedtls. Makefile.am:5 in
# shairport-sync builds lib_pair_ap.a with a hardcoded -DCONFIG_GCRYPT:
#
#   lib_pair_ap_a_CFLAGS = -Wall -g -DCONFIG_GCRYPT -pthread
#
# pair_ap is the AirPlay 2 pairing and encryption, and pair-internal.h branches
# only on CONFIG_GCRYPT / CONFIG_OPENSSL — there is no mbedTLS path in it at
# all. So an AirPlay 2 build needs libgcrypt IN ADDITION to whatever
# --with-ssl selects.
#
# --disable-asm because the NDK assembler and libgcrypt's ARM assembly have
# historically disagreed, and this is a build nobody can iterate on quickly.
# Turn it back on once the binary runs; it is a performance question and this
# device decodes one AAC stream.
git clone --depth 1 --branch libgcrypt-1.12.4 https://github.com/gpg/libgcrypt
(
    cd libgcrypt
    ./autogen.sh
    ./configure --host=armv7a-linux-androideabi --prefix="$PREFIX" \
        --with-libgpg-error-prefix="$PREFIX" \
        --disable-shared --enable-static --disable-doc --disable-asm \
        --disable-tests
    make -j"$JOBS"
    make install
)

# ---------------------------------------------------------------------------
say "libsodium"
git clone --depth 1 --branch 1.0.22-RELEASE https://github.com/jedisct1/libsodium
(
    cd libsodium
    ./autogen.sh -s
    ./configure --host=armv7a-linux-androideabi --prefix="$PREFIX" \
        --disable-shared --enable-static
    make -j"$JOBS"
    make install
)

# ---------------------------------------------------------------------------
say "libplist"
# shairport-sync accepts >= 2.0.0 and takes a different code path at >= 2.3.0
# (HAVE_LIBPLIST_GE_2_3_0); 2.7.0 is current.
#
# --without-cython: the bindings need Python headers for the TARGET, which do
# not exist here, and nothing in this build imports plist from Python.
git clone --depth 1 --branch 2.7.0 https://github.com/libimobiledevice/libplist
(
    cd libplist
    ./autogen.sh --host=armv7a-linux-androideabi --prefix="$PREFIX" \
        --disable-shared --enable-static --without-cython
    make -j"$JOBS"
    make install
)

# ---------------------------------------------------------------------------
say "ffmpeg (trimmed)"
# The AirPlay 2 dependency nobody can avoid: configure.ac requires libavutil,
# libavcodec, libavformat and libswresample for --with-airplay-2.
#
# What it is actually FOR is one codec. AIRPLAY2.md: Buffered Audio is "AAC
# stereo at 44,100 frames per second" and Realtime streams are ALAC. So this is
# --disable-everything plus the two decoders, which keeps a general-purpose
# media framework from becoming several megabytes on a device that shares
# 512MB with Android.
#
# n7.1.5 rather than the newest: 7.1 is the series every Android NDK recipe in
# the wild is written against, and this build cannot be iterated on cheaply.
git clone --depth 1 --branch n7.1.5 https://github.com/FFmpeg/FFmpeg ffmpeg
(
    cd ffmpeg
    ./configure \
        --prefix="$PREFIX" \
        --enable-cross-compile \
        --cross-prefix=llvm- \
        --cc="$CC" \
        --ar=llvm-ar --ranlib=llvm-ranlib --strip=llvm-strip --nm=llvm-nm \
        --target-os=android \
        --arch=arm \
        --cpu=armv7-a \
        --enable-static --disable-shared \
        --disable-everything \
        --enable-decoder=aac --enable-decoder=aac_latm --enable-decoder=alac \
        --enable-parser=aac --enable-parser=aac_latm \
        --enable-demuxer=aac --enable-demuxer=mov \
        --enable-protocol=file \
        --disable-programs --disable-doc --disable-htmlpages --disable-manpages \
        --disable-avdevice --disable-avfilter --disable-swscale --disable-postproc \
        --disable-network --disable-iconv --disable-symver \
        --disable-debug
    make -j"$JOBS"
    make install
)

# ---------------------------------------------------------------------------
say "the compat shims"
# Four now, where the classic build has two. android_shm.c is the nqptp<->
# shairport clock interface (bionic has no POSIX shared memory); android_uuid.c
# is the three libuuid functions shairport calls once, which util-linux would
# otherwise have to be cross-built for.
"$CC" -c -O2 -fPIC -I/compat /compat/android_compat.c  -o /build/android_compat.o
"$CC" -c -O2 -fPIC -I/compat /compat/android_ifaddrs.c -o /build/android_ifaddrs.o
"$CC" -c -O2 -fPIC -I/compat /compat/android_shm.c     -o /build/android_shm.o
"$CC" -c -O2 -fPIC -I/compat /compat/android_uuid.c    -o /build/android_uuid.o
"$CC" -c -O2 -fPIC -I/compat /compat/mdns_ap2.c        -o /build/mdns_ap2.o
llvm-ar rcs "$PREFIX/lib/libemcompat.a" \
    /build/android_compat.o /build/android_ifaddrs.o /build/android_shm.o \
    /build/android_uuid.o /build/mdns_ap2.o

# `-luuid` has to resolve, because configure probes for it by name. The symbols
# are in libemcompat.a; this archive is what makes the -l work, the same trick
# as librt and libpthread above.
llvm-ar rcs "$PREFIX/lib/libuuid.a" /build/android_uuid.o

# ---------------------------------------------------------------------------
say "nqptp $NQPTP_REF"
# The PTP clock daemon AirPlay 2 times against. A SECOND process on the device,
# supervised beside shairport-sync, needing UDP 319 and 320 exclusively and the
# privilege to bind them — both of which a rooted Echo has, and neither of
# which needs hardware timestamping: nqptp's own README says it "does not take
# advantage of hardware timestamping", which is the correction at the centre
# of #79.
#
# The shim is injected with -include, so no upstream source is patched.
git clone --depth 1 --branch "$NQPTP_REF" https://github.com/mikebrady/nqptp
(
    cd nqptp
    autoreconf -fi
    ./configure --host=armv7a-linux-androideabi --prefix="$PREFIX" \
        CFLAGS="-O2 -I$PREFIX/include -include /compat/android_shm.h" \
        LDFLAGS="-L$PREFIX/lib -static-libgcc" \
        LIBS="-lemcompat"
    make -j"$JOBS" CXXLD="$CC"
)

# ---------------------------------------------------------------------------
say "shairport-sync $SPS_REF, with AirPlay 2"
git clone --depth 1 --branch "$SPS_REF" https://github.com/mikebrady/shairport-sync
cd shairport-sync

# OUR mDNS backend replaces upstream's, and it is a whole file rather than a
# patch: --with-tinysvcmdns already compiles this filename, so configure.ac,
# Makefile.am and mdns.c are untouched and the 4.3.7 pin stays movable.
#
# Without it this build would configure, compile, link, run — and never once be
# offered to a phone as an AirPlay 2 device, because upstream's version takes
# ap2name and secondary_txt_records and declares both
# __attribute__((unused)). See compat/mdns_tinysvcmdns.c.
cp /compat/mdns_tinysvcmdns.c mdns_tinysvcmdns.c

autoreconf -fi

BASE_CFLAGS="-I$PREFIX/include -I/compat -O2"

# --with-airplay-2 is the flag; everything else is the same shape as the
# classic build. --without-pkg-config keeps the HOST's .pc files out of a cross
# build, where they hand back host include and library paths that link an x86
# object into an ARM binary — a failure that arrives at the linker with no
# mention of architecture. PKG_CONFIG_LIBDIR confines it as well; neither alone
# is trusted.
#
# LIBS order is a static-link order. Each entry resolves symbols the entries
# before it refer to: mbedtls calls into its siblings, libgcrypt into
# libgpg-error, the ffmpeg trio into libavutil, and -lemcompat last because
# nothing in it needs anything further along.
PKG_CONFIG_LIBDIR="$PREFIX/lib/pkgconfig" \
./configure --host=armv7a-linux-androideabi \
    --with-airplay-2 \
    --with-stdout --with-tinysvcmdns --with-ssl=mbedtls --with-metadata \
    --without-pkg-config \
    --without-alsa --without-pa --without-pw --without-soxr \
    CFLAGS="$BASE_CFLAGS" \
    LDFLAGS="-L$PREFIX/lib -static-libgcc" \
    LIBS="-lmbedx509 -lmbedcrypto -lgcrypt -lgpg-error -lsodium -lplist-2.0 \
          -lavformat -lavcodec -lswresample -lavutil -luuid -lemcompat"

# The shim is added at MAKE time, not at configure time, and that is the
# difference between configuring and not: autoconf probes a libc function by
# declaring it itself as `char clock_gettime();`, and the shim pulls in
# <time.h>, which declares the real prototype. Every check then fails
# identically and the first to report it is `librt needed` — a message pointing
# at the stub archive above, which is perfectly fine.
#
# CXXLD is the other half. configure runs AC_PROG_CXX so automake links with
# clang++, which pulls in libc++_shared.so — a C++ runtime that is not on
# FireOS 5 and never will be. It builds clean, strips clean, and dies at exec.
make -j"$JOBS" \
    CFLAGS="$BASE_CFLAGS -include /compat/android_compat.h" \
    CXXLD="$CC"

# ---------------------------------------------------------------------------
say "checking both binaries before they leave the container"
cd /build

check_elf() {
    local path="$1" name="$2"
    if "$NDK/bin/llvm-readelf" -d "$path" | grep -q "libc++"; then
        echo "ERROR: $name needs a C++ runtime the device does not have" >&2
        "$NDK/bin/llvm-readelf" -d "$path" | grep NEEDED >&2
        exit 1
    fi
    # Proof rather than assumption: the whole hazard is that everything looks
    # fine until the device refuses the file.
    local arch
    arch="$("$NDK/bin/llvm-readelf" -h "$path" | awk -F: '/Machine/ {print $2}')"
    case "$arch" in
        *ARM*) ;;
        *) echo "ERROR: $name is not an ARM binary:$arch" >&2; exit 1 ;;
    esac
    echo "$name:"
    "$NDK/bin/llvm-readelf" -d "$path" | grep NEEDED || true
}

check_elf /build/shairport-sync/shairport-sync shairport-sync
check_elf /build/nqptp/nqptp nqptp

"$NDK/bin/llvm-strip" /build/shairport-sync/shairport-sync
"$NDK/bin/llvm-strip" /build/nqptp/nqptp

cp /build/shairport-sync/shairport-sync /out/shairport-sync-ap2
cp /build/nqptp/nqptp /out/nqptp
( cd /out && md5sum shairport-sync-ap2 nqptp > ap2.md5 )

say "done"
ls -lh /out/shairport-sync-ap2 /out/nqptp
