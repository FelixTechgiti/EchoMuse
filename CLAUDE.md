# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

EchoMuse repurposes Amazon Echo Dot Gen 2 (FireOS 5 / Android 5.1, codename "biscuit") as an open-source voice assistant satellite. Two components:

- **`device/`** — Go binary that runs directly on the rooted Echo Dot
- **`controller/`** — Python asyncio WebSocket server that manages devices, runs wake word detection, and proxies to a voice pipeline
- **`oww_forge/`** — standalone Docker batch trainer for custom openWakeWord models (synthetic TTS positives → augmentation → classifier head → `.onnx`). Not part of the controller; see `oww_forge/README.md`. Upstream pins in its Dockerfile are load-bearing (piper-sample-generator v2.0.0 flat layout; openWakeWord SHA with a `--convert_to_tflite` argparse patch). Models install via the dashboard (Config → Wake word → "+ Custom model" → `/api/oww_models/upload`) into `oww_models/` beside the SQLite DB; `owwModel` stores the file path for custom models. openwakeword keys predictions by filename *stem*, never the path — always score via `em_oww_models.prediction_key`

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
docker build -t echomuse-compiler compiler/
```

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
python -m pytest tests/        # needs: pytest numpy scipy — not the full requirements.txt
```

Controller tests cover the pure-logic modules only (`em_eq`, `em_scenes`, `em_oww_models`, `version`) — keep it that way unless you're prepared to pull openwakeword/aiohttp into the test environment. Both suites (plus `go vet`) run in CI on every push/PR (`.github/workflows/ci.yml`).

**Release:** pushing a `v*` tag triggers `.github/workflows/release.yml`, which builds the binary in the compiler image and attaches it to a GitHub release. **Tag with `git tag -a`** — the annotation message becomes the release body (`body_path` from `git tag -l --format='%(contents)'`), which is what the dashboard shows next to an available update. Write it for the person deciding whether to push firmware to a device they depend on: what changed, what to expect, anything required of them. GitHub's generated commit list is still appended below it. A lightweight tag yields an empty body and falls back to that list, which is a worse experience, not a broken one.

**Device/controller compatibility.** The two halves version independently, so any pairing can occur in the field. Two rules, both guarded by `tests/test_capabilities.py`:
- **Negotiate by capability, not version.** The device announces what it implements in its register message (`internal/client/control.go`: `mic`, `speaker`, `leds`, `led_anim`, `buttons`, `oww_shadow`); the controller reads `Device.capabilities` via properties like `led_anim_capable` / `oww_shadow_capable`. Never compare version strings — that puts release history in the controller and misjudges dev builds. A UI control whose feature the device lacks is shown **disabled with the reason**, never as a control that silently does nothing.
- **Degrade to old behaviour, never to a wrong answer.** Unknown JSON fields and message types are ignored both ways. Where a new field records a measurement, absence stores as **NULL, not 0** — old firmware reporting no `playback_stats` must not read as "zero underruns", and a device that cannot score wake words locally must not read as "scored and missed" (hence `turns.dev_shadow` alongside `dev_wake_score`).

## Versioning / releases

Device firmware and controller are versioned independently from the same repo:

- **Device**: plain `v*` tags (e.g. `v2.7.6`) → `release.yml` → GitHub Release with the `server` binary asset. The tag is embedded in the binary and compared against `firmware_ver` by OTA — don't change this scheme.
- **Controller**: `controller-v*` tags (e.g. `controller-v2.8.0`) → `controller-release.yml` → Docker image pushed to `ghcr.io/wilbowes/echomuse-controller` (`X.Y.Z` + `latest`, CPU-only, amd64). **No GitHub Release is created** — the OTA system's release polling (`em_api._fetch_latest_release`) filters for `v*` tags with a `server` asset, but controller releases stay out of the releases list entirely by design.

The controller's own version is resolved by `controller/version.py` (env `EM_CONTROLLER_VERSION` — baked into the image from the tag — then `git describe --match 'controller-v*'`, then `"dev"`). It's exposed at `/api/system/status` as `controller_version`, shown in the dashboard header, and reported to HA as the ESPHome project version.

`controller/docker-compose.yml` is the local dev/GPU build (`GPU=1` build arg swaps in onnxruntime-gpu); `controller/docker-compose.deploy.yml` is the user-facing compose that pulls the published image.

`device/tools/` contains standalone diagnostics (`capture_mics`, `bf_capture` + analysis scripts) for mapping the 9-channel mic array; they build inside the same compiler image.

## Running the controller

