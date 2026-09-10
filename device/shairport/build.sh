#!/bin/bash
# Build shairport-sync (classic AirPlay) for the Echo Dot.
#
# Two things about this build are unusual, and both are bionic rather than us:
# it cross-compiles popt, libconfig and mbedtls because a host library would
# be a second opinion about what FireOS 5 provides, and it carries a compat
# shim (compat/, see #20) because Android has no pthread cancellation at any
# API level and no getifaddrs below API 24. The shim is injected with
# -include, so no upstream source is patched and the 4.3.7 pin stays movable.
#
# Usage: ./build.sh [git-ref]
set -euo pipefail

REF="${1:-4.3.7}"
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="$HERE/out"
IMAGE=revoice-shairport

echo "Building shairport-sync $REF for armv7a/Android API 22..."
docker build -t "$IMAGE" "$HERE"

mkdir -p "$OUT"
docker run --rm -v "$OUT:/out" -v "$HERE/compat:/compat:ro" "$IMAGE" bash -c '
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

    # The Android compat shim, injected with -include so UPSTREAM SOURCES
    # ARE NOT PATCHED. bionic has no pthread_cancel at any API level and no
    # getifaddrs below API 24; compat/ supplies both, and the header renames
    # the standard symbols onto its own so every call site compiles
    # unchanged. See compat/android_compat.h and #20.
    #
    # Building it into libcompat.a rather than listing objects: configure
    # decides the link line, and an archive named in LIBS is the one place
    # we can add code to it without editing a Makefile it regenerates.
    "$CC" -c -O2 -fPIC /compat/android_compat.c -o /build/android_compat.o
    "$CC" -c -O2 -fPIC /compat/android_ifaddrs.c -o /build/android_ifaddrs.o
    llvm-ar rcs /build/prefix/lib/libemcompat.a \
        /build/android_compat.o /build/android_ifaddrs.o

    # The option names are read off the AC_ARG_WITH list in configure.ac,
    # not guessed, because autoconf ACCEPTS AN UNKNOWN --with SILENTLY. Two
    # were wrong here and only one of them announced itself:
    #
    #   --with-mbedtls        the option is `ssl`, taking a value. This one
    #                         failed loudly — "specify one of --with-ssl=..."
    #                         — because nothing had selected a backend.
    #   --without-pipewire    the option is `pw`. This one did NOTHING, and
    #                         would have gone on doing nothing: the backend
    #                         is off by default, so the flag read as a
    #                         deliberate exclusion while excluding nothing.
    #                         The day that default changes is the day it
    #                         matters, and by then nobody is looking here.
    #
    # LIBS seeds the dependencies of a STATIC mbedtls, and without it the
    # mbedtls check fails on a library that is present and correct.
    # AC_CHECK_LIB([mbedtls],[mbedtls_ssl_init]) links `-lmbedtls $LIBS` and
    # nothing else — enough for the shared library every distro ships, not
    # enough for an archive: libmbedtls.a calls into libmbedx509 and
    # libmbedcrypto, so the probe ends in undefined symbols and configure
    # reports the library as missing. Naming them in LIBS puts them AFTER
    # -lmbedtls on every link line, which is the order a static link needs.
    #
    # -lemcompat goes LAST, for the same ordering reason: it resolves
    # symbols the objects before it refer to, and nothing in it needs
    # anything further along.
    # THE SHIM IS ADDED AT MAKE TIME, NOT AT CONFIGURE TIME, and that is not
    # tidiness — it is the difference between configuring and not.
    #
    # autoconf probes a libc function by declaring it itself, as
    # `char clock_gettime();`, and linking. The shim includes <pthread.h> and
    # <signal.h>, which pull in <time.h>, which declares the REAL prototype —
    # so the probe stops compiling:
    #
    #   error: conflicting types for clock_gettime
    #   error: too few arguments to function call, expected 2, have 0
    #
    # Every check then fails identically, and the first one to report it is
    # `librt needed` — a message that points at the stub archive two steps
    # above and has nothing to do with it. Cost an entire build to find,
    # because the symptom names the wrong file.
    #
    # BASE_CFLAGS is one variable used twice so the two lines cannot drift:
    # make has to be given the same flags configure got, plus the shim, and a
    # hand-copied second spelling is how the optimisation level ends up
    # silently different between what was probed and what was built.
    BASE_CFLAGS="-I/build/prefix/include -O2"

    PKG_CONFIG_LIBDIR=/build/prefix/lib/pkgconfig \
    ./configure --host=armv7a-linux-androideabi \
        --with-stdout --with-tinysvcmdns --with-ssl=mbedtls --without-pkg-config \
        --without-alsa --without-pa --without-pw --without-soxr \
        CFLAGS="$BASE_CFLAGS" \
        LDFLAGS="-L/build/prefix/lib -static-libgcc" \
        LIBS="-lmbedx509 -lmbedcrypto -lemcompat"

    # automake compiles with $(AM_CFLAGS) $(CFLAGS), so overriding CFLAGS here
    # ADDS the shim without discarding the flags shairport sets for itself.
    #
    # CXXLD is the other half, and without it this build produces a binary
    # the Dot cannot exec. configure runs AC_PROG_CXX, so automake links with
    # clang++ — as a driver only, since every object on that line comes from
    # a .c file — and the NDK clang++ links libc++_shared.so by default. The
    # result NEEDs a C++ runtime that is not on FireOS 5 and never will be:
    #
    #   NEEDED  libm.so  libc++_shared.so  libdl.so  libc.so
    #
    # It builds clean, it strips clean, and it dies at exec with a missing
    # library — on the device, at the end of an install, which is the worst
    # place to find out. Linking with the C compiler drops the dependency
    # because there is genuinely no C++ here to support.
    make -j"$(nproc)" \
        CFLAGS="$BASE_CFLAGS -include /compat/android_compat.h" \
        CXXLD="$CC"

    # Proof rather than assumption, and it fails the build rather than
    # warning: the whole hazard above is that everything looks fine until
    # the device refuses the file.
    if "$NDK/bin/llvm-readelf" -d shairport-sync | grep -q "libc++"; then
        echo "ERROR: binary needs a C++ runtime the device does not have" >&2
        "$NDK/bin/llvm-readelf" -d shairport-sync | grep NEEDED >&2
        exit 1
    fi

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
