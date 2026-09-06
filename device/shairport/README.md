# shairport-sync for the Echo Dot

AirPlay on the device needs a `shairport-sync` binary built for armv7a /
Android API 22. There are no Android builds published, so it has to be built
and pushed to each device — the same situation as librespot.

```bash
./build.sh
```

## This targets CLASSIC AirPlay, and that is a decision with reasons

AirPlay 2 remains the goal. What stands between here and there was read off
shairport-sync's own build documentation rather than assumed, and it is more
than a configure flag:

| | classic | AirPlay 2 |
|---|---|---|
| native libraries | libssl (or mbedtls), libpopt, libconfig | **+ libplist, libsodium, libgcrypt, uuid, libsoxr, libavutil, libavcodec, libavformat** |
| audio codec | ALAC, decoded in-tree | AAC-ELD, via ffmpeg |
| mDNS | bundled `tinysvcmdns` | **Avahi**, which is a D-Bus daemon |
| timing | NTP-ish, in-process | **nqptp**, a second daemon doing PTP on UDP 319/320 |
| stated minimum | runs on far less | "2018 onwards Linux", "a Raspberry Pi B or better" |

Three of those are hard on this hardware rather than merely laborious:

- **Android has no D-Bus and no Avahi.** AirPlay 2's discovery is built on it.
- **nqptp wants timestamps a 2015 MediaTek kernel does not provide** in
  hardware. Software PTP may be good enough; nobody has tried it here.
- **This device is under the stated minimum** — a 2015 MT8163 on Android 5.1,
  where the floor is a 2018 Linux. Under the minimum is not the same as
  impossible, and it is not a footing to plan from either.

**The device-side code does not care which one it gets.** Both put PCM on
stdout, and the only difference that reaches the firmware is the sample rate:
AirPlay 2 is 48kHz and passes through untouched, classic is 44.1kHz and goes
through `internal/resample`. When an AirPlay 2 build lands, it is a config
value and a binary, not a rewrite.

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
