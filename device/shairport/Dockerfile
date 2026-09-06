# Builds shairport-sync for the Echo Dot: armv7a, Android API 22, bionic.
#
# On the firmware's own compiler image, for the reason device/librespot's
# recipe is: that image already carries NDK 21.4.7075529 at API 22, and it is
# the toolchain every shipped firmware has been built with and run on real
# hardware. A second toolchain is a second opinion about what FireOS 5
# provides, and that difference shows up as a device that boots and a
# subprocess that does not.
FROM ghcr.io/binozo/echogo@sha256:c9456551c639290b1cc3e1df891177beadb5d0d7621a5c2ec81079a276019147

ENV NDK_ROOT=/opt/android/ndk/21.4.7075529
ENV NDK=/opt/android/ndk/21.4.7075529/toolchains/llvm/prebuilt/linux-x86_64
ENV PATH=$NDK/bin:$PATH

# autotools and cmake for the source trees; xxd is in shairport-sync's own
# dependency list and is used by its build to embed a key.
#
# flex and bison are for libconfig, and they are here because the build
# otherwise depends on TIMESTAMP LUCK. libconfig commits its generated
# scanner.c and grammar.c alongside the scanner.l and grammar.y they come
# from, and `git clone` writes every file at the same instant — so whether
# make decides the generated file is out of date is a coin toss settled by
# the order the checkout happened to touch them. It came up heads on the
# first run here and tails on the next, in the same image, with nothing
# changed between them. Carrying the two generators removes the coin toss
# rather than hoping it lands the right way.
RUN apt-get update && apt-get install -y --no-install-recommends \
        autoconf automake libtool pkg-config cmake xxd git ca-certificates \
        flex bison \
    && rm -rf /var/lib/apt/lists/*

# The autotools cross-compile triple. shairport-sync's configure needs these
# named explicitly; without them it finds the host compiler and produces an
# x86 binary that looks like a successful build right up until the device
# refuses to exec it.
ENV CC=armv7a-linux-androideabi22-clang
ENV CXX=armv7a-linux-androideabi22-clang++
ENV AR=llvm-ar
ENV RANLIB=llvm-ranlib
ENV STRIP=llvm-strip

WORKDIR /build