**Bare metal (Python 3.12):**
```bash
cd controller
cp .env.example .env   # fill in SERVER_IP
pip install -r requirements.txt
python em_controller.py
```

**Docker:**
```bash
cd controller
docker-compose up --build
```

Dashboard available at `http://<SERVER_IP>:8768`. WebSocket devices connect to port 8767.

Key env vars in `.env` (see `.env.example` for the full list):
- `SERVER_IP` — LAN IP advertised via mDNS (devices connect here)
- `OWW_MODEL` / `OWW_THRESHOLD` — OpenWakeWord model name and detection threshold
- `DEVICE_APPROVAL` — `strict` (admin must approve new devices) or `auto`

### Voice backend

The controller impersonates ESPHome voice satellites: one asyncio TCP listener per device on ports 16001+ (persisted in the device registry, never reused). Home Assistant's built-in ESPHome integration dials in and drives voice turns via Assist. Implemented in `em_esphome.py` on top of the protocol layer in `controller/esphome/` (`frame_protocol.py`, `satellite_server.py`, vendored aioesphomeapi protobufs in `esphome/vendor/`). Servers are created at startup for every approved device **and on demand** when a device approved after boot first connects (`_register_device_server` — idempotent on purpose: the startup loop and `device_connected()` race, first creation wins). HA naming: friendly name is `<label> Voice Assistant` (BT proxy: `<label> BT Proxy`); `project_name` carries `ESPHOME_DEVICE_MODEL` after the dot because HA displays that segment as the device Model, overriding DeviceInfo's `model` field. (A legacy `claracore` WebSocket backend was removed 2026-07-12 — ESPHome/HA is the only voice path.)

## Architecture

### Device → Controller protocol

Each device opens **three** WebSocket connections to the controller:

| Path | Direction | Purpose |
|------|-----------|---------|
| `/control` | bidirectional JSON | Registration, LEDs, mic_start/stop, button events, config push |
| `/data` | binary | Mic PCM frames in (0x01 header), speaker PCM frames out (0x02/0x03) |
| `/shell/{device_id}` | raw binary | Root shell proxy (demand-opened by device on `shell_open` command) |

Controller is discovered by the device via mDNS (`_emcontroller._tcp.local`).

### Device-link TLS + token auth

All three WS planes exist twice: plain on `SERVER_PORT` (8767) and TLS on `SERVER_TLS_PORT` (8770, `wss://`). `em_pki.py` generates a private CA + server cert on first start (persisted in `tls/` next to the SQLite DB; delete the dir to rotate — every device then needs a fresh credential push). The leaf's identity is the fixed DNS SAN `echomuse-controller` (`TLS_SERVER_NAME`, coupled with `tlsServerName` in `device/internal/client/tlscreds.go`) — never an IP, so the controller can move address freely. Certs are backdated 10y/valid 25y **and** the device clamps its verification clock to the firmware build time (`BuildUnix` ldflag): Echos boot with bogus clocks pre-NTP, and a device that can't connect can't fix its clock. Don't "normalise" either half of that.

Device behaviour (`tlscreds.go`): credentials live at `/data/local/etc/echomuse/{ca.pem,token}` (canonical path constant: `em_api.DEVICE_TLS_DIR`) and are **re-read on every dial**, so a push takes effect on the next reconnect, no restart. CA present + `tls_port` mDNS TXT property → dial wss; CA present but no TXT → plain with a warning (deliberate rollout fallback). The token rides as `X-EM-Token` on all three dials.

Controller enforcement (`_link_auth_ok`): presented-but-wrong token always rejects; stored-token-but-none-presented is allowed (the credential push itself rides the plain shell plane — rejecting there would deadlock the rollout) until `REQUIRE_DEVICE_TLS=1` flips the posture to TLS+token mandatory. Flip it only when every device shows `wss (TLS)` in the dashboard (Status tab "Link" row; `linkTls` in `/api/devices`).

Credential delivery: the provisioning wizard installs credentials over adb pre-first-contact (`POST /api/provision/tls_credentials` mints the token + pending device row from the serial); already-fleet devices get the dashboard **Secure link** action (`POST /api/devices/{id}/secure_link` — shell-plane file push, then a connection bounce to redial over wss).

### Device audio pipeline

Each mic buffer passes through, in order:

```
raw 9ch S24_3LE → beamformer + fixed mic gain (micGainDb, applied to 24-bit samples) → mono S16_LE → [AEC] → [AGC] → [VAD gate] → /data WebSocket
```

