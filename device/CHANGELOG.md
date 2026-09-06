# Device firmware changelog

Release notes for the firmware binary, one section per version. The
heading is the version WITHOUT the `v` prefix, because that is what
`.github/workflows/cut-release.yml` matches when it builds the tag
annotation from this file — `## 2.15.0-fx.1` for the tag `v2.15.0-fx.1`.

Newest first. Written for the person deciding whether to push this to a
device they rely on, so it says what changed, what to expect, and what is
required of them.

## 2.15.0-fx.1

The Echo can now play music that never touched the controller, and the LED
ring is a Home Assistant light.

First firmware release from the FelixTechgiti fork. It contains upstream's
v2.14.0 in full, plus the work below. The `-fx.1` suffix keeps fork tags from
colliding with upstream's; it has no other meaning.

## What's new

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

## What you need to do

**Spotify and AirPlay need a binary that is not in this release.** The
firmware reports `not_installed` and the dashboard disables both toggles with
the reason, rather than offering a switch that saves and plays nothing. The
build recipes are in `device/librespot/` and `device/shairport/` and have not
been run yet.

**AirPlay is CLASSIC AirPlay**, not AirPlay 2. The dashboard says so.

**Sendspin has never completed a handshake against a live Music Assistant.**
The protocol is implemented and tested against itself; the first real connect
is still owed. It is off by default.

## Updating

Nothing is required of you beyond pressing Update. The device keeps its
previous binary in the other slot and rolls back to it on its own after three
fast exits, so a bad update costs a reboot rather than a device. Update one
Echo first and listen to it before doing the rest.
