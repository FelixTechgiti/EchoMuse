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

⚠ **This recipe has never been executed** — the NDK cannot be downloaded from
the environment it was written in (`dl.google.com` is refused by the network
policy), so the first real run is still ahead.

**Two things were checked against upstream's own sources and one was wrong.**
The Rust pin was 1.83.0, and librespot 0.7.1 and 0.8.0 both declare
`rust-version = "1.85"` with `edition = "2024"` — the build would have failed
on its first line. Fixed to 1.90.0.

The remaining likely friction is `ring`, rustls's crypto backend, which may
need its own `CC_armv7_linux_androideabi` in the environment. That is ordinary
first-cross-compile friction rather than a design problem.

### There is no `--sample-rate`, and that changed the design

The device was originally written to pass `--backend pipe --sample-rate 48000`
so librespot would resample for us. **It cannot.** No released librespot has
that option and neither does `dev` — the resampling pull request was never
merged — and librespot REFUSES TO START on an unknown option rather than
ignoring it.

So the pipe backend emits 44,100 frames a second and the conversion happens on
the device, through `internal/resample`, exactly as it does for AirPlay. That
costs 4–8% of one A53 core while Spotify is playing. Found by reading
librespot's `src/main.rs`; it would otherwise have presented as a Spotify
endpoint that never starts.

The device side is complete and tested without it: with no binary installed
the firmware reports `spotify_status: {ok: false, reason: "not_installed"}`
on its register message, and the dashboard disables the toggle and says so
rather than offering a switch that saves and plays nothing.