Note the real buffer cadence: GoTinyAlsa's `GetAudioStream` reads the whole ALSA buffer per chunk (PeriodSize 512 × PeriodCount 5), so the mic pipeline runs on **160ms batches of 2560 samples**, not single 32ms periods. Anything assuming 512-sample buffers must handle multiples (this silently disabled AEC for four releases — see `aec.Process`).

The always-on wake stream (`mic_start` without `lock_mic`) is **ungated and AGC-free**: every 32ms period is sent continuously (batched into 80ms frames) so openwakeword scores an uninterrupted stream, and no adaptive gain state can drift with room noise. The VAD gate and AGC apply only to bounded `lock_mic` turn streams (button-triggered), which get a fresh `ResetAGC()` per stream.

- **Beamformer** (`internal/beamformer/`) — selects the perimeter mic with the highest onset energy ratio (fast/slow EWMA) at voice turn start, then locks for the duration. Its `extractChannel` also applies the fixed mic gain (`micGainDb`, default +24dB) against the full 24-bit sample before quantising to S16 — captured speech sits at ~−70dBFS, so gain must happen pre-truncation to recover real resolution. `vadThreshold` stays in pre-gain units (the device scales it by the gain internally). **It is a selector, not a summing beamformer, and that is settled — do not propose delay-and-sum.** A frequency-domain implementation (exact FFT phase shifts, no interpolation artefacts) exists in `device/tools/bf_capture` and was measured as only marginally better than mic selection. The reason is the 72mm aperture, not the code: diffuse-field noise coherence is 0.84–0.99 below 1.5kHz where speech energy lives, so a sum has almost nothing uncorrelated to cancel, and 36mm adjacent spacing puts spatial aliasing at 4.76kHz — a working window of roughly 2–4.7kHz. Superdirective/differential beamforming is the only class that works at this aperture and it trades against white-noise gain (20dB+ amplification of sensor self-noise) on unmatched capsules across four ADCs. Full derivation and the coherence table are in SETUP.md's mic-array section. **Far-field reach is therefore not a beamforming problem here** — it is room noise floor, distance and placement; the single-channel levers (`nsAsr`, wake model) are the ones that exist
- **AEC** (`internal/aec/`) — speexdsp echo canceller (vendored C, SpeexDSP-1.2.1), whole mic path including the wake stream; far-end reference tapped at the speaker ALSA write (every period incl. silence), delayed by `aecDelayMs` — **keep 0**: the mic side's 160ms batch reads absorb the speaker's output latency, and higher values make the echo non-causal (zero cancellation). The mic ALSA ring is only 160ms deep, so >160ms capture stalls silently lose whole batches (~every 20–30s in steady state, load-correlated); an occupancy governor trims the resulting reference backlog **without resetting the filter** — the trim restores the alignment the filter converged against, and the reset that used to live there thrashed convergence to ≤5dB (the v2.7.8 fix). `[aec] att=`/`far:` telemetry logs ~1/s during playback; `[mic] clock/stall` lines track capture loss. Default off (`aecEnabled`); ~14dB per response, held across turns
- **Barge-in** (controller-side `_barge_watcher`) — wake word spoken during TTS cancels playback (device does a stateful `speaker_flush`: drains buffer + discards until stream EOS, since the rest of the stream is typically still in TCP buffers; controller-side, both `stream_speaker` and the post-playback drain sleep race `cancel_event`). `bargeInThreshold` is used as-is and sits *below* `owwThreshold` by design (0.05–0.10): echo at the mic is ~25dB louder than the person, so speech-over-TTS scores are depressed (~0.3–0.5 observed), while converged self-echo scores 0.002–0.003
- **AGC** (`internal/processor/`) — lock_mic turns only; release is frozen during silence (RMS speech flag), preventing noise floor amplification. (Device-side RNNoise NS was removed 2026-07-12 — noise suppression is controller-side now: `em_ns.py`/DTLN on the ASR-bound stream, per-device `nsAsr` flag)
- **VAD** (lock_mic turns only) runs on pre-NS/AGC audio; opens gate after `VAD_SPEECH_MS` of speech, closes after `VAD_SILENCE_MS` of silence, then sends an end-of-speech sentinel

### Controller audio pipeline

