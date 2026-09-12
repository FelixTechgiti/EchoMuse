# shairport-sync for the Echo Dot

AirPlay on the device needs a `shairport-sync` binary built for armv7a /
Android API 22. There are no Android builds published, so it has to be built
and pushed to each device — the same situation as librespot.

```bash
./build.sh
```

## This targets CLASSIC AirPlay, and the reasons are cost rather than a wall

AirPlay 2 remains the goal. This section used to name three things that were
"hard on this hardware rather than merely laborious". All three were checked
against the sources on 2026-09-11 and **none of them survived** (#79). They
are kept here with their corrections, because a wrong reason not to do
something is worse than no reason: it stops the next person from looking.

| | classic | AirPlay 2 |
|---|---|---|
| native libraries | libssl (or mbedtls), libpopt, libconfig | **+ libplist, libsodium, libgcrypt (+libgpg-error), uuid, libavutil, libavcodec, libavformat, libswresample** — and mbedtls STAYS, because `pair_ap` is built with a hardcoded `-DCONFIG_GCRYPT` and has no mbedTLS path. Not libsoxr: that is an independent feature, not an AirPlay 2 requirement |
| audio codec | ALAC, decoded in-tree | ALAC for Realtime streams, **AAC-LC via ffmpeg** for Buffered |
| mDNS | bundled `tinysvcmdns` | a backend that advertises a SECOND service and refreshes its TXT |
| timing | NTP-ish, in-process | **nqptp**, a second daemon doing PTP on UDP 319/320 |
| stated minimum | runs on far less | "2018 onwards Linux", "a Raspberry Pi 2 or a Raspberry Pi Zero 2 W, or better" |

### The three that were wrong

- **"nqptp wants timestamps this kernel does not provide in hardware."**
  nqptp's own README says the opposite in one sentence: *"nqptp does not take
  advantage of hardware timestamping."* What it needs is exclusive use of UDP
  319 and 320 and the privilege to bind them. The device is rooted and Android
  runs no PTP service, so both are free. The MediaTek kernel never entered
  into it.

- **"Android has no D-Bus and no Avahi, and AirPlay 2's discovery is built on
  it."** `configure.ac` couples `--with-airplay-2` to no mDNS backend at all,
  and `CONFIG_AIRPLAY_2` appears zero times across all four `mdns_*.c` files.
  What is true is narrower: of the four backends **only `mdns_avahi.c`
  implements the second service** — `mdns_dns_sd.c`, `mdns_external.c` and
  `mdns_tinysvcmdns.c` all take `ap2name` and `secondary_txt_records` and
  declare them `__attribute__((unused))`, and none of the three sets
  `mdns_update`, which `rtsp.c` calls four times to keep `_airplay._tcp`'s TXT
  records current. So with the bundled responder that service is never
  advertised — a real gap, and one of about a hundred lines in one file:
  `mdnsd_register_svc` registers one service per call and can be called twice.
  Not a port of Avahi to a platform with no D-Bus.

- **"This device is under the stated minimum."** The floor was misquoted. It
  is not "a Raspberry Pi B" but *"a Raspberry Pi 2 or a Raspberry Pi Zero 2 W,
  or better"* — a Pi Zero 2 W is a quad Cortex-A53 at 1GHz with 512MB, and the
  MT8163 is a quad Cortex-A53 at 1.3GHz with 512MB. The device is at or above
  the floor, not under it. "2018 onwards Linux" is about library vintage, and
  every dependency here is cross-compiled from a pinned source anyway — which
  is what this whole recipe is.

### What is actually in the way

- **`shm_open` does not exist in bionic**, and it is how nqptp hands the clock
  to shairport-sync: `nqptp.c`, `nqptp-clock-sources.c` and shairport's
  `ptp-utilities.c`, three call sites in total. The shape of the interface is
  what makes this tractable: a double-buffered struct read with a `memcmp`
  retry until two reads agree — **no process-shared mutex, no semaphore**,
  nothing else bionic lacks — so a file under `/data` opened and `mmap`ed is a
  faithful substitute, injected with the same `-include` shim `compat/`
  already uses.
- **ffmpeg for armv7a/API 22**, trimmed to the decoders actually used rather
  than built by default. The largest new dependency, and routine rather than
  novel.
- **Four more cross-builds**: libplist, libsodium, libgcrypt and
  libgpg-error. Each ordinary — and popt below is the standing warning about
  what "ordinary" costs here; libgpg-error turned out to have a trap of its
  own, documented with the build. libuuid is implemented in `compat/` instead,
  and libsoxr is not a dependency at all — both corrected below.
- **512MB shared with Android.** AirPlay 2 wants "more memory for bigger
  buffers and larger libraries"; a Pi Zero 2 W has the same 512MB and does not
  also run Android. This is the one item that cannot be answered by reading.

The mDNS half is **the same work as #77**, where librespot's and
shairport-sync's two responders cancel each other out and the leading fix is
one responder owned by the firmware. Whoever publishes the device's services
can publish `_airplay._tcp` beside `_raop._tcp`. Do not solve it twice — and
note `mdns_external` does not get the second service for free either.

**The device-side code still does not care which one it gets.** Both put PCM
on stdout — but the sample-rate reason given here was also wrong. AirPlay 2 is
**not** 48kHz: `AIRPLAY2.md` says Buffered Audio is "AAC stereo at 44,100
frames per second" and requires an output device "capable of running at 44,100
frames per second", and Realtime streams are ALAC exactly as in classic. So
`internal/resample` stays in the path either way, and an AirPlay 2 build is
still a binary and a config value rather than a rewrite.

**The sequencing is the real reason this is still classic**, and it has not
changed: nobody has started the classic binary on a Dot (see the end of this
file). An AirPlay 2 build before that point means debugging two unknowns at
once.

## First run: 2026-09-06. Seven corrections, and two of them were the platform

The recipe had never been executed. Running it found seven things, and they
fall into two groups: **autoconf asking Linux questions of a platform that is
Linux only in the sense `configure` means**, and **bionic genuinely not having
two POSIX facilities**. It builds now.

### The five that got `configure` through

- **`libpopt` and `libconfig` were simply absent from the recipe.** Both are
  hard requirements — configure aborts without either — and the script built
  neither. Now cross-compiled here rather than apt-installed, for the reason
  the compiler image exists at all: a host library is a second opinion about
  what FireOS 5 provides. Static, so the device carries one file.
- **`librt` and `libpthread` do not exist on bionic**, and configure requires
  both unconditionally. glibc folded them into libc in 2.17 and 2.34, and
  every distro keeps stub archives so exactly these checks keep passing;
  Android keeps none, so the checks fail on functions the platform *has*. Two
  empty archives make the `-l` resolvable and let libc answer.
- **popt needs `ac_cv_header_glob_h=no`.** The NDK ships `glob.h` but declares
  `glob()` only from API 28, so the header check passes and the *link* fails.
  Everything using it is behind `HAVE_GLOB_H`.
- **`--with-mbedtls` is not an option; `--with-ssl=mbedtls` is.** This one
  failed loudly.
- **`--without-pipewire` is not an option either; it is `--without-pw`.** This
  one did *nothing*, silently, and would have gone on doing nothing — autoconf
  accepts an unknown `--with` without a word. The backend is off by default,
  so the flag read as a deliberate exclusion while excluding nothing.

Also `flex`/`bison` in the image, and `LIBS="-lmbedx509 -lmbedcrypto"` so the
mbedtls probe can link a static archive that calls into its siblings.

### The two that needed code: `compat/`

**bionic has no pthread cancellation at any API level** — `pthread_cancel`
appears zero times in its `pthread.h`, and that is a design decision, not a
missing `__INTRODUCED_IN`. It *does* have `pthread_cleanup_push`/`pop`, which
is what makes this tractable rather than a rewrite: the cleanup handler stack
exists and `pthread_exit` unwinds it. So `pthread_cancel` becomes a real-time
signal whose handler calls `pthread_exit`, and the signal is also what brings
back a thread parked in `read()`/`poll()` — which is every one of
shairport-sync's 22 cancel targets.

**`pthread_setcancelstate` is implemented for real, and must stay that way.**
A stub returning 0 is worse than not building: shairport disables cancellation
around critical sections precisely so it cannot be torn down inside them. The
state and a pending flag live in TLS, and a cancel arriving while disabled is
delivered at the next enable or `testcancel`. That is deferred cancellation,
faithfully.

**`getifaddrs` is `__INTRODUCED_IN(24)`** and we build at 22, so `compat/`
implements it over netlink. The shortcut here is a trap worth naming: the
`SIOCGIFCONF` ioctl route is far shorter and cannot report a MAC address —
and `common.c` reads the MAC out of the `AF_PACKET` entries to derive the
**AirPlay device ID**. That version would compile, link, run, and hand out an
all-zero ID.

**The shim is injected with `-include`, so no upstream source is patched** and
the 4.3.7 pin stays movable. It is added at *make* time and not at configure
time, which is not tidiness: the shim pulls in `<time.h>`, autoconf probes
libc functions by declaring them itself as `char clock_gettime();`, and the
two conflict — so every check fails and the first to say so is `librt needed`,
a message pointing at a stub archive that is perfectly fine.

### And one that would only have shown up on hardware

**The link used `clang++` and the binary needed `libc++_shared.so`.**
configure runs `AC_PROG_CXX`, so automake links with the C++ driver even
though every object comes from a `.c` file, and the NDK clang++ pulls in a C++
runtime that is not on FireOS 5 and never will be. It builds clean, strips
clean, and dies at exec with a missing library — at the end of an install, on
the device. `CXXLD` is overridden to the C compiler, and `build.sh` now
*fails* if a `libc++` NEEDED entry reappears, because the whole hazard is that
everything looks fine until the device refuses the file.

### Result

516KB, `ELF32 / ARM / little-endian`, NEEDED: `libm.so`, `libdl.so`, `libc.so`
— all bionic.

**Still unproven: that it RUNS.** This is the right architecture against the
right libc with the right dependencies, verified by reading the ELF. Nobody
has started it on a Dot. The device's own `airplay_status` — read by the
dashboard after every install — stays the authority on that, and the shim's
signal-based cancellation is the first thing to suspect if a thread ever dies
holding a lock.

## The shared memory shim, for AirPlay 2 (not used by the classic build)

`compat/android_shm.c` implements `shm_open` and `shm_unlink`, which bionic
has at no API level. They are the **only** interface between nqptp and
shairport-sync, and therefore the whole of the AirPlay 2 clock path — see #79,
where this is the one blocker that turned out to be real.

Everything else on that path bionic already has: `ftruncate`, `mmap`,
`munmap`, `MAP_SHARED`. There is **no process-shared mutex and no semaphore**
anywhere in it — nqptp writes the record twice and the reader `memcmp`s the
two copies and retries until they agree. That is what makes this ninety lines
rather than a port, and it is worth re-checking on an nqptp bump rather than
assumed: if either side ever reaches for `pthread_mutexattr_setpshared` the
answer changes completely.

**The backing directory has to be a tmpfs, and the shim checks.** nqptp
rewrites the struct at PTP rate, and a `MAP_SHARED` mapping of a file on flash
has its dirty pages written back by the kernel on its own schedule, for the
life of the daemon. Backing this with `/data/local/tmp` is not a slower
version of the right answer, it is continuous writes to the eMMC of a 2015
speaker — with nothing failing and nothing logged. `/dev` is a tmpfs on
Android, so the default is `/dev/revoice-shm`; `REVOICE_SHM_DIR` overrides it,
and a directory that `statfs` says is not tmpfs gets one loud line on stderr
rather than a refusal.

Names are flattened rather than nested (`/nqptp/client3` →
`<dir>/nqptp_client3`), which also means a name can never escape the
directory. That is a security property and not tidiness: the name reaches the
shim from a config file.

**It is in its own header**, not in `android_compat.h`, because nqptp needs it
too — a different program with its own build — and handing a daemon that uses
no threads a header full of pthread cancellation is the wrong shape.

`compat/shmcheck.c` `#include`s the implementation whole and drives it on the
host, the same trick `emos/init/pwcheck.c` uses and for the same reason: the
alternative is a shim nobody can run until it is on a device, and the device
is where a mistake here is expensive. It asserts the name mapping, the
traversal containment, the POSIX error cases, and a full writer/reader round
trip including the torn-write case the double-buffered retry exists to catch.
CI runs it.

```bash
cc -O2 -Wall -Wextra -o /tmp/shmcheck compat/shmcheck.c && /tmp/shmcheck
```

**Nothing builds against it yet.** `build.sh` is the classic 4.3.7 build,
which does not use PTP at all, so the shim is not linked into anything that
ships today. It is the first item done on #79's list, not the last — nqptp
still has to be cross-compiled, and ffmpeg and five more libraries with it.

## The AirPlay 2 build (`./build-ap2.sh`) — written, never run

```bash
./build-ap2.sh            # shairport-sync 4.3.7 + nqptp 1.2.8, armv7a/API 22
```

Produces **two** binaries, `out/shairport-sync-ap2` and `out/nqptp`. AirPlay 2
needs both: nqptp is a separate daemon holding UDP 319 and 320 and publishing
the PTP clock that shairport-sync times against.

**Nothing in it has been executed.** The classic recipe needed seven
corrections the first time it ran, and this one is larger. What it is worth is
the decisions, each read off a source rather than guessed — those are below,
and three of them contradict what this file or #79 said before.

### It is a separate script, not a mode inside `build.sh`

The two recipes share four library builds and differ in everything else, and
`build.sh` is the path currently being proven on hardware (#16). A mode flag
would add a branch to the one file whose failure mode is "the device refuses to
exec it", in a script CI cannot run. `Dockerfile.ap2` is separate for the same
reason and `FROM`s the same pinned digest, so the toolchain is identical and
only the build-host tooling differs.

The in-container half is **a mounted script**, not a `bash -c` string. The
classic recipe carries a warning in three places that an apostrophe in a
comment ends its single-quoted body and breaks the build; that trap has nothing
to teach and costs a cycle every time.

### libgcrypt is not optional and does not displace mbedTLS

`Makefile.am:5` builds `lib_pair_ap.a` with a **hardcoded** `-DCONFIG_GCRYPT`:

```make
lib_pair_ap_a_CFLAGS = -Wall -g -DCONFIG_GCRYPT -pthread
```

`pair_ap` is AirPlay 2's pairing and encryption, and `pair-internal.h` branches
only on `CONFIG_GCRYPT` / `CONFIG_OPENSSL` — there is no mbedTLS path in it at
all. So libgcrypt (and libgpg-error under it) is **in addition to** whatever
`--with-ssl` selects, not a replacement for it. `--with-ssl=mbedtls` stays.

### libgpg-error needs a host triplet spelled differently from every other one

The single trap in this dependency, and it fails at build time with a message
about a missing header:

- For a host matching `*-gnu*` or `*-musl*`, its configure generates the lock
  object header by running objdump over a probe.
- Android matches neither, so it falls to `force_use_syscfg=yes` and
  `src/mkheader.c:621` includes `syscfg/lock-obj-pub.$host.h` — a file that
  must **already exist** for the exact canonicalised triplet.
- The tree ships `lock-obj-pub.arm-unknown-linux-androideabi.h`. It ships no
  armv7a one, and `armv7a-linux-androideabi` canonicalises to
  `armv7a-unknown-linux-androideabi`.

So libgpg-error alone is configured `--host=arm-unknown-linux-androideabi`,
with `CC` unchanged, so the objects are still armv7a.

### libuuid is implemented here rather than cross-built

shairport-sync uses `uuid_t`, `uuid_generate_random` and `uuid_unparse_lower`
at one site (`shairport.c:556`) to mint the AirPlay `pi` identifier. libuuid
lives in util-linux — a large autotools tree with a history of needing
Android-specific patches — in exchange for forty lines of RFC 4122 §4.4.
`compat/android_uuid.c` implements them, `compat/uuid/uuid.h` makes
`#include <uuid/uuid.h>` resolve without patching anything, and `uuidcheck.c`
proves the version and variant bits, the formatting and that every free bit
actually varies. The precedent is next to it: `android_ifaddrs.c` implements
getifaddrs over netlink for the same reason.

### ffmpeg is trimmed to two decoders

`--with-airplay-2` requires libavutil, libavcodec, libavformat and
libswresample. What they are FOR is one codec: `AIRPLAY2.md` says Buffered
Audio is "AAC stereo at 44,100 frames per second" and Realtime streams are
ALAC. So `--disable-everything` plus the AAC and ALAC decoders, rather than a
general-purpose media framework on a device sharing 512MB with Android.

Pinned at `n7.1.5` rather than the newest (9.0.1 at the time of writing): 7.1
is the series every Android NDK recipe in the wild is written against, and this
build cannot be iterated on cheaply.

### Three corrections to what was written before

- **libsoxr is NOT an AirPlay 2 dependency.** The table further up this file
  listed it among the extras. `configure.ac`'s AirPlay 2 block requires
  libplist, libsodium, libgcrypt, the four ffmpeg libraries, libuuid and `xxd`
  — soxr is an independent `--with-soxr` feature, and this build passes
  `--without-soxr` exactly as the classic one does.
- **`plistutil` is a 5.x requirement, not a 4.3.7 one.** A comment on #79 said
  `configure.ac`'s AirPlay 2 block hard-fails without it. That is true of
  5.5.1 (`configure.ac:441`) and the string does not appear anywhere in 4.3.7.
  If the pin ever moves to 5.x, `libplist-utils` has to go in the image or
  configure fails with a message about a library.
- **`xxd` really is required**, and for AirPlay 2 specifically:
  `configure.ac:421` aborts without it and `Makefile.am:152` uses `xxd -i` to
  embed `plists/get_info_response.xml`.

### And the firewall, which is a different subsystem's problem

FireOS drops every inbound port it was not told about, so a device advertising
AirPlay 2 perfectly can still be unreachable — see "Advertised is not
reachable" in `device/CLAUDE.md`. AirPlay 2 needs TCP 7000 and a UDP range that
the classic rules do not cover, and nqptp needs 319/320 inbound.
`internal/netfilter` is where that goes, and it is not done.
