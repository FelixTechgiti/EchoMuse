#!/bin/bash
# Build mdnsprobe for the Echo Dot.
#
# It links NO libc — only raw ARM EABI syscalls — so any ARM cross-compiler
# produces a working binary and bionic never enters into it. That is also why
# this does not use the pinned `revoice-compiler` image the Go tools use: there
# is no libc to match, so pinning one would imply a compatibility question that
# does not exist here.
#
# The result is ~3KB, which matters: the way this reaches a device is base64
# through the controller's shell plane, and a static glibc build of the same
# source is 455KB.
set -e

CC=${CC:-arm-linux-gnueabi-gcc}
STRIP=${STRIP:-arm-linux-gnueabi-strip}
OUT=${1:-mdnsprobe}

command -v "$CC" >/dev/null || {
    echo "No $CC. On Debian/Ubuntu:" >&2
    echo "  apt-get install gcc-arm-linux-gnueabi libc6-dev-armel-cross" >&2
    echo "(the headers are for <stddef.h> and friends; nothing is linked)" >&2
    exit 1
}

# -nostdlib is the point. -fno-builtin stops gcc turning a loop into a memcpy
# call that would then not resolve, and ARM has no hardware division, so the
# source divides by ten by hand rather than pulling __aeabi_uidivmod out of
# libgcc — which drags in `raise`, which needs the libc we are not linking.
"$CC" -Os -march=armv7-a -nostdlib -static -fno-stack-protector -fno-builtin \
      -o "$OUT" "$(dirname "$0")/mdnsprobe.c"
"$STRIP" "$OUT" 2>/dev/null || true

echo "built $OUT — $(stat -c%s "$OUT") bytes"
echo
echo "To put it on a device without a cable, through the controller's shell:"
echo "  gzip -n -9 -c $OUT | base64 -w0"
echo "then on the device:"
echo "  echo -n '<base64>' > f.b64"
echo "  busybox base64 -d f.b64 | busybox gunzip > probe && chmod 755 probe"
echo
echo "ALWAYS compare md5 on both ends before running it. gzip stamps an mtime"
echo "into its header, so two runs of the same source differ — comparing a"
echo "freshly-made digest against an older paste reports corruption that is"
echo "not there, and hides corruption that is. Hence -n above."