1. **Wake word** — openwakeword (ONNX) runs in a thread executor per device on `mic_queue`. When 2+ devices are connected, `em_arbiter.py` applies **first-detector-wins** suppression: the first device to cross threshold answers *immediately* (no added latency, the claim is synchronous) and any other device detecting within `wakeArbitrationMs` (default 700, 0 = off) stands down and logs "Wake ceded". The claim is released at turn end. Do NOT reinstate the original best-SNR-after-a-wait design: it taxed every wake ~364ms (it gated on devices *connected*, not in earshot) and field data showed SNR at detection was indistinguishable across devices (0.9/1.15/0.93) while the SNR winner produced a worse transcript than the first detector.
2. **Voice turn** — on wake or dot-button: drain stale frames → acquire `voice_lock` → stream mic to HA via the ESPHome satellite → receive TTS URL → fetch + ffmpeg-decode straight to 48kHz mono → EQ (`em_eq.py`) → stream back as 0x02 frames
3. **Media playback** (`em_player.py`) — the HA `media_player` entity accepts `play_media`/browse (PLAY_MEDIA+BROWSE_MEDIA feature flags): ffmpeg subprocess streams s16le/48k/mono, fed to the same 0x02 plane, paced to `LEAD_S`=**4.0s** ahead of realtime. That is sized against the device's own depth (`audioChanDepth` 128 periods × 42.7ms ≈ 5.46s) leaving ~1.4s headroom so the feed can never outrun `audioCh`. It was 1.5s until 2026-07-25, which left ~4s of hardware buffer unused and let measured 1.8-2.6s link stalls drain it into audible gaps. **The lead is NOT what makes pause/stop/voice-preempt instant** — `speaker_flush` drains the device buffer and the discard-until-EOS contract swallows what is still in TCP; the old comment misattributed that. Resume passes `-ss` before `-i`, an INPUT seek: ffmpeg does NOT ignore a seek it cannot perform, it decodes and discards until it reaches the timestamp, so a 173s bookmark on a non-seekable live stream (Music Assistant flow) is 173s of silence — a first-chunk deadline (`SEEK_STALL_S`) catches that and rejoins the live edge. Pause = speaker_flush + position bookmark, resume = ffmpeg `-ss` (live streams rejoin the live edge); teardown always EOSes the stream (flush-discard contract, same as barge-in). Voice turns and announcements `interrupt()` an active session and `resume_interrupted()` after. The feed must NOT set `device.speaking` (that makes the wake loop drop frames — deaf for a whole song); wake-over-music scores against `bargeInThreshold` when barge-in is enabled, same physics as barge during TTS. Music EQ runs through `em_eq.StreamingEQ` (chunk-carried filter state — per-chunk `apply()` would click at boundaries)
4. **Speaker** — the wire carries **mono** 48kHz; `_fetch_tts_audio` decodes at the wire rate (the satellite declares `supported_formats` 48k/mono/FLAC so HA transcodes at source when it can; ffmpeg resamples otherwise — no numpy resample step anymore). The device duplicates L=R at the ALSA write (stereo ALSA config is an I2S/codec constraint, not a wire one). Device buffers ~5.5s (`audioChanDepth`) and holds playback until ~1s is queued or EOS arrives (`primePeriods`) — WiFi-stall protection for marginal links

### Key Go packages

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
| `internal/wifi/` | Safe WiFi network change with auto-rollback (wifi_change/wifi_commit/wifi_scan control messages; pending-marker recovery at startup). Reload path is `svc wifi disable/enable` ONLY — see package comment for the hardware-proven constraints |
| `pkg/led/`, `pkg/mic/`, `pkg/speaker/`, `pkg/buttons/` | Hardware abstractions (interfaces) |

### Key Python modules

| File | Role |
|------|------|
| `em_controller.py` | WebSocket server, `Device` registry, voice pipeline, mDNS |
| `em_api.py` | aiohttp HTTP API + dashboard SPA, OTA, shell proxy |
| `em_db.py` | SQLite persistence (devices, config, logs, users) |
| `em_auth.py` | Session auth with bcrypt |
| `em_eq.py` | Parametric EQ applied to TTS audio before playback |
| `em_shadow.py` | On-device wake word shadow mode — correlates device-reported threshold crossings with the controller's own detections (clock domains, match window, consume-on-match) |
| `em_scenes.py` | LED ring scenes — resolves `ledScene`/`ledListenColor`/`ledThinkColor` config into render-ready listening/spinner frames |
| `em_esphome.py` | ESPHome-mode satellite servers (`EchoMuseSatellite`, `DeviceESPhomeServer`) |
| `em_arbiter.py` | Multi-device wake arbitration — pools same-utterance detections, best SNR answers |
| `em_player.py` | Media playback sessions — `media_player.play_media` → streaming ffmpeg decode → paced 0x02 feed; pause/resume/stop; voice preempts music (`interrupt`/`resume_interrupted`) |
| `em_config_sections.py` | Fleet-vs-device config scoping — the six sections, `STATE_KEYS`, and the merge that resolves a device's effective config |
| `em_recordings.py` | Utterance capture storage — WAVs in `recordings/` beside the DB, per-device file-count retention, ownership-checked path resolution |
| `em_ble_proxy.py` | BLE proxy ESPHome servers — a second, separate ESPHome device per Echo (own port from the shared counter, own mDNS, MAC = serial-derived with the locally-administered bit flipped). Forwards `ble_adverts` control messages from the device's passive scanner (`device/internal/bluetooth`, raw HCI over `/dev/stpbt`; enabling durably disables Android's BT stack) to HA as raw advertisements. Lifecycle = idempotent `reconcile()` driven by `bleProxyEnabled` |
| `esphome/` | ESPHome native API protocol layer (framing, handshake, vendored protobufs) |

