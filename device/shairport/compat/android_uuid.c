/*
 * android_uuid.c — uuid_generate_random / uuid_unparse_lower for bionic.
 *
 * # Why this is implemented rather than cross-built
 *
 * libuuid lives in util-linux. shairport-sync uses three things from it —
 * `uuid_t`, `uuid_generate_random`, `uuid_unparse_lower` — at ONE site, to
 * mint the AirPlay `pi` identifier that goes in the `_airplay._tcp` TXT
 * records. Cross-compiling util-linux for Android to get them means a large
 * autotools tree with a history of needing Android-specific patches, in
 * exchange for forty lines of RFC 4122 §4.4.
 *
 * The precedent is in this directory: android_ifaddrs.c implements getifaddrs
 * over netlink rather than dragging in a library, for the same reason. The
 * project's rule about not substituting a HOST library still holds and is not
 * in tension with this — that rule is about a library built for the wrong
 * platform silently answering questions about FireOS 5. This is built for the
 * target like everything else here.
 *
 * # Entropy
 *
 * `getrandom(2)` is __INTRODUCED_IN(28) and this builds at 22, so the source
 * is /dev/urandom — which on Android is present, seeded by the kernel before
 * userspace starts, and is what libuuid itself reads.
 *
 * A failure to read it cannot be reported: libuuid's signature returns void
 * and the caller has nowhere to put an error. So it is answered with a
 * WEAKER source and one line on stderr, never with a constant. The difference
 * matters more than it looks: two Echos that mint the same `pi` are two
 * AirPlay devices claiming one identity, which presents as a speaker that
 * appears and disappears from the picker rather than as anything to do with
 * randomness.
 */

#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/time.h>
#include <sys/types.h>
#include <unistd.h>

#include "uuid/uuid.h"

/* Read exactly n bytes, retrying short reads. Returns 0 on success. EINTR is
 * the only error worth retrying: a signal arriving during the read of sixteen
 * bytes. */
static int read_full(int fd, unsigned char *buf, size_t n) {
  size_t got = 0;
  while (got < n) {
    ssize_t r = read(fd, buf + got, n - got);
    if (r > 0) {
      got += (size_t)r;
      continue;
    }
    if (r < 0 && errno == EINTR)
      continue;
    return -1;
  }
  return 0;
}

/* The fallback. Not a CSPRNG and does not pretend to be — it exists so that a
 * device whose /dev/urandom is unreadable still gets a different identifier
 * from its neighbour, which is the property that actually matters here. */
static void weak_fill(unsigned char *buf, size_t n) {
  struct timeval tv;
  gettimeofday(&tv, NULL);

  uint64_t x = (uint64_t)tv.tv_sec * 1000000000ull + (uint64_t)tv.tv_usec * 1000ull;
  x ^= (uint64_t)getpid() << 32;
  x ^= (uintptr_t)buf; /* ASLR, such as it is */
  if (x == 0)
    x = 0x9E3779B97F4A7C15ull;

  for (size_t i = 0; i < n; i++) {
    /* xorshift64*, enough to spread the seed across the bytes */
    x ^= x >> 12;
    x ^= x << 25;
    x ^= x >> 27;
    buf[i] = (unsigned char)((x * 0x2545F4914F6CDD1Dull) >> 56);
  }
}

void uuid_generate_random(uuid_t out) {
  int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
  int ok = 0;

  if (fd >= 0) {
    ok = (read_full(fd, out, sizeof(uuid_t)) == 0);
    close(fd);
  }

  if (!ok) {
    fprintf(stderr,
            "uuid: /dev/urandom unreadable, falling back to a weak source — "
            "two devices could mint the same AirPlay identifier\n");
    weak_fill(out, sizeof(uuid_t));
  }

  /* RFC 4122 §4.4: version 4 in the high nibble of octet 6, variant 10x in the
   * top bits of octet 8. Applied AFTER filling, to whichever source ran. */
  out[6] = (unsigned char)((out[6] & 0x0F) | 0x40);
  out[8] = (unsigned char)((out[8] & 0x3F) | 0x80);
}

void uuid_unparse_lower(const uuid_t uu, char *out) {
  static const char hex[] = "0123456789abcdef";
  /* 8-4-4-4-12. Written by hand rather than with snprintf so the output cannot
   * depend on a locale or on %02x's width behaviour for a promoted char. */
  static const int dashes[] = {4, 6, 8, 10};
  size_t o = 0;
  for (size_t i = 0; i < 16; i++) {
    for (size_t d = 0; d < sizeof(dashes) / sizeof(dashes[0]); d++) {
      if ((int)i == dashes[d])
        out[o++] = '-';
    }
    out[o++] = hex[(uu[i] >> 4) & 0x0F];
    out[o++] = hex[uu[i] & 0x0F];
  }
  out[o] = '\0';
}
