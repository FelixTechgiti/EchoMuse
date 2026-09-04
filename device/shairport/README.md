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

## Not yet run

⚠ **This recipe has never been executed** — the NDK cannot be downloaded from
the environment it was written in (`dl.google.com` is refused by the network
policy). The likely friction is OpenSSL, which is why the recipe takes the
mbedtls route (`--with-mbedtls`) instead: it cross-compiles far more easily.

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
