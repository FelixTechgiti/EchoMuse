# librespot for the Echo Dot

Spotify Connect on the device needs a `librespot` binary built for armv7a /
Android API 22. **librespot publishes no Android builds**, so this has to be
built once and pushed to each device.

```bash
./build.sh          # defaults to a pinned librespot tag
./build.sh v0.7.1   # or name one
```

The result lands in `out/librespot`, stripped, with its md5 beside it.

## What the build does and does not include

`--no-default-features` drops the rodio backend and with it cpal, alsa-sys and
every other system library. What survives is what this device needs:

- **The pipe backend**, compiled in unconditionally: raw PCM on stdout. There
  is no ALSA in librespot at all, deliberately — the device already owns the
  speaker, and two processes opening it is the #80 failure (a blocking open
  with no timeout, measured at eighteen minutes of a stranded device).
- **rustls**, not native-tls, so there is no OpenSSL to cross-compile and no
  system certificate store to find on a FireOS 5 image that does not have one
  where anything expects it.

## Why it builds on the firmware's own compiler image

That image already carries NDK 21.4.7075529 at API 22, and it is the toolchain
every shipped firmware has been built with and run on real hardware. librespot
therefore links against the same libc, at the same API level, as the binary it
runs beside. A second toolchain would be a second opinion about what FireOS 5
provides, and that difference shows up as a device that boots and a subprocess
that does not.

## Not yet run

⚠ **This recipe has never been executed.** It is written from the
cross-compilation constraints rather than from a successful build, and the
first run should be expected to need adjustment — the likely places are the
Rust version against librespot's MSRV, and whether `ring` (rustls's crypto
backend) needs its own `CC_armv7_linux_androideabi` set. Neither is a design
problem; both are the ordinary friction of a first cross-compile.

The device side is complete and tested without it: with no binary installed
the firmware reports `spotify_status: {ok: false, reason: "not_installed"}`
on its register message, and the dashboard disables the toggle and says so
rather than offering a switch that saves and plays nothing.
