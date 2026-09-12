# CLAUDE.md — `device/`

The Go binary that runs on the Echo Dot. Project-wide direction, the
device/controller compatibility rules, the wire protocol and the release
scheme are in the repo-root `CLAUDE.md`; the controller half is in
`controller/CLAUDE.md`.

## Building the device binary

The Echo Dot runs FireOS 5 (API 22). Standard Go cross-compilation won't work — a custom Docker build environment is required.

**One-time setup:**
```bash
# GoTinyAlsa is a git submodule at the repo root — the wilbowes/GoTinyAlsa
# fork, NOT upstream Binozo: it carries the GetAudioStream defer-in-loop
# leak fix (v2.9.2). Don't repoint it upstream until that fix is merged there.
git submodule update --init

# Build the compiler Docker image (from device/)
cd device
docker build -t revoice-compiler compiler/
```

**The compiler base is pinned by DIGEST, and must stay that way.**
`compiler/Dockerfile` carries the Go toolchain (1.24.0) and NDK
(21.4.7075529) that compile the firmware, so it is the layer sitting
directly on top of FireOS 5 — a 2015 platform that cannot be upgraded. It
was `FROM ghcr.io/binozo/echogo:latest`, a third party's floating tag, and
`release.yml` rebuilds the image **from scratch on every tag push**: every
release was free to pick up a different compiler than the last, with no PR
and no CI signal. The first symptom would be a binary the hardware refuses
to run, which is the one failure here not recoverable from the dashboard.

Moving the pin needs **a real device in the loop**. The host tests and
`go vet` cannot speak to it — they run on amd64 with the host toolchain,
and this image is exercised only by `compile.sh` and `release.yml`, so a
green CI run on a pin change proves nothing about it.

