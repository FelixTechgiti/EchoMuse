# Persistent Root on the Amazon Echo Dot Gen 2 (biscuit)

*A complete guide to rooting, SELinux bypass, Alexa removal, EchoMuse installation, working speaker audio, VAD, wake word detection, and mute button — without tethered boot*

---

> ### 👋 Looking for how to *set up* EchoMuse? Start with the [Quickstart](docs/quickstart.md).
>
> **This file is not the onboarding guide.** It is two things: the low-level
> reference for the one-time rooting of a Dot (the numbered steps below), and
> — in the **Changelog** at the very bottom — the project's **engineering
> journal**: a long-form record of what we built, what broke, and what we got
> wrong, kept in the open on purpose.
>
> It is written for someone with a soldering-iron-adjacent tolerance for
> detail, and it is very long. If you want to get a Dot talking, you want:
>
> | Start here | |
> |---|---|
> | **[Quickstart](docs/quickstart.md)** | Zero to talking to your Dot — install, first run, device approval, Home Assistant. **The actual onboarding guide.** |
> | [Configuration Guide](docs/configuration.md) | Every dashboard setting in plain language. |
> | [The Voice Pipeline, Explained](docs/voice-pipeline.md) | How your voice gets from the mics to Home Assistant and back. |
>
> The Quickstart sends you back here for the rooting step, and only that step.

---

The Amazon Echo Dot 2nd Gen (codename: biscuit) has a small but dedicated hacking community. Most existing guides stop at tethered root — you get a root shell, but only while the device is connected to a computer running a patched preloader. Every reboot requires the cable.

This guide goes further. By combining the persistent amonet unlock with a boot image patch and a pre-seeded Magisk grant database, you get **persistent root that survives reboots** — no cable required after setup. Then we go further still and get EchoMuse running as a proper init service with full hardware access including working speaker audio.

At the end you'll have:
- Full root via Magisk 17.3
- SELinux in permissive mode
- Alexa voice stack completely disabled
- EchoMuse running on boot with full LED, mic, button, and speaker control
- Working audio output via TinyALSA directly (card 0, device 23)
- On-device energy VAD streaming speech bursts to the server over WebSocket
- OpenWakeWord wake word detection ("Hey Jarvis") on the centre/omni mic
- Directional mic selection — best perimeter mic locked for each voice turn
- Hardware mute button with LED feedback and action button lockout
- WiFi wake lock preventing FireOS from suspending the wireless interface
- Orange LED pulse while disconnected from server
- Two-plane WebSocket architecture (control + data) with no inbound ports

---

## Background & Credits

