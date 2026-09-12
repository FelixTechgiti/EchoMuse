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

## 2.31.0-fx.1

### Spotify Connect and AirPlay are reachable at last — the Echo was firewalling them

**If your Echo has never appeared in the Spotify app or an AirPlay picker,
this is why, and this release fixes it.** Nothing about the announcements was
ever wrong: they went out, the whole network heard them, and every reading we
could take on the device said the endpoints were healthy. What nobody had
checked was whether anything could *connect back*.

FireOS runs a default-deny firewall with an allowlist of Amazon's own ports.
Read off a device on 2026-09-12:

```
-P INPUT DROP
-A INPUT -i wlan0 -p tcp -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -i wlan0 -p udp -m udp --dport 5353 -j ACCEPT      <- mDNS
-A INPUT -i wlan0 -p tcp -m tcp --dport 4070 -j ACCEPT      <- Alexa
-A INPUT -i wlan0 -p udp -m udp --dport 5000 -j ACCEPT      <- UDP, not TCP
-A INPUT -p icmp -m state --state RELATED,ESTABLISHED -j ACCEPT
```

Multicast DNS is allowed, so the Echo advertises itself perfectly. Established
connections are allowed, so the three links to the controller — all of which
the Echo dials *outward* — have always worked faultlessly. But a phone
answering that advertisement is a **new inbound connection**, and it is
dropped. Note the fourth line: Amazon opened UDP 5000 for something of their
own, while AirPlay's control port is **TCP** 5000, so even the port that looks
open is not the one we need.

The firmware now opens exactly the ports its own enabled endpoints need, and
closes them again when you turn an endpoint off:

- **Spotify Connect** — TCP 36000, librespot's discovery listener.
- **AirPlay** — TCP 5000 for the session, UDP 6001–6010 for the audio. The
  UDP range is the half that is easy to miss: with only the control port open,
  a session connects and then plays nothing.
- **Ping** — the Echo answers a ping now. It never did, which is why a healthy
  device on a healthy network reads as "not on the network" to anyone trying
  to diagnose it. An afternoon went into that mistake.

Those port numbers are now **pinned** and passed to librespot and
shairport-sync from the same constants the firewall rule is built from.
librespot previously picked a random discovery port on every start, which no
firewall rule can name.

**What is required of you:** nothing. The rules are applied at startup and
again whenever a setting changes, and they are scoped to `wlan0`, to the INPUT
chain, and to those exact ports. The firmware never changes the firewall
policy and never flushes the table — Amazon's own rules, including the one
that keeps the controller link alive, are left exactly as they are.

**What to expect:** after the update, your Echo should appear in the Spotify
app and in AirPlay pickers within a few seconds of the endpoints being
enabled, and should answer `ping`. If it does not appear, the endpoints
themselves may simply be switched off — check Config → Audio-Endpunkte in the
dashboard.

**On emOS there is no such firewall**, so there is nothing to open; the
firmware says so once in the log and does nothing further.

## 2.30.0-fx.1

### An Echo that cannot verify the controller no longer goes silently dead

**This is the other half of the fault described in controller 2.38.0-fx.1,
and it is the half that prevents a repeat.**

The encrypted link is verified against a name inside the controller's
certificate. When that check fails the Echo has, until now, simply retried —
for ever, at five-second intervals, with no fallback and nothing anybody
could read. There is no way in: the Echo's shell is reached through the
controller it cannot connect to, and a power cycle changes nothing.

It now falls back to the unencrypted link after three consecutive
verification failures, says loudly why in the log the controller collects,
and keeps re-testing the encrypted one about once a minute so a repaired
controller is picked up on its own. The dashboard shows the link as **ws
(plain)** instead of **wss (TLS)**, which is the point: a fault you can see
beats one you cannot.

Two deliberate limits. The link token is **not** sent over the fallback — if
verification failed because something is on the network rather than because
of a stale name, handing it the shared secret would be worse than the
outage — and **Require encrypted device connections**, if you have enabled
it, still refuses the fallback outright. Only a failure to VERIFY counts: a
controller that is merely switched off refuses both links equally and never
moves a device off encryption.

### Settings that survived a reboot stopped surviving an update

The rename moved two files the Echo keeps for itself, and nothing carried
them across, so both were silently lost the moment a device took the new
firmware:

- **the remembered controller address**, which is what lets an Echo
  reconnect in seconds after an update instead of searching the network for
  it — losing it put every updated device straight back on the slow path
  the file exists to avoid;
- **the microphone mute state**, which came back unmuted.

Both are now read from the old location when the new one is empty, and
re-written to the new one, so this happens exactly once per device and then
never again.

### The Echo now records what its radio was doing while it was unreachable

**Nothing here changes behaviour.** It adds one measurement to a log that is
only written when something is already wrong, and it exists because the last
outage could not be explained afterwards.

