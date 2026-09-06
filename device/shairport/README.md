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

## First run: 2026-09-06, and it took four corrections

The recipe had never been executed, and running it found four things. None was
OpenSSL — the mbedtls route was the right call and cost nothing — and they are
all the same shape: **autoconf asking Linux questions of a platform that is
Linux only in the sense `configure` means.**

**Two required libraries were simply absent from the recipe.** `configure.ac`
aborts without `libpopt` and `libconfig`, and the script built neither. Both
are now cross-compiled here rather than apt-installed, for the reason the
compiler image exists at all: a host library is a second opinion about what
FireOS 5 provides. Both static, so the device carries one file.

**`librt` and `libpthread` do not exist on bionic**, and configure requires
both unconditionally (`AC_CHECK_LIB([rt],[clock_gettime])` and
`AC_CHECK_LIB([pthread],[pthread_create])`). glibc folded those into libc in
2.17 and 2.34, and every distro keeps stub archives so exactly these checks
keep passing; Android keeps none, so the checks fail on functions the platform
*has*. Two empty archives make `-lrt` and `-lpthread` resolvable and let libc
answer — the same thing a glibc stub does. Patching `configure.ac` would mean
carrying a patch against every future 4.3.x.

**popt needs `ac_cv_header_glob_h=no`.** The NDK ships `glob.h` but declares
`glob()` only for API 28 and up, so the header check passes and the *link*
fails. Everything using it is behind `HAVE_GLOB_H`, so denying the header
compiles the non-glob path — the library is then correct rather than merely
linkable, and nothing here reads `/etc/popt.d` anyway.

**`--without-pkg-config`, for a failure that names the wrong thing.**
pkg-config answers from the host by default, so a `.pc` found there hands back
host include and library paths — which links an x86 object into an ARM binary
and fails at the linker with no mention of architecture.
`PKG_CONFIG_LIBDIR` confines it as well; neither is trusted alone.

## ⛔ It configures completely and then does not compile: bionic has no pthread cancellation

**This recipe does not currently produce a binary, and the reason is not a
flag.** Every check above passes — `configure` completes, having found rt,
pthread, m, popt, config and mbedtls — and then `make` fails in
shairport-sync's own sources:

```
common.c:1789:26: error: use of undeclared identifier 'PTHREAD_CANCEL_DISABLE'
common.c:2154:9:  warning: implicit declaration of function 'getifaddrs'
```

**`pthread_cancel` appears ZERO times in bionic's `pthread.h`.** Android has
never implemented thread cancellation and this is a design decision rather
than a missing API level — there is no `__INTRODUCED_IN` to raise past, and no
NDK version that adds it. shairport-sync is built on it:

| | `pthread_cancel` | `setcancelstate` | `cleanup_push` | `testcancel` | total |
|---|---|---|---|---|---|
| 3.3.9 | | | | | **113** |
| 4.1.1 | | | | | **164** |
| 4.3.7 | 22 | 68 | 72 | 2 | **164** |

No version escapes it, so this is not a pin to move. Every
`pthread_cleanup_push`/`pop` pair is a teardown path that would have to be
rewritten as a cooperative shutdown flag — that is a **port of the threading
model**, not a build fix, and it should be judged as one before anybody starts
it.

`getifaddrs` is the small half by comparison: `__INTRODUCED_IN(24)` against
our API 22, and a netlink implementation is a well-trodden ~200 lines. It is
listed here only so the next person is not surprised by it after solving the
hard one.

**What this recipe is now good for** is that it gets all the way to the real
obstacle. It was previously abandoning at the first `configure` check, so the
five corrections above were each hiding the next one; the platform limit is
what is actually in the way, and it is now the first thing you meet.

**The device side is unaffected and needs nothing.** With no binary installed
the firmware reports `airplay_status: {ok: false, reason: "not_installed"}`,
and the dashboard disables the AirPlay toggle and says so — which is the
correct presentation of exactly this situation. The install path built for
Spotify handles AirPlay identically the day a binary exists.

### The 4.3.7 pin is load-bearing — do not bump it casually

**shairport-sync 5.x removed `--with-tinysvcmdns`.** Checked against 5.5's
`configure.ac`: the only mDNS options left are Avahi (a D-Bus daemon Android
does not have), `dns_sd`, and `--with-external-mdns`, which shells out to a
publishing helper that does not exist here either.

tinysvcmdns is the bundled responder that needs nothing, and it is the only
reason this builds for Android at all. 4.3.x has it; 5.x does not. Bumping the
version to "keep up" removes the feature.

The device side is complete and tested without it: with no binary installed
the firmware reports `airplay_status: {ok: false, reason: "not_installed"}`,
and the dashboard disables the toggle and says so.
