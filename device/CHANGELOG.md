# Device firmware changelog

Release notes for the firmware binary, one section per version. The
heading is the version WITHOUT the `v` prefix, because that is what
`.github/workflows/cut-release.yml` matches when it builds the tag
annotation from this file — `## 2.15.0-fx.1` for the tag `v2.15.0-fx.1`.

Headings INSIDE an entry are `###` or deeper. A `## ` line starts a new
version section, and that is what the extractor stops at.

Newest first. Written for the person deciding whether to push this to a
device they rely on, so it says what changed, what to expect, and what is
required of them.

## 2.23.0-fx.1

The Echo can now say what went wrong.

### What's new

**Until now, when something failed on the device, the reason stayed on the
device.** The Echo writes its log to memory, and the only thing it ever sent
onward was a periodic memory summary. So a receiver failing to start every
minute for two hours was visible only to somebody willing to open a root shell
on their own hardware — which is exactly what diagnosing this week's AirPlay
fault took.

Failures and a few lifecycle lines are now forwarded to the controller and
appear in its log, where they can be read without touching the device, and are
included in a support bundle. Everything still goes to the device's own log
unchanged; this adds a copy of the lines worth reading.

It is deliberately rationed — six lines a minute — because the same connection
carries the health checks that decide whether the Echo is considered online,
and flooding it would break the thing it reports on. When lines are held back,
the next one through says how many.

### What is required of you

Nothing. If you report a problem after this, the answer is far more likely to
be in a support bundle already.

## 2.22.0-fx.1

AirPlay and Spotify Connect survive an update.

### What's new

**After every update the Echo vanished from the AirPlay list, and came back
only after being unplugged.** This is what that was, and it was not about the
network announcement at all.

The Echo runs AirPlay and Spotify Connect as separate programs. Updating the
firmware restarts the firmware — but not those two, which keep running and keep
holding the network ports their protocols are defined on. The new copy then
cannot claim the port, gives up immediately, and tries again a minute later,
for ever. Nothing is announced because nothing is running.

Each start now takes the ports back from a leftover copy before starting its
own. That also repairs a device already stuck in the loop, which is every
device that has been updated — no power cycle needed. The programs are stopped
on the way down as well, but that is the tidy half rather than the fix: it
cannot run if the firmware is killed outright or crashes.

**A device with a cable in the headphone socket no longer fights Android for
the speaker.** Android keeps the speaker for itself while a plug is present,
and since 2.20.0 the firmware asked for it back every few seconds — for ever,
on a device where the answer was never going to change. That worked out at
stopping an Android system service roughly every 2.6 seconds, all day. It now
asks a few times and then settles into a slow retry, so a plugged-in Echo is
quiet about it and picks the speaker up promptly if it does become free.

**A device whose speaker Android will not release no longer floods its own
log.** Since 2.20.0 such a device runs normally and stays silent, which is
deliberate — but it was reporting the refusal for every fragment of audio,
about twenty times a second, into a log held in memory. That crowded out the
very lines needed to explain it. It now says so once every few seconds and
counts what it suppressed.

### What is required of you

Nothing. If AirPlay or Spotify were missing since your last update, they come
back on this one without unplugging anything.

## 2.21.0-fx.1

AirPlay is about a second quicker.

### What's new

**Roughly a second of the AirPlay delay was ours, and it is gone.** The
speaker fills about a second of audio before it starts playing anything. That
cushion exists for music sent by the controller over WiFi, where a two-second
network stall used to punch an audible hole in a track — but AirPlay and
Spotify Connect run as programs on the Echo itself and hand their audio over
a pipe, with no network in between. There was nothing for the cushion to
absorb, and because both of those pace themselves in real time, the delay
never went away after the first second: every sample waited behind it.

Those two sources now start after about 170 milliseconds instead. Music sent
from Home Assistant is unchanged and keeps the full cushion.

**AirPlay is also told what the Echo adds behind it**, so it hands the audio
over correspondingly earlier and the sound lands when your phone intended.
This is the smaller half of the win, and the direction of the correction comes
from shairport-sync's documentation rather than from a measurement on real
hardware — if the delay gets slightly worse rather than better, that is the
sign to flip, and it can be corrected on a device without a new firmware.