On 11 September an Echo was unreachable for 22 hours while still holding its
network address. From the controller's side it was invisible — a scan heard
seven other Spotify Connect devices and two other AirPlay devices on the same
network, and not this one. From the Echo's side the controller did not answer
either. So both halves of the network failed at once on an interface that
still looked configured, and the log had nothing that could say why.

The `no controller` lines in the Echo's own log now carry the WiFi state
alongside the address: whether the radio is still associated, which access
point to, and the signal strength. That separates the two explanations — a
connection that is up and carrying nothing, or one that has dropped and is
searching — which need opposite fixes.

Nothing acts on it yet, on purpose: the repair for one of those cases is to
drop and re-make the WiFi connection, and doing that to a device whose only
remote access IS that connection is not something to attempt on a guess.

To read it: **Devices → your Echo → Updates → Fetch supervisor log**, after
the next outage. Requires controller 2.38.0-fx.1 or newer, which reads the
whole file.

## 2.29.0-fx.1

### The Echo finds the controller again in seconds, not half an hour

**After the controller restarts — every add-on update — the Echo could lose
it for over half an hour, while sitting on the network perfectly healthy.**
Measured on a live device on 11 September: four times in one day, browsing for
4m16s, 33m26s, 38m5s and 36m56s, plus one gap of 2h17m. The Echo never
restarted, its Spotify and AirPlay receivers ran throughout, and a ping to the
controller answered in 1.6ms the whole time.

The cause is one probe. The Echo remembers where the controller was and tries
that address first; if it does not answer, it falls back to searching the
network by name — **and then never tried the remembered address again** for as
long as the search took. The one moment that first probe is certain to fail is
a controller restart, which is also the case that fixes itself seconds later.

The remembered address is now re-tested before every search round. An update to
the controller add-on should now cost the Echo a few seconds rather than the
rest of the evening.

**This does not fix why the search itself can go unanswered for tens of
minutes** — that is still open. It stops that being the only way back.

### What is required of you

Nothing. If your Echo has been dropping off after add-on updates and coming
back only when you pull the plug, this is the update for it.

## 2.28.0-fx.1

The AirPlay volume slider moves the Echo's volume — which it could not do
before, on any device — and the Echo stops telling the network its name is
`localhost`.

### The Echo has a name of its own

It booted reporting `localhost`. Both streaming endpoints publish their
service with a record saying "reach me at `<hostname>`", so what went out was
`localhost` — and any device that looked the name up got its own `127.0.0.1`
back, connected to itself, and never listed the Echo at all.

It is now named after its serial (`revoice-<serial>`), set before either
endpoint starts. Nothing else on the device used the old name.

**This is a fix, not the fix**, for an Echo missing from AirPlay or Spotify
Connect. Running both endpoints at once is a second, separate problem — each
brings its own discovery service and two on one device interfere — tracked in
issue #77. Until that is resolved, one at a time is reliable.

### AirPlay volume control works at all

The setting existed, the dashboard saved it, the controller pushed it and the
Echo stored it. Nothing ever read it back: the internal accessor every
consumer goes through was a hand-written copy that did not carry this one
field, so the code deciding whether to ask shairport-sync for volume messages
always saw "unset" and never asked. The feature has been inert since the day
it shipped, with every panel reporting it as on.

Turning it on now does what it says: the slider in the iPhone's AirPlay
control changes the Echo's own volume, so spoken answers get louder and
quieter with it, and the ring shows the level like any other volume change.
Off by default, and unchanged when off.

**Requires the `endpoints-v1.1.0` shairport-sync**, which the controller
installs by itself when the setting is on.

A test now fails if any future setting is stored and then dropped the same
way — the failure had no symptom other than the feature quietly doing
nothing.

## 2.27.0-fx.1

A replaced streaming binary is actually used, and music played from Music
Assistant lands on time.

### A replaced streaming binary is actually used

Installing a new librespot or shairport-sync over one that was already running
left the Echo running the old program — a replaced file does not change what a
running process is executing. The Echo can now be asked to restart just that
endpoint, so the binary you installed is the one running.

It refuses while somebody is listening to it, and says so rather than cutting
the music off.

### Sendspin audio no longer settles a beat behind

Music Assistant tells each speaker the exact instant to play every chunk, and
the Echo aims for it by measuring how much audio is still ahead in its own
pipeline. It was only counting half of it — the part the sound hardware knows
about, not the buffer in front of that — so it aimed at the wrong moment,
corrected towards the wrong moment, and stayed there.

On its own that is latency you would probably not name. In a group with any
other speaker it is an echo, and nothing on the Echo reported anything wrong,
because every number it had agreed with itself.

### What is required of you

Nothing. If you use Music Assistant groups, this is the update to take.

## 2.26.0-fx.1

The AirPlay slider can move the Echo's own volume.

### What's new

Turning the volume down in the AirPlay control on a phone did nothing to the
Echo. The receiver was quietly turning the audio down inside itself, so the
Echo's volume, its ring and Home Assistant all stayed exactly where they were
while the phone believed it was in charge — and the quietening threw away
resolution the speaker's own volume control would have kept.

