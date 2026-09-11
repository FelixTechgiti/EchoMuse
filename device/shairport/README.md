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
| native libraries | libssl (or mbedtls), libpopt, libconfig | **+ libplist, libsodium, libgcrypt, uuid, libsoxr, libavutil, libavcodec, libavformat, libswresample** |
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
- **Five more cross-builds**: libplist, libsodium, libgcrypt (and
  libgpg-error), libuuid, libsoxr. Each ordinary — and popt below is the
  standing warning about what "ordinary" costs here.
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