## On-device wake word (shadow mode)

The Echo can run the wake model itself. `owwOnDevice` = `off` (default) or
`shadow`; a third mode letting the device *trigger* turns is deliberately not
implemented, and an unknown value normalises to `off` rather than being guessed
at — the two plausible guesses are "score silently" and "start triggering", and
one of those is a live behaviour change on a device that cannot honour it.

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
`shadow.DefaultDir` (`/data/local/share/echomuse/oww`, override `EM_OWW_DIR`)
— they are **not** in the firmware, since 12.3MB would double the OTA payload
and both A/B slots. Absence is an ordinary condition, logged once, and the
device carries on with controller-side wake word. Install with
`controller/tools/push_file.py`; `device/tools/oww_probe` verifies a device
reproduces Python and reports the real CPU cost. It costs ~38% of one core
permanently on top of the ~18-20% mic-pipeline baseline, so **enable it on one
device at a time**.

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

## Persistent activity stats

Every voice turn is persisted to SQLite at completion (`turns` table, `db.insert_turn` from `em_esphome`): trigger, wake model/score/threshold, room noise floor at detection, outcome, STT text, stage latencies, and playback underruns.

**Delivery instrumentation (schema v7, firmware v2.9.6+).** Underruns are rare and binary; these measure the *margin* on every stream so degradation is visible before it's audible. Device-reported in `playback_stats`: `min_depth` (fewest periods left in the device buffer mid-stream — the headline number), `prime_wait_ms`, `recv_span_ms` (first→last frame arrival; longer than the audio duration means delivery was slower than realtime), `max_gap_ms`, `bytes_recv`. Controller-measured: `send_ms`, `delivery_ms` (first frame sent → device's `playback_stats` arrival), `eq_ms`. **`send_ms` is a socket-write time and completes near-instantly however slow the link is — never read it as delivery; that mistake cost a whole investigation on 2026-07-20.** `device_metrics` gained link context (`link_speed_last/min`, `wifi_freq_last`, `wifi_bssid_last`, tx/rx byte and error sums) — band and BSSID matter because one SSID spanning 2.4/5GHz lets a device silently re-associate to a much slower radio. `event_loop_lag_monitor` tracks controller-side stalls (peak on `/api/system/status` as `loop_lag_peak_ms`); anything blocking the loop also delays speaker frames. The underrun count arrives asynchronously — the device reports `playback_stats` (periods + underruns) once per completed speaker stream, and the controller attaches it to `device.last_turn_id` (consumed on use so an announcement's report can't overwrite a turn's stats; NULL underruns = never reported, e.g. pre-v2.9 firmware). Two hourly rollup tables ride alongside: `wake_counters` (near-miss counts/max score, flushed through the existing 2s-rate-limited near-miss path; plus non-turn underruns) and `device_metrics` (CPU/RAM/storage/RSSI sums+extremes upserted per ~30s device stats report — averages computed at read). `Device.turn_history` is hydrated from `turns` on connect, so the dashboard Activity tab survives restarts. Read APIs: `/api/devices/{id}/turns` (raw, `limit`/`since`) and `/api/devices/{id}/activity?days=N` (per-day aggregates, per-wake-model rollups, counters, metrics — plot-ready). Keep instrumentation at this cost class: one insert per turn, one upsert per 30s/2s — nothing per audio frame. The v7 device counters honour this: per-period work is one `len(chan)` compare plus one `time.Now()` on a single-writer path (no locks, no allocation, no logging), all of it emitted on the *existing* `playback_stats` message. `wpa_cli` is the one exception that costs a process spawn, so `linkInfo()` caches it for 2 minutes rather than running per stats tick.

**Control-plane RTT (schema v9/v10).** The RF layer is OPAQUE on this hardware and its counters are worthless: the MTK driver leaves retry/discard/missed-beacon at zero in `/proc/net/wireless` whatever the link is doing, reports `NOISE=9999`, and there is no `iw` binary — so `tx_errors`/`tx_dropped`/`rx_crc` are STRUCTURALLY zero and `get_device_metrics` deliberately does not surface them (a zero there reads as "healthy link" and is not). RTT is the latency signal that works: the controller stamps each control-plane `ping` with a sequence id (every `PING_INTERVAL_SEC`=5s), the device echoes it, and RTT is computed against one monotonic clock — the device never stamps its own, because Echos boot with bogus clocks pre-NTP. Unsolicited keepalive pongs carry no id and are ignored rather than paired with whatever ping is outstanding. Samples aggregate in memory (`Device.record_rtt`/`drain_rtt`) and flush on the existing ~30s stats report, so the DB cost is unchanged; note this means **adding an RTT field needs `drain_rtt` updated as well as `record_device_stats`** — the relay guard in `tests/test_db_instrumentation.py` covers both sources. Excursions (≥`RTT_EXCURSION_MS`=200) are split by whether the device was busy at SEND time, and `rtt_samples_idle` is the denominator that makes the split meaningful: without it "every excursion was idle" is vacuous, since almost every sample is idle. Read API exposes per-state RATES, never raw counts. **Measured 2026-07-25: 15-35% of probes exceed 200ms on ALL THREE devices including one at −26dBm on its own AP, with idle and busy rates indistinguishable (Lounge 33.3% vs 36.5%) — which rules out signal strength, WiFi power-save and load contention alike. Still unexplained; next step is ICMP-vs-app-RTT to separate network from above-network.**

**Utterance recordings (schema v12).** Opt-in per device via `saveUtterances` (Config → Microphones): the mic audio streamed to HA for a turn is kept as a 16kHz mono WAV in `recordings/` beside the DB, playable and downloadable from each turn's row in the Activity tab (`GET /api/devices/{id}/turns/{turn}/audio`). Lets you hear what STT heard instead of inferring it from a bad transcript. Buffered in `_stream_mic_audio` **below the denoiser**, so the file is byte-for-byte the ESPHome wire payload — it first shipped tapped pre-NS, which answered "how good is the mic" but could not answer "why was the transcript wrong" on any device with `nsAsr` on, and that is the question people actually ask. **Keep the tap below NS**; if a raw comparison is ever wanted it belongs as a *second* file, not by moving this one. Capped at `MAX_UTTERANCE_BYTES` (30s), written in `_persist_turn` because the filename is keyed on the turn's rowid. Retention is a hard per-device **file count** (`em_recordings.KEEP_PER_DEVICE`=10) — much shorter than `TURN_RETENTION`, so **a non-NULL `audio_file` on an older row is a claim to check, not to trust**; every reader goes through `em_recordings.resolve`, which also re-checks that the file belongs to the device in the URL (the endpoint takes both from the path) and treats a missing file as an ordinary 404. Default OFF and it should stay that way: this is the only feature that writes recognisable speech to disk. `db.delete_device` unlinks a device's recordings explicitly — nothing cascades to the filesystem. Note the dashboard fetches the WAV via `API.blob` rather than an `<a href>`: sessions are Bearer-header-only, no cookie is ever set, so browser-initiated requests would 401.

## OTA update system

The device runs an A/B slot binary system:
- `/data/local/bin/server` is a symlink to either `server_a` or `server_b`
- `start_server.sh` counts fast exits (< 15s runtime); after 3 consecutive failures it flips the symlink to the other slot and exits, letting Android init restart with the fallback binary

OTA is triggered from the dashboard — the controller pushes the new binary via the `/shell` WebSocket.

Device-side payloads the controller distributes (`start_server.sh` via `/api/provision/start_script`; the debloat pair `debloat_packages.txt`/`echomuse-debloat.sh` via `/api/provision/debloat_packages`+`debloat_script`, applied by the wizard's Debloat step — pm hide list + Magisk service.d daemon stops) live canonically in `controller/device_payloads/` and are read from disk per request — never embed copies in `em_api.py` or `dashboard.jsx`. `device/scripts/start_server.sh` is a symlink into that directory. Every firmware OTA also syncs the device's `/data/local/bin/start_server.sh` against the canonical payload (`_sync_start_script` — md5 compare, heredoc push, rename into place; takes effect on next device reboot), so script drift heals fleet-wide without a separate update path.

**Every payload needs an update path, and `tests/test_deploy.py` enforces it** (a file in `device_payloads/` unreferenced by `em_api.py` fails CI). The debloat pair had none until 2026-07-30 and every fielded device needed a manual push. `_sync_debloat` also rides the OTA and reconciles **both** halves — the boot script by md5, and the `pm hide` list by asking the device which listed packages are still visible — because round 2 added a *package* and a script-only sync would have looked like it worked while changing nothing. It is additionally exposed as `POST /api/devices/{id}/debloat` (Updates tab → Maintenance), which is **required, not a convenience**: the OTA path cannot reach a device already on the latest firmware. Two traps in that reconcile, both of which produced confident wrong answers: match package names with `grep -qx` (whole line) — an unanchored `*package:$p*` also matches `package:$p.client` — and never treat `pm list packages -u` minus `pm list packages` as the hidden count, since it includes uninstalled packages.

`com.amazon.whad` is `PERSISTENT`: `pm disable` is ignored, **`am force-stop` is a no-op**, and `pm hide` does not stop a running instance — it stays until the next reboot, which is why the log line says so. Note RSS overstates the win ~6x (shared zygote pages): the measured recovery is ~20-35MB per device by `memUsedMb`, not the 62MB RSS suggests.

## Device config push

`config.ConfigMessage` JSON fields (camelCase) are sent from controller to device on connect and on per-device config change. Non-zero fields are applied; zero/nil fields are ignored (partial update). Changes take effect immediately — no restart required.

Configurable parameters: `vadThreshold`, `vadSpeechMs`, `vadSilenceMs`, `owwThreshold`, `owwModel`, `owwSpeexNs`, `adcDigitalGain`, `adcMicpga`, `micGainDb`, `startupVolume`, `beamAngle`, `beamformingEnabled`, `aecEnabled`, `aecDelayMs`, `aecTailMs`, `agcEnabled`, `nsAsr`, `bargeInEnabled`, `bargeInThreshold`, `bleProxyEnabled`, `eqBands`, `eqLoudness`, `ledScene`, `ledListenColor`, `ledThinkColor`, `meterAttack`, `meterDecay`, `meterFloor`, `meterGamma`, `meterRef`, `meterCurve`, `wakeArbitrationMs`, `owwOnDevice` and `saveUtterances` (the last two are controller-consumed for scoping purposes, though `owwOnDevice` IS acted on by the device; `saveUtterances` and `wakeArbitrationMs` are ignored by it).

### Fleet vs device scoping (schema v8)

Scoping is **per section**, not one boolean. `em_config_sections.py` is the single source of truth mapping each config key to one of six sections (playback, wakeword, microphones, ring, advanced, bluetooth); `devices.config_sections` stores the set a device overrides, and `get_effective_device_config` = fleet overlaid with the device's values for those sections only. `use_global_config` survives as a derived compat view (no sections == fleet) and must not be treated as authoritative.

Three invariants, each guarded by `tests/test_config_sections.py`:
- **The partition must stay total** — a new key in `DEFAULT_DEVICE_CONFIG` that belongs to no section can never be overridden and never renders. Add the key to a section in the same change.
- **`dashboard.jsx`'s `CONFIG_SECTIONS` mirror must match Python** — it is parsed as JSON out of the file, so keep it comment-free and double-quoted. Drift puts a control under a toggle that does not govern it.
- **`STATE_KEYS` (`startupVolume`) are never section-scoped** — persisted device state, always taken from the device, never fleet-inherited.

Reverting a section **discards** its stored values (`set_device_config_sections` prunes), so no shadow values resurrect on a later re-override. Both config write paths push the **effective** config via the shared `_apply_live_config`, never the request body — with per-section scoping a body is partial by design, and the fleet endpoint now pushes every connected device rather than only fully-inheriting ones.

### Volume / mute persistence

Volume is **state, not a setting** — it rides the config channel but has no dashboard control (the slider was removed 2026-07-25: `SeedVolume` ignores later pushes, so moving it did nothing until the device restarted and any real volume change overwrote it). It is listed in `em_config_sections.STATE_KEYS`, exempt from section scoping, and shown read-only on the Status tab.

Volume persists through reboots **controller-side**: every device `volume_state` report is stored into the device's `startupVolume` config, and the device restores it via `Server.SeedVolume` on the **first config push per run only** (later pushes must not stomp live changes). Until seeded (or a local volume change makes the device authoritative), the device suppresses its connect-time `volume_state` report — reporting the boot-default level is what used to clobber the stored value on reboot. Mute is the opposite: **device-sovereign**, persisted locally in `/data/local/etc/echomuse/state.json` (survives OTA slot flips; written on toggle, restored at boot pre-connect — ADC mute immediately, red ring/button LED after LED init).

## LED priority system

Turn-state ring colours (listening ring, thinking spinner) come from **LED scenes** (`em_scenes.py`), configurable per device (`ledScene` + custom colours). Firmware with the `led_anim` capability (v2.9+) **animates locally**: the controller sends one `led_anim` message per state change ({pattern: solid|spin|rotate|pulse|meter|off, colors, periodMs, ttlSec}) and the device renders frames on its own ticker (`internal/server/animator.go`) — controller/WiFi jitter can't judder the ring. `meter` throbs with the live speaker RMS (tapped at the ALSA write, so it tracks audible audio, not the ~5.5s-ahead send); its response curve is config-tunable (`meter*` keys → `AnimSpec` pointer fields → `resolveMeter`, which clamps independently of the dashboard ranges) because it is a taste parameter that needs iterating in a real room, not a firmware OTA per pass. `ttlSec` is bounded per phase — 30s listening, 135s spinner (**coupled to `_fetch_tts_audio`'s 60s timeout ×2 attempts, since the spinner spans HA think time AND the fetch — move one and move the other**), and computed per response for `meter` via `em_scenes.meter_ttl` so a long TTS cannot self-clear mid-answer. Loss-resilience: newer spec or raw `leds` frame atomically replaces the animation (generation counter), and `ttlSec` is a dead-man that self-clears the ring if the controller dies mid-turn. Legacy firmware falls back to controller-streamed frames. Controller `leds` messages carry an explicit `listening: true` flag on listening-ring frames — the device's direction overlay keys off it (pre-scene firmware inferred "listening" from an all-green ring, which breaks for any other scene; the heuristic remains as fallback for old controllers). The direction overlay brightens the base ring colour instead of painting green. Mute ring (red) and volume arc (cyan) are device-local and scene-independent by design.

Turn *outcomes* are distinguished by rhythm, not colour (red/orange/cyan are taken by mute/link/volume): `no_speech` gets one slow throb, `no_tts`/`tts_error`/`timeout` fast blinks, everything else ends silently. Both ride the existing `pulse` pattern with a 1s TTL so they retire on the device's own ticker — no follow-up message to lose. Driven by `device.last_turn_outcome` (set in `em_esphome._persist_turn`, consumed once by `_leds_turn_end`).

Playback ring clearing waits for the device's `playback_stats` (`device.playback_done`), NOT a wall-clock estimate. The old estimate subtracted socket-write time — which completes near-instantly however slow the wire is — so it cleared the ring up to 6.1s early on exactly the links that needed longest. `playback_stats` is emitted once the audio channel drains after EOS, i.e. the real end of audio; the timeout is only a backstop for the report never arriving.

`server.go` maintains a `ledMode` (direction arc vs. system). System-level LEDs (controller commands, mute ring, pulse animations) always win over the beamformer direction arc. Two paint suppressions in `SetLEDs`/`SetDirectionLEDs` (state is still recorded in `baseLEDs` so the ring can be restored):

- **Mute ring** (solid red) is device-sovereign — enforced since v2.7.8: controller LED writes are recorded but not painted while muted. Needed because muting now terminates an active turn (controller cancels + `speaker_flush` on `mute_state`), so the cancelled turn's LED cleanup arrives after the red ring is up.
- **Volume arc** owns the ring for its 2s display window against *animations* — they repaint ~every 100ms and would otherwise stomp the arc within one frame. It does **not** outrank a deliberate action-button press: a dot release calls `CancelVolumeDisplay()`, which drops the hold so the listening frame paints (it deliberately does not repaint — the controller's frame lands within an RTT, and clearing to black would put a dark gap between the two). The arc is protection from repaint churn, not from the user. On expiry the ring repaints the latest `baseLEDs` frame (`onDisplayExpire` → `paintBaseLEDs`), handing back mid-animation. The arc shows only for physical volume button presses (v2.9.5): remote sets and the boot-time volume seed apply silently (`volumeController.Set` showRing flag). The mute-button LED is sysfs gpio444, active-high — not the gpio445 in Amazon's `libled_hal.so`, whose constant is off by one and whose pad is muxed away (stock drives the pin via the `/dev/mtgpio` ioctl; see `mute_button.go`).

## cgo dependency

SpeexDSP C source (AEC) is vendored in `device/internal/aec/`. The compiler Docker image provides the ARM cross-toolchain. If adding new cgo dependencies, they must compile cleanly with the `echomuse-compiler` image against the FireOS 5 sysroot.
