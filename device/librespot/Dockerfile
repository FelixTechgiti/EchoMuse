# Builds librespot for the Echo Dot: armv7a, Android API 22, bionic.
#
# ON TOP OF THE FIRMWARE'S OWN COMPILER IMAGE, deliberately. That image
# already carries NDK 21.4.7075529 targeting API 22, and it is the toolchain
# every shipped firmware has been built with and run on real hardware. Adding
# Rust to it means librespot links against the SAME libc, at the same API
# level, as the binary it runs beside — rather than introducing a second
# opinion about what FireOS 5 provides, which is the kind of difference that
# shows up as a device that boots and a subprocess that does not.
#
# The base is pinned by digest for the reason stated in ../compiler/Dockerfile:
# a floating tag is a third party changing the compiler under us with nothing
# in CI to notice.
FROM ghcr.io/binozo/echogo@sha256:c9456551c639290b1cc3e1df891177beadb5d0d7621a5c2ec81079a276019147

ENV NDK=/opt/android/ndk/21.4.7075529/toolchains/llvm/prebuilt/linux-x86_64
ENV PATH=/root/.cargo/bin:$PATH

# A pinned Rust, for the base image's reason. librespot builds on stable, and
# the version is named so a toolchain change is a commit rather than a
# surprise on a Tuesday.
ARG RUST_VERSION=1.83.0
RUN curl -sSf https://sh.rustup.rs | sh -s -- -y \
        --default-toolchain ${RUST_VERSION} --profile minimal && \
    rustup target add armv7-linux-androideabi

# The linker. Rust's armv7-linux-androideabi target does not know where the
# NDK is, and without this it reaches for a host linker and fails on symbols
# that look like a missing crate rather than a missing sysroot.
RUN mkdir -p /root/.cargo && printf '%s\n' \
    '[target.armv7-linux-androideabi]' \
    'linker = "/opt/android/ndk/21.4.7075529/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi22-clang"' \
    'ar = "/opt/android/ndk/21.4.7075529/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-ar"' \
    > /root/.cargo/config.toml

WORKDIR /build