There is now a setting for it, under Streaming: **AirPlay volume moves this
Echo**. With it on, the slider sets the Echo's volume, flashes the cyan volume
ring the way a button press does, and is remembered like any other volume
change. Muting on the phone reaches actual silence rather than "very quiet".

**It is off by default, and the reason is worth reading before you turn it
on.** An Echo has one volume, shared with the assistant. Turn AirPlay down to
20% and the assistant's next spoken answer is at 20% too. That is arguably
what "set the device volume" means, and it is what was asked for — but it
should be your decision rather than a surprise, so it is a switch.

Takes effect when AirPlay next starts, so toggle AirPlay off and on after
changing it.

### What is required of you

Nothing, unless you want it. This also needs the rebuilt shairport-sync from
the endpoints release — the previous build could not report volume at all.
Install it from the device's Updates tab, or let the automatic fetch do it.

## 2.25.0-fx.1

The Echo remembers where its controller is, and says whether Spotify and
AirPlay are actually running.

### What's new

**This is the fix for "the Echo disappears after an update and only comes back
when I unplug it."**

When the Echo restarts — which every firmware update does — it had no memory
of the controller it had been talking to seconds earlier, and had to find it
again by broadcasting on the network. After a restart that broadcast search
often finds nothing, for minutes, while the Echo sits there with a perfectly
good network connection. Pulling the plug fixed it, because a full reboot
repairs whatever the restart broke.

Measured on a device, the first boot of the previous release, from the log
that now survives a power cut:

```
16:34:39  v2.24.0-fx.1 starting
16:35:58  no controller for 1m15s — 4 browse rounds, wlan0=192.168.178.140
16:40:18  no controller for 5m35s — 8 browse rounds, wlan0=192.168.178.140
16:45:00  (reboot) — connected within seconds
```

The firmware started correctly. The network was fine and the Echo's own
address is right there. Only the *finding* was broken, and the same
restart-then-reboot pair appears six times in that one day.

So the Echo now writes down the controller's address whenever it registers,
on storage that survives both a reboot and an update, and tries that address
first when it starts. A restart reconnects in seconds without broadcasting at
all.

If the controller has genuinely moved, the remembered address simply does not
answer within three seconds and the Echo searches for it exactly as before —
so this can cost three seconds and never a wrong answer. The address is
written only when it changes, because that storage cannot be replaced.

The log also now says **which** of the two failed: a remembered address that
does not answer means the network, and no answer to a broadcast while the
address does answer means the broadcast. Those want opposite fixes and
previously read the same.

### Spotify and AirPlay now say whether they are running

The dashboard could tell you librespot and shairport-sync were **installed**.
It could not tell you whether they were running, and those are not the same
thing — an Echo can have the right file, of the right size, marked executable,
and still appear in no AirPlay list at all. That happened for two hours on a
real device: a leftover copy from before an update was still holding the
network port, so every new attempt gave up immediately, and every screen said
it was fine.

The Echo now reports, every thirty seconds, whether each one is actually
running, how long it has been up, and — if it is not — how many times it has
tried to start and why the last attempt ended. The streaming settings show it
directly: *"shairport-sync: running — up 2h"*, or *"shairport-sync: NOT
running — 118 start attempts — last exit: exit status 1"*.

An Echo on older firmware, and an Echo that has only just connected, both say
nothing rather than claiming something is down. Being wrong in that direction
is how a warning becomes one people learn to scroll past.

### What is required of you

Nothing. The remembering starts one update after this one — this release is
the one that begins writing the address down.

## 2.24.0-fx.1

The Echo now keeps a record that a power cycle cannot erase.

### What's new

**When an Echo cannot find the controller, there is no way to ask it why.**
The device's own log lives in memory, so pulling the plug — the only thing
left to try — erases it. Its shell runs through the controller, so with the
controller missing there is no shell either. Four restarts on one day ended
that way, each after 8 to 30 minutes of an orange pulsing ring, and each took
its explanation with it.

The firmware now writes the few things worth keeping to the same persistent
file the start-up script already uses, on storage that survives a power cut
and an update. Four kinds of line: which firmware version actually started,
a controller that cannot be found (with the device's own IP address, which is
what separates "this Echo is off the network" from "this Echo is on the
network and the controller is not answering"), a controller that is found and
never accepts the connection, and a speaker Android will not hand over.

A working device writes **one line per start** and nothing else. Fault lines
are spaced out deliberately — after one minute, then five, fifteen, thirty,
and half-hourly after that — because the storage they go to cannot be
replaced and these faults can last all night. Anything shorter would wear the
flash of every device that ever restarts, and every ordinary restart is
finished inside that first minute.

Each fault also writes one line when it clears, so the file says how long it
lasted rather than only that it happened.

### What is required of you

Nothing. If your Echo goes quiet and does not come back, the file is already
waiting — the controller collects it by itself after a failed update, and it
is in a support bundle. Pull the plug as you always would; the record is what
survives it.

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