What remains is AirPlay's own protocol delay of about two seconds, which is
imposed by the sender and is not ours to shorten.

### What is required of you

Nothing. If music from Home Assistant develops gaps it never had, that is the
one change that could cause it — tell us, because it would mean the cushion is
being applied to the wrong source.

## 2.20.0-fx.1

The Echo comes up even when the speaker does not.

### What's new

**2.19.0-fx.1 did not fix it.** That release made the device ask Android
again, repeatedly, to hand back the speaker while it waited — which wins the
race most of the time. It still lost it, and the device still had to be
unplugged.

So the device no longer waits for the speaker before doing anything else. The
controller connection, the network announcement, the buttons, the mute and the
LED ring all start straight away, and the speaker opens behind them, trying
again every few seconds until it succeeds. Losing that race now costs the
sound rather than the whole Echo — and the ring shows its orange
no-controller pulse instead of staying dark, which is the difference between
a device you can diagnose and one that looks broken.

This also stops depending on the diagnosis being right. If something other
than Android's media service is holding the speaker, the Echo still comes
back; it simply stays silent and says so in its log.

### What is required of you

Nothing. If a device is reachable but silent after an update, that is this
change working — give it a few seconds, and tell us if the sound does not
return.

## 2.19.0-fx.1

The Echo comes back on its own after an update.

### What's new

**After an update the device stayed dark until it was unplugged.** The
firmware restarted correctly every time — the supervisor log shows it — and
then never came back online, with the ring not even showing the orange
no-controller pulse.

It never got that far. The speaker is opened before anything else starts, and
Android's media service takes it for itself when a plug is in the jack. At a
cold boot that service is still starting and lets go in a fifth of a second;
after an update it is fully up, and the one request to release it had already
been spent. The open then waits for ever, and mDNS, the controller connection,
the buttons and the LEDs are all behind it. The device now asks again while it
waits, which is what makes an update behave like a power cycle.

**A volume press on a muted device turned the ring red.** A leftover of the
rule that mute owns the ring, which went away when the ring became Home
Assistant's — the microphone button's own LED is the mute indicator now. It
painted over whatever colour Home Assistant had set, and Home Assistant was
never told.

### What is required of you

Nothing. If an update still leaves the device dark, unplug the jack before
starting the next one and tell us — that narrows it to the same cause rather
than a new one.

## 2.18.0-fx.1

Two fixes for the streaming endpoints, both found on a real device.

### What's new

**Home Assistant said the Echo was playing AirPlay long after it had
stopped.** The Audio and Audio Source entities from 2.17.0-fx.1 read
`airplay` for as long as the device stayed up, even with nothing playing and
the connection dropped.

The plane was only ever given back when shairport-sync *exited* — and it is a
daemon that runs continuously so the Echo stays in the AirPlay list. When a
phone disconnects it simply stops sending audio, so nothing was released. The
claim now expires after two seconds of silence and is taken again with the
next note. Spotify Connect had the identical fault and is fixed with it.

**The Spotify build could never advertise itself.** librespot was compiled
without `with-libmdns`, which is one of its own default features and the whole
of how a speaker appears in the Spotify app. It ran perfectly, `spotifyEnabled`
was on, the Echo was simply never in the list — and turning the setting off and
on could not help, because there is no announcement to resend.

### What is required of you

**Rebuild and reinstall librespot** if you use Spotify Connect —
`device/librespot/build.sh`, then Updates → Streaming endpoints. The firmware
update alone does not fix it; the fault is inside the binary you installed.
The build script now refuses to produce another one like it.

Nothing is required for the AirPlay fix beyond this update.

## 2.17.0-fx.1

The Echo now tells the controller when it is playing something of its own.

### What's new

**Spotify Connect, AirPlay and Sendspin were invisible to Home Assistant.**
All three play from programs running on the Echo — no audio passes through the
controller — so Home Assistant's media player reported idle over music that
was audibly playing.