This builds on the work of:
- **R0rt1z2** — [amonet-biscuit](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/) persistent unlock and TWRP
- **Dragon863** — [EchoCLI](https://github.com/Dragon863/EchoCLI) tethered root research
- **Binozo** — [GoTinyAlsa](https://github.com/Binozo/GoTinyAlsa) and original EchoGo SDK

The persistent unlock method (amonet-biscuit) is fundamentally different from the older tethered approach. EchoMuse replaces EchoGo with a WebSocket client architecture — no HTTP server on the device, no inbound ports, no ADB forward required for normal operation.

---

## Hardware

- Amazon Echo Dot 2nd Gen (RS03QR, 2016)
- Codename: biscuit
- SoC: MediaTek MT8163, quad-core ARM Cortex-A53 @ 1.5GHz
- RAM: 512MB
- OS: FireOS 5 (Android 5.1, API 22) or FireOS 6 (Android 7.2)
- MicroUSB cable required

---

## Prerequisites

- Linux or macOS machine with ADB and fastboot installed
- Python 3 (for boot image patching and Magisk DB creation)
- The following files downloaded and ready:
  - `amonet-biscuit-v1.1.0.zip` — from R0rt1z2's XDA thread
  - `update-kindle-csm_biscuit-272.6.8.0_user_680767620.bin` — FireOS 5 firmware
  - `f1r30s.zip` — ADB enablement patch
  - `Magisk-v17.3.zip` — from [GitHub](https://github.com/topjohnwu/Magisk/releases/tag/v17.3)
  - `server` — compiled EchoMuse binary (ARM, API 22)

> **Why Magisk 17.3?** Newer versions dropped support for Android 5.1 (API 22). 25.x installs but the daemon silently fails. 17.3 is the last version that works reliably on this device.

> **Linux ADB stability:** Linux aggressively power-manages USB devices by default, causing ADB disconnects. Disable autosuspend before starting: `echo -1 | sudo tee /sys/bus/usb/devices/*/power/autosuspend`. macOS doesn't have this problem.

---

## Step 1 — Update to FireOS 6.5.7.0

Before exploiting, the device must be on the latest FireOS 6.

On the device: **Settings → Device Options → About → Check for Updates**

Target: FireOS 6.5.7.0, build code `12383141252`. Keep updating until you land here.

> Once you're on the right version, work quickly — Amazon can push patches that break the exploit.

---

## Step 2 — Persistent Unlock, TWRP, and FireOS 5

Follow R0rt1z2's amonet-biscuit guide to install TWRP. This involves the kamakiri bootrom exploit and will modify your partition table — **it will wipe userdata**.

Once TWRP is running, flash FireOS 5 and the ADB patch:

```bash
adb shell twrp wipe data
adb shell twrp wipe cache
adb sideload update-kindle-csm_biscuit-272.6.8.0_user_680767620.bin
adb push f1r30s.zip /sdcard/
adb shell twrp install /sdcard/f1r30s.zip
adb reboot
```

The device should boot into FireOS 5 setup mode. ADB will be enabled.

---

## Step 3 — Patch the Boot Image for SELinux Permissive

This is the step that isn't documented anywhere else.

The Little Kernel (LK) bootloader hardcodes `androidboot.selinux=enforce` into the kernel command line — this is set before Android even loads, and it's what blocks every attempt to disable SELinux at runtime. You cannot `setenforce 0` as shell, you cannot `resetprop`, you cannot use `magiskpolicy`. The kernel won't let you.

The fix: we append `androidboot.selinux=permissive` to the boot image's own cmdline field. When both values are present in the kernel cmdline, permissive mode wins in practice on this device.

> **Note:** The `androidboot.selinux` value is a null-terminated ASCII string stored at a fixed offset (byte 64) in the Android boot image header, in a 512-byte field. We patch it directly rather than using magiskboot, which doesn't support cmdline modification on this version.

### From TWRP, extract magiskboot and pull the boot image:

```bash
adb shell 'mkdir -p /tmp/work /tmp/bin'
adb shell 'unzip /sdcard/f1r30s.zip bin/magiskboot -d /tmp/'
adb shell 'chmod 755 /tmp/bin/magiskboot'
adb shell 'dd if=/dev/block/other-boot of=/tmp/work/boot.img bs=1048576'
adb pull /tmp/work/boot.img boot_fresh.img
```

### Patch the cmdline on your Mac/Linux machine:

```python
python3 - <<'EOF'
with open('boot_fresh.img', 'rb') as f:
    data = bytearray(f.read())

cmdline_offset = 64
new_cmdline = b'bootopt=64S3,32N2,64N2 androidboot.selinux=permissive'

# Zero the full 512-byte field, then write new cmdline
data[cmdline_offset:cmdline_offset+512] = b'\x00' * 512
data[cmdline_offset:cmdline_offset+len(new_cmdline)] = new_cmdline

# Verify
print("New cmdline:", data[cmdline_offset:cmdline_offset+60])

with open('boot_patched.img', 'wb') as f:
    f.write(data)
print("Written to boot_patched.img")
EOF
```

Verify the output shows your new cmdline cleanly — no garbage bytes after `permissive`.

### Flash the patched image:

```bash
adb push boot_patched.img /tmp/work/boot_patched.img
adb shell 'dd if=/tmp/work/boot_patched.img of=/dev/block/other-boot bs=1048576'
adb reboot
```

### Verify:

```bash
adb shell getenforce
# Expected: Permissive
```

Check the kernel cmdline in logcat to confirm both values are present:

```
androidboot.selinux=permissive androidboot.selinux=enforce
```

Both appear — LK always appends its value after ours — but the device ends up in permissive mode.

---

## Step 4 — Install Magisk 17.3

With SELinux permissive, Magisk's daemon can now start and run properly.

```bash
adb reboot recovery
adb push Magisk-v17.3.zip /sdcard/
adb shell twrp install /sdcard/Magisk-v17.3.zip
adb reboot
```

Do **not** try `adb shell su -c id` yet — it will hang. The grant prompt requires a screen to approve, and the Echo Dot has no screen.

---

## Step 5 — Pre-seed the Magisk Grant Database

Magisk's `su` hangs on a screenless device because it's waiting for the user to tap "Grant" on a dialog that never appears. The fix is to create the policy database ourselves and push it before booting.

### On your Mac/Linux machine:

```python
python3 - <<'EOF'
import sqlite3
conn = sqlite3.connect('magisk.db')
c = conn.cursor()
c.execute('''CREATE TABLE IF NOT EXISTS policies
             (uid INTEGER, package_name TEXT, policy INTEGER,
              until INTEGER, logging INTEGER, notification INTEGER)''')
# uid 2000 = shell, policy 2 = always grant
c.execute("INSERT INTO policies VALUES (2000, 'com.android.shell', 2, 0, 1, 0)")
c.execute("INSERT INTO policies VALUES (0, 'root', 2, 0, 1, 0)")
conn.commit()
conn.close()
print("Done — magisk.db created")
EOF
```

### Push from TWRP:

```bash
adb reboot recovery
adb push magisk.db /data/adb/magisk.db
adb shell chmod 600 /data/adb/magisk.db
adb reboot
```

### Verify root:

```bash
adb shell su -c id
# Expected: uid=0(root) gid=0(root) context=u:r:magisk:s0
```

If you see `uid=0(root)` — you have persistent root. Reboot again and confirm it survives.

---

## Step 6 — Disable the Alexa Stack

With root, `pm disable` now works. Run these one at a time:

```bash
# Core Alexa voice pipeline
adb shell su -c 'pm disable amazon.speech.davs.davcservice'
adb shell su -c 'pm disable amazon.speech.sim'
adb shell su -c 'pm disable com.amazon.alexa.beaconbroadcaster'
adb shell su -c 'pm disable com.amazon.alexa.externalmediaplayer.fireos'
adb shell su -c 'pm disable com.amazon.wha.mediabrowserservice'

# Whisperjoin (Alexa device provisioning/cloud)
adb shell su -c 'pm disable com.amazon.whisperjoin.middleware'
adb shell su -c 'pm disable com.amazon.whisperjoin.wss.wifiprovisioner'

# Smart home and media agent (crash-loop after disabling above)
adb shell su -c 'pm disable com.amazon.device.smarthome.dshs.services'
adb shell su -c 'pm disable com.amazon.mediaplayeragent'

# WiFi management — only needed if you intend to reconfigure WiFi away from
# whatever network Alexa setup originally connected to. Both actively fight
# manual wpa_supplicant.conf edits by re-asserting their own saved network
# profile. See v2.5.0 changelog for the full investigation.
adb shell su -c 'pm disable com.amazon.android.service.wifiprofilemanager'
adb shell su -c 'pm disable com.amazon.device.smarthome.adapters.wifi'
# pm disable above does NOT stop the native SmartHomeWifid binary — it's
# launched by init via a property trigger chain, not as a normal package
# component. This durably prevents that trigger from ever firing:
adb shell su -c 'setprop persist.wifi.migrate.complete 0'
```

Reboot and check logcat. You should see "Unable to start service" messages for these packages — that's expected and harmless. No crash loops.

> **Keep `com.amazon.device.echoaudioservice` enabled.** This service initialises the MediaTek audio DSP at boot. Without it, the I2S clock never starts and audio playback will hang silently. You can disable Alexa's voice stack without touching this service.
>
> **What echoaudioservice actually does:** The APK is a stub (manifest only, no Java classes). It triggers `audio.primary.mt8163.so` (the MT8163 audio HAL) to initialise the DSP when Android starts the service. The HAL does all the real work — echoaudioservice is just the trigger.

---

## Step 7 — Disable WiFi Direct (p2p0)

The device has a WiFi Direct interface (`p2p0`) that interferes with mDNS multicast interface selection. It must be brought down before EchoMuse starts.

This is handled in `start_server.sh` — no manual action needed if you're following the full guide. If testing manually, run:

```bash
adb shell su -c 'ip link set p2p0 down'
```

---

## Step 8 — Install EchoMuse

EchoMuse runs as a Go binary on the device. It abstracts the hardware (mic, speaker, LEDs, buttons) and connects outbound to the EchoMuse controller over two persistent WebSocket connections (plus a demand-opened shell plane). There is no HTTP server on the device — no inbound ports, no iptables rules required.

### Set up the binary directory (A/B slots):

EchoMuse v2.4.4+ uses A/B slots: `server_a` and `server_b` with `/data/local/bin/server` as a symlink. This allows instant rollback without a binary transfer.

```bash
adb shell "su -c 'mkdir -p /data/local/bin'"
adb push server /sdcard/server
adb shell "su -c 'cp /sdcard/server /data/local/bin/server_a && chmod 755 /data/local/bin/server_a && ln -sf server_a /data/local/bin/server && chown root:root /data/local/bin/server_a'"
```

`server_b` starts empty. The first OTA update from the dashboard populates it.

### Create the startup script:

The canonical script is **`controller/device_payloads/start_server.sh`** in the repo (`device/scripts/start_server.sh` is a symlink to it) — the controller serves that exact file at `/api/provision/start_script` (this is what the provisioning wizard installs), read from disk per request. Don't hand-maintain a copy; earlier revisions of this document and of `em_api.py` embedded copies and they drifted.

```bash
# From the repo root:
adb push device/scripts/start_server.sh /sdcard/start_server.sh
adb shell "su -c 'cp /sdcard/start_server.sh /data/local/bin/start_server.sh && chmod 755 /data/local/bin/start_server.sh && chown root:root /data/local/bin/start_server.sh'"
```

> The script waits for `echoaudio` before starting — this ensures the audio DSP is initialised. `p2p0` is brought down to prevent mDNS interference. The WiFi wake lock prevents FireOS from suspending the wireless interface. All server output is logged to `/tmp/server.log` for debugging via `adb shell su -c 'cat /tmp/server.log'`.

> **Log cap (v2.7.1):** `/tmp` is RAM-backed and the script only ever appends — a background loop in the script checks every 5 minutes and, past 5MB, keeps the newest 512KB in `/tmp/server.log.1` and truncates `server.log` in place (the server's `O_APPEND` fd continues at the new EOF). Total log footprint stays bounded at ~5.5MB. A 45MB log was observed in the wild before this existed.

> The script runs the server as a subprocess (not via `exec`) so SIGTERM can be forwarded from Android init via the `trap`. If the binary exits in under 15 seconds three times in a row, the inactive A/B slot is restored via symlink and the script exits cleanly — init restarts it with the old binary. If the binary runs for ≥15s before crashing, the attempt counter resets (operational crash, not a deployment failure).

### Add EchoMuse and mixer service to the ramdisk:

The init scripts on FireOS 5 live in the boot image ramdisk. We need to unpack it, edit `init.csm.project.rc`, and repack.

Boot into TWRP:

```bash
adb reboot recovery
```

Extract magiskboot and unpack the boot image:

```bash
adb shell 'mkdir -p /tmp/work /tmp/bin'
adb shell 'unzip /sdcard/f1r30s.zip bin/magiskboot -d /tmp/'
adb shell 'chmod 755 /tmp/bin/magiskboot'
adb shell 'dd if=/dev/block/other-boot of=/tmp/work/boot.img bs=1048576'
adb shell 'cd /tmp/work && /tmp/bin/magiskboot unpack boot.img'
adb shell 'mkdir -p /tmp/ramdisk && cd /tmp/ramdisk && cpio -idv < /tmp/work/ramdisk.cpio 2>/dev/null | tail -3'
```

Pull the init script and edit it on your machine:

```bash
adb pull /tmp/ramdisk/init.csm.project.rc init.csm.project.rc
```

Append the following two service blocks to the end of `init.csm.project.rc`. The `mixer` stub must come first — EchoMuse's speaker Init() calls `stop mixer` as its first step:

```
service mixer /system/bin/sh
    oneshot
    disabled
    user root

service echomuse /data/local/bin/start_server.sh
    user root
    group root system
    class late_start
```

Push back, fix permissions, repack and flash:

```bash
adb push init.csm.project.rc /tmp/ramdisk/init.csm.project.rc
adb shell 'chmod 750 /tmp/ramdisk/init.csm.project.rc'
adb shell 'cd /tmp/ramdisk && find . | cpio -o -H newc > /tmp/work/ramdisk.cpio'
adb shell 'cd /tmp/work && /tmp/bin/magiskboot repack boot.img'
adb shell 'dd if=/tmp/work/new-boot.img of=/dev/block/other-boot bs=1048576'
adb reboot
```

### Verify:

After full boot (allow ~90 seconds):

```bash
adb shell "su -c 'getprop init.svc.echomuse'"
# Expected: running

adb shell "su -c 'cat /tmp/server.log'"
# Expected: Initializing... Ready... mDNS browsing...
```

---

## Step 9 — Configure Audio for Speaker Playback

The ALSA mixer is initialised with incorrect defaults — the external speaker amp and DAC are disabled. Without fixing this, tinyplay will open the PCM device and hang silently. This is handled automatically by `start_server.sh`, but it's useful to understand and test independently.

### Understanding the audio hardware

The biscuit uses a MediaTek MT8163 SoC with a TLV320AIC32x4 external codec. Speaker playback goes through ALSA card 0, **device 23**, at 48kHz stereo S16_LE, period size 2048, period count 4.

The mixer has 239 controls. Three are wrong at boot:

| CTL | Name | Default | Required |
|-----|------|---------|----------|
| 5 | Ext_Speaker_Amp_Switch | Off | **On** |
| 56 | Audio_I2S1_Setting | Off | **On** |
| 64 | HP DAC Playback Switch | Off Off | **On On** |

### Test audio manually:

```bash
adb shell "su -c 'tinymix -D 0 5 On && tinymix -D 0 56 On && tinymix -D 0 64 1 1 && tinymix -D 0 61 100 100'"
```

Generate a test tone and play it:

```python
python3 - <<'EOF'
import struct, math
rate=48000; dur=2; freq=440
samples=[int(32767*math.sin(2*math.pi*freq*i/rate)) for i in range(rate*dur)]
stereo=[]
for s in samples: stereo.extend([s,s])
with open('/tmp/test48s.wav','wb') as f:
    f.write(b'RIFF')
    f.write(struct.pack('<I', 36+len(stereo)*2))
    f.write(b'WAVEfmt ')
    f.write(struct.pack('<IHHIIHH', 16, 1, 2, rate, rate*4, 4, 16))
    f.write(b'data')
    f.write(struct.pack('<I', len(stereo)*2))
    for s in stereo: f.write(struct.pack('<h', s))
print('done')
EOF
adb push /tmp/test48s.wav /data/local/tmp/test48s.wav
adb shell "su -c 'tinyplay /data/local/tmp/test48s.wav -D 0 -d 23 -p 2048 -n 4'"
```

You should hear a clean 440Hz tone.

---

## Step 10 — Server Setup

EchoMuse connects to the controller via mDNS discovery. The controller must be running and advertising before the device boots (or the device will retry with exponential backoff until it finds it).

### mDNS advertisement

The controller advertises `_emcontroller._tcp.local` on port 8767 (with a `tls_port` TXT property pointing at the wss listener on 8770 once the PKI is up). The controller container runs with `network_mode: host` so multicast reaches the LAN.

**Proxmox note:** If running in a Proxmox LXC, the bridge requires the mDNS multicast MAC to be added manually:

```bash
# On Proxmox host
ip maddr add 01:00:5e:00:00:fb dev vmbr0
# Add to /etc/network/interfaces for persistence:
# post-up ip maddr add 01:00:5e:00:00:fb dev vmbr0
```

### Verify discovery from a Mac:

```bash
dns-sd -B _emcontroller._tcp local
# Expected: clara._emcontroller._tcp appears
```

---

## End State

```
✅ Persistent unlock (amonet-biscuit)
✅ TWRP installed
✅ FireOS 5 (Android 5.1)
✅ SELinux permissive — survives reboots
✅ Magisk 17.3 — persistent root, survives reboots
✅ Alexa voice stack disabled
✅ echoaudioservice retained (required for audio DSP init)
✅ EchoMuse running as init service on boot (exec mode, no crash loop)
✅ Dummy mixer service for EchoMuse init compatibility
✅ Audio mixer configured at boot (tinymix in start_server.sh)
✅ Mic gain equalised across all four ADCs — digital volume 88, MICPGA 40
✅ WiFi wake lock — FireOS cannot suspend wireless interface
✅ p2p0 (WiFi Direct) disabled — no mDNS interference
✅ Full LED ring RGB control (IS31FL3236A, 12 RGB LEDs)
✅ Microphone streaming (9 channels, S24_3LE, 16kHz, card 0 device 24)
✅ Speaker audio working (card 0, device 23, 48kHz stereo, period 2048 count 4)
✅ Button events (evdev)
✅ WiFi working
✅ Stable boot
✅ No HTTP server on device — no inbound ports, no iptables rules
✅ Three outbound WebSocket connections (control + data + shell planes)
✅ Device identity via ro.serialno — stable across reboots, matches adb devices
✅ Device approval flow — strict mode (pending) or auto mode
✅ Orange LED pulse while disconnected / searching for server
✅ Slow white LED pulse while pending controller approval
✅ On-device energy VAD — VAD end signal (0x04) sent to controller on silence
✅ Wake word detection on ch6 (centre/omni mic) — equidistant, no directional bias
✅ OpenWakeWord — "Hey Jarvis" detected server-side (threshold 0.3)
✅ Mic channel mapping confirmed empirically (tone injection, analyse_capture.py)
✅ Directional mic selection — best perimeter mic locked at voice turn start
✅ Direction estimation — onset ratio (fast/slow EWMA) robust to background noise (TV etc.)
✅ LED direction overlay — light green segment on listening ring during voice turn only
✅ LED mapping calibrated — LED 0 at 240°, confirmed from volume sweep
✅ Audio processing pipeline — speexdsp AEC (v2.7.3) + AGC; device RNNoise removed 2026-07-12, NS is controller-side DTLN on the STT stream (`nsAsr` flag)
✅ AGC applies to lock_mic turns only since v2.7.0 (wake stream is permanently AGC-free)
✅ Ungated continuous wake stream (v2.7.0) — no VAD gate/AGC/preroll on the always-on stream; OWW scores uninterrupted audio; ~32KB/s per device
✅ Mic stream leak fixed (v2.7.0) — ownership check in streamMic exit; stop/start pairs can no longer leak a concurrent duplicate stream (historical "wake degrades over days, reboot fixes it" root cause)
✅ Per-room noise floor tracking (v2.7.0, controller) — measurement-only asymmetric EWMA; drives the SNR-relative 5s no-speech cutoff (wake-then-silence closes quietly again)
✅ Mid-stream beam lock (v2.7.0) — beam_lock/beam_unlock control messages; wake turns get perimeter mic selection without a stream restart
✅ Beamformer lock-back selection (v2.7.2) — Lock() scores directions over a ~2s energy-history ring covering the wake word, not the decayed present (see pipeline state table)
✅ Acoustic echo cancellation (v2.7.3, working since v2.7.7, convergence holds since v2.7.8, default OFF) — speexdsp canceller on the whole mic path; reference tapped at the speaker ALSA write. Keep aecDelayMs at 0 (measured; higher values are non-causal — see v2.7.7). Converges to ~14dB per response and *stays* converged across turns since v2.7.8 (governor trims no longer reset the filter); `[aec] att=` and `[mic] clock/stall` telemetry in the device log show live attenuation and capture health. Enable from the dashboard Microphones advanced section
✅ 24-bit fixed mic gain (v2.7.1) — `micGainDb` (default +24dB) applied to the full 24-bit sample during S16 extraction; recovers the low byte the old truncation discarded (speech was ~3–20 LSB in 16-bit). Validated: STT empty-transcript rate went from 6/19 turns to 0/5, detection rms 0.0003 → 0.006–0.009, clipped=0
✅ PTY dashboard shell (v2.7.1) — device allocates a real pseudo-terminal (mksh prompt, line editing, top/vi, resize); dashboard terminal is xterm.js; programmatic sessions (OTA) keep the raw pipe
✅ /tmp/server.log size cap (v2.7.1) — trim loop in start_server.sh, bounded at ~5.5MB; VAD diag slowed to ~10min with prompt clip-count reporting
✅ State-aware landing page (v2.7.1) — / shows first-run setup (amber ring) or login (green ring) and redirects authenticated visitors to /dashboard; sessions in localStorage
✅ HA-driven conversation continuation — continue_conversation flag wired; after TTS playback, re-triggers voice turn immediately if HA sets flag in INTENT_END (v2.6.4)
✅ Speaker audioChanDepth 32 — prevents mid-stream underrun stutter on longer TTS responses (v2.6.4)
✅ Dashboard offline IP display — shows last known IP with "(last seen)" annotation when offline; suppresses Docker-NAT 127.0.0.1 artefact (v2.6.4)
✅ Per-turn structured trace — [TURN] log line with full stage timing at turn end
✅ OWW near-miss visibility — scores > 0.05 logged at INFO (rate-limited 1/2s per device), persistent counter on dashboard status tab (v2.6.5)
✅ VAD threshold tunable down to 0.0001 (dashboard slider floor corrected)
✅ Beamformer structural fix — smoothers always run, output by lock state not flag
✅ AGC release frozen during silence — prevents noise floor amplification past VAD threshold
✅ Acoustic feedback fix — controller sleeps for audio duration after EOS before mic restart
✅ Spinner runs for full response duration — duration calculated from PCM length
✅ VAD threshold default 0.001 — matches measured conversational speech range at 1.3m (v2.6.5; was 0.003, which sat above soft speech)
✅ Mute button — toggles mic mute, red LED ring, blocks action button
✅ Volume buttons — local interception, cyan LED ring feedback
✅ Amp boot click suppressed — mute → clock DAC with silence → amp on → unmute ordering in pcm_speaker.go Init (fixed order 2026-07-10)
✅ Amp idle hiss eliminated — graceful SIGTERM shutdown mutes + disables amp (PcmSpeaker.Close); start_server.sh repeats amp-off after every server exit as SIGKILL/panic backstop
✅ LED thinking spinner — triggered by THINKING signal from voice server
✅ Preroll discard — first frames of mic stream discarded to avoid wake word bleed-through
✅ Speech threshold — quiet recordings discarded without hitting Whisper
✅ OWW suppressed during speaker playback — prevents false wake triggers on own voice
✅ Stale mic queue drained after voice turn — prevents immediate re-trigger
✅ Config pushed from controller on connect — VAD/OWW params applied at runtime
✅ Device logs streamed to controller over control WebSocket
✅ Mute state change notifications — device sends mute_state message to controller
✅ Shell access — device dials outbound to controller on shell_open, no inbound ports
✅ OTA updates via controller dashboard — A/B slot system, local binary upload, instant rollback (symlink flip, no transfer)
✅ Auto-rollback on device — start_server.sh retries 3× before flipping to inactive slot; works without controller
✅ 8-band parametric EQ (controller-side, SVG frequency response curve, live updating)
✅ Wake word model hot-reload without device reconnect
✅ Hardware resource monitoring — CPU%, RAM, storage, WiFi RSSI every 30s; dashboard signal bars
✅ Voice server turn timeout (45s) — controller never hangs on unresponsive voice server
✅ Boot logging to /tmp/server.log
✅ mDNS via grandcat/zeroconf — RFC 6762/6763 compliant, reliable discovery
✅ WebSocket protocol keepalives — dead connections detected within 30s
✅ Controller management dashboard — React SPA, vendored assets, no CDN dependency
✅ Safe per-device WiFi change (dashboard WiFi tab) — device-side executor with auto-rollback: full wpa_supplicant.conf replacement written *while WiFi is disabled* + verified `svc wifi` bounce (via sh — the script has no shebang), gated on associate-to-target-SSID ≤45s → IP ≤20s → controller reconnect ≤90s; any failure restores the backed-up config; uncommitted changes roll back on boot (pending-marker recovery, same philosophy as the A/B binary slots); result delivery is at-least-once (re-sent until the controller's wifi_commit ack); last-known-controller-address fast path makes cross-subnet controllers reachable without mDNS. All three paths hardware-validated 2026-07-11: rollback (garbage SSID, 65s round trip), startup recovery, happy path (30s)
✅ LED ring scenes (controller-rendered) — Standard/Airy/Malevolent/Pride/Custom palettes for the listening ring and thinking spinner (em_scenes.py); mute ring stays red and volume arc stays cyan in every scene; frames carry an explicit `listening` flag so the device's direction overlay works on any colour (falls back to the all-green heuristic for old controllers), and the overlay brightens the scene colour instead of painting green
✅ Dashboard live state — mute/listen/speak/offline via WebSocket events + 5s poll
✅ Dashboard shell terminal — browser-based root shell, Ctrl+C support
✅ ESPHome native API satellite integration (the only voice backend since 2026-07-12)
✅ Both devices registered in HA as voice satellites (port 16001, 16002)
✅ ESPHome setup wizard passes on both devices
✅ TTS announcements via HA Assist pipeline (MP3→PCM via ffmpeg, standalone play)
✅ MediaPlayerState ANNOUNCING/IDLE transitions for wizard audio test
✅ ESPHome port lifecycle — ports up/down with physical device connect/disconnect
✅ mDNS _esphomelib._tcp per device (device_id[-12:] suffix to avoid prefix collision)
✅ DB migration v2 — esphome_api_port, esphome_noise_psk columns, next_esphome_port
✅ ~~VOICE_MODE env var~~ — claracore backend removed 2026-07-12; esphome is unconditional
✅ OWW/button-triggered voice turns in esphome mode — full wake word → STT → intent → TTS → speaker round-trip confirmed working end-to-end against real HA Core 2026.6.4
✅ HA-side announce (setup wizard test, push TTS) plays correctly on device — live callback lookup, not a snapshot taken at connect
✅ Local no-speech timeout (5s) — matches Alexa's "wake word, then silence" behaviour; scoped correctly to bounded voice turns only, never the permanent OWW listening stream
✅ HA VAD-end is the turn endpointing authority — _stream_mic_audio exits on HA's STT_VAD_END/ERROR, device RMS-gate sentinel advisory, 20s hard cap; fixes stuck spinner in noisy rooms (v2.6.5, C1)
✅ Conversation continuation actually works — mic restarted before each continuation turn; shipped broken in v2.6.4 (v2.6.5, C2)
✅ Preroll discard wake-turns-only — button/continuation turns pass 0, no first-word clipping on those paths (v2.6.5, C3)
✅ Mute is device-authoritative — mute stops the running mic stream, unmute restores it; audio stops leaving the device while the ring is red (v2.6.5, C5 partial — full-chip ADC mute pending)
✅ OWW speex NS toggle (owwSpeexNs) — openwakeword's 16kHz-native speexdsp suppressor on the wake path only, dashboard/API/DB wired, off by default (v2.6.5, Q1)
✅ Device preroll ring — ~512ms of pre-gate audio flushed on VAD gate open; fixes onset splice that depressed OWW scores and clipped first phonemes (v2.6.5)
✅ AGC reset at every mic stream start + mic stopped before TTS playback — TTS-echo-crushed gain can't poison the next turn; enabled AGC re-enable (v2.6.5)
✅ Speaker EOS vs underrun disambiguation — 0x03 EOS sets EndStream(), natural drain no longer logged as underrun (v2.6.5)
✅ Mic queue overflow drops oldest frame, not newest — audio tail stays contiguous with real time (v2.6.5)
✅ voice_queue drained before oww_paused routing flip — stale ambient frames no longer bleed into the next turn as STT preamble (v2.6.5 regression fix)
✅ ADC mute controls identified for all four chips — tinymix dump in device/tools/ confirms B–D at 123/124, 141/142, 159/160
```

**HA MVP reached** — this is the milestone ESPHOME_SPEC.md §1 called "the last functional barrier before a public v1 announcement." EchoMuse devices work as real Home Assistant voice satellites without ClaraCore.

---

## Mic Array Architecture

The biscuit has a 7-microphone array captured on ALSA card 0, device 24 as 9 channels S24_3LE at 16kHz. Ch7 and Ch8 are unconnected.

```
Ch0 → MK1 → 330°  (11 o'clock)  perimeter   ← confirmed empirically 2026-05
Ch1 → MK2 →  30°  ( 1 o'clock)  perimeter
Ch2 → MK3 →  90°  ( 3 o'clock)  perimeter
Ch3 → MK4 → 150°  ( 5 o'clock)  perimeter
Ch4 → MK5 → 210°  ( 7 o'clock)  perimeter
Ch5 → MK6 → 270°  ( 9 o'clock)  perimeter
Ch6 → MK7 → centre              omnidirectional
Ch7, Ch8 → unconnected
```

**Mapping confirmed** by tone injection testing (2026-05): phone speaker pressed against each mic hole in turn, per-channel RMS measured via `analyse_capture.py`. Previous documentation had Ch0/Ch1 swapped — corrected.

**ADC architecture:** Four TLV320ADC3101 stereo ADCs (I2C bus 0, addresses 0x18–0x1b). Probe order at boot determines channel assignment: 0x18→Ch0/1, 0x19→Ch2/3, 0x1a→Ch4/5, 0x1b→Ch6/7. All chips share a TDM data bus (confirmed from PCB trace analysis — DOUT shared, not daisy-chained). Array radius: 36mm (confirmed from PCB measurement).

**Why ch6 for wake word?** The centre mic is equidistant from all directions. OWW receives consistent audio regardless of where you're standing, and ambient sounds cannot lock it to a suboptimal direction. Perimeter mics are directional by proximity — good for STT once direction is known, but wrong for always-on wake word detection.

**Why directional mic selection for voice turns?** The mic physically closest to the speaker has the best SNR for that speaker. Selecting it at voice turn start (after wake word or button press) locks in the optimal channel for the duration of the turn. The lock happens at `mic_start` with `lock_mic: true` — not during ambient VAD activity — ensuring ambient sounds before the turn don't influence selection.

**Why mic selection rather than delay-and-sum?** At speech frequencies (<2kHz), a 72mm array has insufficient angular resolution to reliably discriminate between the 6 candidate directions. More critically, the maximum inter-mic delay is ~3.3 samples at 16kHz — requiring sub-sample fractional delay interpolation that introduces frequency-dependent phase errors causing comb filtering. Directional mic selection avoids all phase math and produces clean output.

**Frequency-domain beamforming** (implemented in `bf_capture` diagnostic tool): A frequency-domain delay-and-sum implementation exists applying exact phase shifts via FFT. Testing confirmed the approach works — flat spectral response, no interpolation artefacts. For voice pickup at typical conversational distances the SNR improvement over mic selection is marginal; mic selection remains the production path. The `bf_capture` tool is retained for future research.

**Why that result is structural, not an implementation shortfall.** Recorded 2026-07-29 after the frequency-domain result was very nearly re-proposed as a fix for far-field reach: the note above says the gain was marginal, but not *why*, and without the why it reads like something worth another go. It isn't.

Delay-and-sum improves SNR only against noise that is **spatially uncorrelated** between the mics. In a diffuse (reverberant room) field the coherence between two mics separated by *d* is `sinc(2fd/c)`, and at this aperture that stays near unity right across the speech band:

| Freq | Aperture/λ (72mm) | Coherence @36mm (adjacent) | @72mm (opposite) |
|---|---|---|---|
| 300 Hz | 0.06 | 0.99 | 0.97 |
| 500 Hz | 0.10 | 0.98 | 0.93 |
| 1 kHz | 0.21 | 0.93 | 0.73 |
| 2 kHz | 0.42 | 0.73 | 0.18 |
| 4 kHz | 0.84 | 0.18 | −0.16 |

Below ~1.5kHz — where most speech energy is — every mic is hearing between 84% and 99% the *same* noise, so there is essentially nothing for a sum to cancel. Useful decorrelation only begins around 2kHz, and the 36mm adjacent spacing puts the spatial aliasing limit at `c/2d` = **4.76kHz**. That leaves a working window of roughly 2–4.7kHz, which is exactly why the measured improvement was marginal. It is a property of the 72mm aperture, not of the algorithm or the code: no better delay-and-sum implementation changes it.

The class of algorithm that *does* extract directivity from a sub-wavelength aperture is **superdirective / differential** beamforming (and is presumably close to what the XMOS front-end in purpose-built far-field kit is doing). It is not a free upgrade: superdirective designs trade directly against **white noise gain**, amplifying uncorrelated sensor self-noise by 20dB or more at low frequencies on a 0.1λ aperture, and they need per-element magnitude and phase calibration to come anywhere near theory. Seven MEMS capsules spread across four unmatched TLV320ADC3101s, on a CPU already at ~18–20% baseline just running the mic pipeline, is not the substrate for that. Filed as a research curiosity, not a roadmap item.

**Practical consequence for far-field reach:** it is not a beamforming problem on this hardware, and not recoverable by a config change either — the ch6-vs-best-perimeter SNR difference is negligible at conversational distance (see the `beamformingEnabled` note in `em_db.py`). Reach here is set by the room's noise floor, distance and placement. The 2026-07-29 utterance analysis measured 8.7dB of noise-floor drift between two runs of the *same phrase* at ~1.3m, enough on its own to flip the transcript. The levers that genuinely exist are single-channel: `nsAsr`, wake model choice, and moving the device.

**How Amazon does it:** Amazon's `amazon.speech.sim` reads the same raw 9-channel array via Android AudioRecord and does software processing. There is no hardware beamforming output channel. The MediaTek MAGI Conference DOA feature (in `audio.primary.mt8163.so`) is designed for phone call use cases and is not active in voice assistant mode on this device.

---

## Mic Array — What Actually Happens at Each Stage

This describes the pipeline as of v2.9.4 (2026-07-18; originally written for v2.7.1): the wake stream is **ungated and AGC-free** — the device streams continuously, and all adaptation lives controller-side as measurement. The only gain in the path is the fixed 24-bit mic gain (v2.7.1). Device-side NS is gone entirely (RNNoise removed 2026-07-12; noise suppression is now controller-side DTLN on the speech-to-text stream only, per-device `nsAsr` flag), and speexdsp AEC (v2.7.3+) sits in the mono path when enabled. One cadence correction to the numbers below: GoTinyAlsa delivers whole ALSA buffers, so the mic loop actually runs on **160ms batches of 2560 frames** (69120 raw bytes), not single 512-frame periods. The stages are in order from hardware to HA.

Why the gate came out (2026-07-06 rework): the VAD gate's absolute RMS threshold is wrong in at least one room of every home, openwakeword is a streaming model that scores best on continuous audio (gated bursts spliced together measurably depress scores even with preroll), and the AGC's persistent gain state on a never-restarting stream rebaselined itself to each room's noise floor — the "wake word degrades over days, reboot fixes it" disease. Bandwidth was the reason for the gate and it doesn't survive arithmetic: 16kHz mono S16 is 32KB/s per device, 6× smaller than the TTS playback stream.

### Idle — waiting for wake word

```
ALSA card 0 device 24 (9ch S24_3LE 16kHz)
  → pcm_microphone.go subscriber channel (raw 13824-byte periods at ~31ms intervals)
  → beamformer.Process(raw, beamAngle, gain)
      — unlocked (idle): always returns ch6 (centre/omni mic)
      — smoothers still update every period (baseline stays warm;
        energy ratios are gain-invariant)
      — fixed mic gain (micGainDb, default +24dB) applied to the FULL
        24-bit sample during S16 extraction (v2.7.1) — the old path took
        the upper 2 bytes and threw away the low byte, where nearly all
        of the signal lives at this hardware's capture levels (speech
        ≈ −70dBFS raw). Clipped samples are counted and reported.
      — returns mono S16_LE 512 samples
  → vadPeriodRMS(mono) — computed for the periodic diagnostic log only
      (every ~10min, or within ~16s of a clipped sample — v2.7.1);
      does NOT gate sending on this stream (v2.7.0)
  → aec.Process(mono) — speexdsp echo cancel against the speaker's own
      output (v2.7.3; no-op while aecEnabled=false; ~14dB when converged)
  → AGC: NEVER on the wake stream (v2.7.0 — forced off regardless of config;
      adaptive gain state on a permanent stream is a rebaselining mechanism
      by construction). agcEnabled config now applies to lock_mic turn
      streams only.
  → EVERY period sent — batched into 80ms chunks, ~12.5 frames/s, 32KB/s:
      frame: [0x01][seq_hi][seq_lo][2560 bytes PCM = 80ms]
      No VAD gate, no preroll ring, no 0x04/0x05 sentinels on this stream.

Controller handle_data():
  → oww_paused.is_set()? → voice_queue (during a turn)
  → else → mic_queue (during idle)

wake_word_listener():
  → pulls from mic_queue (10s of silence now means the stream DIED —
    hardware mute still produces zero-filled frames — so the controller
    logs a warning and sends a defensive mic_start, skipped mid-turn)
  → accumulates into 80ms chunks
  → per-chunk RMS updates device.noise_floor (v2.7.0): asymmetric EWMA,
    follows drops fast (α=0.3), rises slowly (α=0.008 ≈ 10s) so speech
    can't drag it up. Measurement only — the audio is never modified.
  → OWW inference (hey_rhasspy_v0.1, threshold 0.30)
  → scores > 0.05 counted as near-misses: INFO log (rate-limited 1/2s,
    now includes rms= and floor=) + dashboard counter
  → score >= threshold → wake detected
```

**Key: the stream runs continuously and is completely stateless — no gate, no adaptive gain, nothing that can drift with room history. OWW always sees uninterrupted audio. ch6 omni during idle. Per-room adaptation happens controller-side as a noise-floor *measurement*, consumed by endpointing — never applied to the signal.**

### Wake word detected → command capture

```
wake_word_listener():
  → oww_paused.set() — routing flips: handle_data() now sends to voice_queue
  → model.reset(), buf.clear()
  → beam_lock control message (v2.7.0) — device locks the beamformer onto
    the perimeter mic with the best speech onset ratio, mid-stream, no
    restart. Sent at detection because that's the freshest onset signal the
    selector will get (though see the beamforming caveat in the table below
    — controller-side detection latency means even this is 300–500ms after
    the wake word started). beam_unlock is sent after the turn completes.
  → _run_voice_locked(device, trigger_label="wakeword(score)")
      → [esphome path] trigger_voice_turn()
          → TurnTrace created (t0 = now)
          → satellite.run_esphome_voice_turn()
              → VoiceAssistantRequest(start=True) → HA Assist pipeline opens
              → _stream_mic_audio() starts reading from voice_queue
                (whole phase wrapped in a 20s hard cap, v2.6.5 C1):
                  → first 3 frames discarded (VOICE_PREROLL_DISCARD=3, 240ms)
                    — removes wake-word tail ("...Jarvis") from audio.
                    WAKE TURNS ONLY (v2.6.5 C3): button and continuation
                    turns pass preroll_discard=0 — they have no wake-word
                    tail, discarding real audio clipped their first word
                  → controller-side 5s no-speech timeout armed
                  → timeout disarms on SPEECH, not on the first frame
                    (v2.7.0 — frames now flow continuously, silence included,
                    so "a frame arrived" means nothing). Speech = chunk RMS ≥
                    max(3 × device.noise_floor, 0.004), OR HA's own
                    STT_VAD_START event (covers quiet speech in a noisy room).
                    Wake-then-silence closes quietly at 5s again instead of
                    sitting through HA's ~10s STT timeout + error cleanup.
                  → frames sent as VoiceAssistantAudio chunks to HA
                  → stream ends on WHICHEVER ARRIVES FIRST:
                      — HA's own VAD end (_ha_vad_end, set on STT_VAD_END or
                        ERROR) — the endpointing authority; noise-robust,
                        model-driven (v2.6.5 C1)
                      — device VAD sentinel (0x04) — only exists on lock_mic
                        (button) streams now; never arrives on wake turns
                    → VoiceAssistantAudio(end=True), t_vad_end logged

NOTE: the stream never stops. No mic_stop, no mic_start_turn on OWW path.
The only changes at wake are the oww_paused flag flipping the queue routing
and the beam_lock switching the mic channel. Command audio flows in with
zero gap.
```

### HA pipeline → response

```
HA Assist:
  → STT (Whisper) → intent resolution → TTS generation
  → VoiceAssistantEvent stream: RUN_START → STT_START → STT_END →
    INTENT_END → TTS_START → TTS_END → RUN_END

Controller satellite:
  → INTENT_END received → tts_event armed (prevents premature RUN_END close)
  → TTS_URL received → t_tts_url_ms logged
  → fetch TTS from HA (48kHz mono FLAC when HA honours our declared
    supported_formats) → ffmpeg decode → 48kHz mono S16_LE PCM (v2.9.4;
    was 22050Hz + numpy resample)
  → t_tts_fetched_ms, tts_bytes logged
  → EQ at 48kHz (mono end-to-end — no resample, no stereo)
  → mic_stop → device stream stops BEFORE playback starts (v2.6.5 —
    previously only in the post-turn finally, so the device processed
    63–65 frames of its own TTS echo per turn, contended the Wi-Fi radio
    against the incoming speaker frames, and crushed AGC gain)
  → stream PCM to device ALSA as 0x02 binary frames, 0x03 EOS
  → sleep for audio duration (acoustic feedback prevention)
  → EITHER (continuation, v2.6.5 C2): HA set continue_conversation →
    mic_start (no lock_mic) → loop into next turn with preroll_discard=0.
    The restarted stream is ungated so audio flows immediately; the
    controller sends beam_lock again the moment the user's answer clears
    the noise floor (the TTS mic restart reset the beam to ch6 omni)
  → OR (normal end): voice_queue drained WHILE oww_paused is still set
    (v2.6.5 regression fix — draining after the routing flip left stale
    ambient frames to arrive as preamble on the next turn)
  → oww_paused.clear() → routing returns to mic_queue
  → mic_start (no lock_mic) → stream restarts on ch6 omni
  → beam_unlock sent (belt-and-braces — matters for no-TTS turns where the
    stream never restarted and a beam lock would otherwise persist into
    idle wake listening)
  → stale frames drained (belt-and-braces no-op now), OWW model reset
  → [TURN] log line emitted with full timing breakdown
```

NOTE the stop/start pair around TTS is safe as of v2.7.0: streamMic's exit
path has an ownership check (d.micStopCh == stopCh) so a draining old
goroutine can't clear micActive over its replacement. Before the fix, that
race let a mic_start spawn a second concurrent stream that no mic_stop could
reach — leaked gated streams were silent while idle but duplicated every
utterance 2× (STT saw "turn on on the on the office…") and their 0x04
sentinels cleared the OWW buffer, progressively killing wake detection until
the process restarted. This was almost certainly the historical
"wake word degrades over days, reboot fixes it" bug.

### Button-triggered turn (differs from OWW path)

```
Button press (clickType=138):
  → oww_paused.set()
  → mic_stop → device stream stops
  → mic_start(lock_mic:true) → new stream with lockMic=true
      → beam.Lock(beamformingEnabled) called
        — beamformingEnabled=true: selects perimeter mic with highest onset ratio
        — beamformingEnabled=false: Lock() no-ops, stays on ch6
      → [beam] locked to chX (Y°) onset_ratio=Z logged
  → _run_voice_locked(device, trigger_label="button")
  → [same HA pipeline as above]
  → mic_stop → mic_start (no lock_mic) → back to ch6 omni
    (explicit stop first, v2.7.0: on no-TTS outcomes — cancel, error,
    no-speech — the lock_mic stream is still running and a bare mic_start
    would no-op against it, leaving the GATED, beam-locked turn stream as
    the permanent wake stream)

Button path retains stop/start because: (a) no dead zone cost — button is
pressed before speech starts, (b) the lock_mic stream is the only place the
VAD gate, preroll ring, sentinels, and (config-gated) AGC still exist.
```

### What's currently off and why

| Stage | State | Reason |
|---|---|---|
| RNNoise NS | **REMOVED** (2026-07-12) | Was calibrated for 48kHz, fed 16kHz — miscalibrated speech probability, degraded HF consonants. P0-3 resolved exactly as predicted here: deleted device-side, replaced by controller-side DTLN (`em_ns.py`, 16kHz-native) applied to the speech-to-text stream only, per-device `nsAsr` flag, default off. Wake stream stays raw. |
| AGC | **OFF on the wake stream, permanently** (v2.7.0 — ignores config). Config-gated on lock_mic turns only. | v2.6.5 re-enabled it after the echo fixes, but ResetAGC only runs at stream start and the wake stream never restarts — in any room with steady noise above vadThreshold, the release path walked gain up toward amplifying the noise floor (the RNNoise interlock that was meant to prevent this is dead while NS is off), then the fast attack compressed the wake word's envelope mid-utterance. Adaptive gain state on a permanent stream = rebaselining by construction. The fixed gain staging that replaced it shipped in v2.7.1: `micGainDb` (+24dB default) applied to the full 24-bit sample pre-truncation. |
| VAD gate (wake stream) | **REMOVED** (v2.7.0) | Absolute RMS threshold can't be right in every room; OWW wants continuous audio; the gate held open by ambient noise was also what let the AGC release run continuously. Still exists on lock_mic (button) streams for endpointing. |
| Beamforming | ON in config, **lock-back selection (v2.7.2)** | Lock is commanded at wake detection (v2.7.0, beam_lock mid-stream); detection lands 300–500ms after the wake word ends, so live onset ratios had decayed and selection was known-poor. Fixed via lock-back: a ~2s ring of per-direction period energies (frozen while locked, like the baseline); Lock() scores each direction by its top-8-period burst within the window relative to its baseline, so it selects on the recorded wake word rather than the decayed present. Unit-tested (TV-vs-decayed-speaker scenario in `beamformer_test.go`). Known caveat: TTS echo enters the ring between turns — the baseline absorbs the same energy, damping its ratio, but continuation-turn locks are the weaker case until AEC. Validate direction LED against speaker position after OTA. |
| owwSpeexNs | OFF | Available (v2.6.5, Q1): openwakeword's speexdsp suppressor, wake path only. Off by default — flip on the lounge device and A/B wake rate with TV on before fleet-wide enable. |
| Noise floor tracking | **ON** (v2.7.0, controller) | Per-device asymmetric EWMA over the continuous wake stream. Measurement only. Consumed by the SNR-relative no-speech timeout; logged as floor= in OWW lines. |

### VAD threshold guidance

**Units (v2.7.1):** all values below are *pre-gain* — measured before the fixed `micGainDb` stage. The device scales `vadThreshold` by the linear gain internally, so the config value keeps these units regardless of the gain setting; the `rms=`/`floor=` values in controller logs are *post-gain* (multiply this table by ~16 at the default +24dB to compare).

Measured signal levels at 16kHz on ch6, MICPGA=40, digital gain=88:

| Condition | Typical RMS |
|---|---|
| Dead silence (quiet room) | 0.00017–0.00019 |
| Ambient room noise | 0.00020–0.00050 |
| Conversational speech at 1.3m | 0.0004–0.0010 |
| Raised voice at 1.3m | 0.004–0.010 |

vadThreshold 0.001 sits comfortably between ambient and speech. Raise to 0.003–0.005 in noisy rooms (TV on). Dashboard slider now goes down to 0.0001 for quiet environments.

**Scope change (v2.7.0):** vadThreshold/vadSpeechMs/vadSilenceMs apply only to lock_mic (button) turn streams now — the wake stream is ungated and ignores all three. Wake-turn endpointing is HA's VAD; accidental-wake cutoff is the controller's 5s SNR-relative timeout against the measured per-room noise floor (no per-room tuning needed). The fixed-gain bump this table originally motivated shipped in v2.7.1 (`micGainDb`).

---

## Voice Pipeline

Home Assistant's Assist pipeline (via the impersonated ESPHome satellite) is
the only voice backend — the legacy claracore WebSocket path this section
used to open with was removed 2026-07-12.

```
"Hey Jarvis"
    → [same on-device path through OWW detection]
    → controller: VoiceAssistantRequest(start=True, flags=0) → HA Assist
    → mic audio streamed as VoiceAssistantAudio chunks to HA
      (wake turns drop the first 240ms of wake-word tail; button and
      continuation turns don't — v2.6.5 C3)
    → VAD end → VoiceAssistantAudio(end=True)
      — HA's STT_VAD_END is the endpointing authority (v2.6.5 C1); the
        device's own RMS-gate 0x04 sentinel is advisory and ends the
        stream only if it arrives first. 20s hard cap as backstop.
    → HA: STT (Whisper) → intent → TTS
    → HA: VoiceAssistantAnnounceRequest(media_id=url, text="...")
    → controller: mic_stop (acoustic-feedback guard, v2.6.5; skipped when
      barge-in is enabled — AEC keeps the live mic usable during playback)
    → controller: fetch TTS (one retry on transient failure; the satellite
      declares supported_formats 48kHz/mono/FLAC so recent HA transcodes at
      source) → ffmpeg decode straight to 48kHz mono S16_LE (cap 15s)
    → controller: EQ at 48kHz (no resample step since v2.9.4) → stream to
      device ALSA as mono 0x02 frames
    → controller: MediaPlayerState ANNOUNCING → AnnounceFinished → IDLE
    → if HA set continue_conversation: mic_start → next turn immediately
      (preroll_discard=0), no wake word needed (v2.6.5 C2)
    → else: LED off, voice_queue drained, mic restart
```

No-speech branch (device's 0x05 sentinel — see WebSocket Protocol below):
```
"Hey Jarvis" → [silence for 5s, nothing said]
    → device: 0x05 (no-speech timeout) instead of 0x04
    → controller: empty VoiceAssistantAudio(end=True) sent to close HA's
      already-open pipeline cleanly, but the 30s wait for a TTS response
      is skipped entirely — no HA round-trip result is awaited
    → turn ends quietly, mic restart
```

Action button triggers the same pipeline directly, bypassing wake word detection. Second press cancels at any stage.

---

## WebSocket Protocol

### Control plane (`ws://server:8767/control`) — JSON

Device → Server:
```json
{"type": "register", "device_id": "G0K0XXXXXXXX", "ip": "...", "version": "v2.3.0", "capabilities": [...]}
{"type": "button", "clickType": 138, "down": false}
{"type": "mute_state", "muted": true}
{"type": "log", "level": "info", "message": "..."}
{"type": "pong"}
```

Server → Device:
```json
{"type": "ack", "device_id": "G0K0XXXXXXXX"}
{"type": "pending"}
{"type": "config", "adcDigitalGain": 88, "adcMicpga": 40, "vadThreshold": 0.001, ...}
{"type": "leds", "leds": [{"id": 0, "r": 0, "g": 180, "b": 0}, ...]}
{"type": "mic_start"}
{"type": "mic_start", "lock_mic": true}
{"type": "mic_stop"}
{"type": "beam_lock"}      // v2.7.0: lock beamformer onto best perimeter mic
                           // mid-stream, no restart (no-op if beamforming
                           // disabled in config or already locked)
{"type": "beam_unlock"}    // v2.7.0: release beam lock, back to ch6 omni
{"type": "shell_open"}
{"type": "shell_close"}
{"type": "ping"}
```

### Data plane (`ws://server:8767/data`) — binary

Device → Server (mic frames):
```
[0x01][seq_hi][seq_lo][mono S16_LE PCM, 2560 bytes = 80ms]  — audio (continuous on the wake stream since v2.7.0; VAD-gated speech on lock_mic streams)
[0x01][seq_hi][seq_lo][0x04]                                 — VAD end (lock_mic streams only since v2.7.0)
[0x01][seq_hi][seq_lo][0x05]                                 — no-speech timeout (lock_mic streams only; see below)
```
All three share the same `frameTypeMic` (`0x01`) wrapper and seq header — the VAD sentinels are single-byte *payloads*, not distinct top-level frame types. (0x02/0x03 below are genuinely distinct top-level types, speaker-direction only, no seq header — don't confuse the two framing conventions.)

**No-speech timeout (0x05), added v2.6.0.** `streamMic` (device/internal/client/data.go) only arms this when `lock_mic: true` was set on the `mic_start` that began the stream — i.e. only for a bounded voice turn (post-wake-word or button press), never for the permanent `lock_mic`-absent OWW listening stream. If no speech is ever detected (RMS never crosses `VadThreshold` for `VadSpeechMs` consecutive periods) within 5s of turn start, the device gives up locally and sends `0x05` instead of waiting on the existing silence-after-speech hysteresis, which never engages if speech never started. Distinguishing 0x05 from 0x04 lets the controller skip contacting HA's Assist pipeline entirely for a turn that never had anything to transcribe — mirrors Alexa's behaviour of quietly giving up on "wake word, then silence" rather than round-tripping to the backend just to receive `stt-no-text-recognized`. **This must never be armed for `lock_mic`-absent streams** — an earlier build armed it unconditionally, which silently killed the permanent wake-word listening stream 5s after every boot/reconnect with nothing to restart it (wake word "stopped working entirely," diagnosed via device log showing repeated `no speech detected within timeout` firing exactly 5s after every idle `Mic streaming started`, with no corresponding `mic_start` to revive it). Since v2.7.0 the failure mode is doubly covered: the `lock_mic`-absent stream has no VAD machinery at all, and the controller detects a dead wake stream (10s without frames) and sends a defensive `mic_start`.

Server → Device (speaker frames):
```
[0x02][mono S16_LE PCM, 4096 bytes = one ALSA period]   — mono on the wire
                                                           since v2.8.4; the
                                                           device duplicates
                                                           L=R at the ALSA
                                                           write
[0x03] end of stream
```

### Shell plane (`ws://server:8767/shell/{device_id}`) — binary

Demand-opened by the Go binary dialling **outbound** to the controller on receipt of a `shell_open` control message. Single session enforced. The controller proxies this connection to the dashboard terminal. No inbound ports on the device.

Two modes (v2.7.1):

- **PTY** (`shell_open` with `pty: true` — dashboard sessions): the device attaches `/system/bin/sh` to a real pseudo-terminal (`/dev/ptmx`, `TERM=xterm-256color`, new session with controlling TTY), giving an interactive mksh with prompt, line editing, job control, and full-screen apps. The device signals the established mode by dialling `/shell/{device_id}?pty=1`; the controller relays it to the dashboard as a `shell_meta` text message before any bytes flow. Input from the dashboard is framed binary: `0x00` + stdin bytes, or `0x01` + cols/rows (uint16 BE each) for resize (`TIOCSWINSZ`). Output is raw. If PTY allocation fails, the device falls back to the pipe and omits the query flag.
- **Pipe** (`pty` absent — programmatic sessions: OTA transfer, `_shell_run`): raw unframed stdin/stdout, no echo, no prompt — exactly what the output-parsing callers need. Unchanged from earlier versions.

The controller proxies bytes verbatim in both modes; the framing is interpreted only at the endpoints.

---

## Connection Lifecycle

```
Device boots
  → orange LED pulse (searching for server)
  → mDNS browse: _emcontroller._tcp.local (grandcat/zeroconf)
  → credentials at /data/local/etc/echomuse/ + tls_port TXT property?
      → dial wss://:8770 with pinned CA + X-EM-Token (v2.9.3)
      → else plain ws://:8767 (rollout fallback until REQUIRE_DEVICE_TLS=1)
  → connect /control → register (device_id = ro.serialno, version)

  CASE: unknown device, strict mode
    → server: sends {"type": "pending"}
    → device: slow white LED pulse — waiting for approval
    → device retries every 30s

  CASE: approved device
    → server: sends {"type": "ack"} + {"type": "config"}
    → device: applies config (tinymix for hardware params)
    → LEDs off (connected)
    → connect /data → identify
    → server: mic_start sent (no lock_mic — OWW mode)
    → device: mic streaming started on ch6 (centre/omni)
    → OWW listening (device shows IDLE state in dashboard)
```

If control drops → data cancelled → orange pulse resumes → both reconnect together on next mDNS discovery.
Controller detects dead connections within 30s via WebSocket protocol keepalives (ping 20s, timeout 10s).

---

## Key Files to Keep Safe

| File | Purpose |
|------|---------|
| `boot_patched.img` | SELinux-patched boot image |
| `magisk.db` | Pre-seeded root grant database |
| `Magisk-v17.3.zip` | Magisk installer |
| `f1r30s.zip` | ADB enablement patch |
| `update-kindle-csm_biscuit-272.6.8.0_user_680767620.bin` | FireOS 5 firmware |
| `server` | Compiled EchoMuse binary (ARM, API 22) — or fetch from GitHub releases |

If you need to reflash: Steps 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9. Your saved `boot_patched.img` already contains the SELinux patch — no need to repatch from scratch.

---

## Troubleshooting

### Device not connecting to server

```bash
adb shell su -c 'cat /tmp/server.log'
```

Common causes:
- **`mDNS: no server found`** — server not advertising. Check `dns-sd -B _emcontroller._tcp local` from Mac — should show `echomuse`.
- **White pulse, not orange** — device found the controller but hasn't been approved yet. Log into the management dashboard and approve the device.
- **`Connection lost: unexpected EOF`** — connecting to wrong server (stale mDNS cache). Another device on network may be advertising `_emcontroller._tcp`. Check `dns-sd -B _emcontroller._tcp local` from Mac.
- **p2p0 interference** — check `ip link show p2p0` on device — should be DOWN.

### No audio

```bash
adb shell su -c 'tinyplay /data/local/tmp/test48s.wav -D 0 -d 23 -p 2048 -n 4'
```

If this hangs: mixer not initialised. Run the tinymix commands from Step 9 manually.

### Mic not working / wake word not triggering

Check mic gain:
```bash
adb shell su -c 'tinymix -D 0 89'  # should be 88
adb shell su -c 'tinymix -D 0 92'  # should be 40
```

Wake word detection uses ch6 (centre/omni). VAD threshold defaults to 0.001 normalised RMS — adjustable via config push from the dashboard. In noisy environments, raise to 0.003–0.005.

Check OWW model is loaded in controller logs — should see `OpenWakeWord model ready` on device connect.

### ADB not available after boot

```bash
adb shell su -c 'setprop persist.service.adb.enable 1'
adb shell su -c 'setprop persist.sys.usb.config mtp,adb'
adb shell su -c 'start adbd'
```

### Monitor active PCM devices

To see which processes own active ALSA devices in real time:

```bash
adb push pcm_watch.sh /data/local/tmp/pcm_watch.sh
adb shell su -c 'chmod 755 /data/local/tmp/pcm_watch.sh && /data/local/tmp/pcm_watch.sh'
```

`pcm_watch.sh`:
```sh
#!/system/bin/sh
while true; do
    for f in /proc/asound/card0/pcm*/sub0/status; do
        line=$(grep "owner_pid" "$f" 2>/dev/null)
        if [ -n "$line" ]; then
            pid=${line##*: }
            name=$(cat /proc/$pid/comm 2>/dev/null)
            state=$(grep "^state:" "$f")
            state=${state##*: }
            echo "$f pid=$pid state=$state -> $name"
        fi
    done
    sleep 2
done
```

---

## Audio Notes

**Why device 23?** The biscuit exposes 25+ PCM devices. Device 23 is the TLV320 DAC output path. Most other devices are modem/voice paths or internal DSP routes that hang or error on open.

**Why keep echoaudioservice?** The MediaTek audio DSP requires initialisation that happens inside Amazon's audio HAL (`audio.primary.mt8163.so`). Without `echoaudioservice` running, the I2S clock never starts and `tinyplay` hangs indefinitely. The service is a manifest stub — no Java code — its sole job is to trigger HAL initialisation via the Android audio framework.

**The mixer defaults are wrong.** Three mixer controls must be set after every boot — `start_server.sh` handles this automatically. Without them, tinyplay hangs silently on device 23.

**The dummy mixer service is required.** EchoMuse's speaker Init() calls `stop mixer` as its first step. Without a `mixer` service in init.rc, this call fails. Adding a dummy service allows `stop mixer` to succeed.

**Amp click/hiss suppression.** Order matters (found on hardware, 2026-07-10): `pcm_speaker.go` Init() mutes the output (tinymix ctl 61 → 0), opens the PCM stream and lets the silence loop clock the DAC for ~100ms, *then* enables the amp (ctl 5 On), waits 50ms for it to settle, and unmutes last. Enabling the amp onto a floating (unclocked) DAC and unmuting before stream-open was the source of the click on every service start. Shutdown is the mirror image: on SIGTERM the server's `PcmSpeaker.Close()` mutes → amp off → closes the stream, and `start_server.sh` repeats mute + amp-off after every server exit (covering SIGKILL/panic) — an enabled amp on an idle DAC audibly hisses for as long as the server is down (worst case: between OTA slots).

**Mute implementation.** The mute button (KEY_MUTE, evdev code 113) arrives on `/dev/input/event1`. Mute sets the ADC mute controls on **all four codec chips** (tinymix ctls 105/106, 123/124, 141/142, 159/160 — chip-A-only coverage was a known gap until v2.7.4; the sibling controls were confirmed from the full `tinymix -D 0` dump in `device/tools/tinymix_controls_output.txt`), so every mic including ch6 is physically muted. The mute controller intercepts the button locally, applies the tinymix change, updates the LED ring (red = muted) and the discrete button LED (gpio444, active-high — v2.9.5; earlier firmware drove gpio445 per Amazon's HAL constant, which is off by one and muxed away, so the button never lit), and blocks dot button events. Mute also stops a running mic stream (v2.6.5) and, controller-side, terminates an active voice turn (v2.7.8). Since v2.9.4 mute is **persistent**: written to `/data/local/etc/echomuse/state.json` on toggle and restored at boot before any connection — device-sovereign, so a muted Dot comes back muted with or without a controller.

**Mic gain — all four ADCs.** All four ADC pairs (A–D) are set to digital volume 88 and MICPGA 40. This matches Amazon's own initialisation values confirmed by analysing the unmodified device mixer state. Equalising all four ensures consistent sensitivity across all perimeter mics for directional selection.

**WiFi wake lock.** FireOS aggressively suspends the WiFi interface during inactivity, dropping WebSocket connections. Writing `"EchoMuse"` to `/sys/power/wake_lock` prevents this.

**Speaker streaming.** Audio is streamed as binary frames (4096 bytes = one mono ALSA period — mono on the wire since v2.8.4; the device duplicates L=R at the ALSA write) over the data plane WebSocket. The device maintains a priority channel — the silence loop yields to real audio naturally, with backpressure at ALSA playback rate (~42ms/period). TTS is 48kHz mono end-to-end since v2.9.4 (ffmpeg decodes at the wire rate; HA transcodes at source when it honours the declared supported_formats — no controller resample step). The device buffers ~5.5s and holds playback until ~1s is queued or EOS arrives (v2.8.4 — WiFi-stall protection for marginal links).

**OWW threshold.** 0.3 works well for a London/Bristol accent — the default 0.5 is calibrated for American English.

**VAD threshold.** 0.001 normalised RMS is the default (v2.6.5 — corrected from a drifted 0.003 that sat above measured conversational speech at 1.3m). Adjustable via config push from the dashboard — no rebuild required. In noisy environments (music, TV), raise to 0.003–0.005.

**VAD end signal.** When the device VAD gate closes (speech followed by `vadSilenceMs` of silence, default 900ms; button/lock_mic turns only — the wake stream is ungated), the device sends a `0x04` sentinel. The controller ends the HA audio stream if it arrives before HA's own `STT_VAD_END` — HA's VAD is the endpointing authority for wake turns; the device sentinel is what actually ends button turns. Note (v2.9.4): the gate windows now run at their configured durations — a counting bug against the mic's 160ms batch size previously made both ~5× longer than set.

**Directional mic locking — onset ratio.** When the controller sends `mic_start` with `lock_mic: true` (voice turn start), the device locks to the perimeter mic with the highest onset ratio: `energySmooth[di] / energyBaseline[di]`. This selects the direction with the biggest *recent energy increase* rather than highest absolute energy, making the lock robust to continuous background noise sources (TV, fan). Two parallel smoothers: fast (α=0.9, ~320ms) and slow (α=0.995, ~10s baseline). The slow baseline is frozen while locked. The lock is idempotent across VAD oscillation. Releases on `mic_stop`.

**Direction estimation — onset ratio.** Two parallel smoothers run per direction: fast (α=0.9, ~320ms) tracking instantaneous energy, and slow (α=0.995, ~10s) tracking the background noise floor. At lock time, the direction with the highest `energySmooth / energyBaseline` ratio is selected — this is the direction with the biggest *recent energy increase* (speech onset), not the direction with the highest absolute energy (TV, fan). The slow baseline is frozen during voice turns to prevent the speaker's own voice from corrupting the noise estimate. This reliably picks the speaker direction even with a television on in the room.

**LED direction overlay.** The direction arc is overlaid on the solid green listening ring during voice turns only (not during idle wake word listening). The overlay uses the controller-set base ring state rather than accumulating — each period resets to the base green and applies the direction marker fresh. Primary direction LED: bright light green (R:0 G:255 B:80). Adjacent LEDs: base green boosted by 60. The overlay stops immediately when the controller sends the thinking spinner (spinner LEDs are not solid green, so `listeningLEDs` flag goes false).

**LED physical mapping.** 12 LEDs (IS31FL3236A), one either side of each perimeter mic. LED 0 is physically at 240° (just clockwise of MK5 at 210°). Volume sweep confirmed: starts at LED 0, sweeps clockwise. Offset formula: `LED = ((angle - 240 + 360) % 360) / 30`.

**Audio processing pipeline.** Each 160ms mic batch of raw beamformed audio passes through: (1) speexdsp AEC (v2.7.3, when enabled) — subtracts the speaker's own output, whole mic path including the wake stream. (2) AGC (button/lock_mic turns only; never the wake stream) — targets -22dBFS RMS with fast attack (0.05) and slow release (0.005); release frozen during silence to prevent noise floor amplification. VAD decisions are made on pre-AGC audio to keep the threshold stable. Device-side RNNoise was removed 2026-07-12 — noise suppression is controller-side DTLN on the speech-to-text stream (`nsAsr` flag).

**Acoustic feedback prevention.** `stream_speaker` completes well ahead of actual playback (the WS write runs ~2× realtime and the device buffers ~5.5s). Without compensation, the mic would restart while the speaker is still playing, and the assistant would hear itself and trigger another turn. The controller sleeps for the remaining playback duration (plus the ~1s prime allowance) after streaming, racing `cancel_event` so barge-in cuts the wait instantly. With barge-in enabled the mic never stops at all — AEC is what keeps the live mic usable during playback.

**Turn timeouts.** The mic-streaming phase of a turn carries a 20s hard cap, and a turn where speech never starts closes locally after 5s (controller-side SNR-relative timeout on wake turns, device `0x05` sentinel on button turns) instead of round-tripping to HA for an empty transcription.

**Stale queue drain.** After each voice turn, the mic queue is drained and the OWW model is reset before wake listening resumes. This prevents the device's own speaker output (buffered during playback) from immediately triggering another wake word detection.

**OWW routing during turns.** While a turn is active (`oww_paused`), incoming mic frames route to `voice_queue` instead of the wake model. During thinking and playback the `_barge_watcher` scores that queue with its own OWW instance — a wake word spoken over the response cancels playback (barge-in, threshold deliberately below `owwThreshold` because speech-over-TTS scores are depressed ~25dB by the echo).

**mDNS library.** The `hashicorp/mdns` library fails to resolve the controller IP when python-zeroconf sends PTR responses with the A record under the hostname rather than the service name. Replaced with `grandcat/zeroconf` which is RFC 6762/6763 compliant and handles this correctly.

---

## What's Next

- **On-device wake word** — TFLite C binary running on-device, eliminating the continuous WiFi audio stream for OWW. OpenWakeWord has a TFLite backend; cross-compilation uses the existing echomuse-compiler Docker toolchain
- **PTY shell** ✅ — complete as of v2.7.1. Hand-rolled `/dev/ptmx` + `x/sys/unix` ioctls (no creack/pty dependency) + xterm.js in dashboard. Note: FireOS toolbox `top` is a dumb scroller by design — install a static busybox on `/data` for a redrawing `top`, `vi`, `less`, etc.
- **Acoustic echo cancellation** ✅ — shipped v2.7.3, functional as of v2.7.7 (silent buffer-size bypass), holds convergence as of v2.7.8 (mic capture overruns tripped the reference governor every ~20s and its filter reset threw away convergence — see the v2.7.8 changelog). speexdsp, ~14dB per response measured on hardware. Future: per-beam-channel filter states so a new-channel turn starts converged; the hardware echo-reference experiment (`Audio_ExtCodec_EchoRef_Switch` + the two unaccounted capture channels — a sample-synchronous reference would delete the ring/governor entirely); possibly speex residual echo suppression on the barge watcher path.
- **Media player integration** — pause room audio on wake word, resume after response (Home Assistant `media_player` service call)
- **Bermuda BT proxy** — room-level presence detection via Bluetooth, once fleet of 5–6 Echo Dots is deployed
- **Adaptive VAD** — calibrate threshold on startup from ambient noise floor × multiplier. Currently fixed at 0.001; in very noisy environments this may need runtime adjustment.
- **RNNoise model upgrade** — vendored v0.1 model (2018). Newer models available via binary blob download; requires model loading API (rnnoise_model_from_file) present in newer source but needing the xiph.org CDN which was unavailable. v0.1 performs well for home environment use.
- **Startup chime** — short audio signature on EchoMuse init
- **Holding response** — play audio while Clara is thinking if response takes >2s
- **ESPHome native API satellite integration** ✅ — complete as of v2.6.0. Both devices registered in HA, voice turns working end-to-end.
- **Bidirectional volume control** ✅ — complete as of v2.6.1. Physical buttons, HA media player slider, and ALSA mixer all stay in sync. Survives controller and device restarts.
- **Continue-conversation without re-waking** ✅ — complete as of v2.6.4. HA-driven: `continue_conversation` flag in `INTENT_END` re-triggers a voice turn immediately after TTS playback without requiring the wake word. User-driven follow-up window (always-on N-second listen after any response) is the next step — recommend as a dashboard toggle, default off, to avoid false triggers in noisy rooms.
- **Rubbish transcription suppression** — wake word triggers on background noise still result in HA's "Sorry, I couldn't understand that" response. Options: audio energy gate in `_stream_mic_audio` before sending to HA (cleanest); or discard HA's stock apology in the TTS handler (tactical). Deferred pending P0-3 NS fix — noise floor situation needs to settle first.
- **C5 hardware half — full-chip ADC mute** ✅ — complete as of v2.7.4. All four codec mute pairs (105/106, 123/124, 141/142, 159/160) toggled by `applyMute`/`applyUnmute`; the red mute ring now physically mutes every chip, including ch6.
- **Q5 — remove speaker underrun instrumentation** ✅ — complete as of v2.7.4. Underrun WARNING removed after several clean sessions; the dead legacy `Pump()` method (HTTP speaker path) removed from the Speaker interface with it.
- **§3.2 Wake-word barge-in** ✅ — complete as of v2.7.6 (`bargeInEnabled`, default off; requires device AEC). With it on, the mic streams through TTS playback and a dedicated per-device OWW watcher scores voice_queue at bargeInThreshold (used as-is since v2.7.7 — deliberately *below* owwThreshold, ~0.10: speech-over-TTS scores are depressed ~25dB by the speaker's loudness while post-AEC self-echo scores only ~0.004); detection sets cancel_event (aborts streaming + drain sleep), sends the new `speaker_flush` control message (device discards buffered periods; ≤~170ms ALSA in-flight still plays), and the turn loop re-enters a fresh turn with the wake-word preroll discard. Since v2.7.8 the filter stays converged across turns, so self-echo peaks at 0.002–0.003 and the threshold can sit at the 0.05 slider floor for easier interrupting. VAD-based barge-in (interrupt by just talking) remains explicitly out of scope.
- **§3.3 NS decision (P0-3)** — RNNoise is 48kHz-calibrated but fed 16kHz. Cheapest path: leave device NS off and test owwSpeexNs on the wake path. If device NS proves needed: replace RNNoise with speexdsp's 16kHz-native preprocessor, then delete the vendored RNNoise.
- **Device preroll ring (§3.4)** ✅ — complete as of v2.6.5. ~512ms of pre-gate audio flushed on VAD gate open; benefits wake onsets and continuation turns (gate starts closed after mic restart).
- **§3.5 Beamformer buffer reuse** ✅ — complete as of v2.7.4. Analysis buffers allocated once in `New()`, reused per period; `extractChannel` still allocates (data.go's preroll ring retains its slices).
- **§3.6 VAD sentinel encoding (B5)** ✅ — complete as of v2.7.4. Sentinel type travels in the queue item (`VAD_SENTINEL_END`/`VAD_SENTINEL_TIMEOUT` strings, defined in em_esphome); `last_vad_was_timeout` deleted.

---

**Document version:** v2.7.8
**Last updated:** 2026-07-08
**Changelog:**
- v1.0 — April 2026: Initial publication. Full pipeline confirmed working.
- v1.1 — 2026-04-26: Fixed ambiguous init.csm.project.rc editing instruction; fixed `server &` → `exec` inconsistency.
- v1.2 — 2026-04-26: Updated start_server.sh; added VAD stream, OpenWakeWord, mute button, amp click suppression; updated end state.
- v1.3 — 2026-04-27: Added THINKING signal, preroll discard, speech threshold, mDNS conflict handling, OWW model loading notes.
- v2.0 — 2026-05-09: Major architecture update. EchoGo replaced by EchoMuse. HTTP server removed from device entirely. Two-plane WebSocket architecture (control + data). gorilla/websocket replacing golang.org/x/net/websocket. p2p0 disable added. Proxmox bridge multicast fix documented. Orange disconnect LED pulse. OWW suppression during playback. Stale queue drain. Boot logging. Updated voice pipeline, end state, troubleshooting, and all file references.
- v2.1 — 2026-05-19: Device ID changed to ro.serialno. Version embedded via ldflags. Three-plane WebSocket (added /shell). Device approval flow (strict/auto modes, pending white pulse). Config push on connect. Device log streaming. VAD end signal (0x04 frame type) replaces server-side silence detection. OWW model download at build time. mDNS library replaced with grandcat/zeroconf. Controller management dashboard (port 8768, auth, DB, API, GitHub release tracking, OTA updates).
- v2.2 — 2026-05-20: Shell architecture corrected — device dials outbound to controller on shell_open, no inbound ports on device. Mute state tracking via mute_state control message. Dashboard live state updates via WebSocket events (mute/listen/speak/offline). WebSocket protocol keepalives — dead connection detection within 30s. Dashboard React SPA compiled via esbuild, fully vendored assets (no CDN). Ctrl+C support in browser terminal.
- v2.3 — 2026-05-25: Mic array architecture overhaul. Wake word detection moved to ch6 (centre/omni) for direction-independent reliability. Directional mic selection — best perimeter mic locked at voice turn start via `mic_start` with `lock_mic: true`, released on `mic_stop`. Lock is idempotent across VAD oscillation. Mic gain equalised across all four ADCs (88/40) matching Amazon's initialisation values. Voice server turn timeout added (45s). pcm_watch.sh diagnostic added. Hardware audio investigation documented — confirmed software-only processing, no hardware beamforming output channel on this device.
- v2.4 — 2026-05-28: Mic channel mapping corrected (Ch0=MK1=330°, Ch1=MK2=30° — previous docs had these swapped; confirmed by tone injection testing). ADC architecture documented (four TLV320ADC3101, I2C 0x18–0x1b, TDM shared bus, array radius 36mm). Direction estimation upgraded to onset ratio (fast/slow EWMA) — robust to continuous background noise sources. Audio processing pipeline added: RNNoise noise suppression (vendored xiph/rnnoise v0.1 via cgo, no external dependencies) + AGC with speech-gated release. VAD threshold lowered to 0.003 for comfortable conversational level. Acoustic feedback bug fixed — controller sleeps for audio duration after streaming. Spinner duration fixed — runs until audio playback truly completes. LED direction overlay redesigned — light green segment on listening ring, shows during voice turns only, stops when spinner starts. LED physical mapping calibrated (LED 0 at 240°). `listeningLEDs` flag gates direction overlay to prevent interference with spinner/other animations. bf_capture diagnostic tool documented. Voice server END handler hardened — THINKING send failure no longer silently drops transcription.
- v2.4.1 — 2026-06-13: Stability and correctness pass. Connection lifecycle: pong ticker goroutine leak fixed (done channel tied to connection lifetime); data-plane reconnects independently on /data drop without waiting for /control to cycle; register message sent before conn published to prevent concurrent write race; controller device registry guarded with identity checks to prevent reconnect races on handler teardown; per-device OWW model instances replace shared singleton (thread-safety, state isolation); mDNS refresh loop implemented fixing silent IGMP keepalive regression on Proxmox bridge; mdns_task NameError on shutdown fixed. Audio recovery: speaker silenceLoop death no longer causes PumpPeriod to block forever (deadCh); mic ALSA stream death closes subscriber channels so streamMic exits cleanly; streamMic defer resets micActive on exit from any cause. Concurrency: LED i2c writes serialised with mutex; SQLite write transactions serialised with threading.Lock; beamformer Unlock moved to mic goroutine (eliminates data race with Process). Beamformer: fixed-beam steerAngle now applied in Process() — nearestDirection was implemented but never called; Lock() uses raw energy during 3s baseline warmup rather than meaningless onset ratios; hfEnergy direction index corrected. Mute: physical mute button is now device-sovereign — controller mic_start refused when muted; mute state reported on every reconnect; red ring restored after orange pulse on reconnect; OWW detection suppressed and buffer cleared when muted. Controller: resample rewritten with numpy (~1-2s blocking loop replaced with <5ms); TTS tail padding prevents last ~42ms of audio being dropped; OWW threshold updates live without reconnect; spinner overshoot fixed — sleep tracks elapsed streaming time and waits only remaining playback duration.
- v2.4.2 — 2026-06-13: Correctness fixes and HTTP layer removal. Small fixes: handle_shell task leak plugged (tasks now cancelled in finally regardless of asyncio.wait outcome); VAD-end sentinel no longer silently dropped when queue full — drains one audio frame to make room rather than losing the end-of-speech signal (previously caused voice turns to hang until 45s timeout); BeamAngle field changed to *float64 so 0° is distinguishable from absent-from-message. HTTP rip-out: gin stack, all HTTP handler files, and pkg HTTP client wrappers removed — the HTTP server was never started (Serve() was wired but never called since v2.0); volume_buttons.sh removed (was stuck in a curl wait loop since the HTTP endpoint never existed; volume buttons handled by Go binary via evdev throughout). go.mod cleaned of gin and 15 exclusive transitive dependencies; binary size reduced accordingly. Run `go mod tidy` inside the compiler container after checkout if go.sum needs regenerating.
- v2.4.3 — 2026-06-13: RNNoise VAD probability, shell console fix, version embedding. RNNoise: ProcessFrame return value (speech probability 0–1) was previously discarded on every call; now stored in Processor and used to gate AGC release — if RNNoise confidence < 0.5, AGC release freezes even when RMS is above stream VAD threshold, preventing gain pumping on loud non-speech (TV, HVAC). Stream VAD gate remains RMS-only; vadHasData flag prevents incorrect gating during the ~30ms startup window before first RNNoise frame. Shell console: inner proxy functions (device_to_dashboard, dashboard_to_device) were accidentally removed in the v2.4.2 handle_shell task-leak fix, causing NameError on every shell connection attempt; restored. Version embedding: compile.sh now uses --entrypoint bash with explicit -ldflags to embed the git tag (or datetime-dev for dirty trees) into the binary at compile time; devices now report their running version to the controller dashboard correctly rather than always showing "dev".
- v2.4.4 — 2026-06-14: EQ, stats, A/B OTA, shell fixes. Output codec documented: TLV320AIC32x4 on I2C bus 2 (separate from the 4x TLV320ADC3101 input chips); hardware biquad EQ (117-byte ALSA control) identified and decoded but software EQ chosen for flexibility. 8-band parametric EQ implemented controller-side using Audio EQ Cookbook biquad formulas (low shelf 125Hz, peaking Q=1.4, high shelf 8kHz); SVG frequency response curve in dashboard updates live as sliders move; 2-column layout with Flat/Clarity/Warmth preset buttons and loudness toggle. WiFi RSSI offset-encoding fix: /proc/net/wireless on this kernel encodes level as positive offset (0-255); values > 0 corrected by subtracting 256 (e.g. 206 -> -50 dBm). Device stats reporting: new stats.go in device/internal/client sends CPU%, RAM, storage, WiFi RSSI to controller every 30s and on connect; status tab redesigned with resource bars and 4-bar WiFi signal indicator. Wake word model hot-reload: owwModel config changes reload the model in the running wake_word_listener without device reconnect. A/B binary update system: start_server.sh rewritten with 3-attempt retry loop (15s minimum runtime threshold), SIGTERM trap, and auto-rollback via symlink flip before clean exit; controller OTA detects/migrates legacy layout in one shell session, streams binary to inactive slot, atomically flips symlink and restarts; rollback is instant symlink flip only; local binary upload (POST /api/releases/upload) with file picker in dashboard. Shell/console fixes: @auth.require_admin removed from _ws_shell (was rejecting WebSocket upgrades — browsers cannot set Authorization headers; auth handled by ws_resolve_session via ?token= query param); programmatic shell race fixed using ws.wait_closed() + per-device asyncio.Lock; _shell_pending cleanup moved exclusively to _release_shell_ws. OTA implementation fixes (all discovered against device): binary transfer uses shell heredoc piped to `busybox base64 -d` — printf and base64 are not in PATH on FireOS mksh, only available as BusyBox applets; decoder auto-detected at transfer time (busybox base64 → python3 → python); service restart uses `kill $PPID` from within the shell session rather than `stop/start echogo` — stop echogo kills the entire Android cgroup including the shell (child of server process), so start echogo never ran; kill $PPID sends SIGTERM to the server binary and start_server.sh's wait loop restarts cleanly from the updated symlink. Controller OTA fixes: `str(msg)` → `msg.decode('utf-8')` in _shell_run (device sends WebSocket binary frames; str() produced repr `b'SLOT:server_a\n'` as a literal string, breaking slot detection); duplicate _get_device_shell_ws/_stream_binary_via_shell/_exec_shell definitions removed (shadowed correct implementations in Python); _ws_shell now checks _shell_lock before opening interactive console (opening terminal mid-transfer previously overwrote _shell_pending, cancelled device shell context, and killed the transfer); _extract_binary_version() scans raw binary for embedded version string pattern (20\d{6}-\d{4}-[a-z]+) so local uploads report the binary's own version rather than a controller-generated local-YYYYMMDD-HHMM label; _monitor_reconnect accepts version != previous_version as success (covers local uploads where binary self-reports its own string); TRANSFER_OK wait extended to 120s; reconnect initial sleep 8s.
- v2.4.5 — 2026-06-18: Dashboard provisioning wizard, first pass. Browser-side ADB client over WebUSB (Chrome/Edge only; no server-side ADB required) — initial implementation was hand-rolled: RSA auth via BigInt modular exponentiation (Web Crypto always pre-hashes, so the PKCS#1 v1.5 pad + private RSA op was done manually), binary file push via `exec:cat >`, binary pull via `exec:cat`. Dashboard gains an admin-only "+" tile in the device grid that opens an 11-step wizard: (0) connect device in Android mode, verify FireOS 5 build, `adb reboot recovery`; (1) reconnect to TWRP; (2) patch boot image — SELinux cmdline (bytes 64–575 zeroed, `androidboot.selinux=permissive` written) and init.rc service entries combined into a single magiskboot unpack/repack cycle; (3) flash Magisk 17.3 via `twrp install`; (4) push pre-seeded magisk.db (generated on-the-fly by controller via `GET /api/provision/magisk_db`, grants uid 2000 and uid 0 always-allow); (5) reboot to Android; (6) reconnect; (7) verify root; (8) configure WiFi — pushes wpa_cli helper script to avoid shell quoting, polls for IP; (9) disable all 9 Alexa voice stack packages; (10) push server binary (A slot) + startup script (`GET /api/provision/start_script`). Two new admin-only controller endpoints added: `GET /api/provision/start_script` and `GET /api/provision/magisk_db`.
  **Superseded in v2.5.0** — the hand-rolled ADB client never worked reliably end-to-end (USB interface claim/reconnect hangs, RSA auth edge cases) and was replaced entirely with the `@yume-chan/adb` library. See v2.5.0 for the working implementation.
- v2.5.0 — 2026-06-20: Provisioning wizard rewritten on a proven ADB library; full WiFi configuration root-caused and fixed end-to-end on hardware. This was a long, evidence-heavy session — the entries below are deliberately detailed since most of what was found here is non-obvious and easy to silently regress.
  **ADB client replaced.** The v2.4.5 hand-rolled WebUSB ADB client (manual CRC32, BigInt RSA, raw USB packet framing) never worked reliably and is gone. Replaced with `@yume-chan/adb@2.1.0` + `@yume-chan/adb-daemon-webusb@2.1.0`, lazy-loaded from esm.sh at runtime (works fine under esbuild `--bundle=false` since dynamic `import()` of a URL passes through untouched). Auth uses the library's own `ADB_DEFAULT_AUTHENTICATORS` — the separate `@yume-chan/adb-credential-web` package was tried first but its only export is `default`, not a usable credential store class; dropped entirely, not needed. Shell commands use `adb.subprocess.noneProtocol.spawn()` (Android 5.1 requires `noneProtocol` — `shellProtocol` needs Android 7+). File push/pull goes through `cat > path` / `cat path` over the same spawn mechanism; push does **not** drain the process's stdout after closing stdin — busybox `cat` on TWRP never closes stdout when stdin closes, so draining hangs forever. Reconnecting to a device the browser already has a USB handle for (e.g. retry after a reboot) hangs indefinitely unless the previous `AdbDaemonWebUsbDevice.disconnect()` is called first — the wizard now tracks the last-connected USB device handle and disconnects it before requesting a new one.
  **TWRP detection** changed from `getprop ro.bootmode` / `ls /sbin/recovery` (both unreliable on this TWRP build) to checking the ADB banner product name directly — TWRP self-identifies as `omni_biscuit`, Android as `csm_biscuit`; this is exposed as a public `Client.banner` property.
  **Device picker naming corrected**: Android-mode connections show as **"AEOBC"** in the browser's USB device picker, TWRP-mode connections show as **"Echo"** — the wizard's step descriptions previously had this backwards.
  **Duplicate-device guard added**: step 0 now reads `ro.serialno` and cross-checks it against the device list already loaded in the dashboard before proceeding into the destructive TWRP/wipe flow; throws with a "delete from controller" action if a match is found. Field name for the controller's device-serial comparison is matched defensively (`serial`/`serial_number`/`device_id`/`id`) since the exact schema wasn't confirmed against `em_api.py` at time of writing.
  **Step resumability**: sidebar steps are now clickable to jump to any non-running step, so an aborted provision can be re-entered without restarting from step 0.
  **`su -c` argument handling — load-bearing finding, do not regress.** `su -c` on this device's `su` binary only correctly passes through its command if everything after `-c` is **one single-quoted argument**. `su -c echo "test \"quoted\" value"` (multiple words) silently mangles/drops the quoted portion; `su -c 'echo "test \"quoted\" value"'` (one argument) works correctly. This was the root cause of an entire afternoon of `wpa_cli set_network` quoting failures before it was isolated. Related: `su -c` appears to give each `;`-separated statement inside that single argument its **own shell instance** — `su -c 'x="hello"; echo "got: $x"'` prints `got: ` with the variable empty, because `echo` runs in a different shell than the one that set `x`. Pipelines (`ps | grep ... | while read ...; do ...; done`) do **not** have this problem since they're a single shell construct, not sequential statements — use pipelines, not `;`-separated sequential assignment, for any `su -c` command that needs to pass a value between steps.
  **mksh shell redirects (`>`/`>>`) are unreliable on this device for arbitrary content writes** — confirmed via `printf`/`tee -a`/bare `>` all failing identically with `can't create ... Permission denied` on files and directories with completely correct ownership, mode, and SELinux context (this consumed a large part of the session chasing permissions/SELinux/quota theories before the actual pattern was found). `cp` and `tee` **without** `-a` (create/truncate mode) both work reliably; append-mode opens (`>>`, `tee -a`) do not. The wizard's WiFi config writer works around this by building the full file content in JS, base64-encoding it, decoding via `busybox base64 -d | busybox tee <path>` (never a raw redirect), then `cp`-ing into place.
  **`chmod 666` on a directory breaks it** — stripping the execute/traverse bit on `/data/misc/wifi` (done while debugging file permissions) made every file inside unopenable by any process, including ones with correct individual file permissions, for hours, while every diagnostic pointed at the file rather than the directory. Restore to `770`. The wizard now does this defensively before every WiFi config write.
  **`wpa_cli` on this build (v2.3-5.1.1) requires both `-p <socket dir>` and `-i <iface>` explicitly** for every non-interactive invocation — `ctrl_interface=/data/misc/wifi/sockets` in the config is non-default, so `wpa_cli` without `-p` either fails outright (`Failed to connect to non-global ctrl_ifname`) or, once other client sockets exist in that directory from unrelated processes, mis-selects one of those instead and fails with `Operation not permitted`. `IFNAME=wlan0` as a bare leading argument is **not** valid syntax on this `wpa_cli` build (despite appearing to work for `add_network`/`scan` by accident, since those happened to fall back to the only non-p2p interface) and fails outright for `status`/`list_networks`. Always use `wpa_cli -p /data/misc/wifi/sockets -i wlan0 <command>`.
  **Two independent `wpa_supplicant` processes can run simultaneously and fight over `wlan0` — this was the actual root cause of WiFi config changes silently reverting.** The bare init service (controlled by `start`/`stop wpa_supplicant`) launches a minimal instance with no p2p, no overlay config, no Android control socket. `svc wifi enable` independently launches the **proper** Android-framework-managed instance (`wlan0` + `p2p0`, overlay configs, entropy file, `-g@android:wpa_wlan0` abstract socket for `WifiStateMachine`/`WifiNative`). If both end up running — e.g. because `svc wifi enable` was called once, then the wizard separately `kill -9`'d and `start`'d the bare service — they contend for the same netdev; the framework instance reasserts whatever network it already knows about via its own saved profile, writes it back to `wpa_supplicant.conf` (since `update_config=1`), and the bare instance's `wpa_state` degrades from `DISCONNECTED` to `INTERFACE_DISABLED` and never recovers. **`stop wpa_supplicant` does not reliably kill the bare-service process either** — it can flip `init.svc.wpa_supplicant` to `stopped` while the old process keeps running untouched, serving stale config indefinitely; `start` afterward then silently no-ops or fails because the old process still holds the control sockets.
  **Fix, and the only WiFi reload mechanism the wizard now uses:** write the config file, then `svc wifi disable` followed by `svc wifi enable`. This manages the proper framework instance exclusively, and on this device it auto-associates and obtains a DHCP lease via the framework's own handling with **no manual `wpa_cli reconnect` and no manual `dhcpcd` invocation needed** — both were required workarounds in earlier iterations of this fix and are now actively wrong, since they encourage standing up the conflicting bare-service path. `runConfigWifi` asserts exactly one `wpa_supplicant` process is running after reload and throws if it finds more than one, so any regression of this fails loudly rather than silently reverting again.
  **`com.amazon.android.service.wifiprofilemanager` and `com.amazon.device.smarthome.adapters.wifi` both interfere with manual WiFi configuration** and are now disabled alongside the original 9 Alexa packages in `runDisableAlexa` (12 packages total). `wifiprofilemanager` re-asserts its own saved network profile through the framework `WifiManager` path (pure Java/Dalvik — `classes.dex` only, no native binary, no shell script; it talks to wpa_supplicant via framework IPC, not anything greppable on disk). `pm disable` on the smarthome wifi adapter package does **not** stop the native `/system/bin/SmartHomeWifid` binary — it's launched directly by `/init.smarthome.rc` via a property-trigger chain (`persist.wifi.migrate.complete=1` → `wifi.launch` reaches `111` → `start smarthomewifid`), independent of the Android package manager. Clearing `setprop persist.wifi.migrate.complete 0` (a `persist.` property, survives reboots) durably prevents the chain from ever reaching `111`, so `SmartHomeWifid` never starts on subsequent boots; `runDisableAlexa` also kills it directly if it's already running in the current boot.
  **Package manager not ready immediately after `su -c id` succeeds.** Root/Magisk being up does not mean the Android framework has finished booting — the first several `pm disable` calls in a fresh-boot wizard run can fail with `Could not access the Package Manager. Is the system running?` before later calls in the same loop start succeeding. `runDisableAlexa` now polls `getprop sys.boot_completed` (up to 30s) before the disable loop, plus a one-shot 3s-delay retry on any individual `pm disable` call that still hits the error.
  **WiFi scan** (`wpa_cli -p ... -i wlan0 scan` / `scan_results`) now feeds a network picker UI with signal-strength sorting and manual SSID/password entry as a fallback; config write is single-network-only (deliberately drops any prior Alexa-era saved network rather than risk silent fallback to it).
  **Other fixes**: `awk`/`cut`/`which`/`head` are all absent on this image (only discovered when a wizard run failed mid-PID-extraction) — every PID/field extraction in the wizard now uses `ps | grep ... | while read user pid rest; do echo $pid; done` style pipelines instead, which need no external tool. EchoMuse install step (was: file upload only) now offers "install latest from GitHub" alongside a custom binary upload — the controller endpoint for this (`/api/provision/latest_binary`) is a naming guess based on the dashboard's existing `/api/devices/{id}/...` convention and the project's known `release_poll_loop()` mechanism, **not yet confirmed against `em_api.py`**. Provisioning now ends with an explicit device reboot and a clear "Done" screen instead of leaving the wizard sitting at the last step.
  **Still open**: `/api/provision/latest_binary` and the device-delete route used by the duplicate-device guard are both unconfirmed against `em_api.py` source (the delete route worked once in testing; the latest-binary route 404'd and needs the real endpoint name).
- v2.5.1 — 2026-06-20: Wizard hardening pass — every "still open" item from v2.5.0 closed out, plus several silent-failure classes found and fixed on real hardware. Recurring theme this session: code that assumed a fresh, never-provisioned device, and silently inherited stale state on a re-flash instead of failing loudly.
  **`/api/provision/latest_binary` confirmed and implemented.** No such route existed in `em_api.py` — the v2.4.5/v2.5.0 naming guess 404'd as expected. `GET /api/releases/latest` exists but only returns release metadata (`{version, url}`), not the binary itself, and the fleet OTA path (`/api/devices/{id}/update`) requires a live WebSocket session a freshly-flashed, not-yet-registered device doesn't have. New route reuses the existing `_get_cached_release()`/`_fetch_binary()` machinery and streams the binary directly, with the release version returned in an `X-Release-Version` header so the wizard log can show what was actually fetched.
  **Duplicate-device field matching confirmed and simplified.** `_merge_device()` in `em_api.py` confirms `device_id` is the only identifying field on a device object — it **is** `ro.serialno` (set at registration in `em_controller.py`), not a separate `serial`/`serial_number`/`id` field. The v2.5.0 defensive multi-field guess is gone; the duplicate check now matches on `device_id` alone.
  **Delete-while-connected dashboard hang root-caused and fixed.** Deleting a device from the controller while its duplicate-detection ADB session was still open caused the dashboard to hang at "Authenticating ADB…" on retry, requiring a page reload. Root cause was client-side, not in `em_api.py`: the duplicate-detection throw path never called `c.close()`/`setAdb(null)` on the already-authenticated ADB session before throwing, unlike the clean-exit path a few lines below it. The live transport stayed open and `_lastUsbDevice` kept pointing at it; the next `requestDevice()` call disconnected the stale WebUSB interface claim but never told the still-open ADB transport to close, so the following `Transport.authenticate()` raced a half-torn-down session. Fixed by mirroring the clean-exit teardown on the duplicate-detection throw path. `DELETE /api/devices/{id}` itself was confirmed correct in the same pass — it only touches the DB row, no live WebSocket teardown, which is consistent with the bug being entirely client-side.
  **Wizard navigation reworked — strictly linear, no jump-back, retry-with-different-input added.** Sidebar step list is now a read-only progress indicator (no click handlers) rather than letting the user jump to any `done`/`error`/`pending` step, since jumping back didn't reset downstream step state and there was no way to get the device into the right boot state (TWRP vs Android) for a backward jump anyway. In exchange, retrying a failed step now actually lets you change what you're retrying with: the Magisk-zip and EchoMuse-binary file pickers are now gated on `stepState !== 'done'` instead of `=== 'pending'` (matching the existing WiFi panel pattern), so a failed attempt re-shows the file input instead of vanishing behind a generic "Retry" button that would silently reuse the same — possibly wrong — file. The file selection is also explicitly cleared on error, forcing a deliberate reselect. The generic "Retry" button is now suppressed on steps that have their own dedicated retry UI (Magisk, EchoMuse binary, WiFi), since both rendering at once meant retry-with-no-file-selected was reachable as a confusing second failure mode.
  **EchoMuse install step rewritten after a real-world failure: GitHub-install button was silently keeping a stale binary in place instead of installing the new one.** The original install command chained `mkdir && cp && chmod && ln -sf` with no stderr capture and no result check — a silent `cp`/`ln` failure anywhere in the chain (this device had a stale `server_a`/`server_b`/`server` from a prior OTA-managed install, surviving a wizard re-flash since `/data` isn't wiped by a boot-image patch) would short-circuit the chain before the symlink flip ran, and the wizard logged "EchoMuse installed" regardless. Fixed in two parts: (1) `server`, `server_a`, and `server_b` are now explicitly `rm -f`'d before every install, so a re-provisioned device can't inherit a stale OTA-managed binary state that the wizard's own install logic never accounted for; (2) every step of the actual install (`cp`, `chmod`, `ln -sf`) now runs as a separate `2>&1`-captured call with its output logged, and the result is verified afterward via `readlink` (must report `server_a`) and a `c.pull()` byte-count check against the pushed binary, rather than trusting the chained command's silence as success.
  **Magisk preseed step rewritten after a real-world ~multi-minute hang traced to `magiskd` hard-rejecting every `su` call.** On a re-flash of a previously-rooted device, `su -c id` took up to ~60s per attempt before being rejected — confirmed via on-device `magisk.log`: `sqlite3_exec: no such table: settings` / `strings` / `policies` on every request, followed by `su: request rejected (2000->0)`. Two compounding causes, both fixed: (1) `_get_provision_magisk_db` in `em_api.py` only ever created a `policies` table, with the wrong schema (extraneous `package_name` column, no primary key) — corrected to match Magisk v17.3's actual schema (`policies` keyed on `uid` alone, plus `settings`, `strings`, and `denylist` tables), confirmed against a real working device's `sqlite3 .schema` dump rather than guessed. (2) This alone doesn't explain the failure, since the same incomplete schema has worked across many prior **fresh**-device provisions — magiskd's own first-boot startup appears to migrate/complete an otherwise-valid-but-incomplete `magisk.db` itself, as long as nothing else interferes. On a re-flash, `/data/adb/magisk.img` (Magisk's own module/data image, entirely separate from `magisk.db`, never touched by this wizard) survives from the prior install — boot-image patching and a TWRP Magisk re-flash don't wipe `/data`. `magisk.img` gets merged/mounted at `post-fs-data`, before any `su` request is handled; stale state there plausibly disrupted magiskd's normal DB-migration path on this boot. `runPreseedDb` now `rm -f`s both `magisk.db` and `magisk.img` before pushing the fresh DB — scoped to those two files specifically, not the whole `/data/adb` directory, since TWRP's Magisk zip install (the immediately preceding step) writes Magisk's own binaries and script directories under there too. This step runs in the TWRP shell session (no reconnect happens between Magisk install and preseed), so the clear uses a plain `rm`, not `su -c rm` — there is no `su`/magiskd to broker through yet at this point in the sequence.
  **`readlink` on this device prints an error message on a missing target rather than returning empty output** — discovered when a new `2>&1`-captured `readlink` check (added for the EchoMuse install verification above) broke the existing, working pattern of treating empty `readlink` output as "symlink absent." The existing OTA code in `em_api.py` already worked around this correctly by using `2>/dev/null`; the new wizard-side checks were initially written with `2>&1` for visibility and had to be corrected to match. A planned third verification step (confirming `server_a`/`server_b` are actually gone via `c.pull()`/`cat` after `rm`) was dropped entirely rather than risk the same failure mode with an unverified command — `cat`'s missing-file behavior on this device's shell was never directly tested, and a verification step that false-aborts a working clear is worse than no verification at all.
  **Device rename and delete added to the dashboard's device detail modal**, both admin-gated to match the existing `@auth.require_admin` on `PATCH`/`DELETE /api/devices/{id}`. Rename is inline (click the device name, edit, Enter/Save or Escape/Cancel); delete requires a two-step confirm given it's unrecoverable from the UI. Neither needed new client-side state-sync logic — both routes already broadcast events (`device_update`, `device_deleted`) over the existing `/api/events` WebSocket that the dashboard already merges into device state.
  **Force release-check (`POST /api/releases/check`) wired up — existed server-side, nothing called it.** `_get_cached_release()` serves a 60s in-memory cache, falling back to a DB cache that's only re-polled in the background once it's older than `update_check_interval` (default 1h) — both the Updates tab and the wizard's GitHub-install step only ever read that cache, so neither could surface a release published in the last hour without waiting. `_post_check_release` (force-poll, bypasses both caches) already existed and worked but had no UI calling it. Added a "Check now" button to the Updates tab and a "Check for newer release" button to the wizard's GitHub-install step (shown inline before committing to install, doesn't change what gets installed — that still reads the now-freshly-populated cache).
  **"Deploy all" on the main dashboard replaced with "Check for updates" — it was not inert, it was silently working.** The button called `POST /api/releases/deploy` (real, fully implemented, fleet-wide: pushes the cached latest release to every connected/approved/not-already-current device via `_run_update`, no confirmation step) and discarded the response on success with zero feedback — no log, no toast, no state change. Any prior click most likely deployed to the whole fleet silently. Server-side route is untouched and still fully functional, `@auth.require_admin`-gated as before; the dashboard's only call site for it is gone. Replaced with the same force-check pattern as the Updates tab — genuine live GitHub check, visible loading state, result feeds the existing "Latest Release" display. If fleet deploy is wanted back later, it should get a deliberate UI (e.g. a confirm step listing exactly which devices and versions) rather than a single bare button.
- v2.6.0 — 2026-07-02: ESPHome native API satellite integration — HA MVP. EchoMuse devices now speak the ESPHome native API on the controller's outward-facing side, making them HA-compatible voice satellites via HA's built-in ESPHome integration (no custom HACS component). Full wake-word → STT → intent → TTS → speaker round-trip confirmed working end-to-end on both physical devices against real HA Core 2026.6.4, alongside the existing `claracore` mode (`VOICE_MODE` env var gates the two, never concurrent).

  **New:** `em_esphome.py` + `esphome/` subpackage (`frame_protocol`, `message_registry`, `feature_flags`, `satellite_server`, vendored `api_pb2`/`api_options_pb2` from `aioesphomeapi==45.3.1`). DB migration v2 adds `esphome_api_port` and `esphome_noise_psk` (nullable placeholder, unused pending a future Noise-PSK follow-up) per-device columns. Ports allocated monotonically from 16001, never reused after deprovisioning (ESPHOME_SPEC.md §2.2) — a stale HA-side "add device" entry pointed at a freed port must never silently reattach to a different physical speaker.

  **Protocol findings confirmed against real HA Core 2026.6.4** (some corrected earlier assumptions from the design spec): `project_name` requires dot-notation (`EchoMuse.<label>`) — HA's `manager.py` splits on it unconditionally, IndexErrors silently and the device never appears in Devices & Services without it. Zero entities on `ListEntitiesResponse` → device silently ignored — `media_player` entity is mandatory. `ANNOUNCE` feature flag gates whether HA sends `VoiceAssistantConfigurationRequest` at all. `AuthenticationRequest` is sent by real HA regardless of `uses_password=False` — must be acknowledged, not ignored. `SubscribeVoiceAssistantRequest` **is** sent by real HA (an earlier design-phase assumption said it wasn't). `AnnounceFinished` must be yielded synchronously from `handle_message()` — the base dispatcher calls `list(handle_message(msg))`, so anything from an async task arrives too late for the wizard's own timeout. `MediaPlayerState` transitions `ANNOUNCING → AnnounceFinished → IDLE`, all three, are required for the setup wizard to pass. `ffmpeg` (apt-installed in the controller Dockerfile) decodes HA's MP3 TTS delivery to PCM. mDNS service names use `device_id[-12:]` rather than a prefix — both devices share a `G090LF11` prefix and would otherwise collide in HA's auto-discovery.

  **Bug: `VoiceAssistantEventType` import — wrong enum name, one-line fix.** The real enum (confirmed by installing `aioesphomeapi==45.3.1` fresh from PyPI and inspecting the generated `api_pb2.py` directly, not assumed from memory) is `VoiceAssistantEvent`, not `VoiceAssistantEventType`. Every `ET.VOICE_ASSISTANT_*` member reference downstream was already correct — only the import alias was wrong.

  **Non-bug: `VoiceAssistantResponse` "unhandled" log line was never a problem.** Cloned `OHF-Voice/linux-voice-assistant` (the official non-firmware reference satellite) directly and confirmed its `handle_message` dispatch has no branch for this message at all — it's HA's ack to the satellite's `VoiceAssistantRequest`, and its `port` field is only meaningful for the UDP-audio-return path real ESP32 firmware uses outside `API_AUDIO` mode; response audio here continues over the same TCP connection via `VoiceAssistantAudio` regardless. An explicit no-op branch with a comment was added anyway (cheap insurance against the next debugging session mistaking a `log.debug` no-op for a gap) — no behavioural change.

  **Bug: premature `RUN_END` silently ended turns before STT/TTS ever ran.** HA can — confirmed repeatedly on real hardware — emit a spurious `RUN_END` event moments after `VoiceAssistantRequest(start=True)`, before `STT_START` even arrives, distinct from the genuine terminal `RUN_END` that follows the real TTS sequence several seconds later. The old code treated *any* `RUN_END` as "nothing more is coming" and unblocked the turn-waiter immediately; that early unblock then won the race against `_stream_mic_audio` (still waiting on the device's VAD-end sentinel), so by the time the turn actually reached its TTS wait, the wait was already satisfied with no TTS URL — the turn ended silently, HA's STT/intent/TTS completed correctly in the background entirely unread, and HA's stock "Sorry, I couldn't understand that" response (the reply for a satellite that dropped off mid-pipeline, not a real transcription failure) is what actually played, if anything did. Fixed by tracking `INTENT_END` (the reliable "STT + intent resolution genuinely finished" marker, always after `STT_END`, always before `TTS_START`) and only letting `RUN_END` close the turn once that's been seen — a turn that legitimately ends with no spoken response still passes through `INTENT_END` first, so this doesn't introduce a stall for that case. Verified by replaying the exact logged event sequence from a real broken turn through the patched method and confirming it now survives the premature `RUN_END` and completes correctly on the real terminal sequence; separately confirmed the old logic reproduces the exact failure when fed the same sequence.

  **Bug: standalone announce (setup wizard audio test, push TTS) silently never reached the speaker.** `EchoMuseSatellite._on_announce` was a one-time value copy of `DeviceESPhomeServer._standalone_play`, taken at the moment HA's TCP connection was accepted (`_protocol_factory()`). `_standalone_play` itself is set by `device_connected()`, fired independently by the **physical Echo Dot's own** `/control` reconnect — no ordering guarantee exists between that and HA's independent ESPHome TCP connect, and in practice the HA-side connection routinely won the race, leaving the snapshot at `None` even on freshly-established connections (ruling out a simpler "just goes stale on reconnect" explanation — confirmed on multiple fresh connections in a row). Fixed by giving `EchoMuseSatellite` a back-reference to its owning `DeviceESPhomeServer` and reading `_standalone_play` live at call time in `_fetch_and_play_announce`, instead of ever snapshotting it. Verified by reproducing the exact ordering (satellite constructed before `_standalone_play` is set, callback wired afterward, announce arrives on that same already-constructed connection) and confirming audio now reaches the callback; separately confirmed the old snapshot logic reliably reproduces the exact "no playback callback set" log line under the same conditions.

  **New feature: local no-speech timeout, matching Alexa's "wake word, then silence" behaviour.** Previously, saying the wake word and then nothing left the listening ring lit for as long as HA's server-side VAD took to notice (observed: over two minutes of silence with no local bound at all — the satellite had no independent liveness guard for "no speech was ever detected," only the existing post-speech silence hysteresis and the unrelated 30s TTS-wait timeout, neither of which engages if speech never starts). `streamMic` (device/internal/client/data.go) now runs a 5s deadline from turn start, armed **only when `lock_mic: true`** — see the WebSocket Protocol section above for the wire-level detail and the regression this caused when first shipped without that gate (armed unconditionally, it silently killed the permanent wake-word listening stream 5s after every boot). Cancelled cleanly via `Timer.Stop()` (with the documented drain-on-race pattern) the instant real speech is first detected; from that point on, end-of-turn is owned entirely by the existing silence-after-speech hysteresis, unchanged. On the controller side, the resulting `0x05` sentinel sends an empty `VoiceAssistantAudio(end=True)` to close HA's already-open pipeline cleanly (a real, valid protocol message — confirmed against the actual `VoiceAssistantAudio` protobuf schema, not invented for this purpose) but skips the 30s TTS-response wait entirely, so a no-speech turn closes in ~5s rather than potentially 35s and without generating a spurious `stt-no-text-recognized` round-trip to HA. Device-side test coverage added (`internal/client/streamMic_test.go`, run against a reconstructed minimal module tree with stubbed `gorilla/websocket`/internal packages since this environment can't reach `proxy.golang.org`/`golang.org`): confirms idle listening survives extended silence indefinitely, a bounded voice turn correctly times out and sends the exact expected wire bytes, and mid-turn speech correctly cancels a genuinely-armed timer. Two of the three tests were initially written against the pre-gating design and passed for the wrong reason (no timer was ever armed in either) — caught and corrected once the `lock_mic` gate was added, since a test that can't fail isn't testing anything.

  **Cosmetic, not fixed:** every `VoiceAssistantEventResponse`/`VoiceAssistantResponse` still logs `unhandled message type ... (no response)` from the base dispatcher even when `EchoMuseSatellite.handle_message` genuinely handled it — a `return` (no `yield`) inside a generator function yields nothing, so `list(handle_message(msg))` comes back empty regardless of whether real work happened. Purely a misleading debug-log line, not a functional gap; left alone this session to avoid touching shared base-class logging behaviour in the same cycle as the real fixes above.
- v2.6.1 — 2026-07-03: TCP keepalive, bidirectional volume control, Assist satellite idle transition.

  **Bug: HA reconnect failure after HA restart.** After HA restarted (update-triggered or from the system menu), the ESPHome satellite showed as unavailable in HA and voice turns silently no-opped with `esphome: no active HA connection`. Rebooting the *physical Echo Dot* fixed it — the clue: `device_connected()`/`device_disconnected()` correctly manage the TCP listener socket lifecycle but there was no equivalent for the HA-side client connection. If HA died without a clean TCP FIN, asyncio never got `connection_lost()`, `_active_satellite` was never cleared, and `get_satellite()` returned a stale dead object forever. Fixed by enabling `SO_KEEPALIVE` with explicit Linux tuning in `PlaintextFrameProtocol.connection_made()` via `get_extra_info("socket")` — idle 30s, interval 10s, 3 probes, dead peer detected and `connection_lost()` fires within ~60s. Existing `_on_satellite_disconnected` → `_active_satellite = None` chain was already correct; it just never got invoked. Confirmed on real hardware against a graceful HA restart.

  **New feature: bidirectional volume control.** Volume is now shared state across HA's media player entity, the physical volume buttons, and ALSA — all three stay in sync, survive controller and device restarts, and never drift. Device side: `volumeController.Set()` fires `onVolumeChange` after every ALSA write; `SendVolumeState(level int)` pushes `{"type":"volume_state","level":N}` on change and on every connect; `OnVolumeSet(cb)` + `case "volume_set"` inbound dispatch; `SetVolume(level int)` exported on `Server`. Controller side: `Device.volume` (HA-normalised 0.0–1.0) seeded from `startupVolume` on connect; `volume_state` handler converts 0–175 int, updates `device.volume`, persists to config via read-modify-write; `update_device_volume()` in `em_esphome.py` updates `DeviceESPhomeServer.volume` and immediately pushes unsolicited `MediaPlayerStateResponse` to HA via `satellite._send_one()` (synchronous `transport.write`, no await); `_send_volume_set` async callable injected at `device_connected()` time (same pattern as `_standalone_play`); `MediaPlayerCommandRequest` handler now reads `msg.has_volume`/`msg.volume`, converts to 0–175 int, and fires `send_fn` via `asyncio.create_task`; all four `volume=1.0` literals replaced with `self._current_volume` property on `EchoMuseSatellite`. Boot-restore: the existing config push at `device_connected()` already carries `startupVolume` which `applyHardwareConfig()` applies to tinymix — now fed with a real persisted value rather than always the default.

  **Bug: Assist satellite panel stuck on "Responding" after voice turns.** HA's `RUN_END` arrives while the satellite is still fetching and playing TTS — HA considers its pipeline done, but the satellite hasn't signalled completion. Root cause confirmed by reading `OHF-Voice/linux-voice-assistant/satellite.py` directly: `_tts_finished()` sends `VoiceAssistantAnnounceFinished()` as the idle-transition signal — not a voice-protocol-specific message despite the name. Neither `VoiceAssistantRequest(start=False)` nor `MediaPlayerStateResponse(state=IDLE)` alone are sufficient (both were tried and failed before going to the source). Fixed by sending `VoiceAssistantAnnounceFinished(success=True)` followed by `MediaPlayerStateResponse(state=IDLE)` in `run_esphome_voice_turn`'s `finally` block, after TTS playback and buffer drain complete.
- v2.6.2 — 2026-07-03: Global fleet config with per-device overrides; change-password UI.

  **New feature: global device config with per-device override.** All device config (mic gain, VAD, beamforming, EQ, OWW model/threshold, startup volume) now has two layers: a fleet-wide global default stored in `system_config` (key `global_device_config`, JSON blob, same shape as `DEFAULT_DEVICE_CONFIG`) and an optional per-device override. DB migration v3 adds `use_global_config INTEGER NOT NULL DEFAULT 1` to `devices` — all existing devices default to inheriting fleet config with no behavioural change on upgrade.

  **Config resolution** is handled by `get_effective_device_config(device_id)` in `em_db.py`, which replaces the previous `get_device_config` call in `device_connected()`. When `use_global_config=1`, returns the global config; when `0`, returns the device's own config column. `set_device_use_global(device_id, enabled)` manages the flag — when reverting a device to global, it also resets the per-device config column to a copy of the current global so the stored value stays coherent if the flag is toggled again later.

  **startupVolume is always per-device.** Volume is hardware state set at provisioning, not fleet policy. `get_effective_device_config` always merges `startupVolume` from the per-device config column on top of whatever the global config returns, even when `use_global_config=1`. The `volume_state` read-modify-write path in `em_controller.py` already writes to the per-device column directly and is unchanged — the two mechanisms compose correctly: volume persists per-device, everything else inherits from global unless explicitly overridden.

  **API changes:** `GET /api/global/config` (any auth) serves fleet defaults. `POST /api/global/config` (admin) saves fleet defaults and immediately pushes the updated config to all currently-connected devices with `use_global_config=1`. `GET /api/devices/{id}/config` now returns `{config, use_global_config}` using effective config. `POST /api/devices/{id}/config` accepts an optional `use_global_config` bool in the body: `true` reverts to global (config fields in body ignored), `false` enables per-device override (supplied config written and pushed), absent leaves the flag unchanged (plain config update, used by the global push path). `_merge_device` now includes `use_global_config` so the dashboard has it without a separate fetch.

  **Dashboard — gear icon settings panel.** New `⚙` button in the header opens a `SettingsPanel` modal with two tabs. "Fleet Config" tab: `DeviceConfigForm` (new shared component, also used by the per-device config tab) pointing at global defaults — "Save & push to fleet" persists and pushes to all on-global devices immediately. "Account" tab: change-password form (current password, new password, confirm) backed by new `POST /api/auth/change-password` endpoint — any authenticated user, verifies current password via bcrypt before accepting the new hash. Does not invalidate existing sessions.

  **Dashboard — per-device config tab redesigned.** Toggle banner at the top shows current state: blue tint ("Using fleet config") or green tint ("Device-specific config"). Enabling the toggle seeds local config state from the current global config and makes all controls editable; push sends `{use_global_config: false, ...config}`. Disabling reverts to fleet defaults — push sends `{use_global_config: true}`, body otherwise ignored. Controls render at 45% opacity with `pointer-events: none` when on global — values visible but not interactive without explicitly enabling the override.

- v2.6.3 — 2026-07-04: Speech quality overhaul — dead zone removal, pipeline diagnostics, beamformer structural fix, pipeline toggles.

  This session was driven by a formal speech quality review (SPEECH_QUALITY_REVIEW_Findings_-_4-7-26.md) identifying multiple architectural issues in the mic→OWW→STT chain. The primary symptom was needing to pause after the wake word and over-enunciate the command — same hardware had worked fine under Alexa. Changes are ordered by actual impact as discovered through instrumentation.

  **Root cause analysis — what the instrumentation revealed.** Before fixing anything, VAD diagnostic logging was added to `streamMic` (periodic RMS every ~3.2s) and OWW near-miss score logging added to `wake_word_listener` (scores above 0.05 logged at DEBUG). The data showed: idle RMS at 1.3m is 0.00017–0.00019; conversational speech hits 0.0004–0.0009; vadThreshold was 0.003–0.004 — a 6–10× gap. The device VAD gate was barely opening at conversational distance, so OWW was receiving near-silence. Additionally, AGC was drifting to near its 20× maximum during idle periods, amplifying room noise above VAD threshold and poisoning OWW's internal state with noise frames. RNNoise, running at 16 kHz against a model calibrated for 48 kHz, was miscalibrating speech probability — feeding bad AGC gating decisions and degrading the audio OWW received.

  **P0-1: Wake→turn dead zone removed (controller-only).** The most impactful single change. Previously, on wake word detection: controller sent `mic_stop` → drained queues → sent `mic_start(lock_mic:true)` → device tore down and restarted `streamMic`. Every sample spoken between wake-word end and the new VAD gate opening was lost — OWW chunk quantisation + inference latency + two WebSocket RTTs + fresh gate re-trigger. For a naturally spoken "Hey Jarvis turn on the lamp", the first word of the command fell in this hole, forcing the pause-and-enunciate behaviour. Fix: controller no longer sends `mic_stop`/`mic_start_turn` on wake. Sequence is now: wake detected → `oww_paused.set()` → routing flips from `mic_queue` to `voice_queue` — the stream was already running and the VAD gate was already open (that's how the wake word arrived), so command audio flows in with zero gap. `VOICE_PREROLL_DISCARD = 3` (240ms) discards the wake-word tail ("...Jarvis") from `voice_queue` before sending to HA. Controller-side 5s no-speech timeout replaces the device's `0x05` sentinel (which only arms on `lock_mic` streams). Button path retains `mic_stop`/`mic_start_turn` since there's no dead zone cost — button press happens before speech, so stop/start RTT is fine and directional lock is appropriate.

  **P0-2: Beamformer structural fix (device rebuild).** `BeamformingEnabled` flag previously controlled both smoother updates and output channel selection in `beamformer.Process()`. When disabled, smoothers froze — turning beamforming on mid-session gave cold baselines and garbage direction picks. Fixed by decoupling: smoothers always update regardless of flag; output channel is determined by lock state alone (unlocked → always ch6 omni, locked → selected perimeter mic); flag only gates whether `Lock()` does directional selection or no-ops. This means the baseline is always warm when beamforming is enabled. `Lock()` now takes `enabled bool` parameter; `Process()` signature drops the `enabled` parameter entirely. Button path sends `mic_stop`/`mic_start(lock_mic:true)` to engage directional lock — wake word path does not (stays on ch6 per P0-1). Beamforming is currently off by default: at typical conversational distances (≤1.5m) inter-mic SNR differences are marginal and the wrong-lock risk outweighs the directional benefit. Re-enable once the baseline audio quality issues (P0-3, P0-4) are properly addressed.

  **AGC identified as primary stability problem; disabled.** Instrumentation revealed AGC was the main culprit for the "works for a bit then stops" pattern: during idle periods, AGC drifted toward its 20× maximum gain chasing the -22dBFS target against near-silence. Amplified room noise crossed vadThreshold, filling `mic_queue` with noise frames, running OWW inference on continuous "speech" that wasn't — poisoning OWW's internal state until detection failed entirely. The gain state persisted across stream restarts (mic stream stops/starts on every TTS playback), so there was no natural recovery. AGC is now off by default. The AGC code remains and is re-engageable via dashboard toggle for A/B testing.

  **RNNoise (NS) identified as secondary problem; disabled.** RNNoise vendored model operates natively at 48 kHz. The pipeline feeds it 16 kHz audio — speech energy is squashed into the bottom third of the model's Bark bands, suppression decisions are miscalibrated, and consonant/HF content (exactly what STT needs) gets chewed. Additionally, the miscalibrated speech probability fed back into AGC gating was compounding the AGC drift problem. NS is now off by default. The RNNoise code remains and is re-engageable via dashboard toggle. Proper fix (P0-3) is to either resample 16→48 kHz around RNNoise or replace with a 16 kHz-native suppressor — deferred, but now clearly worth doing since disabling NS+AGC produced the best observed speech quality to date.

  **Pipeline toggles added (device + controller + dashboard).** `nsEnabled` and `agcEnabled` added as `*bool` fields to `config.go` `Device` struct, `ConfigMessage`, `Apply()`, `Snapshot()`, and `loadDefaults()`. Both default true (no behaviour change on upgrade; dashboard global config stores the actual values). `processor.go` `Process()` gains `agcEnabled bool` parameter — AGC block only runs when true, gain state preserved so re-enabling is smooth. `data.go` reads both flags from config snapshot each period. Dashboard advanced section: two new toggles ("Noise suppression (NS)", "Auto gain (AGC)"); VAD threshold slider floor dropped from 0.001 to 0.0001 to allow tuning to the actual measured signal levels.

  **VAD threshold corrected.** Dashboard slider floor was 0.001; measured conversational speech at 1.3m is 0.0004–0.0009; idle noise floor is 0.00017–0.00019. Default now 0.001 (down from 0.003–0.004), slider goes to 0.0001. With NS+AGC off, 0.001 gives reliable gate-open at normal voice levels with adequate noise margin.

  **Controller code quality fixes (no behaviour change).** B3: `DeviceESPhomeServer.stop()` now clears `_active_satellite = None` — previously a device bounce could leave a stale satellite reference causing `get_satellite()` to return a dead connection. B4: `satellite_server.py` dispatch now distinguishes handled-but-no-response from genuinely-unhandled messages via `_HANDLED` sentinel — `handle_message()` implementations yield `_HANDLED` for silent no-ops; only truly unrecognised message types log "unhandled". Four handlers in `em_esphome.py` updated (`SubscribeVoiceAssistantRequest`, `SubscribeHomeassistantServicesRequest`, `VoiceAssistantResponse`, `VoiceAssistantEventResponse`). `VOICE_PREROLL_DISCARD` moved to `em_esphome.py` module level (was a dead constant in `em_controller.py` doing nothing). Inline `import time` and `import em_controller` inside `_stream_mic_audio` replaced with proper module-level imports.

  **Per-turn structured trace added (controller).** `TurnTrace` dataclass in `em_esphome.py` collects timestamps at each pipeline stage (first audio frame, VAD end, STT result, TTS URL, TTS fetch, playback start, completion) and emits a single `[TURN]` log line at turn end. Example: `[TURN] trigger=wakeword(0.522) outcome=ok total=+9216ms first_frame=+257ms vad_end=+5973ms audio=74frames/5920ms stt=+7382ms text='What time is it?' tts_url=+7397ms tts_fetch=+8355ms tts_bytes=74880 playback=+8355ms`. Wake word turns carry the OWW score in the trigger label; button turns are labelled "button". Makes per-turn latency attribution possible without manual log reconstruction.

  **OWW near-miss score logging added (controller).** `wake_word_listener` now logs OWW scores above 0.05 at DEBUG level on every inference chunk. Critical for diagnosing "not responding" — distinguishes "score consistently 0.15–0.25, just below threshold" (tuning problem) from "score 0.01–0.03" (audio quality or pipeline problem). Previously only detections were logged.

  **Known working state as of this version.** NS off, AGC off, vadThreshold 0.001, beamforming off, vadSilenceMs 900. Responding reliably at conversational voice level from 1.3m in a quiet room. Lounge device (TV background) also confirmed working. The "must pause and enunciate" behaviour is gone.

  **Still open (deferred).** P0-3 proper NS fix: resample 16→48 kHz around RNNoise, or replace with a 16 kHz-native model. P0-4 AGC: if re-enabled, needs gain state reset on stream restart and lower max gain (current 20× is too aggressive). P0-5 device-side preroll ring. P0-6 OWW stream continuity (VAD-gated → continuous with zero-fill). Beamformer direction presets in dashboard (Front/Rear/All-round) advertise DSP the device implements but which isn't useful until P0-3/P0-4 are resolved and the baseline is solid.

- v2.6.4 — 2026-07-05: Bug fixes, conversation continuation, speaker stutter fix.
  **Bug: stuck LED ring on error/no-speech turns.** `on_thinking_esphome()` is scheduled via `asyncio.create_task()` at STT_VAD_END. On fast-exit turns (STT error, no-speech timeout), `cleanup_esphome()` ran and set `stop_spin` before the task actually executed. The task then created a new spinner task that nothing owned — LED ring spun forever with no wake or cancel able to clear it. Fixed by guarding `on_thinking_esphome` with `if stop_spin.is_set(): return` at the top — `cleanup_esphome` always sets `stop_spin` first, so this is an unambiguous "turn is over" signal.
  **Bug: TTS audio silently never played.** In `EchoMuseSatellite._handle_voice_event`'s `TTS_END` branch, `self._tts_audio_url = url` and `self._tts_event.set()` were incorrectly indented inside `if self._trace:`. The turn would unblock via RUN_END instead (after INTENT_END), find `_tts_audio_url = None`, log "No TTS audio URL received", and return silently — device said nothing, LED ring kept spinning. Fixed by moving both lines outside the `if self._trace:` block; only the timestamp recording belongs inside it.
  **Feature: HA-driven conversation continuation.** Wired up `continue_conversation` flag in `INTENT_END` data (confirmed present in logs since v2.6.0, previously discarded). `_handle_voice_event` now sets `self._continue_conversation = True` when flag is `'1'`. `trigger_voice_turn()` return type changed `None` → `bool`, returns the flag. `_run_voice_locked` continuation loop: if `should_continue` and not cancelled, drains stale frames, re-arms listening LEDs, loops back into `trigger_voice_turn` without clearing `oww_paused` or returning to OWW idle. Follow-up `VoiceAssistantRequest` is `start=True` with no `conversation_id` — HA threads conversation context server-side (confirmed from linux-voice-assistant source; the settle delay the reference uses after TTS is already covered by `_run_post_turn_playback`'s buffer drain sleep).
  **Fix: speaker mid-stream stutter.** `silenceLoop` in `pcm_speaker.go` uses a non-blocking `select` with a `default` silence-pump case. With `audioChanDepth = 4`, the channel could drain momentarily mid-stream (WebSocket jitter, goroutine scheduling), causing the `default` case to fire and inject a 42ms silence period — audible as a brief "CD skip" dropout. Fixed by raising `audioChanDepth` from 4 to 32 (~1.3s of headroom). Underrun instrumentation added to `silenceLoop` (`[speaker] underrun` log line) — remove once confirmed resolved across a few sessions.
  **Fix: dashboard offline IP display.** When a device is disconnected the dashboard was showing `127.0.0.1` — a Docker NAT artefact where `ws.remote_address` resolves to loopback when traffic comes through the Docker network, used as fallback if the device register message's `ip` field is missing. Fixed at the display layer: `127.0.0.1` treated as absent (shown as `—`); real IPs shown as `X.X.X.X (last seen)` in card subtitle and status tab when offline; small card badge shows `X.X.X.X ↑`. The DB write of `127.0.0.1` only occurs if the device doesn't send an `ip` field in its register message — devices do send it correctly, so this only affects early-registered devices and isn't worth correcting in the DB retroactively.
- v2.6.5 — 2026-07-06: Full implementation of the 2026-07-05 code review, plus follow-on fixes from live testing.
  **C1: HA VAD-end is the endpointing authority.** `_stream_mic_audio` previously had no exit except the device's own RMS-gate sentinel — in a noisy room the gate never closed and the turn (and spinner) hung indefinitely after HA had finished. Now an `_ha_vad_end` event (set on `STT_VAD_END` and `ERROR`) is raced against `voice_queue.get()` every iteration; the device sentinel remains advisory and still wins in quiet rooms. Whole streaming phase wrapped in a 20s hard cap; ffmpeg TTS decode capped at 15s (C1b, was unbounded).
  **C2: conversation continuation actually works.** The continuation loop's `finally` stopped the mic on every iteration but nothing restarted it before looping — continuation turns (shipped v2.6.4) silently timed out as no-speech every time. `mic_start()` now called in the continuation branch before looping.
  **C3: preroll discard is wake-turns-only.** The 240ms `VOICE_PREROLL_DISCARD` was applied to all turn types; button and continuation turns have no wake-word tail, so it just clipped their first word. `preroll_discard` is now an explicit parameter (wake: 3, button/continuation: 0), controlled by an `is_wakeword` flag rather than parsing the logging label. Dead duplicate constant removed from `em_controller.py`.
  **Regression fix: voice_queue drain race.** `oww_paused.clear()` ran before the post-turn drain, so ambient frames accumulated in `voice_queue` between turns and arrived as preamble on the next turn — first turn clean, every subsequent turn garbled (49–75 frames vs the normal 27–38). Drain moved inside `_run_voice_locked`'s `finally`, before the routing flip.
  **Acoustic-feedback guard: mic stops before TTS playback.** Previously only the post-turn `finally` stopped the mic, so the device processed 63–65 frames of its own TTS echo per turn, contended the Wi-Fi radio against incoming speaker frames (audible stutter), and crushed AGC gain. `mic_stop()` now sent immediately before playback in voice turns and around standalone announcements. Combined with the device-side `ResetAGC()`, this allowed **AGC to be re-enabled fleet-wide** (6/6 turns clean after re-enable).
  **Device: preroll ring (§3.4).** `streamMic` keeps the last 16 processed periods (~512ms) while the VAD gate is closed and flushes them upstream at gate open. Fixes the hard splice at speech onset that depressed OWW scores (real attempts measured 0.05–0.27 against the 0.3 threshold) and clipped first phonemes; also covers the continuation-turn gate-starts-closed wrinkle.
  **Device: ResetAGC at stream start.** AGC gain returns to unity on every mic stream start — a gain crushed by loud TTS echo (fast always-active attack, slow speech-gated release) previously persisted across streams and deafened the wake word for seconds.
  **Device: C4 config pointer race.** `Snapshot()` returned `&d.BeamformingEnabled` — a pointer into the mutex-guarded singleton, dereferenced by `streamMic` after `RUnlock` and racing `Apply()`. Copied to a local like the other fields.
  **Device: C5 (partial) — mute stops the stream.** Muting previously only refused *new* controller `mic_start` calls; an already-running stream kept sending audio while the ring showed red. The mute callback now calls `StopMic()`/`StartMic(false)`. Hardware half still open: ctls 105/106 mute chip A only; the on-device `tinymix -D 0` dump (now committed at `device/tools/tinymix_controls_output.txt`) confirms the B–D mute controls at 123/124, 141/142, 159/160 — adding them to `applyMute` is the next device rebuild item.
  **Device: B7 mutex encapsulation** — `SetOnMuteChange`/`SetOnVolumeChange` methods added so `Server` no longer reaches into the controllers' locks. **Q3** — misleading AGC/vadProb comments corrected (no behaviour change).
  **Device: speaker EOS vs underrun.** 0x03 EOS now calls `EndStream()` so `silenceLoop` logs "stream complete" instead of a false underrun when the audio channel drains at natural end of stream.
  **Q1: owwSpeexNs toggle.** openwakeword's built-in speexdsp noise suppressor (16kHz-native, wake path only) exposed as a per-device/global config toggle with live model reload; `speexdsp-ns==0.1.2` pinned in requirements (wheel confirmed installable against python:3.12-slim). Off by default pending an A/B wake-rate test in a noisy room.
  **Q2: default vadThreshold 0.003 → 0.001** — 0.003 sat above the measured conversational speech range (0.0004–0.0010 at 1.3m), so a fresh device or config reset failed to gate speech; 0.001 matches the validated v2.6.3 value and the dashboard fallback.
  **Q4: OWW near-miss visibility** — scores > 0.05 now log at INFO (rate-limited 1/2s per device) and increment a controller-owned counter shown on the dashboard status tab (kept out of `device.stats`, which the device's 30s hardware report overwrites).
  **M1** — button voice-turn task keeps a reference and logs exceptions via a done-callback instead of vanishing silently.
  **Misc controller** — `handle_data` queue-full now drops the oldest frame, not the newest (keeps the audio tail contiguous with real time); `_fetch_tts_audio` retries once (0.5s backoff) on intermittent tts_proxy fetch failures; idle OWW mic-queue timeout log demoted WARNING→DEBUG (fired every 10s per idle device); dashboard "omni" beam preset now sets `beamformingEnabled: false` (was `true`, which is AUTO perimeter-mic selection — not what the label promised).
- v2.7.0 — 2026-07-06: Ungated wake stream, mic-stream leak fix, noise-floor endpointing, mid-stream beam lock.
  **Ungated, AGC-free wake stream.** The always-on (!lock_mic) stream sends every 32ms period continuously (batched into 80ms frames) — no VAD gate, no preroll, no sentinels, AGC forced off regardless of config. OWW scores uninterrupted audio; no adaptive gain state can rebaseline against room noise (the root cause of the lounge device's wake death). VAD gate/AGC/preroll remain for bounded lock_mic (button) streams only.
  **Mic-stream leak fixed.** StopMic→StartMic pairs (sent after every turn) could spawn a replacement stream while the old goroutine drained; the old goroutine's defer then cleared micActive over the new stream, and the next mic_start spawned an unstoppable duplicate. Leaked gated streams duplicated all speech 2× and their VADEnd sentinels cleared the OWW buffer — the historical "wake degrades over days, reboot fixes it" root cause. Fixed by ownership check (`d.micStopCh == stopCh`) in the defer.
  **Controller-side noise floor + SNR endpointing.** Per-device asymmetric-EWMA noise floor (measurement only); esphome no-speech timeout restored to 5s, disarming only on SNR-relative speech or HA STT_VAD_START. beam_lock/beam_unlock control messages lock the beamformer at wake detection without a stream restart. Button path does mic_stop+mic_start post-turn so a gated turn stream can't persist as the wake stream.
- v2.7.1 — 2026-07-07: 24-bit fixed mic gain, PTY dashboard shell, log cap, state-aware landing page.
  **Fixed mic gain (`micGainDb`, default +24dB).** 20h of v2.7.0 fleet logs showed speech RMS at wake detection of 0.0001–0.0006 FS (~3–20 LSB in 16-bit) and a 6/19 empty-transcript rate: the S24→S16 extraction took the upper 2 bytes and discarded the low byte, where nearly all the signal lives at this hardware's capture levels. Gain is now applied to the full 24-bit sample (Q12 fixed-point, clamp to int16, clip counter) before quantisation — recovering real resolution rather than amplifying 16-bit quantisation noise. `vadThreshold` stays in pre-gain units (the device scales it by the linear gain internally), so stored configs never need retuning in lockstep. Validated on hardware: detection rms 0.0003 → 0.006–0.009, 5/5 clean transcripts, clipped=0. This is the "fixed gain" stage of the dumb-transducer target architecture.
  **PTY dashboard shell.** `shell_open` accepts `pty: true` (dashboard sessions only) — the device attaches sh to a real PTY (`/dev/ptmx` + `x/sys/unix` ioctls, `TERM=xterm-256color`); input is framed (0x00 stdin / 0x01 resize), output raw, controller proxies verbatim and announces the established mode via `shell_meta`. Dashboard terminal is vendored xterm.js 5.5.0 with a local-echo line-mode fallback for pre-PTY firmware. Programmatic sessions (OTA, `_shell_run`) keep the raw pipe.
  **/tmp/server.log cap.** Background trim loop in start_server.sh (5MB cap, newest 512KB kept in server.log.1; truncate-in-place is safe against the O_APPEND fd). A 45MB log was found on a device — /tmp is RAM-backed. Device VAD diag slowed from ~16s to ~10min cadence, with a prompt line whenever the clip counter moves.
  **State-aware landing page.** `/` now checks a stored session (→ /dashboard), then `GET /api/system/setup-state` (public, boolean only): first-run setup form with amber pulsing LED ring, or login form with green ring. `/setup` redirects to `/`. Sessions moved from sessionStorage to localStorage. The dashboard's internal Login component deleted — the landing page owns auth; logout and unauthenticated /dashboard visits redirect to `/`. SVG favicon (mini Echo ring) on both pages.
  **Config tab reorder.** Global + per-device config now run Playback → Wake word → Microphones → Advanced (turn processing + speech gate combined, "button turns only"); flow connectors dropped since order is by relevance, not signal path. All controls audited post-pipeline-changes: none dead; VAD threshold slider relabelled with pre-gain units.

- v2.7.2 — 2026-07-07: Beamformer lock-back selection.
  **Lock-back.** Controller wake detection lands 300–500ms after the wake word ends, so `Lock()`'s live onset ratio scored a decayed spike — the selected mic (and direction LED) was often unrelated to the speaker (the "known-poor selection" caveat since v2.7.0). The beamformer now records a ~2s ring of per-direction period energies (frozen while locked, like the baseline); `Lock()` scores each direction by the mean of its top-8 period energies within the window relative to its baseline, selecting on the recorded wake word rather than the present. Falls back to the live onset ratio when the ring is empty and raw energy when the baseline is cold. Unit tests added (`beamformer_test.go`, runnable in the compiler image with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` — the image cross-targets ARM by default). Known caveat: TTS echo enters the ring between turns (the baseline absorbs the same energy, damping its ratio); continuation-turn locks remain the weaker case until AEC.

- v2.7.3 — 2026-07-07: Acoustic echo cancellation (default off).
  **AEC.** speexdsp echo canceller (MDF, vendored SpeexDSP-1.2.1, float build, cgo like RNNoise) on the mono mic stream, right after beamformer+gain and before NS/AGC — the whole mic path including the wake stream. Far-end reference is tapped at the ALSA write in the speaker silence loop (every period *including silence*, so the reference clock advances in lockstep with playback), downmixed and 3:1 box-decimated 48k stereo → 16k mono, and buffered in a ring seeded with `aecDelayMs` of silence — modelling write-to-ear latency (speaker ALSA buffer ≈340ms). Both PCM devices share the codec clock, so ring occupancy cannot drift. Config: `aecEnabled` (default **false** — inert until enabled per deployment), `aecDelayMs` (default 250), `aecTailMs` (default 300); applied live on config push (echo state rebuilt on param change). Functional unit test drives the full WriteFar→ring→Process path with a synthetic aligned echo: 42dB attenuation, zero ring underruns (run with `GOOS=linux GOARCH=amd64 CGO_ENABLED=1` in the compiler image). Tuning guidance: enable on one device, speak during/after TTS playback; if residual echo persists, sweep aecDelayMs ±100ms (watch `[aec] reference underrun` — those indicate delay far too small); raise aecTailMs in reverberant rooms. Vendoring note: `_kiss_fft_guts.h` gained an include guard (kiss_fft.c + kiss_fftr.c share one cgo translation unit).

- v2.7.4 — 2026-07-07: Backlog quick fix-ups (C5 mute, §3.5, §3.6/B5, Q5).
  **Full-chip ADC mute (C5).** The mute button previously muted codec chip A only (ctls 105/106) — chips B–D, including ch6 (the mic OWW and STT actually use), stayed physically hot and the mic stream-stop was what made mute effective. All four confirmed pairs now toggle together; the red ring means hardware mute.
  **Beamformer buffer reuse (§3.5).** decode + band-diff analysis buffers allocated once in `New()` instead of ~24kB per 32ms period (~750kB/s GC pressure gone). `extractChannel` deliberately still allocates per period — data.go's preroll ring retains those slices.
  **VAD sentinel encoding (§3.6/B5).** The end-of-speech queue sentinel now carries its own type (`VAD_SENTINEL_END`/`VAD_SENTINEL_TIMEOUT` strings, defined in em_esphome.py; consumers accept legacy None defensively) — the old None + `device.last_vad_was_timeout` side-channel could have its flag overwritten by a second sentinel queued before the first was consumed.
  **Q5.** Speaker mid-stream underrun WARNING removed (clean since the v2.6.5 EOS disambiguation); dead legacy `Pump()` removed from the Speaker interface and implementation.

- v2.7.6 — 2026-07-07: Wake-word barge-in (default off).
  **Barge-in (§3.2).** Saying the wake word during TTS playback cancels the response and starts a fresh turn. Config `bargeInEnabled` (default false) + `bargeInThreshold` (default 0.6; effective threshold is max(barge, oww) so residual post-AEC echo can't self-trigger) — dashboard toggles live in the Wake word section. Mechanics: with barge-in on, `post_turn_play_esphome` skips the pre-playback `mic_stop` (the pre-AEC acoustic-feedback guard — safe now because device AEC subtracts the speaker output and AGC no longer exists on the wake stream) and runs `_barge_watcher`, which drains voice_queue (fed via oww_paused routing, otherwise unread during playback) and scores it with a dedicated per-device openwakeword instance (the main wake listener task is blocked awaiting the turn). Detection sets `barge_detected` + `cancel_event` — aborting `stream_speaker` and the drain sleep — and sends the new `speaker_flush` control message; the device discards its queued speaker periods (up to ~1.4s of buffered TTS; the ≤4 ALSA periods ≈170ms already in hardware still play). The turn loop then re-enters a fresh turn ("barge-in" trigger, wake-word preroll discard), keeping the mic running so words spoken in the same breath as the wake word survive. **Enable AEC first** — without it the watcher scores raw echo and the raised threshold is the only defence. Old device binaries log speaker_flush as unknown and let buffered audio play out (degraded, not broken). Standalone announcements (wizard/push TTS) remain non-interruptible.

- v2.7.7 — 2026-07-08: AEC actually works now; barge-in validated end-to-end; controller/device versioning split; HA-restart reconnect fix. A long root-causing session — four of the five bugs below masked each other, and every one produced the same symptom ("barge-in doesn't hear me").
  **ESPHome listeners never came back after an HA restart (controller).** Python 3.12 changed `asyncio.Server.wait_closed()` to block until all *accepted connections* finish, not just the listener. `DeviceESPhomeServer.stop()` (run on every device control-WS blip) closed the listener then parked forever while HA stayed connected; `device_connected()` saw `_server` still set and reported "already listening" against a dead port. When HA eventually restarted, the parked stops completed, the ports went down for good, and HA got connection-refused until the controller was restarted. `stop()` now detaches state before awaiting and closes the active satellite connection.
  **Controller/device versioning split.** Device firmware keeps plain `v*` tags (embedded in the binary, compared by OTA — unchanged). Controller releases use `controller-v*` tags → new `controller-release.yml` workflow → Docker image on `ghcr.io/wilbowes/echomuse-controller` (`X.Y.Z` + `latest`, CPU-only; **no GitHub Release created** — the OTA system polls repo releases for device firmware, and `_fetch_latest_release` is additionally hardened to filter for `v*`-tagged releases carrying a `server` asset). `controller/version.py` resolves the controller's own version (baked env → git describe → dev); surfaced in the dashboard header, `/api/system/status`, and as the ESPHome project version in HA. `requirements.txt` now defaults to CPU onnxruntime with a `GPU=1` Docker build arg for the CUDA swap; `docker-compose.deploy.yml` is the user-facing pull-and-run compose; quickstart/README lead with the prebuilt image.
  **AEC had never processed a single sample on hardware (v2.7.3–v2.7.6).** The load-bearing discovery: GoTinyAlsa's `GetAudioStream` reads `pcm_get_buffer_size` per chunk — the *whole* ALSA buffer (PeriodSize 512 × PeriodCount 5 = **2560 frames = 160ms**), not one period. The mic pipeline therefore runs on 160ms batches, and `aec.Process`'s `len(mono) != FrameSize*2` guard silently passed every buffer through untouched. Zero cancellation at every `aecDelayMs`, ring pegged at capacity, and — the cruel part — zero underruns or any other log to give it away, while the unit tests (single-frame buffers) showed 42dB. The v2.7.3 hardware "validation" (enabled + no underruns + clean turns) never had a chance of catching it. Fixes: `Process` handles any multiple of FrameSize (subframe loop); unsupported sizes are **logged loudly, never silently bypassed**; `TestHardwareShapedBuffers` drives real 2560-sample batches (45.8dB). Found via staged telemetry now kept permanently: `[aec] att=…dB mic= out= ref= ring=` ~1/s during playback and `[aec] far: rms= ring=` on the reference side.
  **Reference-ring staleness governor.** `WriteFar` fills the reference ring continuously (speaker silence loop) but `Process` stops with the mic stream, which restarts around every voice turn — each gap leaves unconsumed reference behind, and with equal produce/consume rates the backlog never drains; it compounds until the ring holds 3s-stale audio. An occupancy governor (post-consume low-water check) trims backlog beyond `delaySamples`+128ms and resets the filter; regression test simulates the mic gap.
  **`aecDelayMs` correct value is 0, defaults changed (was 250).** With the mic side reading 160ms batches, the capture path absorbs most of the speaker's write-to-ear latency; values ≥100 made the echo arrive *before* its reference (non-causal → zero cancellation, undetectable by the underrun counter, which only catches delay-too-small). Measured on hardware: converges to ~14dB per response at delay 0. Note the filter re-converges each turn — the beamformer locks a different channel per turn and each mic has a different echo path (per-channel filter states are the future fix).
  **Barge-in flush didn't actually stop playback.** `stream_speaker` writes the whole response into the WebSocket ahead of playback, so at barge time the remainder sits in TCP buffers on both ends; the device's `Flush()` drained its ~1.3s channel once and the WS reader refilled it — playback carried on after a skip, the interrupting turn open-mic'd the still-playing TTS, STT transcribed the assistant's own voice, and HA answered itself. Fixed device-side with a stateful flush (drain + **discard-until-EOS**: drop every subsequent 0x02 period until the stream's 0x03 arrives — immune to any amount of network buffering; a `streamActive` check keeps a flush racing a natural stream end from eating the next stream), plus controller-side guaranteed EOS: `stream_speaker` now sends 0x03 from a `finally` under `asyncio.shield`, since barge cancels the task mid-send and a stream ending without EOS would leave the discard armed against the next turn's audio.
  **Barge threshold semantics inverted — it must sit *below* the wake threshold.** The old `max(bargeInThreshold, owwThreshold)` floor guarded against echo self-triggering before AEC worked. Measured with working AEC: self-echo peaks 0.004 converged / 0.055 worst-case-unconverged, while a person talking over TTS scores only ~0.10–0.12 (the echo is ~25dB louder than the talker at the mic — speech-over-playback scores are inherently depressed). The max() is gone, `bargeInThreshold` is used as-is, default 0.10, dashboard slider floor lowered 0.3 → 0.05. Validated end-to-end: trigger at 0.104 → flush (33 periods + discard) → interrupting turn transcribed the user's actual words.
  **Barge re-entry re-arms listening LEDs** (the ring went dark while listening for the interrupting command — cleanup ran but the re-entry skipped the LED re-arm the continuation branch already had). *Known issue: LED state after barge-in is still not fully accurate — needs a dedicated pass.*

- v2.7.8 + controller-v2.8.1 — 2026-07-10: barge-in works in *real use* now, not just validation; mute and volume behave sanely mid-turn.
  **AEC filter no longer resets on reference resyncs — the real-world barge-in killer.** v2.7.7's validation passed, but a field test the same week failed completely (watcher peak 0.007 against the 0.10 threshold across 10s of the user repeating the wake word). Root cause: the mic ALSA ring is only PeriodSize 512 × PeriodCount 5 = **160ms deep**, so any stall of the capture chain longer than that silently loses whole 2560-sample batches at the hardware — and this happens every ~20–30s in steady state (confirmed: resync backlogs are always integer multiples of 2560 + phase). Each overrun left excess reference in the AEC ring; the occupancy governor trimmed it *and reset the speex filter* — including mid-playback — so the canceller lived in a permanent converge → reset → converge loop and never held more than ~5dB. The trim itself restores the exact alignment the filter converged against (both sides lose matching audio), so the learned echo path is still valid: the reset is now simply removed. `TestGovernorRecoversFromMicGap` post-gap attenuation went 22dB → **43dB** with the change. Live result: speech-over-TTS barge scores went 0.007 → 0.267–0.538, and converged self-echo peaks at 0.002–0.003 across consecutive turns — `bargeInThreshold` can safely sit at the 0.05 slider floor.
  **Mic capture-loss telemetry** (`pcm_microphone.go`): `[mic] capture stall:` logs any inter-batch gap >2× the batch duration (an overrun in progress, with estimated ms lost), a ~1/min `[mic] clock:` ledger tracks audio-received vs wall-clock (steady deficit growth = chronic loss; it also cleanly distinguishes overruns from clock-rate mismatch, which the first hour of data ruled out — deficit flat), and subscriber-queue drops are counted. Overruns are load-correlated (every ~5s during an OTA transfer); deeper ALSA buffering needs a GoTinyAlsa change first (per-period reads — `GetAudioStream` currently reads the whole buffer per chunk, so raising PeriodCount would also balloon the 160ms batch).
  **Post-playback drain sleep races cancel_event (controller).** `stream_speaker` finishes *writing* the response ~2× faster than it plays, so a barge-in usually lands during the buffer-drain `asyncio.sleep` — which nothing could cancel. Observed: device flushed instantly, controller hung for the remaining 5.7s of response length — no listening LEDs, and everything said in the window (including the wake word itself) piled into voice_queue, handing STT garbage like "Stop. Hang Rothsby, stop." The sleep now races cancel_event; barge → listening ring in well under a second and the interrupting turn carries only post-wake-word audio.
  **Volume arc survives active turns.** The turn animations repaint the ring continuously (~100ms cadence), so a volume press mid-turn showed the cyan arc for one frame — a glitch, not a reading. Controller LED frames now keep recording into `baseLEDs` during the arc's 2s display window but don't paint; on expiry the ring repaints the *latest* stored frame, handing back mid-animation with no dark gap. Idle behavior unchanged.
  **Mute terminates an active turn (controller) and the red ring is now actually sovereign (device).** Pressing mute mid-turn previously left the turn running against a silenced mic until it timed out. Now `mute_state(muted=true)` with the voice lock held cancels the turn exactly like the dot button, plus a `speaker_flush` so in-flight TTS goes silent immediately. That exposed a device gap: nothing actually blocked controller LED writes while muted (it never came up — turns couldn't overlap mute before); `SetLEDs`/`SetDirectionLEDs` now refuse to paint over the mute ring, making the long-documented sovereignty real. The cancelled turn's LED cleanup lands harmlessly in `baseLEDs`.

- 2026-07-11 (untagged, on top of v2.7.8): per-device WiFi change hardware-validated — three latent bugs found and fixed in one test session, each one a lesson in how differently the same commands behave inside the init-spawned Go binary vs an ADB shell.
  **The first "successful" switch never happened — `svc` is a shebang-less script.** `/system/bin/svc` starts with a `#` comment, not `#!`: a shell interprets it fine (which is why every provisioning-era test over ADB worked), but Go's `exec.Command` uses raw execve, which returns ENOEXEC — and `bounceWifi` discarded the error. WiFi never bounced, the supplicant kept its in-memory config, the SSID-blind `associated()` gate saw `wpa_state=COMPLETED` (for the *old* network) and waved the change through, and the false success **committed** — deleting the rollback backup while a garbage conf sat on disk. A reboot would have stranded the device; recovery was `wpa_cli save_config` (the running supplicant still held the working config in memory). Fixes: `svc` runs via `/system/bin/sh` with errors checked; the disable is *verified* to have dropped association before proceeding; the association gate requires the target SSID specifically, not just COMPLETED. Also fixed en route: `compile.sh` used bare `git describe --tags`, which picked up `controller-v*` tags — device builds now `--match 'v*'`.
  **The conf must be written while WiFi is down.** With the bounce actually working, the switch *still* failed — the supplicant rejoined the old network every time. On `svc wifi disable`, WifiStateMachine saves its in-memory network list back over `wpa_supplicant.conf`, clobbering anything written beforehand. The provisioning wizard's write-then-bounce order got away with it because a factory device has no framework-known networks to save; a provisioned device faithfully restores its current one. New order everywhere (change, revert, startup recovery): disable (verified) → write conf → enable. `associateTimeout` also raised 20s → 45s — only first-joins pay it; reverts re-associate to a known network well inside 20s.
  **Result delivery is now at-least-once.** The first genuinely-successful switch reported "timed out" to the dashboard: the switch kept the same IP, so the control WebSocket's TCP connection survived as half-open — `IsConnected()` said true, the single `wifi_result` send vanished into the dead socket, and the old take-and-send semantics meant the queued result was already consumed. The device now keeps the result pending and re-sends it (on reconnect + a 10s ticker) until the controller acks with `wifi_commit`, which the controller now sends for **every** wifi_result — for failures the marker/backup are already gone, so the ack is a harmless no-op; duplicate results are recorded/logged once.
  **start_server.sh fleet drift closed (same day, follow-up).** The startup script is installed at provisioning and had no update path afterwards — audit found Lounge a revision behind Office (missing the amp_off/idle-hiss fix; canonical md5 531dc3f2, Lounge d26fce30). Two fixes: Lounge got a one-off push (heredoc transfer, md5-verified, rename into place — takes effect on next reboot since the running shell holds the old inode), and every firmware OTA now syncs the device's script against the canonical `controller/device_payloads/` copy (`_sync_start_script`: md5 compare → skip when current; push + verify + rename when stale; best-effort, never blocks the firmware update). The binary-transfer helper was generalised to `_stream_file_to_device` for this — future payloads (BLE proxy etc.) ride the same path.
  All three safety paths validated on the Lounge device: rollback (garbage SSID — dropped, reverted, reported in 65s), startup recovery (marker found at boot → previous network restored + reported), and the happy path (Neptune-Secure → Neptune-Media in 30s, committed, no artifacts left). The WiFi tab's scan also works live (note: hidden SSIDs render as `\x00…` — cosmetic, unfiltered for now).

- 2026-07-11 (later session, untagged after v2.7.9): three quality-of-life fixes.
  **Barge-in now works during the thinking phase.** The watcher previously spawned only when TTS playback began, so a wake word spoken while the assistant was still thinking was buffered and then discarded — barge-in "only kicked in during the response" (user report). The watcher now starts at STT_VAD_END (thinking onset) and spans thinking → playback with a phase-dependent threshold: during playback it uses `bargeInThreshold` (speech-over-TTS scores are depressed ~25dB by the echo), during thinking — where nothing is playing and a 0.05 threshold would fire on random speech — it uses the device's normal wake threshold. Detection during thinking cancels the in-flight HA pipeline (`cancel_voice_turn`, same mechanism as mute/dot-button) instead of flushing a speaker that isn't playing; the turn loop's existing barge re-entry handles the rest. Watcher lifecycle moved to the turn loop's `finally` so every exit path stops it.
  **HA's wake-word dropdown no longer goes stale.** The ESPHome satellite advertises the device's wake word in `VoiceAssistantConfigurationResponse`, which HA requests only at connect time — and the advertised model was cached when the satellite server object was created, so dashboard wake-word changes never reached HA (it showed the provision-era model forever). `update_oww_model` now runs on every wake-word config change (per-device, global, and device-connect): it updates the stored id and bounces the active HA connection, which redials within seconds and re-reads the configuration. (The "Wake Word 2 = none" slot HA shows is normal — we advertise one model with `max_active_wake_words=1`; slots exist for satellites doing on-device multi-wake-word.)
  **Hidden SSIDs filtered from WiFi scan results.** wpa_cli renders hidden networks' zeroed SSIDs as literal `\x00\x00…` escapes, which showed up as garbage rows in the WiFi tab's network list. Scan now drops any entry that is entirely `\xNN` escapes — they're unjoinable by name anyway.

- 2026-07-12 (untagged): thinking-phase barge-in gained a second, lower detection tier. First real-world test of thinking-phase barge-in (three interruptions in succession) showed a genuine attempt scoring 0.240/0.242 on two consecutive frames against the single-frame wake threshold of 0.50 — missed, and the unwanted answer played in full. The watcher's thinking phase now fires on either a single frame at the wake threshold (as before) or two *consecutive* frames at `max(0.2, 0.4 × wake threshold)`. The consecutive-frame requirement is what keeps the low tier safe: random-speech near-misses observed in the logs are isolated single frames, while a real wake word sustains its score across frames. Playback-phase detection is unchanged (`bargeInThreshold`, single frame — echo depression already forces it low). A stream-reset sentinel clears the consecutive-frame history, so frames spanning a mic-stream restart can't pair up. Controller-only change, no device OTA needed.

- 2026-07-12 (later, untagged): the legacy `claracore` voice backend is gone. ESPHome/HA has been the fleet's only mode since 2026-07-06 and the claracore path (bespoke `VOICE_WS_URI` WebSocket exchange, `run_voice_turn`, the `VOICE_MODE` switch and all its branches) was unmaintained and already incompatible with the ungated wake stream. ~210 lines removed from `em_controller.py`; `.env` loses `VOICE_MODE`/`VOICE_WS_URI` (both ignored if still set). The ESPHome satellite path is now unconditional.

- 2026-07-12 (later still, untagged): P0-3 closed — noise suppression on the ASR path, done the way the architecture said it should be. The device's RNNoise stays dead (48kHz model, 16kHz audio — the original P0-3); instead the controller now runs DTLN (dual-signal LSTM, 16kHz-native, MIT, ~1M params, two ONNX models riding the existing onnxruntime dependency) on exactly one stream: the turn audio sent to HA's STT (`em_ns.py`, hooked into `_stream_mic_audio` behind the per-device `nsAsr` flag, default off, dashboard toggle in Microphones → advanced). The wake stream and the noise-floor measurement stay raw by design. Synthetic validation in-container: ~28dB attenuation on noise-only segments, −0.6dB on the speech band, 32× realtime on one CPU core (2.5ms per 80ms frame, run in the shared executor). Fail-open everywhere: missing models or a mid-turn inference error log a warning and stream raw. Models are vendored into the Docker image at build time pinned to a commit (bare-metal: `NS_MODEL_DIR`). Validation tooling: set `NS_DEBUG_DIR` and every denoised turn writes a raw/denoised WAV pair — listen to exactly what STT received. Expectation to hold it to: helps steady noise (fan/AC/hum) at marginal SNR — the "what's the time" → "bang bang" class of garble; does little against competing TV speech (that's the beamformer's fight).

- 2026-07-12 (device batch, untagged — binary `20260712-0155-dev`): two device changes riding together.
  **On-device RNNoise removed.** It never worked (48kHz-native model fed 16kHz audio — P0-3) and its replacement now lives controller-side (DTLN, `nsAsr`). Deleted `internal/rnnoise/` (vendored C + cgo bindings), the NS stage in `internal/processor`, and the `nsEnabled` config key end-to-end (device `ConfigMessage`, controller default, dashboard toggle). The RNNoise speech-probability interlock on AGC release went with it — it was dead code whenever NS was off (the shipped state for months), so AGC behaviour is unchanged: release gates on the RMS speech flag alone. Binary shrinks ~1MB. A stale `nsEnabled` in stored configs is harmless both directions (new firmware ignores unknown fields; old firmware keeps honouring the stored False until OTA'd).
  **Mute-button LED wired in.** The Dot has a discrete red LED under the mic-off button that stock Alexa lights when muted and EchoMuse never used. Recon via the shell proxy found it: it's not on the IS31FL3236 ring controller (all 36 channels = 12×RGB) but a bare GPIO — `/sys/class/gpio/gpio445/value` — confirmed by Amazon's own `libled_controller.so` symbols (`IssiLedDevice::setMuteButtonBrightness`, GPIO export path) and by toggling it live. New `internal/bindings/led/mute_button.go` (defensive export + direction, binary on/off); `muteController.applyMute/applyUnmute` drive it alongside the red ring, and startup init forces it off so a crash-while-muted can't leave a stale red button. Being GPIO-backed it needs none of the ring's repaint-suppression machinery.

- 2026-07-12 (follow-up, binary `20260712-0326-dev`): mute-button LED polarity was inverted — first field test showed no light on mute (and, per the electrical reality, the button would have been glowing while *unmuted*). Ground truth came from pulling `/system/lib64/libled_hal.so` off the device (base64 over the shell proxy) and disassembling `IssiLedDevice::setMuteButtonBrightness`: it streams **0** to `gpio445/value` for brightness > 47 and **1** for ≤ 36 — the line is active-low. (Same ELF confirmed `k_muteButtonGPIOAddress` = 0x1BD = 445, so the GPIO identification was right all along.) `SetMuteButtonLED` now writes 0=on/1=off, init parks it at 1.

- 2026-07-12 (BLE proxy, untagged — device + controller): the Dot becomes a Home Assistant **Bluetooth proxy** — a second use for hardware that was sitting dark. Per-device `bleProxyEnabled` toggle (dashboard stage 06, default off), feeding Bermuda room-presence and advert-based BLE sensors.
  **Hardware path (validated on Office before any code was written).** The MT8163's combo chip is exposed by MediaTek's WMT stack as `/dev/stpbt` — a raw H4-framed HCI char device; opening it powers the BT function on and downloads the firmware patch. It's single-owner, so enabling the proxy durably disables the Android Bluetooth stack (`pm disable com.android.bluetooth` + the Amazon csmbluetooth/bluetoothdfu packages + `settings put global bluetooth_on 0` — survives reboots; nothing EchoMuse uses needs Android BT). Validation from a raw shell: `autobt inquiry` (chip init + classic inquiry, found a neighbour), then hand-rolled HCI over `busybox printf`/`hexdump` — Reset → LE Set Scan Parameters (passive 100ms/50ms) → LE Set Scan Enable produced a live stream of LE Advertising Reports with the chip's **default event masks** (the vendor patch leaves LE Meta enabled — the Go init sequence stays minimal to match exactly what was validated).
  **Device** (`internal/bluetooth`, pure Go, no cgo): H4 stream reassembler + LE Advertising Report parser (unit-tested against bytes from the live capture), passive scan with HCI duplicate filtering off (Bermuda needs continuous RSSI), adverts coalesced per (address+payload) and batched (250ms/48-distinct) up the control WebSocket as `ble_adverts` JSON, watchdog re-init after 30s of HCI silence (mtk_stp_psm power-save insurance), scanner stats (+ chip BD address via Read_BD_ADDR) folded into the periodic stats message, scan-off + close on SIGTERM. Toggled live by config push — same `applyAecConfig`-style snapshot pattern.
  **Scan cadence was the load lever (found in the first live test).** The initial 100ms/50ms scan params — a 50% radio duty cycle — caught ~7× the advert volume of a normal passive scan; on this weak SoC (which already GC-stalls the mic every ~25s) the advert-processing load starved the control-WS goroutine into keepalive **ping-timeout disconnects** (Office dropped 3× in 8 min, felt sluggish). Fix: standard passive cadence **320ms/30ms** (matches ESPHome's `esp32_ble_tracker` default, ~9% duty), plus per-(address+payload) coalescing in the flush window so a beacon re-broadcasting identical data collapses to one advert carrying the latest RSSI — distinct payloads (ADV_IND vs SCAN_RSP) are preserved. After the fix: zero disconnects, a voice turn ran cleanly during an active scan (coex OK), ~1000 adverts/min forwarded to HA. Both knobs env-overridable (`BLE_SCAN_INTERVAL_MS`/`BLE_SCAN_WINDOW_MS`).
  **Controller** (`em_ble_proxy.py`): each enabled device gets a **second ESPHome device** — port is **voice satellite port + 1000** (16001 → 17001; deterministic, visibly paired, no separate allocator; v4 migration adds `ble_proxy_port`, persisted via `ensure_ble_proxy_port`), own mDNS entry (`echomuse-…-bt`), own MAC (the serial-derived MAC with the locally-administered bit flipped — deterministic and stable; the chip's real BD address is diagnostics-only, since it isn't known until the scanner first runs and changing HA identity later would orphan the registry entry). DeviceInfo advertises `bluetooth_proxy_feature_flags = PASSIVE_SCAN | RAW_ADVERTISEMENTS`; adverts forward as `BluetoothLERawAdvertisementsResponse` (msg 93 — already in the vendored protobufs). One diagnostic sensor (advert counter) — HA was observed to ignore zero-entity ESPHome devices. Deliberately separate from the voice satellite in HA, per design (HA discovered and subscribed to it as its own device — validated). Lifecycle is a single idempotent `reconcile()`: enabled+online → listener up; enabled+offline → mDNS only (HA shows unavailable); disabled → nothing exists. Dashboard: Bluetooth panel on the Status tab (scanner state, adverts seen, nearby-device count, HA link state, forwarded count).
  **Dashboard changes riding along:** each device's Status tab now shows its WiFi network name and ESPHome port; the voice-turn observability panel moved out of the cramped bottom of Status into its own **Activity** tab; and the **WiFi tab was folded into the top of the Config tab** as a distinct section above the fleet-inheritable config (WiFi is always per-device), removing the standalone WiFi tab.
- Released as device **v2.8.0** and controller **controller-v2.9.0** (the controller minor bump also rolls up the earlier claracore removal and DTLN NS work). Fleet OTA'd to v2.8.0.

- 2026-07-12 (deploy-all reliability): a fleet **deploy-all** updated one device fine but left the other stuck on "updating…". Root cause was **not** the OTA path — it was a latent SQLite concurrency bug. The controller shares one `sqlite3.Connection` across the `run_in_executor` thread pool (`check_same_thread=False`); `_tx()` writes held a lock but the read helpers (`_q`/`_q1`) took none. Deploy-all fires each device's `_run_update` concurrently, so one task's read raced another's write-commit on the same connection object → `SQLITE_MISUSE` ("bad parameter or other API misuse"), killing the second device's update before it even reached slot-detect. Solo updates never tripped it (only one task at a time). WAL's "concurrent readers" only applies across *separate* connections; on one shared connection every access must serialise — so reads now take the same lock (renamed `_write_lock` → `_db_lock`). Reproduced and confirmed fixed with an 8-thread × 500-cycle interleaved read/write stress test (was `SQLITE_MISUSE`, now clean). A general controller robustness fix, not just deploy-all.
  **Deploy-all is also resumable now.** The fleet deploy always ran server-side (per-device detached tasks — closing the modal never actually stopped it), but the only progress view lived *inside* the modal, so clicking out lost it and threw a React unmounted-update error. Deploy state now lives at the App level: a header pill ("Deploying vX — n/m" → "✓ Fleet on vX") persists across close and reopens the live per-device progress view; the modal makes clear updates continue in the background; the in-flight request is unmount-guarded.

- 2026-07-12 (oww_forge, untagged — new component): **custom wake-word trainer** at `oww_forge/`, a standalone Docker batch job (deliberately not part of the controller — ~25GB of training assets and a PyTorch image have no business in an always-on service). It containerises openWakeWord's official automatic-training pipeline: piper-sample-generator (LibriTTS-R, ~900 voices) synthesizes positives plus phoneme-overlap adversarial negatives, clips are augmented with MIT room impulse responses and AudioSet/FMA background noise, and a small classifier head trains against ~2,000 hours of precomputed ACAV100M negative features — output is a sub-megabyte `.onnx`, the exact format the controller's `OWWModel` already loads. `forge.py` wraps it in five subcommands (`assets` / `new` / `google-tts` / `build` / `test`), every stage resumable. Optional Google Cloud TTS layer mixes premium-voice positives (Neural2/Studio/Chirp, rate/pitch swept) into the training set for cross-family voice diversity at ~$0.50 per 2,000 clips. Upstream pins are load-bearing: piper-sample-generator is pinned to v2.0.0 (the last flat-layout release whose root-level `generate_samples.py` openWakeWord's `train.py` imports), openWakeWord to a verified main SHA with a one-line patch for its `--convert_to_tflite` argparse bug (string default `"False"` is truthy — every training run would otherwise end by importing TensorFlow, which the image deliberately omits; we ship ONNX only). Ships with a web frontend (`forge_web.py` + a single static page, aiohttp on port 8769, `docker compose up -d forge-ui`): asset checklist, wake-word cards with build/resume, a streaming job console, wav-upload scoring, and one-click `.onnx` download — jobs are forge.py subprocesses, one at a time, state re-derived from disk so the UI survives restarts. GPU: image builds CUDA 12.8 torch 2.7.1 — required for Blackwell cards (the RTX 5060 Ti is sm_120; notebook-era torch 2.1/cu121 can't see it at all), and carrying compiled kernels for everything Volta/Turing and newer — with automatic CPU fallback when no GPU is visible, plus a `forge-cpu` compose service for hosts without the nvidia runtime. Installing a finished model needs no controller change: `owwModel` accepts a file path, so drop the `.onnx` into the controller's data volume and point the config at it (dashboard model tiles are still a fixed list — API only for now; scan-and-merge dashboard integration sketched in `oww_forge/README.md`).
  **Validated end-to-end 2026-07-12/13** — first custom model (`hey_clara`) trained on the 5060 Ti: held-out synthetic positives score 0.86–0.94, noise controls 0.001. The first real runs surfaced five fixes, all landed: the AudioSet HF dataset had dropped its tar archives for parquet shards whose embedded metadata is too new for the pinned `datasets` (now read directly with pyarrow); the piper voice-config JSON lives in the generator repo's `models/` dir — which the image replaces with the /data symlink — so the assets step fetches it explicitly; containers get `shm_size: 8gb` (torch DataLoader workers SIGBUS at Docker's 64MB default); the `onnx` package rides along for `torch.onnx.export` (the first training run completed and then died at the final save); and deep-phonemizer's checkpoint load needed the same torch≥2.6 `weights_only=False` patch as piper — reached only for out-of-dictionary words, i.e. exactly the phonetic spelling variants below.
  **Accent & pronunciation support** (the training voices are American): comma-separated phrase variants train one model that fires on any spelling ("hey clara, hey clarra" covers the British reading — DeepPhonemizer maps 'clarra' → [K][L][AE][R][AH]); the Google TTS mix-in takes a language list (`en-GB,en-AU` for UK/Australian premium voices; usually free — a 2,000-clip run is ~2% of the monthly free premium quota); and the UI's "+ Family recordings…" uploads real household audio (any format, ffmpeg-converted, 1-in-10 held out for test) into the positive set, displacing synthetic clips — the strongest accent lever. Testing: "🎤 Try it" records from the browser mic and returns a plain verdict against the 0.5 wake threshold; file upload accepts any audio format.
  **UI restyled to the dashboard's design language** (greige chassis, gradient key-cap pill buttons, DM Sans/Mono, LCD-green inset console) and reworked for non-technical users: numbered steps, a per-word pipeline stepper (Created → Voices → Mixing → Ready) with a pulsing active stage, contextual primary action (Train/Continue/Retrain), accuracy boosters grouped with a "then retrain" nudge, and plain-language test verdicts with raw scores as small print.

- 2026-07-13 (oww_forge follow-up): **builds now survive container restarts mid-augment.** A `docker compose up` while the augment stage was writing its four feature `.npy` files left two of them missing — and upstream train.py's resume check only tests the *first* file, so the next build skipped augment entirely and crashed at training on the absent features. `cmd_build` now detects a partial feature set and forces a full recompute (`found 2/4 feature files from an interrupted augment — recomputing all`); clip generation was never at risk (per-clip files, counted on resume).
  Second custom model trained: `hey_clarra` (two-spelling variant "hey clara" + "hey clarra", exercising the DeepPhonemizer path for out-of-dictionary words). Comparison against single-spelling `hey_clara`: noise controls identical (~0.001), but the variant model trained markedly more conservative — the auto-trainer drove false positives to literally 0.0/hour at the cost of recall (0.43 vs 0.52 on the augmented test set; raw close-mic clips still mostly score 0.9+). Covering two pronunciation clusters with one 32-wide classifier head is a real trade — documented in the README with the mitigation ladder (real household recordings + retrain first, then a lower per-device `owwThreshold`). Deciding test is the humans: 🎤 mic-test both models with the household's actual pronunciation.

- 2026-07-13 (v2.8.1 + controller changes, untagged controller — deployed same day): playback-time jitter fixes from a live-symptom investigation (LED spinner visibly speeding up and slowing down during TTS, dashboard console typing landing in bursts, playback stutter on the weaker-WiFi device). The two UI symptoms shared one root cause in the controller: EQ + the 48kHz stereo resample ran synchronously on the asyncio event loop at the start of every playback — a solid numpy crunch that froze LED frames and the shell proxy for every device at once. The spinner is rendered controller-side and sent as a frame every 80ms, so its apparent speed *is* a live visualisation of end-to-end delivery jitter; the crunch now runs in the executor. Device-side (v2.8.1), the speaker `silenceLoop` mid-stream underrun log returns (removed in the Q5 2026-07-07 cleanup once the original underruns were fixed): a drain while streaming with no EOS or barge-flush pending means the network fell behind real-time playback and a silence gap is being injected — the audible stutter symptom on a weak AP link, now measurable (`grep UNDERRUN /tmp/server.log`) instead of anecdotal. Rate-limited so a chronically starved link can't flood the log.

- 2026-07-13 (v2.8.2): device concurrency fixes from a full review of `device/`. The big one: a `StopMic`→`StartMic` pair — which the controller sends after every voice turn — spawns the replacement mic-stream goroutine while the old one can still be draining periods (a select on a closed stop channel racing a ready mic channel picks randomly), so for a brief window two goroutines ran the beamformer and AGC concurrently against shared unsynchronised state, and the old goroutine's deferred beam unlock could land *after* the new turn's lock — silently dropping the turn onto the omni mic. The DSP pipeline is now serialised (`pipeMu`, uncontended outside the overlap window), the beam unlock is ownership-checked with each new stream claiming a clean beam at start, and stop now has priority over pending mic data. A new `TestStreamRestartOverlapIsRaceFree` hammers the exact sequence under `-race` — reliably red on the old code, clean on the new. Also fixed: the data WebSocket published its connection before the unlocked identify write (a mic start racing that window — possible device-locally via unmute — was a concurrent write on one gorilla conn, i.e. a panic; same ordering fix the control socket got long ago); a barge-in `Flush` racing a stream's natural `EndStream` could arm discard-until-EOS with no EOS coming and silently swallow the entire next response (the two flags are now one mutex-guarded unit); and four minor callback/log reads outside their locks. Dashboard (untagged, riding the same deploy): the **Deploy all** button now hides behind the progress pill while a fleet deploy is in flight instead of sitting next to it.

- 2026-07-13 (v2.8.3): the Office device was idling at 55% CPU while Lounge sat at 18% — and the culprit wasn't EchoMuse (the `server` binary was at ~4%) but Amazon's `/system/bin/SmartHomeWifid` busy-looping at ~50% CPU, 40+ points of it in syscalls. It's the WiFi Simple Setup daemon (BLE+WiFi provisioning of *neighbouring* Amazon devices) — useless on a repurposed device, and FireOS init only starts it on some boots at all: Lounge, with identical BLE-proxy config and the same disabled Bluetooth stack, simply didn't have the process. Stopping it live restored Office from 38% to 69% idle; the permanent fix is a `stop smarthomewifid` at server startup, the same stock-service takeover as `stop mixer`/`acebutton`/`ledcontroller`. Confirmed after the fleet OTA: Office 55% → 18%, Lounge 13%. A likely quiet contributor to the load-correlated mic capture stalls and AEC reference resyncs tracked since 2026-07-10 — watch the `[mic] clock:` ledger cadence.

- 2026-07-14 (v2.8.4): two fixes from one morning's "Office won't wake" report. First, the **WebSocket half-open wedge**: the data socket (mic in / TTS out) had no read deadline and sent no pings, so when its TCP connection dropped silently overnight (WiFi blip — no FIN or RST ever reached the device), the client sat blocked in `ReadMessage` forever and the redial loop never started. The control socket stayed healthy, so the device *looked* fine — green ring on button press — while every turn died as a 5s no-speech timeout and the controller's defensive `mic_start` fired every 10 seconds into a dead pipe, for seven hours. Data-socket recovery had only ever worked by accident: the data client's lifecycle is tied to the control connection, so a control-socket blip incidentally respawned it. The control client carried the write-side twin of the bug (its pong ticker returned on write error without closing the conn, wedging its own read loop the same way). Both sockets now run WS ping/pong keepalive — pings every 20s, pong-refreshed 45s read deadline, 10s deadline on every write so a full send buffer can't hold the write lock — and every error path closes the conn so the read loop unblocks immediately and the existing 5s redial recovers. Worst-case silent-drop outage is now under a minute instead of "until someone reboots it".
  Second, the **speaker path stops shipping stereo to a mono speaker** (BREAKING wire change — controller and fleet must deploy together; no version gating by explicit single-user-fleet agreement). The 0x02 wire format is now mono 48kHz (4096 B/period): the stereo ALSA config is an I2S/codec-path constraint, not a wire requirement, so the device duplicates L=R in `PumpPeriod` and the controller's resampler (`resample_to_48k`) stops writing two identical channels — TTS bandwidth halves, ~1.5Mbps → ~770kbps, aimed squarely at the far-AP stutter. On top of that, the device audio buffer deepens from ~1.3s to ~5.5s (`audioChanDepth` 32 → 128; the sender delivers at ~2× realtime, so its anti-stall lead was capped at 1.3s and most responses now land on-device entirely mid-playback), and playback holds on silence until ~1s is queued or EOS arrives (`primePeriods` — protects the opening seconds, when the lead is still zero; costs ~0.5s start latency, accepted). Barge-in flush is depth-agnostic and unchanged; the controller's post-playback drain sleep gains a matching prime allowance. HA is untouched by all of this — TTS arrives as a URL and the wire format is EchoMuse's own.

- 2026-07-14 (v2.8.5 + controller, untagged controller — deployed same day): **persistent activity stats.** Every voice turn now writes a row to the controller's SQLite database at completion (schema v5, `turns` table): trigger, wake-word model + score + threshold, the room's noise floor at detection, outcome, transcript, per-stage latencies, and playback underruns — so the Activity tab's history survives controller and device restarts (it hydrates from the database on connect), and there's finally longitudinal data for tuning `owwThreshold`, A/B-ing custom wake models, and correlating garbled turns with room noise. Wakes that fire while Home Assistant is unreachable persist too (outcome `no_ha`) instead of vanishing. Two hourly rollup tables ride alongside: `wake_counters` (near-miss counts and the best near-miss score per hour — the "is the threshold too high" signal, flushed through the existing rate-limited near-miss path) and `device_metrics` (CPU average/max, RAM, storage, WiFi RSSI average/min from the ~30s device stats report the controller previously discarded after the live dashboard update — 180-day retention, the historic view for things like the SmartHomeWifid CPU mystery or the Lounge marginal-link diagnosis). Read side: `/api/devices/{id}/turns` is now database-backed (`limit`/`since` params) and a new `/api/devices/{id}/activity?days=N` returns plot-ready per-day aggregates (outcome breakdown, p50/p95 latency, wake-score stats, underruns), per-wake-model rollups, and the hourly counters/metrics. Instrumentation cost is one insert per turn plus one upsert per 30s/2s — nothing runs per audio frame.
  The v2.8.5 firmware half is **playback underrun reporting**: the speaker's silence loop already counted mid-stream drains (the audible-stutter signal reinstated in v2.8.1); it now also tracks periods played and underruns *per stream* and reports one `playback_stats` control message at each stream's end (flush included; the callback runs off the ALSA pump goroutine). The controller attaches the report to the turn it belongs to via a two-sided rendezvous — the device's buffer usually drains *before* the controller's deliberately-overestimated drain sleep ends, so the report often arrives before the turn row exists (stashed with a 30s staleness window); when the row lands first, `last_turn_id` catches the late report. An unmatched report (HA announcement playback) rolls its underruns into the hourly counters instead. In the data, `NULL` underruns means "never reported" (pre-v2.8.5 firmware) — distinct from a clean `0`. The Lounge stutter question now answers itself from the dashboard instead of `grep UNDERRUN` over a device shell.

- 2026-07-15 (controller only): two day-one fixes for the activity stats, found by feeding the first real turns through them. **Numpy scores poisoned the database**: openwakeword scores are `np.float32`, which survives `round()` and which sqlite3 silently stores as a 4-byte BLOB — the turn row looked fine until the read side hit it (`/turns` 500'd on JSON serialization, and SQL `MAX()` over the near-miss column compared raw bytes). Fixed with `float()` casts at the detection sites plus a defensive numpy-scalar coercion in the DB layer; the three poisoned live rows were repaired in place. **Near-miss counter counted the wakes themselves**: the gate was simply `score > 0.05`, so every successful detection also incremented the near-miss counter and set the hour's `near_miss_max` to the wake score — exactly the frames the counter exists to exclude. The gate is now `0.05 < score < owwThreshold`, so the dashboard counter and the `wake_counters` rollup reflect only genuine below-threshold events (the "threshold too high?" signal the tuning docs describe).

- 2026-07-15 (controller only): **the Lounge 11-hour deafness, root-caused from the day-old activity stats.** From 13:34 to 22:30 on the 14th every Lounge turn — wake word and button alike — failed as `no_ha` while wake detection itself was demonstrably healthy (scores 0.43–0.51 in the log, gap-free device metrics all day). The trigger was a control-socket replacement at 11:30: when the device reopened its WebSocket, the *old* socket's close handler ran four seconds later, and while its device-registry cleanup was identity-guarded against exactly this, the ESPHome and BLE-proxy teardowns were not — the stale handler stopped the live connection's ESPHome listener and closed Home Assistant's satellite connection. Nothing restarts that listener until a full device bounce, so HA's redials got connection-refused for eleven hours and the device sat there hearing every wake word with nowhere to send the turn. The fix skips all shared-service teardown when a replacement connection has already registered (the data-plane handler always guarded this way). The `no_ha` outcome rows that made this diagnosable landed in the schema one day earlier — instrumentation paying for itself on day two.

- 2026-07-15 (v2.9.0 + controller — device-local LED animations): the thinking spinner was never smooth because it wasn't local: the controller rendered every frame in Python and sent it over WiFi at a cadence set by `asyncio.sleep`, so every event-loop stall and WiFi hiccup landed on the ring as visible judder. The device now runs its own animation engine (`led_anim` control message, advertised as a `led_anim` capability at registration): the controller sends *one* message describing the animation — pattern, colours, frame period — and the device renders it on a local ticker until told otherwise. Patterns: `solid` (listening ring, with the direction-overlay flag), `spin` (head+trail spinner), `rotate` (pride's rotating palette), `pulse` (sinusoidal throb), and `meter` — the playback ring now **throbs with the response's live audio level**, from RMS measured at the ALSA write itself, so the throb tracks what's audible right now rather than the controller's send pace (~5.5s of device buffer sits between the two). Statefulness is layered so a lost message can't strand the ring: messages ride the ordered control WebSocket, a raw `leds` frame or newer spec atomically replaces the running animation (generation-counted, so a stale ticker can never paint over its successor), reconnects reset the ring explicitly, and every spec carries a `ttlSec` dead-man — if the controller dies mid-turn the ring self-clears instead of spinning forever. Animation frames go through the same paint path as controller frames, so mute-ring sovereignty and the volume-arc window behave exactly as before. Old firmware without the capability gets the legacy controller-rendered spinner unchanged.

- 2026-07-16 (unreleased: device + controller — the zombie mic stream): Office went deaf to the wake word for 4.7 hours after a morning WiFi blip, and the failure needed a *partial* connection loss to trigger: the control WebSocket died (keepalive timeout) while the data socket's TCP path stayed perfectly healthy. The control client's reconnect cancels the data client's context and spawns a replacement — but cancelling that context only ever aborted a dial in progress; nothing closed an *established* data connection. So the old connection and its mic-streaming goroutine lived on as zombies: the stream kept `micActive` held against a socket whose frames the controller was already (correctly) discarding as stale, and every one of the controller's defensive `mic_start`s — 1,700+ over the morning — was refused device-side with "already active". The v2.8.4 keepalives, ironically, kept the zombie socket alive indefinitely. Recovery came from the dot button purely because a button turn sends `mic_stop` first. Three fixes: **(device)** the data connection now watches its context and closes the socket on cancellation, so the read loop errors out and the stream is released for the replacement connection — with the exit cleanup ownership-guarded so a late-exiting old connection can never kill the *new* connection's stream or unpublish its socket (a new `TestContextCancelReleasesMicStream` reproduces the wedge — red on the old code); **(controller)** after three fruitless defensive `mic_start`s the watchdog escalates to `mic_stop` + `mic_start`, which self-heals this wedge and any future stuck-stream variant the same way the button did; **(controller)** the watchdog goes quiet while the device reports muted — hardware mute rejects `mic_start` by design, so the retry-every-10s loop was pure log spam (Lounge, muted by hand, was logging it all morning).

- 2026-07-18 (v2.9.2: device — **the ~1MB/h heap leak, found and fixed**): the fleet-wide memory creep classified on the 17th (live-heap floor climbing linearly, identical on both devices, goroutines flat) turned out to live in the GoTinyAlsa submodule, not EchoMuse's own code: `GetAudioStream`'s frame loop registered a `defer func(){ recover() }()` **inside** the infinite read loop. Deferred calls only run at function return — which is never, for a stream that runs the process lifetime — so every 160ms mic batch pushed another ~46-byte defer record + closure onto the goroutine's defer chain, forever. 22,500 batches/hour × ~46 bytes ≈ the measured 1MB/h exactly, and the mechanism explains every observed property: both devices identically (always-on mic read), independent of AEC/debloat/turn count, GC healthy throughout (the memory was live, not garbage), and the uptime-correlated CPU-max creep along for the ride (GC scanning an ever-growing defer chain each cycle). The recover is now registered once, outside the loop — same panic protection, zero growth. Patched in the local submodule checkout (`replace` directive builds pick it up); upstreaming/forking still open — the release workflow's `git submodule update` fetches from upstream Binozo/GoTinyAlsa, which doesn't have the fix yet. Rides with the same build: **heap-profile dump on SIGUSR1** — the devices accept no inbound connections, so instead of a pprof HTTP endpoint, `kill -USR1 $(pidof server)` writes a GC-preceded `pprof.WriteHeapProfile` to `/tmp/heap-{0,1}.pprof` (two alternating slots, can't fill /tmp), pulled over the existing shell proxy for `go tool pprof` on the host. Verification of the fix is the existing `[mem]` telemetry: the heap_alloc floor should now sit flat across a 24h soak.

- 2026-07-18 (controller, untagged — deployed same day — **debloat moved into the provisioning wizard**): the package-hide + daemon-stop recipe proven live on the Lounge device (2026-07-15: −130MB RAM, cpu_avg down 2–3pp, Java apps 24→7, no voice regressions) is now a wizard step, so freshly provisioned devices come up debloated instead of needing a manual shell session. New auto step 9 ("Debloat", between Disable Alexa and WiFi): `pm hide`s the non-essential Amazon package list — hide, not disable, because FireOS 5 ignores `pm disable` for PERSISTENT system apps and starts them at boot anyway — and installs a Magisk `service.d` boot script that re-stops the init-launched native daemons (vitals_service, perfmonitord, perfrecoveryd, shblemeshd, meshmgrservice, drm) 45s after every boot, since `stop` doesn't persist and init daemons aren't packages. Both halves are served from `controller/device_payloads/` (`debloat_packages.txt`, parsed server-side to JSON at `/api/provision/debloat_packages`, and `echomuse-debloat.sh` at `/api/provision/debloat_script`), so tuning the list is a text-file edit — no dashboard rebuild, and a future fleet-wide debloat OTA can reuse the same canonical files. Undo on any device: `pm unhide <pkg>` and delete the service.d script. The package list is the canonical 31-package hidden set pulled verbatim from the Lounge device 2026-07-18, and the service.d script matches the live one (including its backgrounded subshell — Magisk runs service.d scripts sequentially, so a foreground sleep would stall boot scripts behind it).

- 2026-07-18 (v2.9.3 + controller — **TLS + token auth for the device link**): everything between device and controller previously travelled plaintext on the LAN — mic audio, TTS, and a root shell that anything on the network could impersonate a device to reach. The link is now authenticated and encrypted end to end: the controller generates a private CA on first start (`em_pki.py`, persisted in `tls/` next to the DB) and runs a second wss listener on 8770 carrying the same three planes, advertised to devices via a `tls_port` mDNS TXT property; devices holding pushed credentials (`/data/local/etc/echomuse/{ca.pem,token}`) dial it with the CA pinned and a per-device token on every connection. Design points that matter on this hardware: the server cert's identity is a fixed DNS SAN (`echomuse-controller`) rather than an IP, so the controller can move address without re-issuing anything; certs are backdated 10 years *and* the device floors its verification clock at the firmware build time (`BuildUnix` ldflag) — an Echo fresh off a power cut boots with a bogus clock until NTP syncs, and a device that can't connect can't fix its clock; and credentials are re-read on **every dial**, so a push takes effect on the next reconnect with no firmware restart. Delivery: the provisioning wizard installs credentials over adb before the device's first contact (token minted against the serial, pending-approval flow unchanged), and existing devices get a **Secure link** button on the Status tab — the controller pushes the files over the still-plain shell plane, bounces the connection, and the device comes back on wss. Enforcement is deliberately staged: a wrong token always rejects, but a missing one is tolerated until `REQUIRE_DEVICE_TLS=1` — flipped only once every device's Status tab shows `wss (TLS)` — because the credential push itself depends on the plain shell plane. Rollout note: the fleet OTA to v2.9.3 restarted both device processes, so the v2.9.2 heap-leak soak rebaselines from this deploy — the `[mem]` heap-floor check now reads against v2.9.3 uptime.

- 2026-07-18 (v2.9.4 + controller — **volume/mute persistence + a backlog sweep**): volume finally survives a reboot — and the fix turned out to be repairing a loop that already half-existed. The controller has long persisted every device volume report into the per-device `startupVolume` config and re-applied it on connect; what broke it was the device itself: on boot it reported its hardware-default volume the moment it connected, and the controller dutifully saved *that*, clobbering the real value — while the config push applied `startupVolume` as a raw mixer write the device's own volume state never saw. Now the device stays quiet about volume until it holds an authoritative level (the config push seeds it through the proper volume path, first push per run only, so a mid-session reconnect can't stomp a live change), and the loop closes: set it low at night, and it comes back low. Mute is persisted the opposite way, device-locally (`/data/local/etc/echomuse/state.json`, surviving OTA slot flips) — mute is device-sovereign by design, so a muted Dot must come back muted, red ring and all, with or without a controller. Riding along, four backlog items: **the VAD gate timing bug** — the speech-gate/silence-gate windows were counted in units of the mic's real 160ms batches while being divided as if 32ms periods, so both ran 5× longer than configured (600ms of silence to end a button turn was really 2.9 seconds); they now derive from the actual batch length, and button turns end noticeably snappier. **OTA failures reach the dashboard** — every abort path (fetch, transfer, slot detect, exception, timeout, auto-rollback) now records a reason surfaced in the fleet deploy modal (red ✗ per device, "n ok, m failed" in the header pill) and the per-device update log, instead of leaving tiles at "updating…" forever with the error buried in logs. **`[mem]` telemetry forwards to the controller's device log** (one line per ~5min) so the next memory investigation reads from the dashboard rather than shell-proxy archaeology. **TTS is 48kHz end-to-end** — the ESPHome satellite now declares `supported_formats` (48kHz mono FLAC), so recent HA versions transcode at source; ffmpeg decodes straight to the wire rate either way, and the numpy linear-interpolation resampler — and its ~2dB rolloff above 8kHz — is gone, with EQ running at 48k. And the **dashboard got a mobile pass**: on phone widths the device/settings windows go full-screen, the header and summary tiles wrap, two-column layouts stack, and tab strips scroll sideways. Desktop is pixel-identical.

- 2026-07-19 (v2.9.5: device — **the mute-button LED actually lights, and the volume arc stops ghost-flashing**): the discrete red LED under the mic-off button finally works, and the answer overturns two prior "ground truths". Amazon's `libled_hal.so` constant (`k_muteButtonGPIOAddress` = 0x1BD = 445) that v2.8.0/v2.9.x firmware faithfully drove is **off by one** against the kernel's sysfs gpio numbering — and worse, pad 88 (which gpio445 maps to) is muxed to `MSDC2_DAT1`, so those writes never reached any pin. Stock firmware never exposes the discrepancy because it bypasses sysfs entirely, driving the pin through the `/dev/mtgpio` ioctl. The truth came from ftrace on a stock-rooted donor device (Wil at the adb console, live): with the `regmap` tracepoints enabled, each mute press showed a Binder thread writing pinctrl `DIR-set 0x54` + `DOUT-set 0x454` (LED on) / `DOUT-clr 0x458` (LED off), value bit 7 of bank 5 → pin 87 → **sysfs gpio444, active-high** — confirmed by hand (`echo 1 > /sys/class/gpio/gpio444/value` lights the button). `mute_button.go` now drives gpio444 with 1=on. Along the way the investigation also proved the ring chip's 36 channels are exactly the 12×RGB ring (individual frame-triplet writes light ring segments only) and that the `lp855x`/`lp55231` LED chips in the device tree never probe on biscuit — phantom DTS entries for other Echo variants.
  Riding along: **the volume arc only paints for physical button presses.** Remote volume changes (dashboard slider, Home Assistant) and the boot-time `startupVolume` seed used to flash the cyan arc on a ring nobody was standing near — the seed meant a 2s arc on every reconnect. `volumeController.Set` now takes a `showRing` flag; only the device's volume up/down buttons pass true.

- 2026-07-19 (controller, untagged — deployed same day — **devices approved after controller startup now reach HA, plus HA naming cleanup**): provisioning the third device (Retreat) worked end to end, but it never appeared in Home Assistant and its dashboard Status showed ESPHome port `-`. Root cause: ESPHome satellite servers were only ever created by `start_esphome_servers()`, which walks the approved-device list **once at controller boot** — a device approved afterwards had no `_servers` entry, so `device_connected()` silently no-oped: no port allocation, no `_esphomelib._tcp` mDNS record, nothing for HA to discover until the next controller restart. The per-device creation now lives in `_register_device_server()`, and `device_connected()` calls it on demand for any approved device without a registered server (PR #13). The first deploy immediately exposed a race in that fix: devices reconnect while the startup loop is still iterating, so the loop and the on-connect path each created a server for Retreat — doubled mDNS registration, and `_servers` left pointing at a never-started duplicate while HA sat connected to the first listener, which would have dropped every wake-word turn (`get_server()` → satellite None). `_register_device_server()` is now idempotent: checked on entry and re-checked after the executor awaits, first creation wins (PR #15). Naming rode along (PRs #13/#14): HA device names are now **`<label> Voice Assistant`** (DeviceInfo `friendly_name` + mDNS TXT), matching the BT proxy's `<label> BT Proxy` convention — and the HA **Model** field now reads `Echo Dot Gen 2 (biscuit)` instead of echoing the label, because HA's ESPHome integration displays `project_name`'s post-dot segment as the Model, overriding the `model` field; `project_name` now carries the shared `ESPHOME_DEVICE_MODEL` constant for both the voice satellite and the BT proxy. HA picks the new names up on reconnect unless a device was manually renamed (user-set names win in HA's registry).

- 2026-07-19 (controller, untagged — **custom wake-word models install from the dashboard, and a latent scoring bug is fixed**): oww_forge output no longer needs a hand-copy plus a raw API call. The Config tab's wake-word tiles now merge stock models with custom `.onnx` files discovered in `oww_models/` beside the SQLite DB (persisted data volume, so models survive image upgrades): `GET /api/oww_models` scans per request, a **+ Custom model** tile uploads (multipart → atomic tmp+rename, auto-selects on success), and a `×` deletes — refused with 409 while any device or the global default still references the file. `owwModel` stores the absolute file path for custom models; a selected model whose file has vanished shows a "missing file" tile rather than disappearing. Building this exposed that the documented "works today via the API" path **never actually worked**: openwakeword keys its prediction dict by filename *stem* (`hey_clara`), but the wake listener and barge-in watcher both looked scores up by the raw `owwModel` value — for a path that reads 0.0 forever, i.e. the model loads cleanly and then never, ever fires. `em_oww_models.prediction_key()` now maps path → stem at every score lookup, and the ESPHome layer applies the same mapping so HA's wake-word dropdown shows "hey clara", not a container path. Also new: the controller's first unit-test suite (`controller/tests/` — em_eq, em_scenes, em_oww_models, version; pure-logic only, no openwakeword/aiohttp in the test env) and a CI workflow running pytest + `go test`/`go vet` on every push/PR — regression coverage that previously existed only for the device's Go packages, run by hand.

- 2026-07-19 (controller, untagged — **multi-device wake arbitration: one utterance, one responder**): with three Echos on the fleet, a wake word said in earshot of two of them started two competing voice turns. New `em_arbiter.py` pools detections stock-Alexa-style (their "ESP"): the first detection opens a round with a `wakeArbitrationMs` deadline (default 300ms, dashboard slider under Wake word, 0 = off); detections joining before the deadline are ranked by **SNR at detection** (speech RMS over that device's own tracked noise floor — a proximity proxy, since raw wake score saturates for any clean detection and only breaks ties) and the best one answers. Losers revert their optimistically-armed capture (the winner's command audio must flow from the first syllable, so `oww_paused`/beam-lock are set up *before* arbitration and undone on loss), drain the frames they buffered during the window, and log "Wake ceded to `<winner>`". Stragglers detecting within 1s of a resolved round lose to its winner instead of opening a fresh round — device 160ms batching spreads same-utterance detections by a few hundred ms, and without this a late loser would double-answer. Solo fleets skip the window entirely (no latency cost until a second device connects). Rides the normal config push (`wakeArbitrationMs` in the shared config dict; the device ignores it). Also: the Dockerfile was missing COPY lines for `em_oww_models.py` (this morning's PR #17 — would have crash-looped the container on next rebuild, the em_scenes.py gotcha again) and the new `em_arbiter.py`; both added, plus a deploy-shape unit test that fails if any `em_*.py` ever lacks a COPY line, so this class of breakage is now caught in CI instead of at container start. Suite is at 35 tests.

- 2026-07-19 (controller, untagged — **the media_player grows up: real music playback from HA**): the per-device HA `media_player` entity was a voice-pipeline facade (volume + announcements; play/pause/stop logged "(unhandled)", `media_url` ignored) — now it's a real player. New `em_player.py`: `media_player.play_media` (and everything that funnels into it — HA's media browser, Music Assistant, radio URLs) spawns a **streaming ffmpeg decode** (s16le/48k/mono on a pipe — music can be minutes long or a live stream with no end, so the fully-decode-then-stream TTS approach doesn't transfer) fed to the existing 0x02 speaker plane **paced at ~1.5s lead over realtime**. That lead is the design pivot: the turn path deliberately writes far ahead into the device's ~5.5s buffer, but anything buffered survives a pause — a 1.5s lead keeps flushes feeling instant while still riding out WiFi hiccups. Pause = `speaker_flush` + wall-clock position bookmark; resume restarts ffmpeg with `-ss` a second before the bookmark (live streams rejoin the live edge); every teardown EOSes the stream first-flush-then-EOS, the same discard-disarm contract barge-in established. **Voice preempts music**: turns and announcements `interrupt()` an active session and `resume_interrupted()` when done (user-paused sessions stay paused — only sessions *we* paused auto-resume). Two integration subtleties that would each have been a field bug: (1) the feed must NOT set `device.speaking` — the wake loop drops mic frames while that's set, which would have left a device deaf for the length of a song; instead wake-over-music scores against `bargeInThreshold` when barge-in is enabled (same acoustics as barge during TTS — the speaker is ~25dB louder at the mic than the person). (2) Music EQ can't reuse `em_eq.apply()` per chunk — biquad state would reset at every boundary and click; new `em_eq.StreamingEQ` carries `sosfilt` state across chunks (unit test proves chunked == whole-buffer to ±1 LSB). HA sees honest state throughout: PLAY_MEDIA+BROWSE_MEDIA feature flags advertised, PLAYING/PAUSED/IDLE pushed proactively via the satellite on every transition. Suite at 44 tests (player state machine runs against a stubbed decoder + fake device).

- 2026-07-20 (device + controller — **delivery instrumentation: measuring the thing we kept arguing about**): playback underruns appeared on two devices on 07-19 and resisted diagnosis for hours, because every metric available measured something other than delivery. The worst offender was the controller's own `Streaming took 0.0s`, which times writing frames into the **kernel socket buffer** — 315KB fits, so it reads ~0s no matter how slowly the device is actually being fed. It led to a confident and wrong conclusion ("the device got the audio instantly, so this is device-side"); the device log showed it processing that stream's EOS **five seconds** after the controller sent it. A second dead end (`playback_ms / audio_ms` as a delivery ratio) turned out to run 2–6× on *clean pre-deploy* turns too, because it's dominated by fetch/decode plus the deliberately overestimated drain sleep. Rather than keep theorising, this adds measurement at every hop. **Device, per speaker stream** (rides the existing `playback_stats` message — no new traffic): `minDepth`, the fewest periods left in the buffer mid-stream, which is the key idea here — underruns are a *rare binary* event, whereas buffer margin is continuous, so a stream that dipped to 2 periods is visibly one hiccup from breaking even though nothing was audible; plus `primeWaitMs`, `recvSpanMs` (first→last frame arrival — longer than the audio duration is the definitive "the wire couldn't keep up"), `maxGapMs` (one brief stall vs a uniformly slow link) and `bytesRecv`. **Controller, per turn**: `send_ms` kept deliberately alongside the new `delivery_ms` (first frame sent → device's stats report) so the gap between them makes the old illusion self-evident, plus `eq_ms`. **Device, per 30s**: negotiated PHY rate, frequency and BSSID — band and AP identity matter because a single SSID spanning 2.4/5GHz lets a device silently re-associate to a much slower radio (Retreat was found on 2.4GHz ch1 at 72Mbps while the others sat on 5GHz at 135–150) — plus tx/rx bytes and tx_errors/tx_dropped/rx_crc_errors deltas from sysfs. **Controller, continuous**: an event-loop lag monitor, since anything blocking the loop also delays speaker frames; peak is on `/api/system/status`. Cost discipline held throughout: per-period work is one channel-length compare and one `time.Now()` on a single-writer path (no locks, no allocation, no logging), everything else piggybacks reports that already existed, and the only process spawn (`wpa_cli`, which needs `-p /data/misc/wifi/sockets` on FireOS or it answers UNKNOWN COMMAND) is cached for 2 minutes. Schema v7; migration verified against a snapshot of the live database (200 turns, 297 metric rows preserved, new columns NULL = "never reported" rather than 0 = "perfect"). Wire change is backward-compatible both ways: `periods`/`underruns` stay at the top level for older controllers, and older firmware simply omits the `stats` block.

- 2026-07-20 (controller — **wake arbitration rebuilt: first detector wins, and it costs nothing**): the original arbiter (2026-07-19) pooled same-utterance detections for a window and let the best SNR answer. Two things were wrong with it. First, it **awaited the full window on every wake** — measured at a flat 363–370ms on every device, because the gate was "2+ devices *connected*" rather than "in earshot", so a wake in an empty house still paid the tax. Setting `wakeArbitrationMs=0` restored snappy wake and immediately re-exposed what it existed to prevent: at 03:57:58 one utterance woke all three Echos within **184ms** (Office 1.000, Lounge 1.000, Retreat 0.997) and each ran its own conversation. That is worse than duplicated answers, because HA returned `continue_conversation`, each device reopened its mic, heard the others' TTS through the house, transcribed it as a follow-up ("Clarification in your smart home later, which you at least talked more about what you needed up with", mic RMS 0.53 against a 0.002 noise floor) and HA answered again — a self-feeding loop that ran ~70 seconds until a no-speech timeout broke it. Second, and only visible because the failure was captured in full: **the ranking metric was wrong**. SNR at detection across the three devices was 0.9 / 1.15 / 0.93 — statistically indistinguishable — and the SNR winner (Lounge) transcribed "What's the technique?" while the *first detector* (Office) got the actual utterance, "What's the weather like today?". So the arbiter is now first-detector-wins: the first device across threshold claims the utterance **synchronously** and answers on the same event-loop tick, and any other device detecting within `wakeArbitrationMs` (now a pure suppression window, default 700ms, released at turn end) stands down. Nobody waits, on either path. Sound reaches the nearer microphone sooner *and* louder, so detection order is both a better proximity proxy than a ratio of two noisy RMS estimates and free to compute. Worth recording against the "is this new?" instinct: querying wake clusters by *wake* time rather than turn-completion time (turn rows are timestamped at completion, so simultaneous wakes get spread apart by differing turn lengths — an easy and misleading mistake) shows Office+Lounge have double-woken five times going back to **2026-07-15**, i.e. long before arbitration, media_player or instrumentation existed. What changed on 07-20 was Retreat joining the fleet, making a three-way possible, and the continuation loop turning one bad transcript into something impossible to ignore.

- 2026-07-20 (device — **minDepth metric was broken; the rest of the instrumentation earned its keep on first contact**): the first real turns after the v2.9.6 OTA validated the delivery instrumentation and immediately exposed one bad metric of my own. `recvSpanMs`/`maxGapMs`/`delivery_ms` worked exactly as intended and settled the underrun question in a single query: the one underrunning stream took **7852ms to deliver 6700ms of audio** — slower than realtime — with a **3402ms gap between consecutive frame arrivals**, against healthy streams delivering 2.8–3.7s of audio in 150–170ms with 20–30ms gaps. `send_ms=1363ms` on the same turn shows the controller's socket write blocked too, so both ends agree the wire stalled mid-stream; no more inferring delivery from metrics that measure something else. But `minDepth` read **0 on every single stream**, healthy ones included, and was therefore worthless: it sampled buffer occupancy after taking each period, and the final periods of any stream necessarily drain the buffer to zero, so it measured the normal end-of-stream tail rather than starvation. Now gated on `!eosPending` — set by EndStream the instant the 0x03 EOS arrives — so depth is only sampled while more audio is still expected, which is the only window where a low buffer means anything. Lesson worth generalising: a margin metric has to exclude the drain it is supposed to be distinguishable from, and "always reads the same value" is the cheapest possible tell that a metric is measuring the wrong thing.

- 2026-07-20 (controller — **config POSTs now refuse to silently delete settings**): making the arbitration fix live required setting `wakeArbitrationMs`, and I did it by POSTing `{"wakeArbitrationMs": 700}` to `/api/global/config`. That endpoint **replaces** the stored dict rather than merging it, so all 26 tuned fleet settings were wiped and fell back to `DEFAULT_DEVICE_CONFIG` — the wake model reverted hey_mycroft → hey_jarvis (the real wake word stopped working), `owwThreshold` dropped 0.5 → 0.3 (devices began false-waking on ordinary room conversation at scores of 0.31–0.49, transcribing things like "Um I'll just finish recording uh"), and AEC, barge-in, NS, beamforming, the BLE proxy and the EQ curve all switched off. Recovered in full from the DB snapshot taken before the v7 migration, verified key-by-key against it. The dashboard had never tripped this because it always submits the complete config; a hand-written partial POST is not equivalent. Both config endpoints now compute which stored keys an incoming body would delete and refuse the write with 409 unless it passes `replace: true`, naming the keys at risk. Two details worth keeping: the comparison is against the **raw** stored config, not the defaults-underlaid view returned by `get_global_device_config()` — otherwise a controller upgrade that introduces a new default key (exactly what `wakeArbitrationMs` was) would make every save from an already-open dashboard tab look like a deletion and be refused; and the guard covers **both** write paths, since the per-device endpoint has identical replace semantics and would otherwise leave the same trap open next door. The broader lesson is about method rather than this endpoint: the failure was verifying the intended effect ("did the new telemetry appear?") instead of the blast radius ("what else changed?"), when the replace semantics were visible in a handler I had already read earlier the same session.

- 2026-07-20 (controller — **the config guard stole an auth decorator; caught by live-testing it, not by CI**): the guard added earlier the same day was written as a helper placed directly above `_post_global_config` — which was already decorated `@auth.require_admin`. Inserting a function between a decorator and its target silently rebinds the decorator to the *new* function, so `_dropped_keys` became an admin-wrapped route handler and **`_post_global_config` was left with no authentication at all**. It surfaced as an opaque 500 (`_dropped_keys() takes 1 positional argument but 2 were given`) when the incident payload was replayed against the live controller as a post-deploy check; the config itself was never at risk, because the exception fired before any write. CI had passed, and could not have caught it: the unit tests extract the helper's source text and exec it, which drops decorators entirely, so they were exercising a different function than production. Fixed by moving the decorator back and adding AST-based tests that parse the real file — one asserting `_dropped_keys` carries no decorator, one asserting every mutating handler still carries `require_admin`. Both were verified to fail against the broken code before being kept. The source-extraction helper also moved from regex to AST, since the regex silently widened to swallow decorated handlers the moment a neighbouring boundary shifted. Two lessons: a test that reconstructs code from source text is not testing production; and a post-deploy check that replays the original failure is worth more than any amount of green CI.

- 2026-07-25 (**ring UX pass — three field reports, and one of them turned out to be ours**): Wil reported sluggish wake and button response on Retreat, the sluggishness compounding under repeated button presses, and the ring animation finishing before the audio did. Only the third was a bug in our code, and the investigation is worth recording because two of the three had *measurement* problems rather than code problems.

  **The ring finishing early was real and ours.** `_run_post_turn_playback` never learned when playback actually ended — it estimated from wall-clock and *subtracted* the socket-write time, which completes near-instantly however slow the wire is, so the estimate shrank on precisely the streams that needed the most patience. Measured against `delivery_ms` across the last 40 instrumented turns: 4 cleared the ring more than half a second early, the worst by **6.1s** on Retreat and 3.2s on Lounge, and every one of them correlated with an inflated `recv_span_ms`. It now waits on the device's `playback_stats`, which the device emits once its audio channel drains after EOS and is therefore the real end of audio. `cancel_event` is still raced so barge-in and mute land as before; the timeout is only a backstop for the report never arriving. This was the fix identified on 07-20 and deliberately not rushed in — the evidence is now unambiguous.

  **The wake sluggishness was the link, and it is measurable.** Correlating 79 wake turns to devices and measuring detection → first mic frame: Office 264ms median / 272ms p90 / 278ms max; Lounge 260 / 294 / 952; **Retreat 258 / 1046 / 1225**. The medians are identical — that is the inherent frame batching and preroll discard — but the tails diverge hard and track RSSI monotonically (−25 / −52 / −66). Retreat takes ~1s excursions on more than a tenth of its turns. The leg measured is *after* detection, but it rides the same path as the leg before it, so late-arriving wake audio means late detection means a late ring. A stale fact retired at the same time: Retreat is no longer on 2.4GHz, it sits on 5805MHz at 108–135Mbps, so the "congested channel 1" explanation from 07-20 is dead and RSSI is what remains.

  **The compounding was not reproduced, and that is recorded rather than papered over.** Ruled out device-side: no leak (goroutines pinned at 17, heap flat, RSS stable over 5 days), `stalls=0` and `sub_drops` static in the capture ledger, and `mic_start`/`mic_stop` do not reopen ALSA at all — they subscribe and unsubscribe from an always-open stream, which killed the teardown-cost theory. Ruled out controller-side: sub-millisecond button handling, no `voice_lock` queueing, **zero** "ignoring wake" events in five days, no queue drops, no loop-lag warnings. One real mechanism *was* found that should compound — `cancel_turn()` is local-only, since the ESPHome protocol has no server-side abort, so every cancelled turn leaves an HA pipeline running to completion with its result discarded — but the turn immediately after a 21-press mash had `ha_think=2980ms` against a 3984ms median, i.e. *faster* than typical. One sample, not conclusive, and not sold as the answer.

  The blocking gap is instrumentation, not analysis: the device logs no LED lines at all, the controller never logs `send_led_anim`, and device timestamps are second-resolution. The exact quantity being complained about — press to ring — is the one thing neither end can currently measure.

- 2026-07-25 (**the playback meter was invisible, and the fix is a lesson in perceptual vs numeric range**): "I like it as a concept, but it's currently too subtle and barely distinguishable from a solid listening ring." Four causes compounded, and the arithmetic is the interesting part. The envelope decay coefficient of 0.12 at a 40ms tick gives a time constant of ~333ms, which smooths away everything below phrase rate — speech syllables run 150–250ms, so the ring never tracked the rhythm of speech at all. `sqrt(rms/0.35)` mapped ordinary speech (rms 0.05–0.20) into 0.38–0.85, the top half of the range. The 0.15 floor narrowed that to 0.47–0.87. And then the result was painted as a raw duty cycle, when perceived brightness goes roughly as the 1/2.2 power — so 0.47→0.87 was *seen* as 0.71→0.94, a **23% perceptual variation**, which is indeed indistinguishable from constant.

  Fixed by attacking all four: decay 0.30 (t≈133ms, syllable rate), floor 0.06, input curve `(rms/0.22)^0.7`, and — the one that mattered most — **gamma-encoding the output**, so `floor + span·env` is a *perceptual* target that gets raised to 2.2 before painting rather than being used as the duty cycle directly. Perceptual swing goes from ~0.28 to ~0.70, about 2.5×, with a regression test asserting it stays above 0.55 and that silence still reads as lit.

  All six curve parameters ship as config (Config → Ring → Advanced), which is the more durable decision. This is a *taste* parameter: finding a value that reads well in a real room takes several passes, and doing that as several firmware OTAs is not a sane tuning loop. The device clamps every value independently of the dashboard ranges, so a bad config push cannot produce a dead or strobing ring. Confirmed by eye in the room, which is the only test that counts for this.

- 2026-07-25 (**per-section fleet/device config scoping — schema v8**): "the all or nothing is becoming unwieldy." `use_global_config` was one boolean for the entire config, so wanting a device-specific ring scene meant forking that device's mic gain, wake threshold and EQ too — and those forks then stopped tracking fleet changes permanently. The cost was visible in our own data: Retreat had an override, so **none of that morning's six new `meter*` keys ever reached it**.

  A device now stores the SET of sections it overrides, and its effective config is the fleet config with its own values layered over it for just those sections. Six sections matching the dashboard stages. `em_config_sections.py` is the single source of truth for which key belongs where; the dashboard carries a mirror for rendering and a test parses it straight out of the file, because drift would put a control under a toggle that does not govern it — visibly fine, silently wrong. A second test asserts the partition stays **total**: a config key belonging to no section can never be overridden and never renders, and nothing else would notice.

  The migration was verified against a copy of the live DB *before* deploying, per the pre-flight discipline: flag 1 → no sections, flag 0 → all six, effective config byte-identical for every device. The only deltas were those `meter*` keys, whose values equal the firmware defaults the device was already using implicitly — so no behavioural change, and a neat illustration of the very problem being fixed.

  Two consequences worth naming. The fleet endpoint now pushes **every** connected device its own resolved config rather than only fully-inheriting ones, because a device overriding only Ring still follows the fleet everywhere else — and it must be sent its *resolved* config, since pushing the raw body would blow away the overrides being respected. And the live-apply logic was extracted to a shared `_apply_live_config`: it had been duplicated inline across both write paths, which is exactly the shape that produced the v7 stats-relay miss, where a field added to one of three sites and not the others read as working.

- 2026-07-25 (**`startupVolume` reclassified as state, and a slider that never did anything**): asked whether the key was still needed at all. It is — it is the entire mechanism by which volume survives a reboot: the controller writes it from every `volume_state` report, and the device re-applies it via `SeedVolume` on the first config push per run. But the instinct behind the question was right, because it is *persisted device state wearing a config key's clothes*. Three tells: it is the only key the controller writes automatically from device reports rather than from a human; it was the only key special-cased out of fleet inheritance; and **the dashboard slider was a trap** — because `SeedVolume` ignores later pushes, moving it did nothing until the device restarted, and any real volume change overwrote it in the meantime. Key kept, slider removed, current level now read-only on Status, and the key declared in `STATE_KEYS` so its exemption from section scoping is documented rather than hidden.

- 2026-07-25 (**`last_seen` measured the wrong thing entirely**): "last seen doesn't seem to update, shouldn't that show time since the last successful ping?" Correct — it was only ever written by `upsert_device_seen`, which runs on connect, so it meant "last **connected**". All three devices read 88–91 minutes stale while online and streaming audio perfectly, that figure being simply when they last reconnected after an OTA. Exactly backwards from what the field is for, which is knowing when a device went away. Now refreshed from the ~30s stats report — itself proof of life, and it rides an executor hop the loop was already making — and stamped again on disconnect so an offline device shows the moment it actually dropped. Verified live: 12–24s after deploy. The Status panel lost a row in the process: `Connected` and `Last seen` were redundant in both directions (while connected the timestamp says nothing, while offline the Yes/No says nothing the timestamp does not), so they merged into one `Status` row, which freed the space for the new read-only Volume with no net growth.

- 2026-07-25 (**a deliberate press outranks the volume arc**): "if I adjust the volume and then immediately press the action button I get no indication that it's listening." The arc's 2s hold makes `SetLEDs` record frames into `baseLEDs` without painting them, so the listening ring was being computed and stored but never shown — the device was genuinely listening and simply could not say so. The hold exists to stop turn animations, which repaint every ~80ms, from stomping the arc within a frame of it appearing; it was never meant to outrank the user. A dot-button release now calls `CancelDisplay`. It deliberately does **not** repaint on cancel: the controller's listening frame arrives within a round trip, and clearing to black would put a visible dark gap between the arc and the ring, so the arc simply stops being sovereign and gets replaced in place. Worth recording that this was a miss in the state-model document written hours earlier the same day — the event table described this behaviour as row A9 and recorded it as correct without asking whether it *should* be. An event table makes behaviour visible; it does not interrogate it.

- 2026-07-25 (**first outside contribution — PR #28**): a first-time contributor hit a 10s cap in `_fetch_tts_audio` generating long responses. Two changes proposed; one taken, one sent back. The timeout is a *synthesis* deadline rather than a download one — HA's `tts_proxy` generates while the GET is held open — and checking our own history showed **we had been hitting it too and not noticing**, because the single retry absorbs a first-attempt timeout and surfaces it as a latency spike rather than a failure (two of 198 timed turns exceeded 10s from URL to decoded audio, both recorded as successes). Raised to 60s, with the thinking spinner's dead-man TTL coupled to it and a test guarding the pair, since the spinner spans HA think time *and* the fetch. The second change — a reconnect retry inside `send_data` — was sent back: the wait sits in the per-chunk send path rather than firing once per drop, so a device that never returns leaves the stale task waiting 15s × ~70 chunks. **The deeper answer is that we should not be buffering the whole response at all**: `em_player` already streams a URL through ffmpeg into the same 0x02 plane with `StreamingEQ`, and pointing that at TTS would remove the deadline entirely and start playback seconds sooner, since today every reply waits for generate + download + decode + EQ before a single sample plays. Recorded as the next real piece of work rather than bolted onto someone else's PR.

  A process lesson from the same PR, and the more important one: the review comment initially quoted `tts_gen` and fetch figures pulled from `docker logs` — which the controller rebuild earlier that session had silently truncated to 57 samples, when the `turns` table held 198 and told a materially different story, including that we *had* exceeded the timeout. Wil caught it only by asking. For anything outward-facing, re-derive every number from the authoritative store at the moment of writing, state `n`, and check the claim's negation too: retries and fallbacks mask failures as latency, so a clean success count proves nothing about whether a limit was ever reached.

- 2026-07-25 (**"Alexa worked fine here for years, so what are we doing differently?" — and the answer was not RSSI**): Wil rejected the signal-strength explanation for Retreat's latency on the grounds that the same hardware in the same room worked for years under stock firmware. Both halves of that turned out to matter, and chasing it produced the most useful instrumentation of the day.

  What we do differently, quantified: mic capture is 16kHz mono S16 in 2560-byte frames every 80ms, so every device holds a **permanent 256 kbps uncompressed uplink, 24/7** — measured tx confirms 34-35 KB/s on all three. Stock Alexa detects the wake word on-device and uploads essentially nothing when idle. At idle the ratio is not marginal, it is total.

  Two corrections to my own earlier reading. First, the zero `tx_errors`/`tx_dropped`/`rx_crc` counters were never evidence of a healthy link: the MTK driver populates none of them, `/proc/net/wireless` reports no retries or missed beacons, and `signal_poll` returns `NOISE=9999`. They are structurally zero on this hardware whatever is happening, and are now deliberately not surfaced by the read API — a zero there reads as "healthy" and would mislead the next person exactly as it misled me. Second, the RSSI correlation was three points across devices differing on more than one axis; Lounge and Retreat share an AP radio while Office is on a different one entirely. The one genuine RF signal available does still single Retreat out — over 36h Office and Lounge sat below 150Mbps for 2 and 4 hours, Retreat for 35 of 36, down to 81 — but rate adaptation is not the same as causation.

  So: control-plane RTT (schema v9). Each `ping` carries a sequence id the device echoes; RTT is computed controller-side against one monotonic clock, because Echos boot with bogus clocks pre-NTP. Probe cadence 5s, aggregated in memory and flushed on the existing ~30s stats report so the DB cost is unchanged. Excursions are split by whether the device was busy at send time — contention should make latency track load, power-save should make it spike when idle.

  **The first data killed the RSSI theory outright.** Office, at −26dBm on its own AP, showed excursions to 1561ms — essentially the same as Retreat's 1624ms — against a floor of 1-4ms. Zero controller event-loop stalls in the same window, so not a measurement artefact. And once the idle/busy denominator was populated, Lounge read 33.3% idle against 36.5% busy: indistinguishable, which rules out power-save and load contention as well. **15-35% of probes exceed 200ms on every device, and it remains unexplained.** Recorded here rather than resolved, because a stated unknown is worth more than a tidy wrong answer.

- 2026-07-25 (**a metric that reads clean because it is broken — three times in one day**): worth recording as a pattern rather than three incidents. The dead driver counters above read zero and meant nothing. `rtt_samples_idle` was added in v10 with `DEFAULT 0`, so rows already accumulating in that hour reported every sample as "busy" — which produced a confident, completely wrong table showing idle excursions at 3-5x busy, caught only because Office showed 131 busy samples on a device that had done nothing for 45 minutes. And PR #25's `min_depth` earlier in the month read 0 on every stream for the same species of reason. In each case the tell was identical: **a metric that always reads the same value is measuring the wrong thing, and a clean reading deserves more suspicion than a dirty one.** Sanity-check every new metric against independent evidence before believing it, especially when it agrees with you.

- 2026-07-25 (**migrations are append-only, and I edited one ten minutes after shipping it**): v9 needed one more column, so I added the `ALTER TABLE` to the v9 entry. The live DB had already reached `schema_version = 9`, so the migration never re-ran, the column was never created, every ~30s stats report failed, and all three devices disconnect-looped until the post-deploy error grep caught it about a minute later. The rule is stated in a comment directly above the list, in a file I had read repeatedly the same session. Fixed as a proper v10. The schema-version test now asserts `schema_version == len(MIGRATIONS)` rather than a hardcoded number, so appending an entry without bumping its own statement fails in CI instead of in production.

- 2026-07-25 (**the dashboard showed one thing while the device did another**): found while investigating something else. Office's Config tab displayed `owwModel=hey_rhasspy` and `ledScene=standard` while the device was actually running `hey_mycroft` and `malevolent`, with every section labelled "Fleet". The dashboard computed effective config as a blind merge of the stored per-device dict over the fleet config, which is only valid if that dict holds nothing but overridden-section values — true for rows written through the API, which prunes, but not for rows the v8 migration backfilled. Lounge and Retreat were clean precisely because their sections had been toggled since. The devices were never wrong; `get_effective_device_config` only ever reads in-scope keys. A display-only bug, which is arguably worse, because the dashboard is where you go to check what a device is doing. Fixed twice over: the dashboard now filters rather than merges, and migration v11 prunes stored configs to match their scoping (Office: 26 keys down to 1, verified lossless against a live-DB copy first).

- 2026-07-25 (**music: a resume that could never work, and a buffer a quarter the size of the hardware's**): two real bugs surfaced by Wil actually using the media player, both worth the telling.

  Barging in over a Music Assistant stream paused at 173.6s, resumed, and left the device silent while HA still showed playing. The controller *did* resume — the fault is that `-ss` is passed before `-i`, making it an INPUT seek. On a seekable file that is a fast demuxer jump; on a non-seekable stream **ffmpeg does not ignore a seek it cannot perform, it decodes and discards input until it reaches the timestamp**. On a continuous flow stream that means 173 seconds of wall-clock silence. The module docstring asserted the opposite ("ffmpeg ignores -ss it can't do") and `stderr=DEVNULL` meant it could not even complain. Fixed with a first-chunk deadline that rejoins the live edge — for a continuous stream that IS the correct resume point — and the docstring now says what actually happens.

  Then the stream kept glitching regardless. The device buffers `audioChanDepth` 128 periods × 42.7ms ≈ 5.46s, but the feed held only 1.5s ahead of realtime, leaving roughly four seconds of hardware buffer unused — and the RTT instrumentation from earlier the same day had just measured 1812ms and 2610ms stalls during that very playback, each longer than the 1.5s being maintained. Lead raised to 4.0s with ~1.4s headroom under the device's depth. The old comment claimed the short lead was what made pause/stop/voice-preempt instant; it is not, `speaker_flush` plus the discard-until-EOS contract are, and the lead only governs what is in flight. Explicitly mitigation rather than cure, and the code says so — it rides out the stalls without explaining them.

  A footnote on method: the reported symptom was twice dismissed as user error ("put it down to muppetry on my part") and was twice a genuine defect with a timestamp. The instrumentation is what made the difference between a shrug and a fix.

- 2026-07-29 (**hear the microphone — utterance recordings, schema v12**): from Reddit feedback after the project was shared publicly. The request was simply to be able to listen to captured audio to judge mic quality, and it lands on a real gap: every mic-tuning decision on this project — `micGainDb` at +24dB, the beamformer's channel choice, whether `nsAsr` helps or hurts — has been made by *inference* from wake scores and garbled transcripts. A bad transcript tells you something went wrong; it does not tell you whether the room was noisy, the gain was low, or the denoiser ate a consonant. Thirty seconds of listening does.

  Opt-in per device (`saveUtterances`, Config → Microphones → Advanced). The audio streamed to Home Assistant for a turn is kept as a 16kHz mono WAV; each row in the Activity tab grows a ▶ and a ⤓. Tapped **below noise suppression**, so the file is byte-for-byte what went on the wire — see the correction in the entry below, because this shipped tapped the other way round.

  Three decisions worth recording, each of which could reasonably have gone the other way:

  **Default off, and it should stay off.** This is the only feature in the project that writes recognisable speech to disk. That deserves to be a decision someone makes rather than a default they discover later, and the cost of the wrong default here is not symmetric.

  **Retention is a hard per-device file count (10), not a time window or a share of the turn history.** `TURN_RETENTION` is far longer, so a turn row can outlive its recording — meaning **a non-NULL `audio_file` is a claim to check, not to trust**. Every reader resolves through `em_recordings.resolve`, which treats a missing file as an ordinary 404 rather than an error, and also re-checks that the file belongs to the device in the URL, since the endpoint takes device and turn id from the same path. Files are ordered for pruning by turn id parsed from the filename rather than by mtime, so a volume restored from a backup that flattened timestamps still prunes correctly.

  **The dashboard fetches the WAV through `API.blob`, not an `<a href download>`.** Sessions here are Bearer-header-only — no cookie is ever set — so anything the browser fetches on its own behalf gets a 401. This is the second time that has caught us out (the xterm.js shell hit it and was solved with a `?token=` query param). The blob route is the better answer anyway: play and download share one transfer, and the token never reaches the URL bar.

  Two things the test suite caught that a human review plausibly would not have. The Dockerfile guard flagged the missing `COPY em_recordings.py` — that would have crash-looped the container at import, and it is the same class of miss the guard was written for. And `test_v11_prunes_out_of_scope_values_from_migrated_rows` failed on `duplicate column name: audio_file`: it rewinds `schema_version` to 10 and re-runs `_migrate`, so it re-applied v12 to a database that already had it. That test would have broken on *every* future migration, not just this one, so it is now pinned to `MIGRATIONS[:11]` — a test that fails for a reason unrelated to what it asserts is a test that will eventually be ignored.

  Deployed the same day. Migration v11 → v12 clean against the live DB (snapshot first, per the pre-flight discipline; 496 turns intact), no errors, all three devices back on wss with satellites and BLE proxies reconnected.

- 2026-07-29 (**the recording was tapped in the wrong place, and the first analysis is what proved it**): the capture shipped buffered *above* the DTLN denoiser, reasoned as "raw capture is the mic-quality question, and it's what you'd judge NS against". Correct as far as it went, and wrong about which question people ask. It took exactly one real analysis to find out.

  Office had `nsAsr` **on**, so the four recordings were what the microphone delivered, not what the recogniser received — meaning the one thing they could not diagnose was the misrecognition they were sitting next to. Turn 497 heard "turn off the office light" as "turn off the artist lights"; turn 499, same phrase 42 minutes later, was correct. The recordings showed why the *room* differed — noise floor +8.7dB, and the excess weighted to 640Hz–8kHz (+10 to +14dB across those bands, exactly where fricative contrast lives), plus a discrete Q≈56 tone at 891Hz that moved to 758Hz in the later turn, so a variable-speed fan rather than mains. What they could not show was whether DTLN helped or hurt on the way past. Wil's call: "whatever we save should be the final output before STT."

  Tap moved below the denoiser. The file is now byte-for-byte the ESPHome wire payload. Saving both stages was considered and deliberately not done yet — it is a real want (per-stage capture is how you'd ever measure whether NS is earning its place) but it belongs with a replay harness, not bolted on here.

  Two findings from the same analysis worth keeping. **`micGainDb` is not the bottleneck any more** — speech peaked at −10 to −12 dBFS with zero clipped samples and identical spectral tilt across both turns, so the mic and beamformer behaved the same and only the room differed; the +24dB fix solved the quantisation problem it was aimed at, and further gain lifts speech and fan equally. And **a wake-threshold raise is not the free win it looks like**: the instinct was to lift Office 0.5→0.7 to reject a 0.614 false wake, but across 107 wakes p25 is 0.693 and the low tail *mixes* false positives with genuine commands ("Open the office blinds" at 0.656). The overlap is a model problem, not a threshold problem. Related: several sub-0.5 wakes transcribed ambient TV and are recorded `outcome=ok` — **`ok` means HA responded, not that the wake was real**, so outcome stats overstate wake precision and a false-accept metric needs its own definition.

- 2026-07-29 (**on-device wake word, part 1: it runs, and the defaults are a trap**): the long-deferred question — can openWakeWord run on the Dot itself? Wanted for privacy (audio never leaves the device unless you are talking to it), for the permanent 256kbps uplink, and for wake latency. One stated motivation did **not** survive contact with measurement: "let the controller run on lower-powered hardware" is unsupported, because OWW costs the controller only **1.8% of one core per device** (~5.4% for the fleet). Moving that off unlocks nothing.

  **The runtime loads.** ONNX Runtime 1.19.2 `armeabi-v7a` from the Maven AAR runs on FireOS 5 / Android 5.1 / API 22: `CreateEnv status=0x0`. No ELF blocker — the four symbols missing from the API 22 sysroot (`__cxa_thread_atexit_impl`, two gcov stubs, one TLS helper) are all **WEAK** so the linker nulls them, and there is no `PT_TLS` segment because it uses `__emutls`, the pre-API-29 path. Only libc/libm/libdl/liblog needed; libc++ is statically linked. It links in the **existing** compiler image with no Dockerfile change. Worth recording that `ro.product.cpu.abi` on this device **lies** — it claims `arm64-v8a` while `ro.product.cpu.abilist` is `armeabi-v7a,armeabi`. Userspace is 32-bit; arm64 is not a lever.

  **Per-frame latency is the wrong metric, and following it would have produced a terrible answer.** Flat-out, the chain costs 43.9ms per 80ms frame single-threaded. Paced at the real 12.5Hz duty cycle and measuring actual process CPU, the picture inverts: **ORT's thread pool spin-waits between inferences**, burning **243% of one core** — 2.4 cores pinned — to do work that needs 23%. `session.intra_op.allow_spinning=0` fixes it. And **more threads makes total CPU worse** (63% vs 36%) even though latency halves, because we are duty-cycled with 51ms of headroom: latency is free, CPU is scarce. Best configuration is the counter-intuitive one — **XNNPACK + one thread + no spinning = 36.2% of one core**, at which point measured CPU equals pure inference time exactly, i.e. zero remaining overhead.

  XNNPACK is a straight **1.5× with bit-identical output**, and is reachable only through the generic string-based EP API — the stock Android AAR exposes no `OrtSessionOptionsAppendExecutionProvider_Xnnpack` symbol, so it looks unavailable until you check the `__FILE__` strings and find `providers/xnnpack/nn/conv.cc` compiled in. NNAPI is present but needs API 27, so it is useless here. **int8 dynamic quantisation is dead**: it shrinks the model 1.33MB→383KB and then fails to load with "Could not find an implementation for ConvInteger(10)" — the armv7a build has no such kernel — and it was 4× *slower* on x86 with 5-8% error anyway, because dynamic quantisation targets MatMul/LSTM and this model is 20 Convs. Conv wants static quantisation with calibration.

  The **biggest remaining inefficiency is architectural, not a kernel**: the embedding window advances 8 of 76 mel frames per call, so **89.5% of every inference recomputes what the previous one already did**. A streaming CNN caching per-layer activations could plausibly take 15.8ms to ~2ms. That is precisely what **microWakeWord** is — which is why it runs on a 240MHz ESP32-S3, and which means "write our own inference engine" has already been done by someone else. The cheap version of the same idea, untried: gate inference on acoustic energy so the chain does not run in a silent room.

  Numerics are not a risk: ARM and x86 agree to **~7 significant figures** on identical input, six orders of magnitude below anything that could move a wake decision against a 0.5 threshold.

- 2026-07-29 (**on-device wake word, part 2: the buffering is the algorithm**): first piece of real code (PR #45) — `device/internal/wakeword`, pure Go, inference behind an interface so it is host-testable with no ONNX, no cgo, no hardware.

  Three constants were found by reading `openwakeword/utils.py` rather than by assuming, and each fails **silently**: the melspectrogram is computed over the chunk **plus 480 samples of left context** (1280 new samples yield 8 mel frames, not 5 — get it wrong and the embedding window drifts out of sync with the audio while everything still runs); the mel ring **starts pre-filled with 1.0**, not zero or empty; and model output passes through **`/10 + 2`** before entering the ring, a transform that exists to realign the ONNX export with Google's original TensorFlow input distribution, so dropping it feeds the embedding model out-of-distribution data and every score is wrong. Input is raw int16 magnitudes cast to float32, **not** normalised to ±1. Finding the 480-sample context also retired an earlier measurement: the melspec timing had been taken with 1280-sample input, ~37% optimistic.

  Validation asserts **the exact tensors handed to each model** — hashes of the 76×32 embedding window and the 16×96 classifier input — rather than final scores, because a score-only comparison passes with the windows subtly misaligned, which is the failure that matters. Model outputs are replayed from a fixture captured from openwakeword itself, so the test covers the buffering and not the models.

  One deliberate divergence: upstream seeds its feature ring with **four seconds of random audio**, so its first ~16 scores are partly noise-derived. Ours starts clean and reports not-ready for ~1.28s instead. Better behaviour, pinned by a test, and the reason the classifier assertion starts at chunk 16.

  A note on method, since three separate harness bugs produced convincing wrong answers before any real result appeared. A file-push reported success by matching its own **PTY echo** of the completion marker, on a transfer that never happened. `unsigned long` is **32-bit on armv7a**, so a `>>33` in a test generator was undefined behaviour and made ARM and x86 appear to disagree numerically. And a resumable pusher that resumes on **file size** silently ran a stale binary when a rebuilt one of similar size was pushed to the same path — caught only because the numbers were suspiciously identical. Every one was transport or harness; none were the technology. Same family as the metrics-that-read-clean pattern recorded above, and the same lesson: a clean result deserves more suspicion than a dirty one.

- 2026-07-29 (**this document was being read as the onboarding guide**): people arriving from the public post were starting here and bouncing off, which is fair — it opens with preloader flashing and SELinux patching. That was never the intent. SETUP.md is two things, the rooting reference and this journal, and neither is where a newcomer should start. A banner at the top now says so and points at `docs/quickstart.md`, which is the actual onboarding path. Recording it because the failure was invisible from the inside: the file is perfectly well organised *if you already know what it is*, and no amount of internal structure fixes a document whose first job is to tell you it is not the one you want.

- 2026-07-30 (**on-device wake word, part 3: the runtime is dlopen'd, not linked**): `device/internal/wakeword/ort` fills in the `Inferer` interface part 2 left open, so the pipeline can actually score audio. Only ONNX Runtime's MIT C header is vendored; the 12.3MB library is **dlopen'd at runtime**, and that is a deliberate reliability choice rather than a packaging convenience. Linked properly, a device missing the library would fail to *exec* — and on hardware whose entire recovery story is "count fast exits, flip the A/B slot", turning an absent optional file into a boot failure is a bad trade. Opened by hand instead, a missing library is an error return and the device keeps using controller-side wake word. The claim is checked, not asserted: the ARM test binary's `DT_NEEDED` is `libdl/liblog/libc` only, with **zero undefined `Ort*` symbols**. A side benefit is that no ORT symbol is needed at build time either, so `go build` works on any machine.

  The API forced the shape of the C shim: ORT's C API is a **struct of function pointers**, and cgo cannot call a function pointer held in a C struct, so every call needs a C wrapper regardless. Configuration is the measured optimum from part 1 baked into `DefaultOptions` — one thread, XNNPACK, `allow_spinning=0` — because those numbers are not defaults anyone would guess and a future reader adjusting them deserves to find out why in the same file.

  **The tests are the point of this piece.** Part 2's fixture replays model outputs, which proves the buffering but cannot catch a wrong tensor shape or a misconfigured session; running the models by hand proves the opposite half and cannot catch a misaligned window. So a second fixture drives the real pipeline through real ONNX Runtime and compares **every stage** — melspectrogram output, embeddings, and scores — against what Python produced from the same audio. Two things about it are deliberate. It carries **its own input audio** rather than regenerating it from a matching generator in Go, precisely because part 2 lost time to a `>>33` on 32-bit `unsigned long` making two "identical" generators disagree; a fixture that carries its input cannot have that bug. And the audio is **synthetic**, both to keep recognisable household speech out of a public repository and because the assertion is numerical agreement between two engines, for which noise discriminates as well as speech. That does leave one hole worth naming: noise scores 0.0000 at every chunk, so a classifier hard-wired to return zero would pass — hence a probe block feeding one wide-range tensor whose score is small but distinctly non-zero.

  Getting from "correct on the host" to "correct on the device" turned out to be two questions, not one, and separating them is what made the first cheap. Question one — *does the binding work on this hardware at all* — needs no firmware change, no OTA and nothing that alters fleet behaviour: `device/tools/oww_probe` is a standalone ARM diagnostic in the same vein as `capture_mics`, which runs **the same comparison the host test runs** against the same golden capture, then paces frames at the real 12.5Hz duty cycle and reports process CPU from `getrusage`. That paced measurement matters more than it sounds: it catches what a C benchmark structurally cannot, namely cgo call overhead and Go GC pressure from the buffering. Question two — *does it hear the wake word in a real room, at a cost worth paying* — needs firmware, config plumbing and shadow-mode reporting, and there was no sense building any of that on top of an unverified binding.

  Two things fell out of writing the probe that were not the point of it. `fixture.Verify` had to move into a shared package, because the probe and the test asking *slightly different* questions is the failure mode that matters here — it is the probe's answer that gets trusted, since it runs on the hardware, so it must be the stricter of the two by construction rather than by intention. And the probe validated on the host before going anywhere near a device reproduced the previously measured controller cost (1.7ms/frame, 2.5% of a core against 1.43ms/1.8%), which is the kind of agreement that makes a measurement believable rather than merely available.

  One design mistake, caught by its own test. The runtime started as a process-wide singleton holding the handle, `OrtApi` and `OrtEnv` in C statics. The missing-library test then failed with *"already open, cannot also open"* — a bookkeeping error standing in for the real one, and worse, the graceful-degradation path became **untestable once any library had loaded**. The statics are gone; each runtime owns its handle and environment, memoised per path in Go so that opening the same library twice still shares one set of thread pools. Worth recording because the singleton looked like the obviously right call for a device with one core online, and the thing that exposed it was writing a test for the failure mode rather than the happy path.

- 2026-07-30 (**on-device wake word, part 4: it runs on the Dot, and the Go stack costs 1.5 points**): `oww_probe` on Lounge, ONNX Runtime 1.19.2 with XNNPACK attached. **Phase 1 passes** — the Go streaming pipeline driving real ONNX Runtime reproduces openWakeWord's Python output on ARM: melspectrogram agrees to **6.3e-07 of tensor scale**, embeddings to **1.4e-06**, and the classifier score to **8.9e-08 absolute** against a threshold of 0.5. Phase 2, paced at the real 12.5Hz duty cycle: **37.7% of one core**, p50 31.4ms per frame, max 63.7ms, **zero of 375 frames overran their 80ms slot**, 22.5MB peak RSS. Set against last night's pure-C measurement of 36.2%, **the entire Go binding — cgo call overhead, GC pressure from the buffering, the lot — costs about 1.5 percentage points**, which is the number that could not be known without measuring it in the real shape and was the main reason to build the probe rather than reason about it.

  **The first run failed, and the failure was the test's fault rather than the device's.** 10 of 24 embedding chunks fell outside tolerance, worst case 6.7e-05 absolute. The criterion was per-element relative error, which is meaningless for a tensor whose values straddle zero: the same 2e-5 absolute error reads as 1.7e-4 on an element at 0.11 and would read as 2000 on an element at 1e-8, so it fails on whichever element happens to sit nearest zero while saying nothing about whether the two engines agree. Against the **tensor's** scale (embeddings span ±45-62 here) the same error is 1.4e-06 — the ~7 significant figures measured independently between ARM and x86 the day before. What settled it beyond argument was the score: if the embeddings were wrong in any way that mattered, the value with a decision attached would have moved, and it agreed to 8.9e-08. Tolerances now scale with the tensor, and the probe **prints the agreement rather than just a verdict**, because "passed at 1.4e-06 of scale" and "passed at 9e-05" say very different things about a device.

  Which produced a second, smaller instance of the same lesson immediately: quoting an of-scale ratio for the *score* printed "0.75 of tensor scale" for a run that agreed to 9e-8 and passed on the absolute floor, because the score tensor on non-speech audio is ~1e-7 and there is no magnitude to be relative to. Alarming, meaningless, and left in a log it would have cost somebody an hour. The ratio is now only shown for tensors with a scale to speak of.

  **Transport, again.** The first version of `push_file.py` sent 24KB of base64 per shell command and wrote **nothing at all**, while every step reported success — the shell is a PTY in canonical mode, where a line beyond ~4096 bytes is mangled by the line discipline. The fix was to stop inventing and read what already worked: `em_api._stream_file_to_device` moves 10MB of firmware by streaming **short 76-char base64 lines inside a heredoc**, and it does that precisely because of this limit. One heredoc per 96KB block keeps resumability. The plane also drops *mid*-block, not only between blocks, so each block retries and recovery trims the file back to the block's start offset with `dd` rather than trusting a partial base64 decode. The reason none of this silently shipped a corrupt binary is that success is defined as **md5 agreement** and nothing weaker — the very first failure was caught by the md5 check, not by the transfer noticing.

- 2026-07-30 (**on-device wake word, part 5: shadow mode, and the rule that shaped it**): the device can now run the wake model over its own mic stream and report what it *would* have detected, without acting on it — `owwOnDevice=shadow`, schema v13. The point is to answer whether on-device detection is trustworthy by comparing both detectors on the same audio, before anything depends on the answer.

  **One rule drove most of the design: a shadow feature must never be able to damage the real audio path.** Inference costs ~31ms per 80ms frame, the mic goroutine reads 160ms ALSA batches, and the capture ring is only 160ms deep — so scoring two frames inline would spend 62ms of that budget and invite exactly the capture stalls that lose whole batches and silently broke AEC for four releases. Scoring therefore runs on its own goroutine behind a buffered channel, and when it falls behind it **drops frames and counts them** rather than blocking. A shadow run that drops frames still tells you something; one that stutters the microphone tells you nothing and costs a working device. The same rule made inference errors non-fatal and counted rather than propagated.

  The tap sits exactly where the ungated wake stream's frames are written to the wire, so the device scores **byte-identical frames on identical 80ms boundaries** to what the controller receives. Without that, a score difference would have two candidate explanations — the engine, or the framing — and only one of them is interesting.

  **Nothing is sent per frame**, which is the project's standing instrumentation rule. Threshold crossings go immediately, because their entire value is the timing and a report arriving 30s late cannot be matched to anything; they are rare, since a refractory period collapses each utterance to a single crossing (an unguarded version reports a handful per wake, because scores stay above threshold for several consecutive frames). Everything else — frames, drops, crossings, max score, errors — is a window summary riding the **existing** 30s stats tick, so the DB cost of on-device scoring is one extra upsert per 30s per device and it shares the executor hop the stats write already takes.

  Correlation turned out to be the only genuinely subtle part, so it lives in its own dependency-light module (`em_shadow.py`) where the test suite can reach it — `em_controller` cannot be imported without dragging in openwakeword and aiohttp. Three decisions there are worth recording. The device **never sends a timestamp**: an Echo's wall clock is bogus before NTP, so it reports how long *ago* a crossing happened and the controller converts against its own monotonic clock, the same reasoning as the RTT work. The match window is **deliberately loose at 2s**, because the two detectors see the same frames but not in the same detector *state* — the controller drops wake frames while a turn or TTS is in flight — and of the two possible errors, a false "miss" is the more misleading, since it argues against a feature that is actually working. And a match is **consumed**, so two turns in quick succession cannot both be credited to one crossing, the same discipline `playback_stats` already follows.

  The distinction that took the longest to get right is not in the code at all, it is in the schema: **a NULL device score means three different things** — shadow mode was off, the firmware predates it, or the device genuinely did not hear the wake word. Only the third is a finding; the first two are absence of data, and reporting them together would manufacture evidence against on-device detection. Hence `turns.dev_shadow`, a flag for "the device was known to be scoring", which makes a NULL beside it a real miss. The hourly `wake_counters.dev_*` columns carry the other half: crossings that never matched a turn are the false-accept side, which per-turn rows structurally cannot show — recorded honestly as an *estimate*, because turn rows and hourly counters are pruned on different schedules and the difference drifts over a long window.

  Cost is the thing to be clear-eyed about: ~38% of one core, permanently, on top of the ~18-20% mic-pipeline baseline, on hardware where sometimes only one core is online. Default off, enable one device at a time, and the runtime plus models are installed out of band rather than shipped in the firmware — 12.3MB would double both the OTA payload and the space each A/B slot occupies, and a missing file is an ordinary logged condition that leaves controller-side wake word exactly as it was.

- 2026-07-30 (**the Dot has four cores, and cpuPct was lying to me**): shadow mode put Lounge at 51% CPU against ~20% for the other two, and I reported that as "51% of one core, and only one core is online" — which was half right in the way that matters. `/proc/cpuinfo` does show one processor, but `/sys/devices/system/cpu/present` is `0-3`: the MT8163 is a **quad-core A53** and MediaTek's hotplug strategy parks three of them when idle. A power state, not a ceiling. Wil asked the obvious question I should have asked myself.

  The governor is legible once found. `/proc/hps/up_threshold=80` with `up_times=2` brings another core online after two samples above 80%; `down_threshold=70` with `down_times=20` parks it again, slowly; `rush_boost` at 98%; `input_boost_cpu_num=2` on button presses. cpu0 already runs at 1.3GHz, its maximum, so nothing was being withheld on frequency either. Lounge sat at 54% avg / 60% peak — *below* the 80% trigger — which means it stayed on one core precisely **because the work fit**, and three cores were available on demand the whole time.

  So we raised the floor. `num_base_perf_serv` is HPS's minimum core count (the `num_limit_*` files are its ceilings); setting it to 2 held two cores online while leaving `up_threshold` to scale to 3 and 4 exactly as before. Worth doing not for throughput but for **deadlines**: the mic pipeline has a hard 160ms budget and now timeshares with inference running in ~31ms bursts, and two cores let those genuinely run in parallel rather than depending on hotplug noticing a burst that has already started. Cost measured rather than assumed — +0.3°C at the PMIC, no change at the CPU zone, on a mains-powered device idling at 33°C. It lives in the binary rather than a provisioning script because procfs does not survive a reboot, and it sets the floor rather than writing `cpu1/online` directly, because HPS would re-park that within `down_times` and leave a setting that looks applied and silently isn't.

  **The instructive part is what the number did next.** Lounge went from 51% to 25.5%, level with the untouched devices — and *nothing about the load changed*. `cpuPct` is computed from the aggregate `/proc/stat` line, so it is a share of ONLINE capacity: doubling the cores halves the reported percentage for identical work. Read carelessly, that reads as "the core floor fixed our CPU problem", which would have been a satisfying and completely false conclusion. The metric was never self-describing, and had been reported alone for months. It now ships with `coresOnline`/`coresTotal` beside it, in the status page and in the hourly rollup, so a future reader cannot draw the inference I nearly drew.

  Thermals went in at the same time, and it turns out the useful signal is not a temperature. There are 11 zones (`mtktscpu` the CPU, `mtktspmic` the PMIC, `tmp103` a discrete board sensor, plus board-temp siblings), all sitting at 31–34°C — so no thermal story at all, which is itself worth being able to see. But **`num_limit_thermal` is the sharper instrument**: it is how many cores the thermal governor will currently permit, so anything below four means capacity is already being capped, and that bites long before a temperature reading looks worrying. A missing sensor is stored as NULL rather than 0, because a zero averaged into a mean reads as a *cool* device — the wrong direction for a metric whose only job is to warn.

- 2026-07-30 (**capabilities, not versions — and release notes someone can actually decide from**): two questions from Wil while cutting v2.9.9, both of which turned out to belong *inside* the tag rather than after it.

  **How do device/controller dependencies get handled?** The honest answer was that this feature happened to be safe and the general mechanism was implicit. New firmware against an old controller leaves shadow mode dormant (the controller never sends `owwOnDevice`); old firmware against a new controller ignores the unknown field and reports no summary, so `dev_shadow` stays NULL. Both directions degrade correctly — but by *my having thought about it*, not by anything that would stop the next change getting it wrong. The project already had the right mechanism and I hadn't used it: devices announce capabilities on connect (`led_anim` is the precedent, with legacy firmware falling back to controller-streamed frames), so the firmware now announces **`oww_shadow`** and the controller asks whether the device says it can, rather than whether its version is at least something. Version comparison means encoding release history into the controller and misjudging a dev build the first time you run one. The dashboard consequence matters more than it sounds: a toggle that silently does nothing on old firmware reads as a *broken feature*, so it is now shown disabled with the reason instead. Guarded by a test that parses the capability list out of the Go source and asserts every string the controller checks is one the device sends — verified by breaking it in both directions, because a typo there makes a feature permanently unavailable while looking exactly like unsupported hardware.

  The second rule is the one already in force but undocumented: **degrade to the old behaviour, never to a wrong answer**. Absence of a measurement stores as NULL, not 0. Old firmware reporting no playback stats must not read as "zero underruns", and a device that cannot score locally must not read as "scored and missed every time" — which is why `turns.dev_shadow` sits beside `dev_wake_score`. Both rules are now in CLAUDE.md and in a Compatibility section of the README, because they are the kind of thing that is obvious while you hold the whole system in your head and invisible six months later.

  **Release notes.** The OTA poller was discarding the GitHub release body entirely, so the dashboard could say "update available" and nothing about what the update *was*. Deciding whether to push firmware to a device you depend on, from a version number alone, is a guess rather than a decision. Notes now come from the **annotated tag message** — so writing the tag *is* writing the notes, and there is no separate changelog to forget — published via `body_path` with GitHub's generated commit list kept below. A lightweight tag yields an empty body and falls back to that list: worse, not broken.

  Two details worth keeping. The restart path is the one that would have rotted silently: the in-memory release cache is populated from the DB when cold, so notes captured on first poll but absent from that path would appear once and vanish on every controller restart — there is a test pinning all four hops. And the notes render as preformatted text rather than markdown, deliberately: React and xterm are the only vendored libraries, and adding a markdown renderer to style a release note is a poor trade when simply-written notes read fine as text and a GitHub link covers the rest.

  Also caught while writing it: `var(--line)` is used in `dashboard.jsx` but defined nowhere. The existing use carries a fallback so it works; mine would have been an invisible border. The convention is `rgba(0,0,0,0.06-0.08)`.

- 2026-07-30 (**the shadow comparison was asking two different questions**): the first on-device agreement figure was 83%, and one of the two "misses" was not a miss at all. The controller had woken at a score of 0.055 — below its own nominal 0.5 threshold — because barge-in lowers the bar to `bargeInThreshold` during playback, where echo at the mic is ~25dB louder than the person and speech-over-TTS scores are depressed. The device was scoring against 0.5 throughout, so it could not possibly have crossed. Two detectors, two different questions, one number pretending to compare them.

  Chasing it turned up an older bug underneath. `turns.wake_threshold` recorded the *nominal* threshold rather than the one the wake actually cleared, so rows read `wake_score 0.055` against `wake_threshold 0.5` — a turn that woke below its own bar. Present in the data since at least 2026-07-25, harmless-looking, and precisely the field the comparison needed to tell a real miss from an unanswerable question. It now records the effective threshold, which makes every historical-style row internally consistent and gives the comparison its evidence.

  The fix has two halves and both were necessary. The device now **mirrors** the controller: `SetBargeThreshold` drops its bar while `PcmSpeaker.IsStreaming()` is true, so shadow mode measures barge-in agreement instead of guaranteeing disagreement on it. That needed `bargeInThreshold` plumbed to the device for the first time, and an `IsStreaming()` accessor — read under `stateMu` rather than as an atomic, because `streamActive` is deliberately paired with `discarding` under one lock and reading it outside that lock is the thing the pairing exists to prevent. A guard test pins that a barge threshold configured *above* the normal one never raises the bar: it exists to make detection easier during playback, never harder.

  The other half is honesty in the metric. The device reports the threshold in force with each window summary, it lands on the turn (`dev_threshold`, schema v15), and the rollup now reports **three** buckets rather than two — agreed, missed, and *not comparable*. Unknown-threshold turns (older firmware) go in the third bucket too, because "we cannot tell" is a real state and folding it into either of the others invents a result. A large third bucket is itself the useful signal: it says the agreement figure is describing a small slice of reality.

  The general lesson is not about barge-in. It is that a comparison between two systems silently assumes they were asked the same thing, and nothing in the data announces when they were not. The number looked plausible, agreed with itself across turns, and was wrong in a direction that made the feature look worse than it is — which is the flattering direction to be wrong in, and therefore the one least likely to be questioned.

- 2026-07-30 (**debloat round 2, and a payload that could never be updated**): Wil spotted Amazon services still running post-debloat. The honest survivors list was shorter than it looked: `echoaudioservice`, `device.settings`, `simplelauncher` and `NativeAccessorProxyServices` are all on a *deliberately kept* list from the validated first pass, so the only real candidate was **`com.amazon.whad`** — Whole Home Audio, the multi-room function EchoMuse replaces outright — plus its native half `whad_cc` and the `avahi-daemon` it finds peers with. Checked before touching: the firmware resolves the controller with its own in-process mDNS (`grandcat/zeroconf`), so nothing of ours needed avahi.

  The mechanism had to be learned rather than assumed. whad is `PERSISTENT`, so `pm disable` is ignored (which is why this list uses `pm hide` at all), **`am force-stop` is a no-op** — same pid afterwards, not even a restart — and `pm hide` alone does not stop a running instance. A direct `kill` worked and it did not respawn once hidden. Verified across a reboot, because a runtime-only win evaporates at the next power cut.

  **The numbers overstate it twice and both traps are worth remembering.** whad shows ~62MB RSS, but RSS counts shared zygote pages and killing it live recovered ~9-10MB. Post-reboot `free` then showed a 60MB improvement — inflated in the other direction, because a cold boot has empty caches (buffers 14→6MB). The defensible figure is the controller's own metric: Office 179→160MB, Retreat 188→153MB. Real, worth having, and a third of what the first number suggested.

  Applying it exposed something bigger: **`echomuse-debloat.sh` had no update path at all**. `start_server.sh` has synced on every OTA since 2026-07-11 precisely because payloads drift, and the debloat pair had been left out — so every fielded device needed a manual push. `_sync_debloat` now mirrors `_sync_start_script` and reconciles **both** halves, which mattered because round 2 added a *package*: a script-only sync would have looked like it worked and changed nothing anywhere. It rides the OTA and is also a button, and the button is not a nicety — the OTA path cannot reach a device already on the latest firmware, which was exactly Lounge's situation. A test now asserts every file in `device_payloads/` is named somewhere in `em_api.py`, so a fourth payload cannot ship without a sync.

  Two measurement traps hit while verifying, both of which produced confident wrong answers. `ALL - VISIBLE` from `pm list packages -u` is **not** the hidden count — it includes uninstalled packages, and it happened to equal the list size once, which read as confirmation. And the visibility test used shell `case "$VIS" in *"package:$p"*)`, an **unanchored** match: `package:com.amazon.tcomm` is also a substring of `package:com.amazon.tcomm.client`, so three packages reported as un-hidable on *both* devices, agreeing with each other and with an existing note about FireOS ignoring state changes for system apps. All false — `dumpsys` said `hidden=true` throughout. What broke the story was checking one package directly instead of trusting the aggregate.

- 2026-07-30 (**"it only notices a new release when I click Check"**): Wil's observation, and there were two independent causes — one of which had already bitten me without my recognising it. The dashboard fetched the release **once**, on entry to the Updates tab, so a tab left open never learned anything however often the backend polled; the Activity tab had a refresh timer for exactly this reason and Updates did not. And `_get_cached_release` fired its refresh into the background while **returning the stale value**, so even a fresh tab entry could show the previous version. That second half is why my own OTA pushed v2.9.9 while v2.9.10 was current an hour earlier — the update endpoint reads the same cache. I had written that off as "my script skipped a step a user wouldn't". It wasn't: a user pressing **Push update** would have hit it too. The refresh is now awaited when the cache has aged out, a version change is broadcast on the existing event stream, and the tab keeps asking as a fallback.

- 2026-07-30 (**a blank tab that nothing on the server could see**): I gated the new "Score on device" toggle on `device.connected && !device.owwShadowCapable` from inside `DeviceConfigForm` — a component that receives config, not a device, and which is *also* rendered for the fleet-config view where no device exists even in principle. `device` was undefined, reading `.connected` threw during render, and the entire Config tab went white. Behind it sat a second bug: the disabled path passed `onChange={undefined}`, and `Toggle` calls it on click, so it would have thrown on click even once render was fixed.

  What makes this one worth recording is the shape of the blindness. Every API returned 200, the logs were clean, CI was green, and the test suite never touches a browser — so from the server side the system looked perfect while a whole tab was unusable. It was findable only by clicking. The guard added is narrow but exact: a test asserts `DeviceConfigForm` contains no `device.` references, verified by reintroducing the bug and watching it fail. Anything a control needs about a device must now arrive as an explicit prop, which the fleet view can default sensibly.

  Also moved **Re-apply debloat** off the Status panel to a Maintenance panel on the Updates tab, at Wil's prompting and correctly: Status describes what a device *is*, and re-applying a payload is something you *do*. It belongs beside deploy and rollback.


*Device: Echo Dot 2nd Gen (RS03QR). Tested on macOS with ADB 35.0.2.*