**A dependency bump can move the pin from the other end, and only half of
that was guarded.** A module's `go` directive is a floor the toolchain has to
meet, so `golang.org/x/net@v0.55.0` raising `device/go.mod` to `go 1.25.0`
(#122) is a request to move a Go 1.24.0 image — and the pinned-compiler job
went red, correctly. The same bump against **`device/tools/*/go.mod`** went
GREEN (#123, proposing `go 1.25.0` for `sendspin_bench`), because that job
compiles `./cmd/` and nothing else: the tools are documented as building in
this image and have never been built in CI. So the guard existed for one
module and for none of the others, which is worse than having none, because
the green tick reads as coverage.

`Every go.mod agrees with the pinned toolchain` in `ci.yml` now checks all of
them, and **reads the pinned version out of the image** (`go version`) rather
than from the Dockerfile comment beside the digest. That comment is the
obvious place to read it from and is exactly the thing that goes stale when
somebody moves the digest — the same rule as the firmware constants pinned by
test against `em_oww_assets`: do not write the number down a second time.

**Compile:**
```bash
cd device
./compile.sh
# Output: build/server
```

`compile.sh` embeds the git version string via `-ldflags "-X .../client.Version=..."`. Dirty trees get a `YYYYMMDD-HHMM-dev` timestamp instead of the tag.

**Run Go tests (host):**
```bash
cd device
go test ./...
```

Tests only cover pure-Go logic — hardware-dependent code is not testable on the host.

**Run controller tests (host):**
```bash
cd controller
python -m pytest tests/        # needs: pytest numpy scipy pyyaml — not the full requirements.txt
```

Controller tests cover the pure-logic modules only (`em_eq`, `em_limiter`, `em_mbc`, `em_scenes`, `em_oww_models`, `em_oww_warmup`, `version`, `em_hostip`, `em_ingressauth`, and the decision modules — `em_linkauth`, `em_button`, `em_shadow`, `em_turnclock`, `em_runbarrier`, `em_announce`) — keep it that way unless you're prepared to pull openwakeword/aiohttp into the test environment. Both suites (plus `go vet`) run in CI on every push/PR (`.github/workflows/ci.yml`).

**Release:** pushing a `v*` tag triggers `.github/workflows/release.yml`, which builds the binary in the compiler image and attaches it to a GitHub release. **Tag with `git tag -a --cleanup=verbatim`** — the annotation message becomes the release body (`body_path` from `git tag -l --format='%(contents)'`), which is what the dashboard shows next to an available update. Write it for the person deciding whether to push firmware to a device they depend on: what changed, what to expect, anything required of them. GitHub's generated commit list is still appended below it. A lightweight tag yields an empty body and falls back to that list, which is a worse experience, not a broken one.

**`--cleanup=verbatim` is not optional if the notes use Markdown headings.**
`git tag -a` defaults to `--cleanup=strip`, which treats a line beginning with
`#` as a comment and deletes **the whole line** — so `## Volume` does not lose
its markers, it disappears entirely. v2.12.0 shipped that way: the notes were
structurally correct in the file, five headings gone from the published body,
and the only visible sign was a wall of paragraphs. Fixing it afterwards means
`gh api -X PATCH repos/<owner>/<repo>/releases/<id> -F body=@notes.md`
(`gh release edit` has no `--notes-file`), and re-appending GitHub's generated
commit list by hand, since the PATCH replaces the whole body.

## Device audio pipeline

Playback has a second plane: music rides `0x04`/`0x05` into its own buffer and
is mixed against voice at the ALSA write, so a voice turn **ducks** music
rather than pausing it. The rules for that mix — the constant-slew ramp, the
per-sample interpolation, `music_flush` vs `speaker_flush` — are under
"Ducking" in `controller/CLAUDE.md`.

Each mic buffer passes through, in order:

```
raw 9ch S24_3LE → beamformer + fixed mic gain (micGainDb, applied to 24-bit samples) → mono S16_LE → [AEC] → [AGC] → [VAD gate] → /data WebSocket
```

Note the real buffer cadence: GoTinyAlsa's `GetAudioStream` reads the whole ALSA buffer per chunk (PeriodSize 512 × PeriodCount 5), so the mic pipeline runs on **160ms batches of 2560 samples**, not single 32ms periods. Anything assuming 512-sample buffers must handle multiples (this silently disabled AEC for four releases — see `aec.Process`).

The always-on wake stream (`mic_start` without `lock_mic`) is **ungated and AGC-free**: every 32ms period is sent continuously (batched into 80ms frames) so openwakeword scores an uninterrupted stream, and no adaptive gain state can drift with room noise. The VAD gate and AGC apply only to bounded `lock_mic` turn streams (button-triggered), which get a fresh `ResetAGC()` per stream.

- **Beamformer** (`internal/beamformer/`) — selects the perimeter mic with the highest onset energy ratio (fast/slow EWMA) at voice turn start, then locks for the duration. Its `extractChannel` also applies the fixed mic gain (`micGainDb`, default +24dB) against the full 24-bit sample before quantising to S16 — captured speech sits at ~−70dBFS, so gain must happen pre-truncation to recover real resolution. `vadThreshold` stays in pre-gain units (the device scales it by the gain internally). **It is a selector, not a summing beamformer, and that is settled — do not propose delay-and-sum.** A frequency-domain implementation (exact FFT phase shifts, no interpolation artefacts) exists in `device/tools/bf_capture` and was measured as only marginally better than mic selection. The reason is the 72mm aperture, not the code: diffuse-field noise coherence is 0.84–0.99 below 1.5kHz where speech energy lives, so a sum has almost nothing uncorrelated to cancel, and 36mm adjacent spacing puts spatial aliasing at 4.76kHz — a working window of roughly 2–4.7kHz. Superdirective/differential beamforming is the only class that works at this aperture and it trades against white-noise gain (20dB+ amplification of sensor self-noise) on unmatched capsules across four ADCs. Full derivation and the coherence table are in SETUP.md's mic-array section (SETUP.md is the architecture reference; the chronological log is JOURNAL.md, the rooting prerequisites docs/rooting.md). **Far-field reach is therefore not a beamforming problem here** — it is room noise floor, distance and placement; the single-channel levers (`nsAsr`, wake model) are the ones that exist
- **AEC** (`internal/aec/`) — speexdsp echo canceller (vendored C, SpeexDSP-1.2.1), whole mic path including the wake stream; far-end reference tapped at the speaker ALSA write (every period incl. silence), delayed by `aecDelayMs` — **keep 0**: the mic side's 160ms batch reads absorb the speaker's output latency, and higher values make the echo non-causal (zero cancellation). The mic ALSA ring is only 160ms deep, so >160ms capture stalls silently lose whole batches (~every 20–30s in steady state, load-correlated); an occupancy governor trims the resulting reference backlog **without resetting the filter** — the trim restores the alignment the filter converged against, and the reset that used to live there thrashed convergence to ≤5dB (the v2.7.8 fix). `[aec] att=`/`far:` telemetry logs ~1/s during playback; `[mic] clock/stall` lines track capture loss. `far:` carries `rms`, `mean` and `peak` — **rms alone cannot tell audio from a constant offset**, since both read high, and that ambiguity cost an evening on #117 where the device was writing rms≈4000 to a codec while every speaker stayed silent. `mean≈±rms` with a small peak-to-peak is a DC offset; `mean≈0` with peak well above rms is real audio and the fault is downstream. Note this tap sits after the (L+R)/2 downmix and 3:1 decimation, so DC survives intact but `peak` is mildly smoothed — read it as a floor. It reports only while `aecEnabled`, so a diagnosis that needs it must not have AEC turned off. Default off (`aecEnabled`); ~14dB per response, held across turns

  **Implemented (#385), detected rather than assumed.** `beamformer.EchoRef`
  pulls ch8 out of the same raw period the mic channels come from, and
  `aec.ProcessWithRef` cancels against it with no ring, no `aecDelayMs` and no
  occupancy governor — all three are bypassed on that path, and `WriteFar`
  returns early so nothing fills a ring nothing drains. Measured 41.2dB in the
  unit test with the real 33-sample offset and polarity inversion applied.

  **The detector is deliberately narrow, and the narrowness is the point.**
  A reference channel is BIT-EXACT ZERO when nothing plays *and* carries audio
  when something does; only both together promote it (`data.go`,
  `noteEchoRef`). "Has energy" alone would promote a genuine microphone on a
  board that wires ch8 differently, and cancelling the near end against
  another mic is far worse than not cancelling at all. Confirmation is
  one-way — a device that flipped sources every time the room went quiet would
  throw away a converged filter for nothing. `EM_AEC_HW_REF=off|on` overrides
  it; an env var and not a config key, because this is a property of the board
  rather than a user preference, and it exists so the two paths can be A/B'd on
  one device without a controller round trip.

  **The reference is scaled by the device's own volume, and this is what made
  it work.** The tap is pre-volume, so left alone every volume change is a step
  in the echo path gain that the filter can only find by re-converging. First
  hardware run, 2026-08-29: cancellation collapsed to **−1.7dB** immediately
  after a change and took 3–4s to recover, over and over, while `ref` sat at
  4000–8000 through a `mic` swing of 1263→16766. We are not obliged to guess
  the scalar — the device SETS that volume — so `SetPlaybackLevel` feeds it
  from the existing volume-change callback and the reference is multiplied by
  `10^((level−127)/40)`, the control's own 0.5dB-per-step law. Worth **32.7dB**
  of residual in the frames after a change, in the test that reproduces it.
  Software-tap frames are deliberately NOT scaled: that ring holds audio
  written before the change, so the correction would land on the wrong
  samples, and leaving it alone preserves the baseline being compared against.

  **Unity gain on the extraction, non-negotiably.** Mic channels get
  `micGainDb` (+24dB default) applied pre-truncation because speech sits at
  ~−70dBFS; the reference is playback at −7.3dBFS, and the same gain on it is
  17dB of hard clipping — which does not merely cancel badly, it teaches the
  filter a distorted echo path. Pinned by test.

  **The hardware has been handing us a sample-aligned reference all along, on
  Ch7/Ch8** (measured 2026-08-29 — see SETUP.md's Mic Array section). Those
  two channels are not unconnected mics: they are a stereo loopback of the
  device's own playback, Ch7 left and Ch8 right, present unconditionally with
  no mixer change. It is the *same signal* as the software tap above — the
  same bytes we write to ALSA — so the win is not fidelity, it is that the
  reference arrives **in the same TDM frame as the mic samples**. The offset is
  fixed by hardware at +33 samples (2.06ms, polarity-inverted) instead of being
  inferred, which is what `aecDelayMs`, the occupancy governor and the
  capture-stall trim all exist to approximate. Anyone rebuilding this path
  should start there rather than tuning the delay further.

  Two bounds. It is **pre-volume** (unchanged across a commanded 33.5dB cut),
  so it does not track loudness and the adaptive filter must find that gain
  itself — no worse than the current tap, which is also pre-volume. And it does
  not represent the acoustic echo once the DAC clips: at index 170 the mic's
  loudest component is the *seventh* harmonic while the reference stays a clean
  fundamental. Unreachable in shipping firmware, because `DEVICE_VOLUME_MAX`
  caps the control at 127 for the distortion reason under Volume — but it is a
  hard reason never to raise that ceiling.
- **Barge-in** (controller-side `_barge_watcher`) — wake word spoken during TTS cancels playback (device does a stateful `speaker_flush`: drains buffer + discards until stream EOS, since the rest of the stream is typically still in TCP buffers; controller-side, both `stream_speaker` and the post-playback drain sleep race `cancel_event`). `bargeInThreshold` is used as-is and sits *below* `owwThreshold` by design (0.05–0.10): echo at the mic is ~25dB louder than the person, so speech-over-TTS scores are depressed (~0.3–0.5 observed), while converged self-echo scores 0.002–0.003. **A barge must abort HA's run before starting the interrupting turn** — see the voice backend section in `controller/CLAUDE.md`
- **AGC** (`internal/processor/`) — lock_mic turns only; release is frozen during silence (RMS speech flag), preventing noise floor amplification. (Device-side RNNoise NS was removed 2026-07-12 — noise suppression is controller-side now: `em_ns.py`/DTLN on the ASR-bound stream, per-device `nsAsr` flag)
- **VAD** (lock_mic turns only) runs on pre-NS/AGC audio; opens gate after `VAD_SPEECH_MS` of speech, closes after `VAD_SILENCE_MS` of silence, then sends an end-of-speech sentinel

## Key Go packages

| Package | Role |
|---------|------|
| `cmd/server.go` | Entry point: wires hardware, callbacks, and clients together |
| `internal/client/control.go` | WebSocket client to controller `/control` — registration, message dispatch |
| `internal/client/data.go` | WebSocket client to controller `/data` — mic streaming, speaker playback |
| `internal/server/` | Local state machine: mute, volume, LED mode priority |
| `internal/config/config.go` | Global runtime config; env var defaults, overridden by controller push |
| `internal/bindings/` | Hardware drivers: mic PCM, speaker PCM, LED I2C, button evdev |
| `internal/wakeword/` | openWakeWord streaming feature pipeline (mel ring → 76-frame windows → embedding ring → classifier). Pure Go: inference sits behind the `Inferer` interface so the buffering is host-testable with no ONNX/cgo. Validated tensor-for-tensor against Python via a golden fixture (`testdata/`, regenerate with `gen_fixture.py`) |
| `internal/wakeword/ort/` | The `Inferer` implementation: ONNX Runtime via cgo. The library is **dlopen'd at runtime, never linked** (only the MIT C header is vendored) so a device without it boots normally and falls back to controller-side wake word — verified by the ARM binary needing only libdl/liblog/libc with zero undefined `Ort*` symbols. `DefaultOptions` (1 thread, XNNPACK, `allow_spinning=0`) is the measured optimum: 37.7% of one core against 243% for ORT's defaults. Don't "fix" the thread count — more threads lowers latency and *raises* CPU, the wrong trade for duty-cycled work |
| `internal/wakeword/shadow/` | On-device scoring that reports but never acts (see "On-device wake word"). `Push` must never block: inference runs on its own goroutine and drops frames when behind |
| `internal/wakeword/fixture/` | Shared golden-fixture parser, tolerance policy and `Verify`. Used by both the host test and `tools/oww_probe`, deliberately — the probe's answer is the trusted one because it runs on hardware, so it must be exactly as strict as the test by construction. Tolerances are relative to the **tensor's** scale, not per element: per-element relative error is meaningless for tensors straddling zero |
| `internal/bindings/als/` | Ambient light (ams **TSL2540** on i2c). Android does not expose it AT ALL — `dumpsys sensorservice` reports an empty list, nothing under `/sys/class/sensors`, no input device; it is visible only on the raw i2c bus, the same shape as the mute LED being on a different GPIO than the vendor HAL believed. Resolved **by name, not address** (`0-0039` is an enumeration accident). **The bus listing is not a hardware inventory**: both ALS names are registered by Amazon's board file, so a `tsl2540` at 0x39 and a `tsl2584tsv` at 0x29 appear on every unit whatever is soldered on (`modalias` is static kernel data). Which one answers differs by batch — ours have the 2540 and nothing at 0x29 (`taos_probe() err = -6`, ENXIO), the `G090LF096` batch has the 2584 instead, reachable only through IIO at `/sys/bus/iio/devices/iio:device0` (#90). A second-sourced part, not a driver fault, so the answer is to read the IIO sensor too, never to loosen the match to a `tsl` prefix. The **boot log is the real inventory** — both drivers probe on every unit and log what replied — but `dmesg` rolls, so it needs reading soon after a reboot. Never `unbind` the driver to experiment: it succeeds, leaves the `als_*` attributes in place, and the next read hangs the device until a power cycle. **Polled every 5s, not every 1s, and the reason is the kernel log rather than the syscalls.** The driver prints a line on every read under its darkness threshold (`tsl2540_get_lux: darkness (0 <= 10)`), so a 1Hz poll is ~86,000 kernel lines a day — and it only fires in the dark, so it runs all night, which is exactly when a device sits idle and a crash most needs explaining. Measured on EFF 2026-09-04: the whole log ring was that one line and `messages.last` had reached 609KB. The cost is not disk — MediaTek's ram_console is the ONLY crash channel this kernel has and it is a fixed-size ring we do not control, so anything filling it evicts the evidence. `MinInterval` already refuses to report more often than every 2s, so 1Hz was finer than the reporting floor it feeds; if #296 ever wants faster, make the poll adaptive rather than paying a permanent flood. `Lux()` returns **nil, never 0** — a covered sensor reads a genuine 0. `Watch` reports a step change immediately (25% relative, 10-lux floor, measured noise ±1.5%); the steady value rides the ~30s stats tick. `Report()` says **why** there is no sensor (`ok`/`no_chip`/`no_attribute`/`unknown`, plus every i2c name it saw) and rides the register message as `ambient_light_status` — absence used to be logged only to the device's own stdout, which support bundles do not collect, so two users could not be told apart without a shell session (#90). The whole bus is enumerated **before** matching: returning at the match truncated the list on working devices, which is exactly the side you compare against |
| `internal/bindings/jack/` | Headphone jack detect (`/sys/class/switch/h2w`, mediatek accdet). Polled, not evented — the ACCDET input node reports no keys on this hardware. `Watch` dispatches the state it STARTS in as well as every change: accdet is edge-triggered and a boot has no edge, so a device booted with a cable in got no correction at all. The callback (`PcmSpeaker.SetJackRouting`) owns both positions — the amp switch, and the `HP Driver Gain Volume` that accdet drops to the floor of its range on insert and nothing used to raise. Output *destination* is still physical, done by the jack's own switch contacts, so no mux layer should be driven — but level is ours |
| `internal/wifi/` | Safe WiFi network change with auto-rollback (wifi_change/wifi_commit/wifi_scan control messages; pending-marker recovery at startup). Reload path is `svc wifi disable/enable` ONLY — see package comment for the hardware-proven constraints |
| `internal/bluetooth/` | BLE proxy — raw HCI passive scan over `/dev/stpbt` (single-owner, so Android's Bluedroid is durably `pm disable`d first), parsed into adverts and forwarded to the controller. `emit.go` decides which of them are worth sending; see "The BLE proxy" below, and read it before changing the scan cadence or the filtering |
| `internal/outchain/` | The output chain — EQ, bass guard, limiter — applied POST-MIX at the ALSA write (#243). A port of the controller's `em_eq`/`em_mbc`/`em_limiter`, and correctness means **agreement with them**: `internal/outchain/fixture` replays golden captures and all fifteen cases match bit for bit. No build tag, deliberately — it is arithmetic and belongs in the host suite. `Chain` adds the one thing the reference does not have: parameter changes that do not click, by crossfading between two complete chains rather than interpolating coefficients (which can pass through unstable states). Keep the STAGES faithful and put every divergence in `Chain`, or the fixture stops being evidence. The controller stands down for a device announcing `output_chain` (`controller/em_outchain.py`) — shaping at both ends is two limiters in series |
| `internal/musicplane/` | Who is filling the music plane. It had exactly one producer by construction until the device started speaking protocols of its own; Sendspin, Spotify Connect and AirPlay are each a SECOND producer of the same plane, and summing two of them is two songs rather than a mix. Home Assistant wins — a request routed through HA is the direct request made to this device — and among local sources the newest claim wins. The rule that makes it usable is that a displaced source is told to LEAVE, not merely stopped writing: a server still streaming to a client that went quiet keeps filling a buffer nobody hears and the group's view of this device stays wrong. Nothing rejoins when HA's music ends. **A pipe-fed source holds the plane only while audio FLOWS** (`IdleClaim`): librespot and shairport-sync are daemons that run continuously so the device stays in the Spotify and AirPlay pickers, and a phone that disconnects just stops writing to the pipe — so a plane released on process exit is released at the next reboot. Nothing depended on that until Home Assistant did: the arbiter was only ever asked "may I write", which a stale owner answers correctly, and the question it cannot answer is "is this Echo making a sound" — measured 2026-09-10, the Audio Source entity read `airplay` for minutes after the session was disconnected. The claim expires after `DefaultIdle` (2s, twenty missed reads) and is retaken by the next chunk; the controller's own `audioHoldoffMs` is what keeps a gap between tracks from reaching an amplifier, so this value does not have to. It also carries the ONE observer of those handovers (`OnChange`), which is what lets the controller tell Home Assistant this Echo is audible: Spotify, AirPlay and Sendspin play from programs on the device and no frame of their audio passes through the controller, so nothing else can see them. One observer, not a list — there is exactly one consumer and a slice would invite a second nobody sequenced against the first. It fires OUTSIDE the lock and BEFORE the eviction callback: outside because holding the lock across anything that can stall blocks every other claim, and before because eviction does protocol I/O on a socket that may itself be stalled, and the amplifier should not wait for a goodbye |
| `internal/sendspin/` | The Sendspin protocol (#89) — Music Assistant straight to the device, no controller hop. Built and host-tested: the two-dimensional Kalman **time filter** (this is a SCHEDULED protocol; every chunk carries the server timestamp its first sample must leave the speaker at, and drift matters because an unmodelled 50ppm crystal is 30ms of skew over a track against a ±1ms spec floor), the **binary framing** (type byte, fragmentation at 65518 to leave room for Noise's tag, big-endian µs timestamps), the **message shapes** read off `aiosendspin` rather than off prose, and the **connection state machine**, driven in tests by a scripted server over an in-memory transport. Three things not to get wrong: the SERVER is the Noise initiator; `client/hello` answers `server/hello` and nothing earlier, because it is the one-shot carrying the format list and a format missing from it can never be requested (the server falls back **silently**); the advertised list is ordered DECODABLE-FIRST, because the server picks the client's highest priority and can encode all three — so FLAC is advertised, and advertised *behind* PCM until its decoder exists, since advertising it first would guarantee a stream the device turns into noise with no error anywhere; and `available: true` may not be reported before the clock is synced, so it is OMITTED rather than sent as false — absent means "not yet", false means "this speaker will not play". Crypto is an interface, not an assumption baked through the transport: the spec calls Noise mandatory and Music Assistant's own server shipped implementing none, and both have to work. `ClientInit`/`ServerInit` are the only two shapes NOT read off the reference and are unverified until the handshake has completed against a live server once Discovery, the WebSocket transport and the playback-position reader (`delay` = `appl_ptr - hw_ptr` out of the ALSA status file) are in too: our own bookkeeping is perfect by construction and therefore useless as an error signal — it cannot see the hardware consuming 47973 frames a second rather than 48000, which is the whole of the drift a group has to correct The lifecycle is `sendspinEnabled` on the config push, applied live; stopping SAYS GOODBYE rather than dropping the socket, or turning the setting off leaves a speaker listed in the group and silent until the server's own timeout FLAC decoding is `github.com/mewkiz/flac`, pure Go — which is why FLAC and not Opus: the codec choice is constrained by what cross-compiles for armv7a/API 22 without a toolchain problem, and the spec requiring every SERVER to support all three is what makes advertising a subset legal. A FRESH decoder per chunk, deliberately: each chunk is independently timestamped and the scheduler drops chunks that are far out of position, so a decoder carried across them would let one dropped chunk corrupt everything after it |
| `internal/spotify/` | Spotify Connect on the device — librespot as a subprocess, the Echo appearing in the Spotify app as a speaker with no Home Assistant in the audio path. A THIRD producer of the same music plane, on the same arbiter. librespot is Rust and this is Go, and that boundary is a choice: `go-librespot` needs Go 1.25 (above the pinned compiler) and cgo against libogg, libvorbis, flac and mpg123, where librespot with rustls needs no system libraries at all — plus a subprocess that crashes takes nothing with it on a device sharing 512MB with Android. **There is no `--sample-rate`, and the design assumed there was.** No released librespot has that option and neither does `dev` — the resampling pull request was never merged — and librespot REFUSES TO START on an unknown option rather than ignoring it, so the pipe backend emits 44.1kHz and `internal/resample` converts it, exactly as for AirPlay. **The pipe backend being famously "too fast" IS the feature here** though: `PumpMusic` blocks when the plane is full, the pipe backpressures, and librespot is paced to playback for free. The capability says the FIRMWARE can run it; `spotify_status` on the register message says whether the binary is actually installed — the same "could it" vs "is it" split as `aec_hw_ref` against `aecRef`, and it has to be a runtime answer because the controller pushes the binary long after registration. **Its build must enable `with-libmdns` explicitly.** `--no-default-features` is reached for to drop the rodio backend and takes librespot's discovery backend with it — a binary that runs perfectly and can never appear in the app, with nothing logged at either end because from librespot's side nothing is wrong (shipped, found 2026-09-10). `build.sh` now diffs upstream's own `default = [...]` against what this build enables and what it deliberately drops, and refuses to build on anything unaccounted for; a librespot release that adds a default feature fails rather than losing it silently. **Pinned at v0.8.0 since 2026-09-12, on a measurement rather than hygiene.** v0.7.1 authenticated, took a 251-track context and refused every track in it with `<name> is not available in any supported format` — with `Country: "DE"`, a resolver that answers and an 8.6ms ping to Spotify's edge, so neither the account nor the network. 0.8.0 carries `[metadata] Fix incorrect parsing of audio format` and an `AudioFileFormat` rewrite, plus three that name this platform: `TryAnotherAP` on Android, `Invalid Credentials` with a Keymaster token on Android, and a CDN URL fallback. **Two traps in reading that failure.** The error librespot prints is `unable to load track …: ()`, and the `()` is its internal error type rather than a reason — the reason is always the line BEFORE, so quote both. And `busybox nslookup` failing on this device proves nothing: it reads `/etc/resolv.conf`, which Android does not have, while librespot resolves through bionic and the property service (`net.dns1`). Two resolvers, and only one of them is the one under test. Build recipe in `device/librespot/` |
| `internal/pcm/` | The sample conversions and the period discipline more than one audio source needs. Two rules, both subtle, both silent when broken. The stereo downmix: adding two int16 channels near full scale wraps to full-scale NEGATIVE, a crack rather than distortion. And `PeriodWriter`: **the music plane is not a byte stream** — the ALSA loop takes ONE item off the channel per iteration and hands it to the hardware as a period, and the mixer returns the music buffer directly when nothing else plays, so a short buffer becomes a short period. It glitches, and the per-stream period accounting counts it as whole, so the underrun margin the instrumentation exists to measure is quietly wrong. `PumpMusic([]byte) error` reads like a stream, which is exactly why the rule is written down once instead of remembered three times |
| `internal/airplay/` | An AirPlay receiver on the device — shairport-sync as a subprocess, PCM on stdout. A fourth producer of the same music plane. **CLASSIC AirPlay, and the gap to AirPlay 2 is cost rather than a wall.** This row named three blockers until 2026-09-11 and all three were wrong (#79): nqptp does not use hardware timestamping and says so in its own README; `configure.ac` ties AirPlay 2 to no mDNS backend, so Avahi is not required — of the four backends only `mdns_avahi.c` implements the second service, and `mdns_tinysvcmdns.c` declares `ap2name` and `secondary_txt_records` `__attribute__((unused))` and sets no `mdns_update`, which is ~100 lines rather than a D-Bus port; and the stated floor is a Pi 2 / Pi Zero 2 W (quad A53, 1GHz, 512MB), which a 1.3GHz quad-A53 meets rather than misses. What is real: **`shm_open` is absent from bionic** and is the nqptp↔shairport clock interface (three call sites, and no process-shared mutex in it, so a file-backed `mmap` substitutes faithfully), ffmpeg has to be cross-built, and 512MB is shared with Android. The mDNS half is the same work as #77. **The sample rate was wrong too** — AirPlay 2's Buffered Audio is AAC-LC at 44.1kHz, not 48kHz, so `internal/resample` stays in the path either way. This code does not care which it gets: both put PCM on stdout. Build recipe in `device/shairport/` |
| `internal/netfilter/` | Opens the device's own ports in FireOS's default-deny firewall, and closes them when the endpoint is turned off. Announcing a service and being reachable are two different things and nothing had ever measured the second — see "Advertised is not reachable" below before touching it. Only `-I`/`-D` on fully specified INPUT rules, never a policy change and never a flush: this is a firewall on a device whose only management path is the network |
| `internal/resample/` | 44.1kHz → 48kHz, a polyphase FIR at exactly 160/147. It exists because classic AirPlay is 44.1kHz by definition and this speaker is 48kHz — Sendspin asks the server for 48kHz and Spotify hands the job to librespot, so AirPlay is the one source that cannot avoid it. **`taps` is a measurement, not a round number**: 16 gives a 15kHz transition band, which sounds like plenty of filter and measures 5.3dB down at 20kHz; 64 gives 3.8kHz, flat to ~19kHz. Cost is BENCHMARKED rather than claimed — ~4ms per second of audio on x86, so 4-8% of one A53 core, which is affordable and not free |
| `pkg/led/`, `pkg/mic/`, `pkg/speaker/`, `pkg/buttons/` | Hardware abstractions (interfaces) |

## On-device wake word (shadow mode)

The Echo can run the wake model itself. `owwOnDevice` = `off` (default),
`shadow` or `on`; an unknown value normalises to `off` at BOTH ends rather than
being guessed at — the two plausible guesses are "score silently" and "start
triggering", and one of those is a live behaviour change on a device that
cannot honour it. Neither end may assume the other is the careful one.

**`on` is gated on the `oww_trigger` capability, which is separate from
`oww_shadow` on purpose.** Shadow shipped first, so there is firmware in the
field that scores and reports without being able to act on it; offering those
`on` produces a device that scores perfectly and never answers.
`em_shadow.effective_mode` degrades `on` to `shadow` when the capability is
absent — never to `on`, which would leave the controller waiting for wakes the
firmware has no code to send while no longer acting on its own. That is a wrong
answer rather than the old behaviour, which is the line the whole capability
rule is drawn along.

### `on` — the device decides, the controller keeps watching

The device sends `oww_wake` (score, the threshold it actually cleared, and how
long AGO — never a timestamp) instead of `oww_shadow_cross`. It lands in
`Device.pending_wake` and the wake listener acts on it on its next mic frame
(~80ms), because that is where turn setup lives: capture routing, beam lock and
arbitration have to happen together, and driving them from the control-plane
handler would be a second copy of the most delicate sequence in the controller.
`em_shadow.decide_wake_source` is the decision, pure and tested, for the reason
`em_button.decide` and `em_linkauth.decide` are.

- **The controller keeps scoring, and its detections stop triggering.** Its
  score still records whether it agreed (`turns.ctrl_wake_score` /
  `ctrl_wake_delta_ms`, schema v17) — the comparison that justified shipping
  this, with the roles inverted, and the only place a *controller* miss can be
  seen at all. Without it, turning a device `on` would silently end the
  measurement: every turn would show a device score with nothing to compare
  against, which reads as perfect agreement rather than as no data.
- **It is also what leaves barge-in alone.** Barge is scored controller-side
  over the turn's own audio (`_barge_watcher`) and is untouched by this.
- **`last_wake_mono` is the CROSSING instant, not arrival.** Using arrival
  would fold the network hop into every comparison and every arbitration
  decision, which this fleet's measured 1.1–2.6s RTT excursions make certain
  to matter.
- **Mute is checked on the device** (`onWakeCrossing`), not only controller-
  side. The existing `mic_start` refusal plus the hardware ADC mute already
  make a muted wake harmless, but "harmless" still means the ring lights up and
  HA runs a pipeline because a muted device thought it heard something. The
  crossing is still *reported* — it is real data about the detector.
- **A pending wake expires** (`MAX_PENDING_WAKE_S`, 4s, measured from the
  crossing). A wake that stale means the person has finished speaking, so
  acting on it answers into silence; expiry logs the age, which is the
  instrument for whether the trigger needs more slack.
- The trigger label is `wakeword-dev(score)`, which still matches every
  existing reader's `wakeword` prefix — including `_persist_turn`'s shadow
  block.

**Arbitration is NOT yet corrected for this.** `_wake_arbiter.claim` still
compares arrival order, so on a multi-device fleet a device can lose a 700ms
window because its claim was late and the wrong room answers. The fix is to
compare RTT-corrected times, never revoke a granted claim (that cuts a turn
already speaking), and hold the window longer than it is measured. Single-device
use is unaffected — solo fleets skip the window entirely.

**Shadow mode scores and reports; it never acts.** It exists to answer whether
on-device detection is good enough to trust, by comparing both detectors on the
same audio. The tap sits where the ungated wake stream's frames are written to
the wire, so the device scores byte-identical 80ms frames on identical
boundaries — a score difference can then only be the engine, not the framing.

Three things are load-bearing:

- **Inference must never run on the mic goroutine.** It costs ~31ms per 80ms
  frame, the mic loop reads 160ms ALSA batches, and the ring is only 160ms
  deep — two frames inline would spend 62ms of that budget and risk the capture
  stalls that lose whole batches. `shadow.Scorer.Push` hands off to a buffered
  channel and returns; the scorer goroutine **drops frames and counts them**
  when it falls behind. A shadow run that drops frames is informative; one that
  stutters the microphone is not.
- **Nothing is sent per frame.** Threshold crossings go immediately (they are
  rare — a refractory period collapses each utterance to one — and their whole
  value is the timing). Everything else is a window summary riding the existing
  ~30s stats tick, so the DB cost is one extra upsert per 30s per device.

  The window summary carries `maxInferMs` and `maxGapMs` (schema v16) because
  `dev_drops` alone had stopped being able to answer its own question — turn
  bursts, core hotplug and controller redeploys were each falsified by
  measurement. A drop means the 8-frame (640ms) queue overflowed, which has two
  causes needing opposite fixes: the slowest single inference is the CONSUMER
  stalling, the longest gap between frames arriving is the PRODUCER bursting.
  **Maxima, not averages** — a stall IS the tail, and a 700ms event averaged
  over 375 normal frames disappears. They are `atomic.Int64`, not mutex state,
  because `Push` runs on the mic goroutine; and the gap is measured in
  `enqueue` so one site covers `Push` and `PushBytes` alike, the same reason
  drops are counted in exactly one place. Note the first frame must record NO
  gap: a zero-valued `lastPush` would report a gap of however long the process
  had been running and point at a producer stall that never happened.
- **The device never sends a timestamp.** An Echo's wall clock is bogus before
  NTP, so it reports how long *ago* a crossing happened and the controller
  converts against its own monotonic clock — same reasoning as the RTT
  instrumentation.

**Thresholds must match or the comparison is meaningless.** The controller drops
its wake bar to `bargeInThreshold` while the speaker is streaming (echo at the
mic is ~25dB louder than the person, so speech-over-TTS scores are depressed), so
the device mirrors that: `shadow.Scorer.SetBargeThreshold` uses the lower bar
while `PcmSpeaker.IsStreaming()` is true, and never *raises* the bar if
misconfigured above the normal one. The device reports the threshold in force
with each window summary; it lands on the turn as `dev_threshold` (schema v15).
`turns.wake_threshold` now records the **effective** threshold the wake actually
cleared, not the nominal one — recording 0.5 for a wake that fired at 0.055 made
rows self-contradictory (present in data since at least 2026-07-25) and made
every barge-in look like an on-device miss. The activity rollup therefore reports
three buckets, not two: agreed, missed, and **not_comparable** (controller used a
lower bar, or the device's threshold is unknown).

Correlation (`em_shadow.ShadowTracker`, schema v13) happens at turn-persist
time, not at detection: the crossing report can land after the wake it belongs
to, and by turn end it has had seconds to arrive. The nearest crossing within
`MATCH_WINDOW_S` (2.0s) wins and is **consumed**, so two turns in quick
succession cannot both be credited to one crossing. The window is loose on
purpose — both detectors see the same frames but not in the same detector
*state*, since the controller drops wake frames while a turn or TTS is in
flight, and a false "miss" argues against a feature that is actually working.
`turns.dev_shadow` records whether the device was scoring at all, which is what
separates "the device missed this" from "the device was not looking";
`wake_counters.dev_*` carries the hourly view, where crossings with no matching
turn are the false-accept side that per-turn rows structurally cannot show.

Requirements and cost: ONNX Runtime plus the three models must be installed at
`shadow.DefaultDir` (`/data/local/share/revoice/oww`, override `EM_OWW_DIR`)
— they are **not** in the firmware, since 12.3MB would double the OTA payload
and both A/B slots. Absence is an ordinary condition, logged once, and the
device carries on with controller-side wake word. `device/tools/oww_probe`
verifies a device reproduces Python and reports the real CPU cost. It costs
~38% of one core permanently on top of the ~18-20% mic-pipeline baseline, so
**enable it on one device at a time**.

**The scorer pointer must be re-read PER FRAME, never cached for a stream.**
A config push replaces the scorer and **closes** the old one, so a mic stream
holding the pointer it captured at `StartMic` is feeding a dead object. This
cost two bugs in succession on 2026-08-16, and the second is the instructive
one:

- `Close()` used to close the channel `enqueue` sends on, so the next 80ms
  frame panicked the process. It restarted in ~6s, opened a fresh mic stream,
  picked up the new scorer, and worked — the crash was **accidentally
  self-healing**.
- Making `enqueue` drop silently removed the crash *and* the recovery. The new
  scorer then received nothing and detection stayed dead until the next
  `StartMic`, which only follows a voice turn, which cannot happen because the
  wake word is dead.

So `Close()` signals a dedicated `quit` channel and never closes `ch`, AND the
push site re-reads `d.ShadowScorer()` per  frame. A mutex read per 80ms is
nothing beside the inference it feeds. Both halves are needed; either alone
leaves a device that goes deaf or panics. The comment that justified caching
("a stream that began before the change keeps using the scorer it started
with") described exactly what made it fatal.

**A missing classifier used to silently deafen a device under
`owwOnDevice=on`.** The device cannot score without the model, and the
controller has stood down and no longer triggers on its behalf — so nothing
fired, nothing warned, and the dashboard reported the device as healthy. That
is a degradation to *no* behaviour, which the capability rule above exists to
forbid. Selecting a wake word a device was never provisioned with is enough to
produce it, and that is an ordinary dashboard action.

**Bouncing the HA connection flaps EVERY entity for that device.**
`update_oww_model` drops and remakes the connection so HA re-reads the wake
word, and that is the only lever the protocol offers — HA calls
`_update_satellite_config()` from `async_added_to_hass` and nowhere else. The
cost is that the voice assistant, media player, event and sensor entities all
go unavailable and back in the same instant.

For the **event** entity that is user-visible and looks like a fault: HA's
`EsphomeEvent._on_device_update` deliberately writes state on reconnect
("Event entities should go available directly when the device comes online"),
restoring the last event's timestamp. `_trigger_event` is NOT called, so HA
does not think a new event happened — but a **state-triggered** automation sees
`unavailable` → timestamp and fires. Reported 2026-08-17 as "changing the wake
word triggers a long button press"; the controller had sent no event at all.
The user-side fix is `not_from: [unavailable, unknown]`, documented in
docs/configuration.md. Worth remembering before adding any new bounce.

**The fix is install-before-switch, not a fallback.** A device is never told
about a new `owwModel` until the classifier is on it
(`em_api._hold_back_oww_model` swaps the key back to the current model, and
`_install_then_switch` pushes the real config once the file has landed). The
device keeps listening for its CURRENT wake word, on-device, throughout; if the
install fails it simply stays there and says so loudly.

The first attempt stood the device down to controller-side scoring while the
model installed. That works and was rejected: it silently overrides a setting
the user chose, and the dashboard goes on reporting `owwOnDevice: on` — the
same "reports healthy while something else is true" shape the capability rule
exists to forbid. Note there is no privacy difference between the two modes
(the device streams the wake audio either way, and the controller scores it in
`on` mode too — that is what `turns.ctrl_wake_score` records), but a silent
override is a trust problem regardless.

Only devices that actually score locally are held back — with
`owwOnDevice=off` the file is irrelevant, so the change stays instant. Both the
old and the incoming mode are consulted, or a save that enables on-device
scoring while changing the wake word slips through on the old mode.

`em_shadow.effective_mode` also takes `model_ready` alongside
`trigger_capable`. Its writer is **`em_api.reconcile_oww_assets`**, run as a
background task from the connect handler: install-before-switch covers every
path where the device is connected, and this covers the one where it was not.
A device whose wake word changed while it was offline is told to use the new
model by the ordinary connect-time config push, with nothing checking it has
the classifier — so the check happens straight after, and the mode drops to
`off` if it does not. A known-missing model degrades to **`off`, not `shadow`** —
shadow cannot score either, so degrading to it would be the wrong answer
dressed as a fallback; only `off` puts the controller back in charge of
triggering, which is the one arrangement that still answers the user. It
defaults **True**: absence of evidence is not evidence of absence, and standing
every device down because the controller has not looked would be worse than the
bug.

The hold-back is invisible at the call site — the config push looks entirely
ordinary and the whole guard is that one key was swapped out first — so tests
pin the ordering, the capability gate, and that a failed install returns rather
than falling through into the switch. The install runs as a background task:
blocking the config save on a multi-megabyte shell-plane push would time out
the request without making anything safer, and nothing is degraded while it
runs.

**Every device carries all four stock classifiers**, not just the one selected
when it was provisioned (`em_oww_assets.STOCK_MODELS`, 3.04MB for the set).
That removes the whole class of "you selected a wake word this device has never
had" for stock models — which under `owwOnDevice=on` is a device with no wake
word at all. The set is what `dashboard.jsx`'s `WW_MODELS` offers, pinned by
test in both directions: a wake word offered but not installed is #191, and one
installed but not offered is dead weight. openwakeword's `timer` and `weather`
are NOT included — they are intent models, not wake words, and are offered
nowhere.

Both transports share `desired_assets` (`include_stock` defaults True), so a
device provisioned today and one synced today carry the same files.

**`CLASSIFIER_SLOTS` budgets LEFTOVER CUSTOM classifiers, not every classifier
on the device.** It used to be a budget for all of them, which was right while
only the selected model was ever desired; the four stock models fill it exactly,
so under the old rule installing them would have silently deleted every custom
model on the device — including ones a user trained and cannot re-download.
Stock models are required by definition and never evictable.

**Reconcile-on-connect** (`em_api.reconcile_oww_assets`, reached through
`reconcile_on_connect` — see controller/CLAUDE.md for the other two payloads
that ride with it) closes the offline case, and three rules keep it from doing
harm:

- **Failure to LOOK is not evidence of absence.** Any error reading the
  device's inventory leaves `model_ready` alone — the shell plane is very
  likely not up yet moments after connect, and standing a device down because
  the controller could not ask would be worse than the bug. Only a successful
  listing that lacks the model counts.
- **Degrade first, then repair** — the mode drops to `off` the moment the
  model is known missing, so the controller triggers throughout the install
  rather than only after it. Deliberately the opposite ordering to
  `_install_then_switch`, where the device is on a wake word it can still hear
  and must not be disturbed; here it is already deaf.
- **Quiet when there is nothing to do.** Devices reconnect often on this
  fleet, so the ordinary path is one shell round trip and no log line.

Presence is judged by **md5, not filename**: a re-trained custom model keeps
its name, and counting that as installed leaves the device scoring against a
classifier that silently disagrees with the controller.

**"Can it score today" and "is it complete" are two questions, and only the
first was ever asked.** `missing_selected_classifier` decides whether to stand
the mode down, and correctly looks only at the selected model —
a missing spare is not a deaf device. But nothing looked at the spares at all,
so Office ran from 17 August to 2026-09-02 without `alexa`, `hey_mycroft` or
`hey_rhasspy`, scoring its own wake word perfectly and reported healthy by
every panel. The status was right; the question was too narrow, and the cost
lands the day someone selects one of the missing ones — which is the deaf
device the whole path exists to prevent. `em_oww_assets.missing_assets`
answers the wider one, and the reconcile acts on it **without degrading
anything**: repair quietly, log at info, no warn event, because the user has
lost nothing today.

The rest of #191 — custom slots and a per-device Repair action — is designed
on the issue and not yet built.

### Asset distribution (`em_oww_assets.py`)

Installing those files is automatic. `em_oww_assets` plans (pure, unit-tested);
`em_api` carries it out. Two transports, one plan: the **provisioning wizard**
pushes over USB/ADB (a fresh device is not connected to the controller yet, and
USB suits 15MB far better than a base64 heredoc), and **fielded devices** use
the shell plane from the device's Updates tab. The wizard step is **mandatory**
— a device advertising `oww_shadow` without the assets is exactly the "I
enabled it and nothing happened" this removes.

- The ARM runtime is **vendored into the controller image**, pinned by AAR
  sha256 (`onnxruntime-android` 1.19.2), so devices never need internet. The
  models come from the installed openwakeword package or `oww_models/` — no
  second copy to keep in step.
- **md5 is the only definition of success**, both transports. Push to `.part`,
  rename only on match: a truncated file is the right size and fails later at
  `dlopen` with an error naming nothing.
- **Four classifier slots, LRU by device mtime** — no controller-side
  bookkeeping to lose across a restart. The selected model is **pinned**;
  evicting it is the one outcome that breaks a device rather than costing a
  re-push. Only files positively recognised as evictable classifiers are ever
  deleted.
- Free space is checked against **what actually needs sending**, so a device
  that already has everything is never blocked. Read it with
  `parse_free_mb`, never an awk field index — busybox wraps a long filesystem
  name onto its own line, so `$4` is the *percentage* on these devices, which
  parsed as "unknown" and silently disabled the check.
- **`TransferResult` is truthy-compatible so existing `if not …` call sites
  keep working — which is exactly how a call site that treated it as a LIST
  reached a release.** `_sync_oww_assets` assigned the per-file transfer
  result over its own `pushed` accumulator, so the first file replaced the
  list and the append raised `AttributeError`. Every classifier push 500'd,
  and provisioning is the only other path that installs one, so a device
  could never be given a wake word it had not been provisioned with. Pinned
  by test.
- `DEVICE_DIR`, the shared model names and the classifier stem rule are pinned
  against the firmware constants **by test**. Drift installs assets the device
  never looks for, and the only symptom is shadow mode silently never starting.

## The external audio jack

Three separate faults, all fixed 2026-08-09 (#80), and one non-fault worth
knowing so nobody builds it.

**Boot with a plug inserted used to strand the whole device.** Android's
mediaserver claims the speaker PCM when a headset is present, and ALSA parks a
blocking open behind it with no timeout:

```
/proc/<pid>/task/<tid>/wchan          -> snd_pcm_open   (parked indefinitely)
/proc/asound/card0/pcm23p/sub0/status -> PREPARED, owner_pid: 659
fuser /dev/snd/pcmC0D23p              -> 258 (/system/bin/mediaserver)
```

`main()` initialises the speaker **before** `SubscribeToButton`, mDNS and the
control client, so one held device cost everything — no buttons, no wake word,
no registration. That is the whole of the "no wake word and no working
buttons" report. `Init()` now runs `stop media` first (the same stock-service
takeover as `stop mixer` beside it and `stop smarthomewifid` in `main`) and
waits on the substream status before opening.

**An OTA RESTART is the case a cold boot hides, and it cost a whole day**
(2026-09-10). At boot mediaserver is still starting and lets go in ~200ms —
the measured, ordinary path. After an in-place restart it is fully up, and
with a plug in the jack it takes the speaker for itself; `stop media` was
issued ONCE before the wait, so by the time the service had died and come back
there was nothing left to ask it again. The open then blocked, and because
`main()` initialises the speaker BEFORE mDNS, the control client, the buttons
and the LEDs, **the whole device went dark** — no registration, no wake word,
and not even the orange no-controller pulse, since the code that paints it is
never reached. The supervisor log showed the restart working perfectly both
times (`start` two seconds after each `exit`), which is what made it look like
a network fault rather than an audio one. A power cycle was the only recovery.
`waitForFreePcm` now takes a `nudge` and re-issues the stop every
`nudgeInterval`, which makes the race one we win.

**The nudge was not enough, and the real fix is that `main()` is no longer
gated on the speaker at all.** Measured the same day: with the nudge shipped,
a device still failed to come back after an OTA and needed a power cycle. So
`NewPcmSpeaker` now **returns immediately** and the open runs on its own
goroutine, retrying every `speakerRetryInterval` until it succeeds — the
control client, mDNS, the buttons, the mute and the LED ring all come up
whatever Android is doing with the PCM. Two consequences to keep:

- **`waitForFreePcm` refuses instead of opening anyway.** Opening a device it
  has just watched stay held for ten seconds is how a goroutine parks for
  ever, since tinyalsa's open has no timeout. That refusal was rejected as
  "a bigger behaviour change than the bug warrants" while main() was gated on
  it — refusing then meant refusing to start. It is not any more, and the
  comment that said so was true right up until its premise moved.
- **Pumps are REFUSED while the speaker is not ready, never queued.** An
  `audioStream` whose consumer does not exist yet accepts 128 periods and then
  blocks the data plane's read goroutine, which is the head-of-line stall that
  stops the device answering keepalives — trading a silent speaker for a
  dropped connection. `errSpeakerNotReady` is named so the log says why
  nothing played.

`retryOpen` lives in `pcmwait.go` with `waitFree`, untagged, for the same
reason: what is worth pinning is that the loop retries, that it BACKS OFF, and
that a stop is honoured *between* attempts rather than after another full
interval.

**The retry must back off and the nudge must be budgeted, and the reason is a
comment that stopped being true.** `nudgeInterval` was justified as costing
"four fork/execs on a path that runs once per process start" — which the
retrying open silently ended. The two holders look identical from here and are
nothing alike: mediaserver restarting after an OTA lets go within seconds,
while mediaserver holding the speaker **because a plug is in the jack** never
lets go at all. Under the flat 3s retry the second case spent a `stop media`
roughly every **2.6 seconds for the life of the process** — killing an Android
system service in a loop, on a board sharing 512MB with Android — plus a
`stop mixer` and a codec probe per attempt. `maxNudges` (4, exactly what the
original ten-second window already spent) and a doubling delay to
`speakerRetryMax` (60s) leave the recoverable case untouched, since it is over
long before either bound is reached, and turn the unrecoverable one into a
heartbeat. **Watch for this whenever something that ran once starts running
in a loop: the cost comments written for the one-shot are the things that go
stale, and they go stale silently.**

`waitFree` is in `pcmwait.go` with **no build tag**, beside `pcmstatus.go` and
for the same reason as `internal/outchain`: it is a timing loop over two
injected functions, the property worth pinning is its cadence, and everything
left in `pcm_speaker.go` carries `//go:build server` and is therefore compiled
only inside the pinned compiler image and cannot be tested at all. Watch for
that when changing this file — a host `go build` of this package reports
success without having compiled the tagged half.

**`stop media` does NOT stick, and the fix does not depend on it doing so.**
Android restarts mediaserver — measured on hardware: `init.svc.media` reads
`running` again, with a live pid, while our server still owns `pcm23p` in
`RUNNING` state. What makes this work is winning the race ONCE and then
holding the device for the life of the process, and `Init()` re-runs
`stop media` on every start, so an OTA or a supervisor restart gets the same
treatment. Do not "improve" this into a permanent disable: mediaserver
returning is what keeps Amazon's audio HAL — and therefore the DSP and the
I2S clock — initialised, which SETUP.md's Audio Notes describe as load-bearing.

**Android still reacts to jack events.** With mediaserver back, an insert
makes its `AudioOut_2` thread reconfigure the amp, DAC mux and ramp
underneath us (`EXTAMP Enable=0`, `Audio_DacMux_Set()`, `set_ignore_ramp`).
Removal produced no such reaction — only our own `tinymix`. Worth knowing
before blaming our code for codec state changing without us. **The speaker is card 0 device
23**; the mic is 24. Checking `pcm0p` reads `closed` and proves nothing — that
is Android's own device, and mistaking it for ours cost a wrong conclusion.
On timeout it opens anyway, which is the pre-existing behaviour: the wait is
there to make the common case work and to leave a log line naming the holder.

**The log looks healthy while this happens**, which is most of why it went
undiagnosed: `[mic] clock` lines keep appearing every 60s from a goroutine
started before the block. The tell is what is *missing* — `PcmSpeaker
initialised` never appears.

**Unplugging left the speaker silent until the next reboot.** accdet mutes
`Ext_Speaker_Amp_Switch` on insert, which is correct — the Dot should not also
play to the room — and nothing ever turned it back on. `Init()` was the only
thing that set it, which is exactly why a reboot appeared to fix it.
`internal/bindings/jack` watches `h2w`, and `PcmSpeaker.SetJackRouting` now
applies BOTH positions rather than only re-enabling the amp on removal.

**Booting with a plug in was a third instance of the same gap, and it is
edge-triggering all the way down.** accdet acts on the insert *transition*;
`jack.Watch` used to seed its baseline from the first reading and dispatch
nothing; and `Init()` sets the speaker amp **On unconditionally**, because its
click-free startup order needs the amp brought up onto a DAC already clocking
silence and it has no idea whether a plug is present. A boot has no transition,
so nothing corrected it: measured 2026-09-03 with `h2w=1` and
`Ext_Speaker_Amp_Switch=On`, i.e. the Dot playing to the room with a cable
connected, and the jack simultaneously at minimum gain. That is why unplugging
and replugging was the folk remedy — it manufactures the edge the boot never
had. `Watch` now dispatches the state it starts in, which is why
`SetJackRouting` must stay idempotent.

**Routing is PHYSICAL and needs no code — but LEVEL is not, and that
distinction cost three weeks.** The jack's own switch contacts divert the
signal: a voice response was heard in headphones while the mixer still read
`Ext_Speaker_Amp_Switch=On`, `Ext_Headphone_Amp_Switch=Off` and
`Headphone_Speaker_Mux=Speaker`, so those controls do **not** describe where
audio goes and no mux/destination layer should be built.

That is still true. What it was read as — "the mixer has nothing to do with the
jack" — is not, and it is why nobody looked at the gain. **`HP Driver Gain
Volume` (ctl 62) is the jack's output stage**, and accdet drops it to **0, the
FLOOR of a 0..35 range** on insert. On a stock Dot the audio HAL then raises it
to 11; we had nothing that did, so the external output sat at minimum gain.
Measured 2026-09-03 by diffing all 239 mixer controls across an insert on both
a stock FireOS 5.5.5.4 Dot and ours: writing ctl 62 with music playing took the
jack from inaudible to audible, while the `ref` loopback tap stayed flat —
confirming it is a post-DAC analog stage and not something upstream.

Stock changes five controls on insert, we now change two. `Right Channel Only`
and `Ignore Ramp Up` are deliberately **not** copied: our wire is mono and
`toStereo` duplicates L into R, so channel selection carries the same samples
either way (it becomes real the day the wire carries stereo), and the ramp
control's effect on this hardware has never been measured. Copying a stock
value whose effect is unknown is not the same as matching stock.

**Unresolved, and it is not only on removal (#117, #141).** A plug in the
jack degrades the whole audio subsystem for as long as it is present — mic
capture stalls on a ~102.3s metronome, and output dies and cycles between
three audible outcomes, recovering instantly on removal. Downstream the
controller sees `no mic frames for 10s`, breaches `ping_timeout` and tears
down the ESPHome satellite, BLE proxy and data plane, and **that teardown is
what users report** as music pausing and skipping (#141).

**Characterisation, exonerated surfaces and the live hypothesis are on issue
#117 and in JOURNAL.md (2026-08-12) — read them before touching this.** The
short version, so nobody repeats the work: all 6016 codec registers, all 218
MediaTek SoC audio registers and the full ALSA mixer are IDENTICAL between
audible and silent, so **do not go looking there again**; the fault is
load-independent (3-pole, 4-pole and headphones all fail); a stock Alexa Dot
plays the same speaker correctly, so it is ours, not the hardware; and the
live hypothesis is the audio HAL, which we displace by taking `pcm23p`.

Two traps for whoever picks this up:

- **`Ext_Speaker_Amp_Switch` was observed `Off` while the internal speaker
  was audibly playing**, and that observation has NOT been retested since
  the jack gain was fixed. It matters: if the control does not gate the
  internal driver, `SetJackRouting` cannot deliver "external only", and the
  remaining lever is unknown. The test is cheap and specific — cable in the
  jack with the powered speaker switched OFF, so the mic array can only be
  hearing the internal driver, then toggle ctl 5 with music playing and
  watch the `[aec] mic=` level.
  The older warning attached to this — that making the mute deterministic
  would turn "wrong speaker" into "no sound" — **no longer applies**. It was
  conditional on the external path being dead, and it was dead because
  nothing set ctl 62. Now that it is set, muting the internal driver on
  insert leaves a working output rather than silence. Note the residual
  risk this shifts onto volume: a user at a low `PCM Playback Volume` who
  plugs in now gets a quiet external output instead of a loud internal one,
  which reads as a fault. Stock avoids this by sitting at unity (127) and
  attenuating in software; we sit wherever the user left the control.
- The mic stall log line says "ALSA overrun", which is an interpretation.
  It measures the arrival gap in `readLoop`, and the GoTinyAlsa stream
  channel is 16 batches (2.56s) deep, so a stall of that goroutine looks
  the same. A **positive** clock skew does show audio is genuinely lost —
  and the sign is the whole reading. Every healthy device runs negative and
  grows more so forever: the ALSA sample clock is ~345ppm fast (measured on
  SPJ over 11.8h, 2026-09-02), which banks a whole 160ms batch every ~7.7
  minutes and reached -14.8s in one uptime with `stalls=0`. That growth is
  step-shaped, so steps do not distinguish drift from overruns either. The
  field is named `skew` and says `lost`/`capture fast` for this reason.


**Stereo is not supported and the device end is not the blocker.** ALSA is
already opened with two channels and `PumpPeriod` duplicates L=R; the mono
downmix happens at the controller, in three ffmpeg calls (`-ac 1`). That was
right when a mono internal speaker was the only output. With a jack it throws
information away — see the stereo issue rather than reinventing the analysis.

**`tinymix` IS on these devices** (`/system/bin/tinymix`), and the codec
regmap is readable at `/sys/kernel/debug/regmap/2-0018/registers`
(`tlv320aic32x4`). Do not drive `tinyplay` while the server is running: it
contends for the same PCM and wedged a device hard enough to need a power
cycle.

## The BLE proxy, and what it costs the device running it

Passive HCI scan over `/dev/stpbt`, forwarded to the controller and
re-presented to Home Assistant as a second ESPHome device. The recon and the
scan cadence are in `controller/CLAUDE.md`; this is about what it does to the
Echo it runs on.

**It degrades the control plane of its own device, and that is measured, not
suspected** (#404). Crossover on two Dots on one desk, same room as the AP,
2026-09-01: the one running the proxy logged **3615 idle RTT excursions in
24h against its neighbour's 2**, worst 20049ms against 4792ms, and 5
keepalive timeouts against 0. Moving the proxy to the other device moved the
fault within minutes and reproduced the same *rate* — 2.64/min against
2.49/min — on different hardware. It is not RF coexistence: stock FireOS
drove a Bluetooth speaker while streaming over WiFi, so the combo chip does
both. It is our own traffic.

**The mechanism was our own traffic on the liveness channel.**
`SendBleAdverts` wrote to the CONTROL WebSocket through `writeJSON`, which
takes `connMu` — the same mutex and the same TCP stream as the RTT echo, the
keepalive pong, wake events and stats. So bulk telemetry
head-of-line-blocked the channel a device's health is judged on, and RTT
excursions were partly measuring the advert traffic itself.

**Fixed by moving them to the data plane as `frameTypeBleAdverts` (`0x06`),
and the negotiation is the part not to unpick.** The device sends `0x06` only
when the controller announced `ble_adverts_data` in its `ack`; otherwise it
keeps using the control message. Unknown frame types are ignored in both
directions, so an unnegotiated `0x06` would drop every advertisement in
silence — a worse fault than the one being fixed, and one nothing would
report. The controller keeps handling the control-plane message forever, for
firmware that predates this.

**Two sender-side rules in `DataClient.SendBleAdverts`, both of which look
like caution and are not:**

- **A batch that cannot be sent is DROPPED, never failed back to the control
  plane.** Falling back puts bulk telemetry on the liveness channel exactly
  when the link is already struggling. The scanner's own
  `emitGlobalMaxSilence` is 30s and HA retires a scanner after 90s, against a
  data reconnect measured in seconds, so a normal blip costs nothing.
- **Nothing is sent while a BOUNDED TURN is streaming** — and it must be the
  turn, not `micActive`. The always-on wake stream is always on for any device
  scoring controller-side, so gating on "is the mic streaming" drops every
  batch forever and the proxy dies in silence; that bug was written and caught
  in review on 2026-09-02, one branch below the negotiation that exists to
  prevent exactly this. `advertsYieldToTurn` is split out so the decision is
  testable without a socket.
  The rule is narrow on purpose. One WebSocket is one TCP stream and a written
  frame cannot be preempted, so admission control is the only lever — but an
  advert batch is a few hundred bytes against ~32KB/s of mic, so it buys
  little. The control plane suffered because adverts took `connMu` against the
  keepalive pong AND because RTT is measured on that stream; neither is true
  here.

The plane is chosen **per batch** in `cmd/server.go`, not once at
registration: the control connection can drop and re-register against a
different controller without the scanner callback being rebuilt.

### The emission gate (`emit.go`) — derived from what HA reads, not from taste

Nothing downstream wants every broadcast. `habluetooth` tolerates 195s
(connectable) to 900s between advertisements per device before treating one
as stale and retires a *scanner* only after 90s of total silence; it smooths
RSSI itself (EWMA α=0.3) and switches which proxy owns a device on 16dB with
a 6dB deadband. The binding constraint is **Bermuda re-deciding which area a
device is in every second**. So the requirement is about one advertisement
per device per second, and a beacon broadcasting every 100ms is 10x waste.

The gate forwards on: a payload a **known** address has not sent before; an
RSSI move ≥3dB, rate-limited to one per 250ms; or nothing sent for that
payload in 1s. A global 30s ceiling keeps the scanner alive to HA with 3x
margin. Output is therefore bounded by **devices in range, not by how fast
they broadcast** — measured at 20 devices: 10x reduction at 100ms intervals,
5x at 200ms, 2x at 500ms, forwarding 1200 in every case.

**A first sighting is NOT an arrival, and treating it as one is the trap.**
BLE privacy addresses rotate every ~15 minutes, so "never seen this address"
fires continuously in any room with phones in it. The first version of the
gate flushed immediately on that branch, which in a busy room emits **more**
small writes than the plain 250ms batching it replaced — on the goroutine
that reads HCI. Urgency now requires a *known* address whose payload changed
(a button press, a sensor reading), and an early flush **resets the flush
ticker** so it moves a write earlier rather than adding one. A genuine
arrival waits at most one tick, which nothing downstream can perceive.

**Two things that look like the fix and are not:**

- **Lowering the scan duty cycle.** 320ms/30ms is exactly
  `esp32_ble_tracker`'s default, which is what every Bermuda deployment is
  tuned against. Fine as a one-off diagnostic, wrong as a shipped value.
- **`filter_duplicates=1` at the chip.** It suppresses identical
  advertisements — but RSSI is the field that varies and the field Bermuda
  consumes, so the chip filter discards the signal and keeps the noise.
  Filtering on the device can be RSSI-aware; the chip cannot.

### The HCI transport resets, unresolved as of 2026-09-01

**Two on C95 in ~40 minutes of gate runtime, against zero on EFF in 23.5h
with the proxy and no gate.** `read /dev/stpbt: socket operation on
non-socket` (ENOTSOCK), then ~30s of `network is unreachable` — the WiFi
interface itself, not a dropped socket. The counter has been on the Status
tab as `HCI errors / restarts` since 2026-07-12 and read zero for seven
weeks, so these are the first ever observed.

Four things to know before picking this up:

- **BLE is not the first thing to fail.** A **mic capture stall** precedes
  the BLE read error by ten seconds, in a different goroutine reading ALSA.
  Audio, then Bluetooth, then WiFi. Something stalls the whole process and
  the read error is what that looks like from the driver.
- **The WiFi was already failing BEFORE our reopen of `/dev/stpbt`.** The
  "reopening re-initialises the radio WiFi shares" text in `em_ble_proxy`'s
  warning is a hypothesis printed as a fact, and it produced a confident
  wrong call on the night — the timestamps rule it out. Fix that wording.
- **It is not RF coexistence.** Stock FireOS drove a Bluetooth speaker while
  streaming over WiFi.
- **Memory pressure from the gate's table is RULED OUT — do not re-derive
  it.** The theory was that a 250ms buffer became a 5-minute retained table
  and cost GC pauses. The table is ~300 entries at ~200 bytes (privacy
  addresses rotate every ~15min, so a 5-minute window holds one or two per
  device, not a stream), and C95's own `[mem]` lines show `heap_sys` flat at
  7.4MB and RSS flat at 26MB of 471MB. Decisively: **`pause_total` moved 2ms
  → 22ms across five minutes**, against mic stalls of 465ms, 1661ms and
  2481ms — three orders of magnitude short. EFF, with no gate, runs *more*
  GC than C95 (39/min against 17/min).

**What is left is restart proximity and something below us.** Both resets
came 3-7 minutes after a fresh process start on a device flashed four times
that evening, and every `/dev/stpbt` open triggers WMT BT function-on plus a
firmware patch download; EFF's clean record was earned running for days
between restarts. The gate remains correlated (2 events against 0) with **no
known mechanism**, which is where it honestly sits — resist the urge to
promote that to a cause without one.

## CPU topology, thermals and why `cpuPct` lies

The MT8163 is a **quad-core** Cortex-A53 (`/sys/devices/system/cpu/present` =
`0-3`) and MediaTek's hotplug strategy parks all but cpu0 when idle. So
`/proc/cpuinfo` showing one processor is a **power state, not a limit** — a
mistake worth not making twice, because it turns a comfortable measurement into
an apparent ceiling.

HPS (`/proc/hps/`) governs it: `up_threshold=80` / `up_times=2` bring another
core online after two samples above 80% utilisation, `down_threshold=70` /
`down_times=20` park it again (slowly), `rush_boost_threshold=98`,
`input_boost_cpu_num=2` boosts on button presses. cpu0 runs at 1.3GHz — its
maximum — under the `interactive` governor, so no frequency headroom is being
withheld. The `num_limit_*` files are ceilings (all 4 = nothing capping);
**`num_base_perf_serv` is the FLOOR**, and the firmware raises it to 2 at
startup (`applyCoreFloor`). That is deliberate: the mic pipeline has a hard
160ms deadline and now shares a core with wake word inference running in ~31ms
bursts, and a floor of 2 lets them run in parallel instead of relying on
hotplug reacting to a burst that has already begun. It is procfs, so it does
not survive a reboot — hence applying it in the binary, which re-applies every
start. Do NOT write `cpu1/online` directly: HPS re-parks it within
`down_times`, giving a setting that appears to work and silently stops.

**`cpuPct` is a share of ONLINE capacity**, derived from the aggregate
`/proc/stat` line. The same absolute work therefore reads as *half* the
percentage once a second core comes up — measured on Lounge, 51% on one core
became 25.5% on two with the workload unchanged. Always read it next to
`coresOnline`; a `cpu_avg` series without the core count can show a "drop" that
is purely a change of divisor. That is why both are reported and persisted.

Thermals: 11 zones. `mtktscpu` is the CPU/SoC (reported as `cpuTempC`),
`mtktspmic` the PMIC and `tmp103` a discrete board sensor; `maxTempC` is the
hottest of all of them, because trouble does not always appear on the zone you
thought to watch. Idle sits at 31–34°C, nowhere near throttling.
**`thermalCoreLimit` (`num_limit_thermal`) is the sharpest throttling signal
this SoC offers** — below `coresTotal` means the governor is already capping
capacity, which bites well before any temperature reading looks alarming.

## The device's log has to be readable from somewhere else

**Until 2026-09-10 the only lines that ever left this box were the `[mem]`
heap summaries.** Everything else went to stdout, which `start_server.sh` puts
in `/tmp/server.log` — RAM-backed, on hardware with no remote access of its
own. So `[airplay] shairport-sync exited: exit status 1`, repeating every
minute for two hours, was visible to nobody but somebody willing to open a
root shell on their own device. That single gap is what made the endpoint
orphan below cost five shell sessions and two wrong diagnoses, and it is worth
more than either fix.

`internal/logrelay` wraps the process's log destination (`log.SetOutput`) so
every line still reaches stdout and a SELECTION also reaches the controller,
which writes warnings into its own logger — the add-on log, the container's
stdout and the support bundle's `controller_log_tail`. Both halves are
required: the device sending with the controller only storing puts the line in
a database nobody watches, and the controller logging with the device not
sending relays nothing.

Four rules, and the first is the one that would hurt:

- **The forward is ASYNCHRONOUS and must stay so.** `SendLog` takes `connMu`
  on the control client, and `Write` can be reached from code already holding
  it — `writeJSON` logs its own failures. A direct call deadlocks the control
  plane the first time a send fails. `Write` only enqueues, never blocks, and
  drops when the queue is full: the same rule `shadow.Scorer.Push` follows for
  the mic goroutine, for the same reason.
- **It is RATIONED, because the control plane is the liveness channel.** RTT
  is measured on it and the keepalive pong rides it; bulk traffic there is
  #404, where BLE advertisements produced 3615 idle RTT excursions in 24h
  against a neighbour's 2. Six lines a minute, and the dropped count rides the
  next line through rather than costing a message of its own.
- **Match OUTCOMES, not components.** The classifier looks for `failed`,
  `exited`, `could not`, `timeout` and so on, so a subsystem written next year
  is relayed the day it breaks without anyone remembering to add it. The
  lifecycle exceptions are deliberate and few — `PcmSpeaker initialised` is
  relayed because its ABSENCE is the tell for a device whose PCM Android will
  not release, and an absence is only legible when the presence is normally
  there to compare against.
- **The pass-through happens first and cannot fail.** This is the process's
  log destination; a relay able to swallow a line would be worse than no relay.

`[mem]`, `[aec]` and `[mic] clock` are excluded by name: the first has its own
relay and is 89% of the `device_logs` table, and the others run ~1/s during
playback.

### And the relay cannot cover the fault where it is needed most

**The relay runs over the controller connection, so it says nothing about a
device that has no controller connection** — which is the one condition where
somebody is standing in front of a dead Echo with no way to ask it anything.
The device's shell is proxied BY THE CONTROLLER too, so in that fault there is
no channel at all: not the log, not a shell, not the dashboard. Every recovery
is a power cycle, and `/tmp` is RAM-backed, so the act of recovering destroys
the evidence.

That is not hypothetical. Four such restarts on 2026-09-10 ended in a power
cycle after 8 to 30 minutes each, and every one took its own explanation with
it. Told to run a command on the device first, the answer was the obvious one:
*"wie soll ich das machen. Ich gebe die Befehle über den Controller"*.

**So the firmware also writes to `/data/local/etc/revoice/supervisor.log`**
(`internal/bootlog`), the file `start_server.sh` has written its own decisions
to since 2026-08-01 and which the controller fetches after a failed update
(`em_api.SUPERVISOR_LOG`, `_collect_supervisor_log`). One file, not a second
one, so the supervisor's account and the firmware's read as one story in order
rather than two somebody has to interleave by hand — and firmware lines are
tagged `firmware:` so it is clear which wrote what.

Four things are load-bearing:

- **`up=` is SECONDS SINCE BOOT, read from `/proc/uptime`, because that is
  what the supervisor writes.** Timing from process start would be cheaper and
  would put two different zeros in the same column — an OTA restart is exactly
  when they diverge, and exactly when the file is read. The wall clock rides
  alongside as a hint and must not be trusted for ordering: an Echo boots
  reading 2010 and is corrected by the controller it cannot find.
- **Reports are ESCALATING, never periodic** (`bootlog.Escalator`: 1, 5, 15,
  30 minutes, then half-hourly). This is eMMC that cannot be replaced, and
  every fault recorded here is open-ended by nature — with a plug in the jack
  Android never gives the speaker back. A per-attempt line would spend a flash
  write every few seconds for as long as the fault lasts. The first milestone
  sits past every ordinary restart (the successful reconnect measured that day
  took 1m57s), so **a healthy device writes nothing at all** beyond its one
  startup line. Milestones already behind are spent rather than queued, or a
  caller returning from a long block pays out the backlog as a burst.
- **The trim happens BEFORE the append, and the bounds are the supervisor's
  own numbers** — pinned against `start_server.sh` by
  `tests/test_deploy.py`, along with the path. Two programs trim one file, so
  a firmware keeping more than the supervisor does merely has the extra
  deleted at the next boot: a bound that reads as deliberate and is really the
  smaller of the two.
- **Every failure is swallowed.** This is diagnostics for a fault that has
  already happened. A caller obliged to handle an error is a caller that might
  decide not to log.

What gets written, and why each is a fault nothing else can see:

| Line | The fault it is the only record of |
|------|-----------------------------------|
| `<version> starting` | The supervisor logs `start pid=… slot=server_a` — which symlink was followed, never what is IN it. This is what dates an OTA that appeared to work. |
| `no controller for …` (`internal/discovery`) | mDNS browsing and finding nothing. Carries `wlan0=<addr>` or `wlan0=no address`, which is the field separating "this device has no network" from "this device is on the network and the controller is not answering" — two faults with completely different next steps, and the ambiguity that could not be resolved after the fact. |
| `no controller session for …` (`internal/client`) | **The half a search-scoped record misses.** A device that finds the controller every round and never registers — pending approval, a token refused, a TLS listener it cannot complete against — spends no time inside `FindServer` at all. From outside it is the same Echo pulsing orange for twenty minutes. Measured against a registration counter, not against the callbacks, so "connected and later dropped" is distinct from "never got off the ground". |
| `no speaker for …` (`internal/bindings/speaker`) | The ALSA open never succeeding. `main()` is no longer gated on the speaker, so this costs only the audio now — which makes it QUIETER, not smaller: the device registers, answers its buttons and lights its ring while playing nothing. |

Each fault also writes ONE all-clear when it clears, and **only if something
was reported first** — an all-clear for a fault nobody heard about is a flash
write for nothing. `Escalator.Reset` puts the cadence back afterwards, or a
device flapping all night would be recorded once and then be as invisible as
it was before any of this existed.

## The controller's address is remembered, because a restart cannot rediscover it

`lastServer` was an in-memory field, so it existed for the life of ONE
process. A reboot repopulates it the slow way and nobody notices; an in-place
restart — every OTA, every supervisor restart — begins with nothing and has no
path to the controller except mDNS.

**That is the whole of "the Echo disappears after every update and comes back
after a power cycle."** Measured 2026-09-10 on the first boot of
v2.24.0-fx.1, in the persistent log that shipped the same day:

```
16:34:39  start pid=3101 slot=server_a
16:34:39  firmware: v2.24.0-fx.1 starting
16:35:58  firmware: no controller for 1m15s — 4 browse rounds, wlan0=192.168.178.140
16:40:18  firmware: no controller for 5m35s — 8 browse rounds, wlan0=192.168.178.140
16:45:00  boot slot=server_a        ← uptime resets: a REBOOT, connected in seconds
```

The binary started immediately, no fast exit, no rollback. The device held a
valid address the entire time. Only DISCOVERY was broken, and the same
restart-then-reboot pair appears **six times** in that one day's log — every
one of them a person deciding to pull the plug.

`discovery.SaveEndpoint`/`LoadEndpoint` persist the endpoint to
`/data/local/etc/revoice/controller.json`, beside the TLS credentials and
`state.json`, which OTA slot flips do not touch. `Run` seeds `lastServer` from
it when the field is empty, so the fast path exists in a process that has
never registered.

Four things not to undo:

- **It is a HINT and the probe is the judge.** `Run` proves the address with a
  3s TCP connect before using it and browses when that fails, so a controller
  that has moved costs three seconds and is then found the old way. This adds
  no way to be *wrong*, only a way to be fast.
- **Written only when it CHANGES.** The call site is every successful connect
  and this fleet reconnects often; unconditional would be a flash write per
  reconnect on eMMC that cannot be replaced. Same rule as
  `WriteConsolePassword`.
- **The TLS port is stored with it.** Without it, a device holding a CA
  re-browses to discover `tls_port`, which is the mDNS round trip this exists
  to avoid.
- **This does not fix mDNS**, it removes mDNS from the path a restart depends
  on. Why the browse stops being answered after an in-place restart is still
  open — `ip link set p2p0 down` at every start, and Android's multicast
  filtering, are the two candidates that have not been ruled out. The log now
  distinguishes them without anybody being present: `remembered <addr> did not
  answer` while the device holds an IP is a UNICAST failure and the fault is
  the network; `no remembered controller — mDNS only`, or a remembered address
  that answers, points at multicast. Those want opposite fixes and read
  identically until 2026-09-10.

### And ONE failed probe used to retire that fast path for the whole outage

**The cache above is only as good as how often it is consulted, and it was
consulted once.** `control.go` probed the remembered address, and on failure
called `discovery.FindServer` — which browses and does nothing else until
multicast answers, backing off to 60s, with no return. So a single 3s probe
decided the whole recovery, and **the one moment that probe is guaranteed to
fail is a CONTROLLER restart**: its listener is down for the length of a
container restart and back seconds later. The failure that cost the most was
also the most recoverable one.

Measured on the live fleet 2026-09-11, four times in one day, each beginning
within a minute of an add-on restart:

| outage | browse rounds |
|---|---|
| 4m16s | 7 |
| 33m26s | 32 |
| 38m5s | 36 |
| 36m56s | 35 |

plus one `no controller session` gap of **2h17m9s**. Throughout the last of
them the device held its address, its firmware never restarted (both endpoints
reported `uptimeS: 8340` across it, so the process and its Spotify and AirPlay
receivers were alive the whole time), the controller was listening — proved
against the live add-on, an HTTP GET to the device WebSocket port answering
`426 Upgrade Required` from `websockets/17.1` — and once reconnected the ping
to it measured **1.189/1.619/1.885 ms at 0% loss**.

**Confirmed in the field on 2026-09-12, and the number is the whole point.**
A device on v2.30.0-fx.1 took an in-place OTA restart — a fresh process, no
in-memory `lastServer`, exactly the case that used to cost half an hour — and
reconnected in **5.9 seconds** (`Disconnected` 02:06:03 UTC, `Connected …
version=v2.30.0-fx.1` 02:06:09), with both endpoints enabled 160ms after
that. Against 33m26s, 38m5s and 36m56s on the same device and the same
network a day earlier. Note it needed BOTH halves of this to work: the
endpoint cache to have an address to try, and the legacy-path fallback in
`internal/devicepaths` to find it, since the rename had moved
`controller.json` out from under it.

`FindServerWith` re-tests the remembered address before every browse round.
Three things are load-bearing:

- **Before the browse, not after.** When both would work the cheap test should
  win, and a browse round costs the full 10s mDNS timeout.
- **`probeRecheckTimeout` is 1s, against the first probe's 3s.** That one
  decides whether to skip mDNS entirely and is worth waiting on; this one only
  has to notice the controller came back, against a LAN round trip measured in
  milliseconds. Wrong costs one more browse round, not a missed reconnect.
- **The guard is on the CALL SITE as well as the behaviour.** Reverting
  `control.go` to `discovery.FindServer` leaves every other test green while
  restoring the entire fault, so `TestTheReconnectLoopRetestsTheRemembered
  Address` reads the source — comments stripped first, or it matches the
  paragraph explaining the rule rather than the code obeying it, which is this
  tree's recurring source-guard trap.

### The outage that was neither, and the instrument added for it

**PR #89 above would not have shortened the 22-hour outage of 2026-09-11**,
and saying so is the point of this section — the fix is right and its
diagnosis did not cover this case.

Measured while it was happening and immediately after the power cycle that
ended it:

| observation | source |
|---|---|
| Spotify Connect **not** seen while 7 other hosts answered | controller mDNS scan, during |
| AirPlay **not** seen while 2 other hosts answered | controller mDNS scan, during |
| `remembered 192.168.178.174:8767 did not answer` | device supervisor log |
| both endpoints visible, `target=revoice-g090l91180250an1.local.` | controller mDNS scan, after reboot |
| `wlan0=192.168.178.140` throughout | device supervisor log |

**The mDNS half of that table is NOT evidence about the network, and reading
it as such was the mistake of the night.** Corrected hours later on the same
device: **Spotify Connect and AirPlay are DISABLED by default and are started
only by the controller's config push** (`SPOTIFY_ENABLED`/`AIRPLAY_ENABLED`
default false; `applySpotifyConfig` runs at startup against those defaults
and again on every push). So a device with no controller session runs neither
endpoint and advertises nothing — their absence from a scan is a CONSEQUENCE
of the lost session and never independent evidence about the radio. The one
AirPlay answer seen mid-outage was the orphaned `shairport-sync` from before
the restart, still holding port 5000, which is exactly the case
`internal/orphan` exists for.

What survives from that table is the unicast line, and it is enough: a
remembered address that does not answer while the device holds an IP is a
real failure to reach the controller, and PR #89's re-probe cannot shorten an
outage where nothing answers.

**The general trap, and the part worth keeping: a signal that is DOWNSTREAM
of the thing you are diagnosing cannot corroborate it.** Endpoint visibility
is downstream of the controller session, so "the endpoints went quiet too"
reads as a second, independent symptom and is the same symptom seen twice.
The network-visibility check is meaningful only while the device is
CONNECTED — and `em_api._get_device_mdns_scan` deliberately answers for an
offline device, which makes it easy to ask the question at exactly the moment
the answer is worthless.

**The Echo's mDNS invisibility and its controller dropouts are therefore the
same event** — not because one causes the other through the network, but
because the endpoints are started by the session. Still useful: there is no
separate endpoint-announcement fault hiding behind every outage, and any "it
was not in the picker" observation taken while the device was disconnected is
worth nothing (#77).

`wifi.Describe` still rides the `no controller` lines, and is worth more now
rather than less — it answers "was the radio associated" directly rather than
by inference, which is the whole reason the inference above was available to
get wrong. **Nothing acts on it**: the repair for a zombie association is to
drop the WiFi of a device whose only management path is that WiFi, which is
not something to do on a guess. Instrument first.

**And the record of it was being read half at a time.** `supervisor.log` has
TWO writers — `start_server.sh`, which the controller pushes, and
`internal/bootlog`, which arrives by OTA — so they cross the rename on their
own schedules, and a current controller against firmware below v2.28.0-fx.1
puts them in `/data/local/etc/revoice` and `/data/local/etc/echomuse`
respectively. `em_devicepaths.first_readable_command` stopped at the first,
so the fetch returned the supervisor's two lines and reported success while
the firmware's account of the whole outage sat unread in the other directory.
It reads every path now. The general shape: **"try each location until one
works" is correct for a file with ONE writer and silently wrong for a file
with two**, because the second writer's absence looks identical to the file
simply not being there.

**The two failures above are now separable, and only one of them is fixed.**
The 2026-09-10 log in #51 is NOT this case: that device ran `v2.24.0-fx.1`,
which predates the endpoint cache, so it was genuinely mDNS-only. Tonight's
had a remembered address and lost it to one probe. Why a browse then goes
unanswered for 30-40 minutes is still open — and note the same device
received 34,052 mDNS packets from 37 distinct hosts in another window that
day, so multicast receive is not permanently dead, it comes and goes.

## The endpoints are children, and a restart does not take them with it

**This is what "AirPlay disappears after every update and comes back after a
power cycle" actually was**, reported for days, with two mDNS theories in
between that were both wrong. The announcement was never the problem: the
process was never up to make one.

`main()` exits and its children are reparented to init. librespot and
shairport-sync keep running, still holding the ports their protocols are
defined on — shairport listens on TCP 5000 for RTSP — so the new instance
cannot bind and exits immediately. The supervisor then retries for ever.
Measured on a device 2026-09-10, after an OTA from v2.19.0 to v2.21.0-fx.1:

```
1154 /data/local/bin/shairport-sync -a EchoDot  -o stdout    (alive, port 5000)
14:44:29 [airplay] shairport-sync exited: exit status 1       (and every minute after)
```

Three facts made it certain rather than likely, and each is worth knowing as a
technique:

- **`/tmp` is RAM-backed, so the log's own age dates the boot.** It still held
  lines from the previous hour, which proves the device had not rebooted —
  only the process had restarted. That single observation separates "OTA
  restart" from "power cycle" with no other instrumentation.
- **The surviving command line carried no `-c`**, a flag the firmware only
  began passing in the version that was supposedly running. A process older
  than its own parent is an orphan.
- **Port 5000 was listening while our supervisor was looping.** Both at once is
  only possible if the listener is not ours.

`internal/orphan` takes the ports over at **Start**, and that is the
load-bearing half. Stopping the children on the way down is also done (the
SIGTERM handler in `cmd/server.go`) and is NOT sufficient: it cannot run after
`kill -9`, after a panic, or on the supervisor's own restart path, and it does
nothing for a device already looping — which on a fielded fleet is every device
that has ever been updated. Same posture the firmware already takes with
Android's `mediaserver` and `mixer`: ask whoever holds the resource to let go,
every start, so one bad exit cannot strand the feature permanently.

Two details not to simplify:

- **Match argv[0] EXACTLY, never a substring.** `/proc/<pid>/cmdline` is
  NUL-separated and the first field is the executable as invoked. A `busybox
  grep` for the path, a shell about to run it, our own log line — all contain
  the path and none holds the port. A substring match kills the user's shell.
- **`/proc`, not `pkill`.** `pkill` is not on FireOS and busybox's applet set
  varies by SKU, so shelling out would be a check that silently cannot run.

Sendspin is deliberately not covered: it runs in-process, so there is no child
to orphan.

## Installed is not running (`internal/endpoint`)

`spotify_status`/`airplay_status` ride the register message and answer whether
the BINARY is on the device — present, a file, executable, its size. That is a
static property of the boot, correctly placed, and it answers "why is this
off" when the answer is a missing file.

**It cannot answer the question that actually gets asked.** On 2026-09-10 a
device had shairport-sync installed, executable, the right size, reporting
`ok: true` — and appeared in no AirPlay picker for two hours, because an
orphaned copy from before the last OTA still held TCP 5000 and every new
instance exited immediately. Every panel said the endpoint was fine.

`Client.Health()` on both endpoints returns `endpoint.Health`, and the three
fields each rule out a different thing:

- **`Enabled`** — the supervisor is up, i.e. somebody turned this on.
  `Running()` has always meant exactly this and is easily mistaken for the
  next one.
- **`Alive`** — a process exists right now. `Enabled && !Alive`, sampled
  repeatedly, is the fault above.
- **`Restarts`** — counted from the moment the endpoint was ENABLED, not from
  boot, so a deliberate toggle does not read as a fault. Steady is healthy;
  climbing is the signature, and it separates "briefly between sessions" from
  "failing every minute for two hours" without needing a second sample.

`LastExit` carries the reason, because `exit status 1` (a port it cannot bind)
against `signal: killed` (a preemption we asked for) is the whole difference.

Three rules:

- **It rides the STATS tick, never the register message.** Whether a process
  is alive is true at 14:44 and false at 14:45; reported once at registration
  it would be wrong for however long the device stayed connected, which here
  is days. Same rule that moved `base_os` in the other direction — ask where
  the consumer needs the answer.
- **`endpoint_health` is a capability, and a fourth one where three existed.**
  All three endpoints shipped before this, so there is firmware in the field
  that runs them and cannot say how they are doing. A capability to DO
  something is never evidence of a capability to REPORT it.
- **Only ENABLED endpoints appear.** A disabled one has no health to describe,
  and an entry saying so renders as a thing that is down rather than a thing
  nobody asked for. Both disabled sends nothing at all, which `omitempty`
  turns into an absent key — the same absence as old firmware, and correctly
  so: neither has anything to say.

The dashboard's half is `endpointHealthLine` (`dashboard.jsx`, tested by
`controller/tests/endpoint_health.test.mjs`), and its job is mostly to STAY
SILENT: firmware that cannot report, and firmware that has not sent its first
tick yet, must not render as "not running". Accusing a working Echo for the
first thirty seconds of every reconnect is how this becomes the line everyone
learns to ignore.

## A refused Spotify credential must be DELETED, not retried

**An endpoint that can only be repaired from the network cannot be repaired by
restarting it, and librespot's own repair path is what the restart breaks.**

librespot keeps the last login's credential blob in its cache (`--cache`; the
audio half is disabled) so the Echo stays authorised across reboots. When
Spotify stops accepting that blob, spirc initialisation fails and the process
EXITS. The supervisor restarts it, it reads the same dead blob, it exits again.
Measured on hardware 2026-09-12: `could not initialize spirc: Invalid state
{ Login request was denied: INVALID_CREDENTIALS }` every 15-30s, indefinitely,
with `[spotify] librespot exited: exit status 1` behind each one.

**The loop is not merely wasteful — it disables the repair.** Zeroconf sign-in
is two requests: the app reads the device's public key from `getInfo`,
encrypts its blob against it, and POSTs that to `addUser`. The key pair is
generated PER PROCESS, so a restart between those two requests decrypts the
blob with a key it was not encrypted for and the device answers `MAC mismatch`.
That line sat ten seconds before an exit in the same log, and reading it as a
second fault is the trap: it is the recovery failing because of the fault it
would have ended.

`internal/spotify/credentials.go` deletes the blob after it has been refused,
so librespot comes back in the state a speaker nobody has used yet is in —
advertised, waiting to be picked. The cost is one tap in the app.

Three things not to unpick:

- **`MAC mismatch` is deliberately NOT a rejection of the stored credential**,
  and that is the whole subtlety. It comes from `librespot_discovery::server`
  and is somebody else's blob failing to decrypt. Counting it would delete a
  working authorisation every time a phone's sign-in raced a restart — the
  expensive direction, since it signs the speaker out of an account it was
  correctly authorised for.
- **The flag is per SESSION, cleared at each start**, or a refusal would
  outlive itself and delete the credential a later healthy run had just
  stored.
- **The watch lives in the stderr relay because nothing else can see it.**
  Every librespot fault exits 1, so by the time `session` returns, a refused
  credential and a missing ALSA device are the same value. The distinction
  exists only in librespot's own words.

The general shape is worth keeping: **a stored credential that the far end has
stopped accepting is not a transient error and must not be retried.** The same
question applies to the device link token and to any future cached
authorisation — retrying forever looks like resilience and is a device that can
never come back.

## Running on emOS: the Android call sites, and the one that was load-bearing

The firmware runs on two bases (see `internal/platform`) and almost nothing
needs to care — mic, speaker, LEDs, buttons, ambient light, jack detect and
WiFi state are all ALSA, i2c, evdev, sysfs and wpa_supplicant. What DOES care
is the handful of places that ask Amazon's init to let go of hardware, and one
place that reaches into Android's framework.

**`stop <service>` × 6 now goes through `internal/androidsvc`**, which is a
no-op on emOS. Five of those are cost and noise: two wasted fork/execs per
site per start, and `internal/bindings/mic` logged its failure, so an emOS
device would print a line about a service that does not exist on every boot.

**The sixth decided whether the device booted at all.**
`buttons.NewButtonController` returns whatever `stop acebutton` returns and
`cmd/server.go` calls `log.Fatalf` on it. Whether an emOS device came up
therefore rested on what Amazon's toolbox `stop` does when `property_set` has
no socket to write to — it ignores the failure and exits 0, so it would have
worked. **That is a load-bearing assumption about a vendor binary's
undocumented exit code, sitting on the path that decides whether an Echo
starts.** Gating removes the question rather than answering it, and the answer
was never written down anywhere a reader of this tree could check.

`platform.IsAndroid()` is the gate, and **Unknown counts as Android** — the
same default `base_os` takes everywhere else, because firmware that cannot
tell must behave as the existing fleet does. Being wrong that way costs a
failed exec; being wrong the other way is hardware nobody asked Android to
release, which presents as silence, a dead ring or a dead microphone.

`internal/bluetooth` skips its `pm disable` sweep the same way: emOS has no
Bluedroid and no package manager, so `/dev/stpbt` is unowned and there is
nothing to disable. Said once at info, not four `pm` failures and a `settings`
failure per start.

### The WiFi change is the one that would really have broken

Everything in `internal/wifi` is portable except the two lines in the middle
that take wpa_supplicant down and bring it back: the backup, the pending
marker, the association/address/registration gates, the automatic restore and
`RecoverIfPending` are all base-independent and correct on both. FireOS runs
the supplicant under its framework, so `svc wifi disable`/`enable` is the only
safe lever and the package comment lists what happens to anyone who reaches
past it. emOS runs the supplicant directly and has **no `svc` on PATH at
all** — so the Android path does not fail loudly there, it fails as a missing
binary, `disableWifi` returns an error, and the change is refused every time.
A dashboard control that refuses every time is exactly the "control that
silently does nothing" the capability rule forbids.

`reload.go` picks a `supplicant` per base. Three things in the emOS half:

- **`disconnect`, never `terminate`.** It leaves the process running, so the
  control socket every later step needs — the ROLLBACK's included — stays
  open. Killing the supplicant on a device whose only management path is that
  radio removes the means of putting it back.
- **`reconfigure` before `reassociate`, in that order.** `reassociate` alone
  re-joins what the supplicant already holds in memory, which is the OLD
  network; the gates would then pass against the old SSID and commit a change
  that never happened. That is the same shape as the FireOS clobber the
  package comment records, reached from the other side, and it is pinned by
  test.
- **dhcpcd is not touched.** Forcing a fresh lease by killing it rests on
  init respawning it, which is true for a service in emOS's table and
  catastrophic if it is not — the rollback would then also come up with no
  address. The existing client renews or the IPv4 gate fails and rolls back,
  which is the safe direction.

**What makes an unproven path shippable is that the safety model is the
proven part.** A wrong reload cannot strand the device: the gates fail, the
backup goes back through the same reload, and if even that leaves it without
an address, `RecoverIfPending` restores the old conf at the next process
start. The failure mode is a reboot, not an Echo on a network nobody can
reach.

### What is NOT gated, and the correction that matters

`tinymix`, `getprop` and `iptables` are all reached bare and all keep working,
because **emOS mounts Amazon's `/system` read-only** — they are files on a
filesystem, not services. `getprop` additionally has a `/proc/cmdline`
fallback for the serial, since there is no property service to answer it.

That mount is the trap. "emOS has no firewall" was written in this file and in
`internal/netfilter` and is **reasoning about the POLICY presented as a fact
about the BINARY**: the default-deny policy comes from one of Amazon's init
scripts and does not run, while the binary comes from `/system` and does. The
same mistake is available for every Android tool this firmware reaches for.
Ask which of the two an absence would come from before writing it down.

## Advertised is not reachable: FireOS drops every inbound port (`internal/netfilter`)

**This is what #77 was, after weeks of looking at mDNS.** FireOS ships
`-P INPUT DROP` with an allowlist of Amazon's own ports, and ours are not on
it. Read off a live device 2026-09-12:

```
-P INPUT DROP
-A INPUT -i wlan0 -p tcp -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 5353 -j ACCEPT      <- mDNS
-A INPUT -i wlan0 -p tcp -m tcp --dport 4070 -j ACCEPT      <- Alexa
-A INPUT -i wlan0 -p udp -m udp --dport 5000 -j ACCEPT      <- UDP, not TCP
-A INPUT -p icmp -m state --state RELATED,ESTABLISHED -j ACCEPT
... policy DROP 605 packets, 89226 bytes
```

**The blind spot is structural, and it is the part worth carrying forward.**
Every plane this project has — control, data, shell, OTA — is dialled BY THE
DEVICE, so `RELATED,ESTABLISHED` covers all of them and nothing in the system
had ever opened a connection *to* an Echo. mDNS is allowed, so announcements
go out and are heard. So every instrument read healthy while the feature did
not work at all, and each measurement taken to check it agreed. **"Advertised"
was never "reachable", and nothing measured the difference** — which is why
`em_api._tcp_reachable` and the `port_open` column in the mDNS scan were added
alongside this.

Three traps in that one page of output:

- **`udp dpt:5000` is not AirPlay.** Amazon opened UDP 5000 for something of
  their own; shairport-sync's RTSP is **TCP** 5000. A rule read at a glance
  sends the next person away satisfied.
- **ICMP is `RELATED,ESTABLISHED` only**, so an echo request — which is NEW —
  is dropped, while `icmp_echo_ignore_all` reads 0 and says the kernel would
  have answered. A device that will not answer a ping reads as "off the
  network"; this one never was, and that mistake cost an afternoon and a
  wrong accusation aimed at the user's router.
- **The UDP range is the half that gets forgotten.** With only shairport's
  control port open, a session negotiates and then plays nothing — which
  presents as a broken speaker rather than as a firewall.

`internal/netfilter` opens exactly what the ENABLED endpoints need and closes
what they do not, called from the same two sites as the endpoint start/stop
(`applyFirewall` in `cmd/server.go`, at startup and on every config push).
Read the package comment before changing it; the rules that must never be
written (`-P`, `-F`, `-X`; a rule without an interface and a port) are
enforced by test, because this is a firewall on a device whose only management
path IS the network.

Two decisions not to unpick:

- **The ports are PINNED and the daemons are told them from the same
  constants.** librespot picks a random zeroconf port per start, which no rule
  can name. Rule and listener disagreeing is exactly the "connects and plays
  nothing" failure above, so there is one definition and both sides read it —
  pinned by test in `internal/spotify` and `internal/airplay`.
- **Delete-then-insert, not `-C` then `-I`.** The obvious idempotence rests on
  `-C` working, which is an assumption about a binary we do not ship and
  cannot exercise on the host. An iptables where `-C` misbehaves turns "check,
  then insert" into "insert" on a path that runs at every start AND every
  config push — a table that grows for the life of the device. `-D` in a
  bounded loop then `-I` needs only `-D`, and it REPAIRS a table that is
  already wrong instead of merely declining to make it worse.

**emOS still gets the rules, and the first version of this section said it
did not.** emOS has no default-deny policy — that is Amazon's init script,
which does not run — but it mounts Amazon's `/system`, so `/system/bin/iptables`
is present and works. `Sync` therefore inserts four ACCEPT rules into a table
whose policy is already ACCEPT: no-ops, a handful of execs at startup and per
config push. Left ungated on purpose, because the question the package answers
is about the TABLE rather than about which userspace booted — so a device that
one day runs a firewall under emOS works without anybody remembering this file.

`ErrUnavailable` covers a base with no iptables binary at all, which is neither
of the two we ship, and it exists so that case is said ONCE: the log relay
forwards lines matching `could not` to the controller, and a per-rule complaint
would put four warnings into somebody's Home Assistant log on every reconnect.

The correction is worth keeping as a shape: **"emOS has no firewall" was
reasoning about the POLICY, written as a fact about the BINARY**, and the two
come from different places — one from Amazon's init, one from a filesystem emOS
deliberately mounts. The same mistake is available for every other Android tool
this firmware reaches for, because `/system` is there under both bases.

## Two ways to go invisible, and only one of them is repairable (`internal/mcast`)

**A device can be absent from every picker for two completely different
reasons, and every on-device reading looks identical in both.** Both were
measured on 2026-09-12, hours apart, on the same Echo. Conflating them is how a
repair gets credited for outages it cannot touch.

| | what is true | what fixes it |
|---|---|---|
| **the membership is gone** | `/proc/net/igmp` has lost 224.0.0.251 while both responders still hold UDP 5353 | restarting the endpoints re-joins the group — `Watcher` |
| **the membership is present and nothing arrives** | 224.0.0.251 joined, both endpoints healthy, and the device hears *only itself* while the controller hears six other hosts | **unknown** — `Prober` measures it and nothing acts |

**Deaf is NOT the same as unheard, and the first field run proved it.**
v2.37.0-fx.1 went onto a device and within ten minutes logged the deaf line —
while the controller's own scan of that same device answered *"Every enabled
endpoint is visible on the network"*, eight other Spotify hosts seen, the same
minute. Both readings were correct. Announcements go out UNPROMPTED, so a
responder that hears nothing still advertises, and a device can be deaf and
listed at once.

That kills the tidy causal chain this was written against ("the query never
arrives, so nothing answers, so it is in no picker") and it is why the log line
states the measurement and explicitly hands visibility to `em_mdnsscan` rather
than concluding it. Pinned by `TestTheDeafLineDoesNotClaimTheDeviceIsInvisible`,
because the wrong version reads better and would come back.

**The same run separated two hypotheses, which is what the instrument was for.**
`/proc/net/arp` on the deaf device held **28 entries for other hosts on the
LAN** — a table that fills from ARP broadcasts. So L2 BROADCAST arrives and
multicast does not, which rules out the whole class of "the AP is not sending
this station group traffic at all": DTIM buffering and power-save hit broadcast
and multicast together. What is left has to distinguish the two, and IGMP
snooping is exactly that — a switch forwards broadcast always and multicast only
to ports it has seen a membership report on. Note `/proc/net/dev`'s `multicast`
column is **unusable on this driver**: it read 0 against 293,964 received
packets, so it is not implemented rather than measuring zero.

`Watcher` reads the membership on the network-repair ticker and restarts
whatever should be a member. The rules that matter: **it opens no socket of its
own** (joining from here would put the membership on a socket the responders do
not own — the group would read as present, the watcher would fall silent, and
librespot and shairport-sync would still never see a query, removing the symptom
and the instrument together); a **failed read is not absence**; and both the miss
threshold and the doubling backoff exist because a re-association is exactly
when the membership is legitimately gone for a moment AND when a restart is
least likely to help.

`Prober` is the second half, and it is **instrumentation only**. It asks the
same question `device/tools/mdnsprobe` asks — a service enumeration from an
**ephemeral port with the unicast-response (QU) bit set**, so it never binds
5353 and cannot stop its own children from binding it — and counts responders
that are **not this device**. That word is the whole measurement: the fault
reading had a complete, correct four-answer reply in it, from the Echo's own
responder, which is why every check run on the device read healthy through the
outage.

Four things not to unpick:

- **Nothing acts on it**, and that is the same call `wifi.Describe` makes on the
  `no controller` lines. The mechanism is below anything this project controls
  — the AP's IGMP snooping, multicast-to-unicast conversion, the DTIM path —
  and every remedy available here is a guess. What was missing was never another
  theory; it was DURATION and FREQUENCY with nobody present, and that is what
  chooses between the remaining leads (#142).
- **The cadence is adaptive, and the silent one is the fast one** — 5 minutes
  while healthy, 60s while deaf. A probe asks every host on the link to answer,
  so a fixed fast cadence is a poor neighbour on somebody else's network; but an
  outage is dated from its RECOVERY, so the state that needs resolution is the
  quiet one. A device that hears nothing is by definition not being heard, so
  the fast half costs that network nothing.
- **One line per transition, never per probe.** The relay is rationed at six
  lines a minute on the liveness channel — a warning per minute for a
  four-hour outage is #404 with a different payload.
- **Both lines have to survive `logrelay.Classify`, and they are two different
  problems.** The onset carries `cannot` and is forwarded as a failure by the
  outcome markers; an all-clear has no generic outcome word at all, so
  `[mcast] hears` is a named lifecycle marker. Without it the log holds every
  onset and no recovery, which reads as every outage still running — and the
  device is perfectly reachable over unicast throughout, so nothing else in the
  system reports anything.

Both are gated on an endpoint actually being enabled. A device with both
switched off has nothing to be invisible with: the group is correctly absent,
and counting that as a fault would restart what the user turned off and date an
"outage" that was somebody flipping a switch back.

## AirPlay latency, and why the prime depth is not one number

**The music plane's prime gate was ~1s of PERMANENT latency for every
device-local source, and that is ours rather than AirPlay's.** `primePeriods`
= 24 (~1s) exists to protect the opening seconds of a CONTROLLER stream, where
measured 1.8–2.6s link stalls used to drain the buffer into audible gaps. That
is a justification about a WiFi hop — and librespot and shairport-sync are
processes on this device writing to a pipe, so the audio they produce never
takes that path.

It is also not a start-up cost that goes away. Both local producers pace
themselves at realtime — shairport-sync has its own clock and librespot is
paced by pipe backpressure — so the buffer settles at whatever depth the prime
gate demanded and stays there, and every sample waits for the periods ahead of
it. AirPlay already carries ~2s of protocol latency; a second on top is what
"der AirPlay-Ton ist mega verzögert" was measuring.

- **`speaker.MusicPrimeFor(local bool)`** returns 4 periods (~171ms) for a
  device-local owner and 24 for the controller. Set from the ONE observer that
  sees every handover (`musicplane.OnChange` in `cmd/server.go`), because that
  is the only place that knows who is filling the plane. It takes effect on the
  next stream, not the current one — `ready` consults it only while `playing`
  is false — so a mid-track handover cannot re-gate audio already flowing.
- **Not zero and not one.** A pipe read still arrives in bursts, the ALSA loop
  still takes exactly one period per iteration, and a mid-stream drain is
  counted against the stream as an underrun. A few periods of slack keeps a
  scheduling hiccup from reading as a fault.
- **`sampleRate` and `periodSize` moved to `format.go`, untagged**, so
  `musicprime.go` can turn periods into seconds on the host. `pcm_speaker.go`
  is `//go:build server` and nothing untagged can see inside it — the same
  reason `pcmwait.go` and `pcmstatus.go` exist. Both `periodSize` source guards
  (`internal/pcm`, `internal/sendspin`) read that file now.

**shairport-sync has to be TOLD what we add behind it, and `ConfigPath` was
declared and never written.** It plays each packet at the instant the sender
stamped it, so a backend that holds audio makes every packet late by that much;
`audio_backend_latency_offset_in_seconds` is how it is told, and with no config
file at all the setting was unreachable for the life of the package. The value
is derived from `speaker.LocalPrimeSeconds()` rather than written as a literal,
so changing the prime changes the compensation.

Two things not to undo:

- **The sign comes from shairport-sync's own sample** ("if the output device
  delays by 100 ms, set this to -0.1") and is **not measured here**.
  `EM_AIRPLAY_LATENCY_OFFSET` overrides it in seconds — an env var rather than
  a config key, for `EM_AEC_HW_REF`'s reason, and it exists so the number can
  be corrected against a real speaker without a rebuild.
- **A config that could not be written must not reach the command line.**
  shairport-sync REFUSES TO START on a `-c` file it cannot read, so
  `writeConfig` returns `""` on any failure and `args` omits the flag: the
  compensation is worth ~171ms, the receiver is worth AirPlay existing. Written
  to a temp file and renamed for the same reason — a half-written config is one
  it will not parse, and the observable is a receiver that never appears with
  the reason on a stderr nobody is reading yet.
- **A zero delay writes NO offset rather than `0.0`.** Same number, different
  statement: absent means nobody measured, and a caller that does not know its
  own pipeline should not assert there is none.

## What `PlaybackDelay` measures, and the buffer it used to miss

**`PlaybackDelay()` is the only thing standing between Sendspin's scheduler
and reality, and for the life of the package it reported half the pipeline.**
It returned ALSA's own `delay` from procfs — appl_ptr minus hw_ptr, frames the
hardware has been handed and not yet played. In FRONT of that sits the music
plane's software ring, which the DMA pointer cannot see: a period pushed by a
producer waits its turn in a Go channel, gets mixed, and only then becomes a
frame ALSA knows about.

**For a scheduled protocol that gap is a bias, not noise, and it converges
rather than cancelling.** `Runtime.correctLocked` asks when the next sample
would play (`now + queued/rate`), compares it to the timestamp the server
chose, and pads or trims the difference. Under-report the pipeline and it
concludes it is early by exactly the ring depth, so it pads; the padding goes
into the ring; the measurement does not move, because the ring is the part it
cannot see. The loop settles with the audio coming out LATE by the ring depth
and holds it there — and every number it logs agrees with itself, so nothing
on the device reports a fault. In a Music Assistant group with any correct
speaker, the symptom is an echo.

`speaker.PlaybackFrames(hwDelay, queuedPeriods)` is the sum, untagged and
host-tested for `musicprime.go`'s reason. Each ring entry is exactly one
period by construction — `PumpMusic` takes one period and the channel carries
them whole — so the depth is a count of periods and not of bytes.

**It answers about the MUSIC plane specifically.** Its only caller is Sendspin
asking about audio it is about to push. A voice-plane answer would be a
different question wearing the same name, and the name is what a future caller
will read.

**`OutputDelayMs` stays 0, and that is now written down at the call site.**
It covers only what lies beyond the hardware pointer — codec, amplifier,
analog path — because everything in front of it is measured. What remains is a
handful of milliseconds nobody has put a microphone in front of, and a guess
is the one thing that field must not carry: it is a FIXED offset the clock
filter can neither see nor undo, so a wrong number moves this speaker
permanently out of a group that is otherwise correct.

## The AirPlay slider can move the device volume (#30)

**Off by default, and a setting rather than a behaviour, because the
consequence belongs to whoever owns the room.** This device has ONE volume,
shared with the assistant: a phone that drops AirPlay to 20% drops the next
spoken answer to 20% as well. That is a defensible reading of "the slider sets
the device volume" and it is what was asked for — but meeting it for the first
time when the assistant whispers an answer is not, so it is chosen
(`airplayVolumeControl`, default false at BOTH ends).

Four parts, and each has one thing that is easy to get wrong:

- **The build.** `--with-stdout` offers no volume callback, so shairport-sync
  attenuates in SOFTWARE and never tells anyone the slider moved.
  `--with-metadata` is what makes `ssnc`/`pvol` exist. Note `shairport/build.sh`
  runs inside a single-quoted `bash -c`, so an apostrophe in a comment there
  ends the quoting and breaks the build.
- **The parse** (`metadata.go`). The stream has no root element and never
  ends, so `encoding/xml` would block for ever waiting for a close tag that is
  not coming — it is scanned for `</item>` instead. Only the FIRST field of
  pvol is read: the other three are shairport's derived values about a range
  it knows nothing about, since the stdout backend exposes no control. Every
  unrecognised item is skipped SILENTLY, or cover art alone puts a line in the
  log per track change.
- **The pipe** (`metadatapipe.go`). Two traps. Opening a FIFO for reading
  BLOCKS until a writer appears, so the open is `O_NONBLOCK` and the loop
  reopens on EOF — and then clears the flag, because a non-blocking *read*
  would burn a core for the length of every track. And a FIFO with no reader
  fills at 64KB and then blocks the WRITER, which is the process decoding the
  audio: so the config asks for metadata only when something is listening.
  `Options.OnVolume` is the single gate for all of it — nil means no config
  block, no pipe and no goroutine.
- **The mapping** (`server.LevelForAirPlayDB`). Both scales are dB, so it is
  an OFFSET: ctl 61 is 0.5dB a step with unity at 127, so `127 + dB*2` puts
  AirPlay's 0dB on the codec's unity gain. Treating the slider as a percentage
  of 0..127 is wrong twice — the control is dB-LINEAR, so a linear percentage
  crushes the bottom third into inaudibility, and it would discard the fact
  that AirPlay already sends decibels. `-144` is a MUTE SENTINEL and is
  checked explicitly, so a future change to the range cannot quietly turn mute
  into quiet. `volumeButtonFloor` is deliberately NOT applied: it exists so
  physical presses do not spend themselves crossing a silent third of the
  scale, and nothing about that applies to a slider somebody drags where they
  mean.

It paints the ring (`SetVolumeFromAirPlay` → `Set(level, true)`), unlike a
controller command: a remote set is nobody standing at the device, but a
slider is a person watching for the speaker to answer — the same thing a
button press is. It also marks the volume SEEDED, so the stored
`startupVolume` cannot land on top of a change the user just made.

**The handler is installed LIVE, and assuming it could be fixed at `New()`
was a bug in the first draft.** The setting arrives on a config push long
after the client is wired, so a callback resolved at startup meant turning it
on did nothing until the firmware restarted — while the label promised
otherwise. `SetVolumeHandler` is called from `applyAirplayConfig` on every
push, and a change that crosses nil↔non-nil restarts the receiver, because
whether metadata is asked for is written into shairport's CONFIG FILE and that
is produced when the process starts.

**The reader's lifetime follows the handler, and the ordering is the part that
bites.** Turning it on while the endpoint runs is the dangerous direction: the
config would tell shairport-sync to write down a FIFO, and a FIFO with no
reader fills at 64KB and then blocks the WRITER — the process decoding the
audio, so the symptom is the music stopping. `syncMetadataReader` starts the
reader *before* the restart that rewrites the config, and stops it when the
handler goes away so it cannot outlive what it reads for. One owner
(`metaStop != nil`), reconciled, rather than two places that can disagree.

**Unverified end to end.** The format and the flags are documented, the parse
and the mapping are covered by tests, and nothing here has met a real phone:
`airplayStatus` will say whether the rebuilt binary runs, and the first
listening test is what confirms the slider reaches the codec rather than
shairport's own attenuator.

## Volume / mute persistence

**The scale stops at the codec's unity gain, and that ceiling is load-bearing.**
tinymix ctl 61 is the tlv320aic32x4 DAC *digital* volume: 176 steps of 0.5dB
spanning −63.5…+24dB, with 0dB at index **127**. The firmware shipped
`volumeMax = 175` — the control's own maximum — so the top 27% of the range
applied up to +24dB of digital gain to already near-full-scale PCM and
saturated inside the DAC. Measured on hardware 2026-08-13 (1kHz at −6dBFS,
recorded through the mic array): THD 1.5% at index 127, 2.3% at 136, **65% at
153, 89% at 170**, with the output level *flat* from 153 upward because it had
stopped being able to get louder, and h3 at −1.1dB relative to the fundamental
(very nearly a square wave). The control that isolates it: index 170 with the
source scaled down to land at the same acoustic level reads 1.1% — clean — so
the gain stage is fine and it is purely source × gain exceeding full scale.
Stock FireOS never writes this control **at all** (absent from
`/system/etc/audio_device.xml` and from every `/system` binary), leaving the
DAC at its 0dB reset default and taking user volume from AudioFlinger's
software attenuation, which only ever attenuates — that is why native Alexa
has no such distortion.

Two things not to undo: `DEVICE_VOLUME_MAX`/`volumeMax` stay at 127 (both
pinned by test), and the conversion lives in **one** place — `em_volume.py`,
because `level / 175` was copy-pasted into `em_controller`, `em_esphome` and
`em_api` with no test on any of them, which is how the wrong ceiling survived.
The lost headroom **cannot** be bought back from `Ext_Amp_Gain` (ctl 13): that
control is inert on this board — sweeping its full 6/12/18/24dB range moves
the output 0.0dB while still reading its new value back, the same shape as the
mute LED being on a different GPIO than Amazon's own HAL believed.
`HP Driver Gain Volume` (ctl 62) *is* live (+18dB commanded → +18.1dB actual,
THD 2.25%) if more output is ever wanted, but that is a taste call to make by
ear, and the speaker's behaviour above stock level is unmeasured.

The **physical buttons** traverse `volumeButtonFloor`(47, −40dB)…127 in 4dB
steps rather than the whole control: the scale is dB-linear, so the bottom
third is indistinguishable from silence and stepping across it spends presses
to go nowhere. Silencing the device is the mute button's job. Explicit `Set()`
calls are deliberately **not** floored — HA's volume 0.0 must still mean
silent — and a press from below the floor lands *on* it, so one press always
reaches audible.

Volume is **state, not a setting** — it rides the config channel but has no dashboard control (the slider was removed 2026-07-25: `SeedVolume` ignores later pushes, so moving it did nothing until the device restarted and any real volume change overwrote it). It is listed in `em_config_sections.STATE_KEYS`, exempt from section scoping, and shown read-only on the Status tab.

Volume persists through reboots **controller-side**: every device `volume_state` report is stored into the device's `startupVolume` config, and the device restores it via `Server.SeedVolume` on the **first config push per run only** (later pushes must not stomp live changes). Until seeded (or a local volume change makes the device authoritative), the device suppresses its connect-time `volume_state` report — reporting the boot-default level is what used to clobber the stored value on reboot. Mute is the opposite: **device-sovereign**, persisted locally in `/data/local/etc/revoice/state.json` (survives OTA slot flips; written on toggle, restored at boot pre-connect — ADC mute immediately, button LED after LED init; the ring is not touched).

## LED priority system

Turn-state ring colours (listening ring, thinking spinner) come from **LED scenes** (`em_scenes.py`), configurable per device (`ledScene` + custom colours). Firmware with the `led_anim` capability (v2.9+) **animates locally**: the controller sends one `led_anim` message per state change ({pattern: solid|spin|rotate|pulse|meter|off, colors, periodMs, ttlSec}) and the device renders frames on its own ticker (`internal/server/animator.go`) — controller/WiFi jitter can't judder the ring. `meter` throbs with the live speaker RMS (tapped at the ALSA write, so it tracks audible audio, not the ~5.5s-ahead send) — measured on the **voice plane only, before the music mix**, unlike the AEC far-end tap which deliberately sees the mixed output; a meter fed the mix throbs to the music bed before the response has started; its response curve is config-tunable (`meter*` keys → `AnimSpec` pointer fields → `resolveMeter`, which clamps independently of the dashboard ranges) because it is a taste parameter that needs iterating in a real room, not a firmware OTA per pass. `ttlSec` is bounded per phase — 30s listening, 135s spinner (**coupled to `_fetch_tts_audio`'s 60s timeout ×2 attempts, since the spinner spans HA think time AND the fetch — move one and move the other**), and computed per response for `meter` via `em_scenes.meter_ttl` so a long TTS cannot self-clear mid-answer. Loss-resilience: newer spec or raw `leds` frame atomically replaces the animation (generation counter), and `ttlSec` is a dead-man that self-clears the ring if the controller dies mid-turn. Legacy firmware falls back to controller-streamed frames. Controller `leds` messages carry an explicit `listening: true` flag on listening-ring frames — the device's direction overlay keys off it (pre-scene firmware inferred "listening" from an all-green ring, which breaks for any other scene; the heuristic remains as fallback for old controllers). The direction overlay brightens the base ring colour instead of painting green. The volume arc (cyan) is device-local and scene-independent by design. There is no mute ring: mute is shown on the button's own GPIO LED, so the ring stays available (see the LED priority bullets below).

Turn *outcomes* are distinguished by rhythm, not colour (red/orange/cyan are taken by mute/link/volume): `no_speech` gets one slow throb, `no_tts`/`tts_error`/`timeout` fast blinks, everything else ends silently. Both ride the existing `pulse` pattern with a 1s TTL so they retire on the device's own ticker — no follow-up message to lose. Driven by `device.last_turn_outcome` (set in `em_esphome._persist_turn` **and in `_record_dropped_turn`**, consumed once by `_leds_turn_end`).

**`no_ha` is the one cue that uses colour, deliberately.** A turn with no ESPHome server or no HA connection behind it does not report an outcome of the turn — it reports that there is nothing above the device to answer — and orange already carries exactly that on this hardware, since it is what `pulseOrange` shows while the device cannot find a *controller*. HA missing is the same condition one hop further up, and no rhythm in the scene colour can say "the fault is upstream". It runs as two throbs (500ms period against the 1s TTL floor; `runPulse` starts and ends dim, so the count is the readable part) after a 600ms hold of the listening ring — the wake word WAS heard, and the ack has to land before the fault or the two read as one signal. The hold costs the wake listener the same delay before it restarts.

**Start a device-local pulse ONCE per state, never once per attempt.** `OnDisconnected` fires at the top of every reconnect-loop iteration and again after each failed connect, and the handler used to cancel the running goroutine and start a new one at phase zero — mid-brightness, rising. So the ring ran ~two cycles and hard-cut back to the middle, at an interval that is not a multiple of the pulse period, which is why the jump landed somewhere different each time ("like a poorly repeating gif", reported 2026-08-29). `pulseKind` at the call site makes the restart idempotent; all three state callbacks run on the single `Run` goroutine, so it needs no lock. Phase is derived from elapsed time (`pulsePhase`), not a step counter, for `runPulse`'s reason: a step counter advances one step per tick however late the tick was, so the cycle stretches under load instead of skipping ahead within it.

Playback ring clearing waits for the device's `playback_stats` (`device.playback_done`), NOT a wall-clock estimate. The old estimate subtracted socket-write time — which completes near-instantly however slow the wire is — so it cleared the ring up to 6.1s early on exactly the links that needed longest. `playback_stats` is emitted once the audio channel drains after EOS, i.e. the real end of audio; the timeout is only a backstop for the report never arriving.

`server.go` maintains a `ledMode` (direction arc vs. system). System-level LEDs (controller commands, pulse animations) always win over the beamformer direction arc. ONE paint suppression is left in `SetLEDs`/`SetDirectionLEDs` — state is still recorded in `baseLEDs` so the ring can be restored:

- **The ring is NOT a mute indicator, and mute holds nothing back.** Mute
  lives on the button's own LED (sysfs gpio444), which is a GPIO rather than
  part of the ring driver — it cannot be overpainted, survives every LED-mode
  transition for free, and has reported mute in parallel since v2.9.5. So the
  twelve-LED ring belongs to whoever asks for it, which is what lets Home
  Assistant own it completely (`em_ring_light`).

  **The rule that went and its exception went together, and that is the part
  worth remembering.** Mute used to suppress every paint, so that a cancelled
  turn's LED cleanup could not clear the red ring. `linkDown` then had to be
  an exception to *that*: the device's own orange/white pulse had to paint
  THROUGH the mute suppression, because a muted device with no controller sat
  showing red, and red says "muted and working" — false, not merely less
  useful. Taking the ring away from mute removed the reason for both, and
  `suppressPaint` is down to one input. The exception looked like the
  load-bearing part right up until its cause was removed.

  **The cost is stated rather than hidden**: a glance at the ring no longer
  tells you the microphone is off. The red button LED does, it is the
  indicator stock FireOS uses, and it is the only one nothing can overpaint.

  **One paint site survived the removal and was found on a device, not by
  reading** (2026-09-10). The volume arc's expiry still painted a RED RING
  when muted — so a volume press on a muted device left the ring red, over
  whatever resting colour Home Assistant had set, with no TTL and nothing to
  clear it until the next paint. HA's own light entity read `off` for the six
  hours it was lit, because the paint went straight to the hardware and the
  entity was never told. That is the general hazard of removing a rule: the
  places that ENFORCED it are easy to find, and the places that merely
  ASSUMED it are not. `expireDisplay` is a named method now so a test can
  drive it without waiting out the 2s window.

  **The action and volume buttons still go inert while `linkDown`**, gated at
  the consumers in `cmd` rather than in the evdev binding, which is the
  portable hardware layer and knows nothing about sessions. **The MUTE button
  stays live**: the ADC mute is hardware and its button LED is a GPIO, so it
  is the one control that works with no controller at all — and making it
  inert would hand back a live mic on reconnect, since mute is persisted in
  `state.json`.
- **Volume arc** owns the ring for its 2s display window against *animations* — they repaint ~every 100ms and would otherwise stomp the arc within one frame. It does **not** outrank a deliberate action-button press: a dot release calls `CancelVolumeDisplay()`, which drops the hold so the listening frame paints (it deliberately does not repaint — the controller's frame lands within an RTT, and clearing to black would put a dark gap between the two). The arc is protection from repaint churn, not from the user. On expiry the ring repaints the latest `baseLEDs` frame (`onDisplayExpire` → `paintBaseLEDs`), handing back mid-animation. The arc shows only for physical volume button presses (v2.9.5): remote sets and the boot-time volume seed apply silently (`volumeController.Set` showRing flag). The mute-button LED is sysfs gpio444, active-high — not the gpio445 in Amazon's `libled_hal.so`, whose constant is off by one and whose pad is muxed away (stock drives the pin via the `/dev/mtgpio` ioctl; see `mute_button.go`).

## The emOS console password

`consolePassword` arrives on the config push and the firmware does exactly one
thing with it: writes `/data/local/etc/revoice/console.pw`
(`config.WriteConsolePassword`). It never checks it. **emOS's init reads that
file and puts the prompt in front of the shell**, because the console has to
work when the firmware is not running — which is precisely when someone needs
it.

Four things not to undo:

- **It is written to EVERY directory in `devicepaths.AllDirs()`, and an empty
  record removes every one of them.** This is the one record whose reader is
  not updated by the same OTA that updates its writer: a new init arrives only
  when somebody flashes a boot partition, so the firmware cannot know whether
  the init in front of it opens `/data/local/etc/revoice/` or the pre-rename
  `/data/local/etc/echomuse/`, and cannot upgrade it either. Writing both
  needs no answer from the other side — the same reason
  `em_devicepaths.write_dirs()` writes both when the CONTROLLER pushes to a
  device, and there could be no capability for it, because the reader is not
  on the wire.

  **The CLEARING direction is the dangerous one, and the reason this was worth
  fixing before anybody hit it.** Somebody clears a console password because
  it belongs to a PREVIOUS OWNER — that is what the wizard's clear exists for
  — so a removal that misses the path an old init reads reports success while
  leaving the new owner locked out by exactly the password the operation was
  for. Change detection therefore has to consider a write needed when *any*
  path disagrees, not just the current one; comparing against one file would
  report "no change" and leave the running init on a stale record for ever.

  Init's half is `open_record` in `emos/init/init.c`: current path first,
  legacy second, covering the mirror case of a new init in front of old
  firmware. `emos/init/pathcheck.c` drives the real function through all four
  combinations, and `device/internal/config/console_paths_test.go` holds the
  firmware side — including that the set really does contain more than one
  directory, or every assertion about "every path" would pass while proving
  nothing. `console.timeout` gets the identical treatment in the same change:
  splitting them would leave a device whose password migrated and whose
  timeout did not, which is a state nobody would think to look for. Filed as
  #94 and fixed before it was ever reached, since the fleet runs FireOS, where
  the console password is inert.
- **The field is a POINTER.** An empty record is the legitimate "no password"
  setting, so with a plain string plus `omitempty` a removal would be
  indistinguishable from a field nobody sent, and clearing the password could
  never reach a device. Same reason `DuckDb` is a pointer.
- **Written only when the content changes.** The config push repeats every
  setting on every reconnect, and this device runs for years on eMMC that
  cannot be replaced, so an unconditional write spends a flash write per
  reconnect to store bytes already there.
- **Written from the config handler in `control.go`, not through
  `OnConfigApplied`.** There is no in-process consumer for a callback to serve,
  and a callback nobody registers is a feature that silently does nothing.

Written to a temp file and renamed, so init can never read a half-written
record: a truncated one parses as unusable, which is read as NO password, and
would leave the console open exactly while it looked configured.

The record is `<iterations>:<salt hex>:<hash hex>`, already hashed by the
controller — no plaintext passes through the firmware. The rest of the design,
including why hashing is worth it when deleting the file defeats it, is in
`controller/CLAUDE.md`.

## cgo dependency

SpeexDSP C source (AEC) is vendored in `device/internal/aec/`. The compiler Docker image provides the ARM cross-toolchain. If adding new cgo dependencies, they must compile cleanly with the `revoice-compiler` image against the FireOS 5 sysroot.