The firmware now reports which source owns its speaker, both on every handover
and on the register message, so a reconnect mid-track does not read as
silence. The controller turns that into two Home Assistant entities, **Audio**
and **Audio Source**; see the controller's own notes for the automation this
was built for.

Nothing here changes what the speaker does or how it sounds. It is one small
message on a transition, and the entities only appear once the controller is
on 2.26.0-fx.1 or newer.

### What is required of you

Nothing beyond the update. The two entities appear on their own once both
halves are new enough.

## 2.16.0-fx.1

The headphone jack works, and the Echo finally knows what time it is.

Everything upstream shipped after v2.14.0, on top of the fork's own
v2.15.0-fx.1.

### What's new

**The external jack was silent, and now it is not.** Its output stage sits at
the FLOOR of its range whenever a plug goes in — on a stock Dot, Amazon's
audio software raises it; nothing of ours ever did. So the jack was not
broken, it was at minimum gain, which is why it read as "the jack does not
work" and survived for months. The same omission from the other side: a Dot
booted with a cable already in played out its internal speaker. Both are
handled now, in both directions and at startup.

**The Echo takes the time from the controller.** These devices boot with a
nonsense clock and no battery, and until now nothing corrected it. Needs
controller 2.24.0-fx.1 or newer, which is the half that sends it.

**The ambient light sensor stops flooding the crash log.** It was writing to
the one channel that survives a reboot, which is the channel you need
readable when something has gone wrong.

**The codec's own audio routes are brought up.** This changes nothing on a
normal Echo and matters enormously if one ever runs without Amazon's
software: the audio HAL had been silently configuring the codec all along,
and nothing in our firmware ever did. Without it both the microphones and
the speaker come up powered down.

### What you need to do

Nothing. The jack fixes take effect on their own; the clock needs the
matching controller.

Spotify Connect and AirPlay are still announced and still unusable — the two
programs they need have never been built. See issue #16.

### Updating

The device keeps its previous binary in the other slot and rolls back to it
on its own after three fast exits, so a bad update costs a reboot rather than
a device. Update one Echo first and listen to it before doing the rest.

## 2.15.0-fx.1

The Echo can now play music that never touched the controller, and the LED
ring is a Home Assistant light.

First firmware release from the FelixTechgiti fork. It contains upstream's
v2.14.0 in full, plus the work below. The `-fx.1` suffix keeps fork tags from
colliding with upstream's; it has no other meaning.

### What's new

**The ring is an HA light.** Every LED except the one under the microphone
button is yours — colour, brightness, and three notification effects (Notify,
Alert, Sweep) selectable as light effects. Voice states still outrank it: the
light decides the RESTING colour, not what you see mid-turn. The mute LED
never changes hands.

**Mute from Home Assistant, one way.** A switch that only closes. Turning it
on mutes the microphone; turning it off does not unmute it — that stays a
physical act at the device, because the red LED under the button is a promise
and nothing over the network should be able to break it while the LED still
makes it.

**Streaming the device does itself** — Sendspin (Music Assistant groups),
Spotify Connect and AirPlay, each off by default under Config → Streaming.
Home Assistant always wins the speaker; a local source is ended rather than
starved when it does.

**The output chain runs on the device**, post-mix, so one limiter finally
sees voice and music summed and a tone change is heard in ~43ms rather than
~4s. Pair this with controller 2.23.0-fx.1 or newer — an older controller
shapes the audio as well, which is two limiters in series and audibly wrong.

### What you need to do

**Spotify and AirPlay need a binary that is not in this release.** The
firmware reports `not_installed` and the dashboard disables both toggles with
the reason, rather than offering a switch that saves and plays nothing. The
build recipes are in `device/librespot/` and `device/shairport/` and have not
been run yet.

**AirPlay is CLASSIC AirPlay**, not AirPlay 2. The dashboard says so.

**Sendspin has never completed a handshake against a live Music Assistant.**
The protocol is implemented and tested against itself; the first real connect
is still owed. It is off by default.

### Updating

Nothing is required of you beyond pressing Update. The device keeps its
previous binary in the other slot and rolls back to it on its own after three
fast exits, so a bad update costs a reboot rather than a device. Update one
Echo first and listen to it before doing the rest.
