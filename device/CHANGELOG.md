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
