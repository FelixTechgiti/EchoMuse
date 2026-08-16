# Changelog

## 2.20.0-ea.5 — Early Access

- **Volume above about three-quarters was distorting, and no longer is.**
  EchoMuse was driving the codec's digital volume past the point where it
  can only clip — measured at 65% distortion three button presses from the
  midpoint, and 89% at the top. Stock Alexa never touches that control,
  which is why it sounded worse than stock at high volume. Thanks to
  @kdkavanagh, who measured it.

  Two things you will notice. **The volume percentage shown in Home
  Assistant will jump** — a device sitting at the same physical level now
  reads higher, because the scale no longer includes a range that only
  distorted. Nothing got louder or quieter. And **the top of the range is
  quieter than it was**, because what used to be above it was a square
  wave. Everything below is cleaner.

  The physical buttons now step 4dB at a time across the audible range,
  instead of spending presses in territory indistinguishable from silence,
  and the volume ring spans that range so a press always moves it.
- **Greyed-out switches no longer do the opposite of what they show.** A
  disabled toggle could still be clicked, and stored the inverted value.
  Thanks again to @kdkavanagh.

## 2.20.0-ea.4 — Early Access

- **Entity names no longer repeat the device name.** Home Assistant already
  puts the device name in front of every entity, and we were adding it again
  — so a sensor read "Kitchen Voice Assistant Kitchen Ambient Light". Only
  the displayed name changes: entity IDs stay as they are, so automations
  keep working. If you renamed an entity yourself, your name is kept.
- **The ring stays lit long enough to see.** Adding a device asks you to say
  the wake word; since 2.20.0-ea.3 released the microphone immediately, the
  ring lit and cleared too fast to register and looked like a glitch. It now
  holds for a moment to show it heard you.

## 2.20.0-ea.3 — Early Access

- **The device stops listening as soon as Home Assistant is done with a
  turn.** Adding a device asks you to say the wake word twice; 2.20.0-ea.2
  fixed the first step, and the second then arrived while the device was
  still listening for the first, clearing only after a timeout. Home
  Assistant was ready again 18 milliseconds after ending the first — the
  wait was entirely ours.

## 2.20.0-ea.2 — Early Access

- **Adding a device in Home Assistant now completes.** The voice satellite
  setup dialog asks you to say the wake word, and never moved on — the ring
  lit, the device answered, and Skip was the only way past. Home Assistant
  listens for the *name* of the wake word that fired and the controller never
  sent one; it does now.
- **Your wake word may need selecting once more.** The name we report loses
  its version suffix: "hey jarvis v0.1" becomes "hey jarvis". The "_v0.1" is
  an openWakeWord filename convention that was never meant to be read by a
  person. Home Assistant restores a wake word choice by name, so after this
  update a device may show none selected — set it again under Settings →
  Devices & Services.
- **Debug logging is now an add-on option.** The controller could always do
  this, but only if you ran it as a container and knew the environment
  variable. Leave it off unless you are chasing something.
- Home Assistant's wake word dropdown was writing choices the controller
  discarded without saying so. It now says what it does with them.
- A turn Home Assistant ends before it starts listening was recorded as
  "no speech", blaming the speaker for something at the other end. It is now
  recorded as what it was.

## 2.20.0-ea.1 — Early Access

- **Leaving "Server IP" empty now detects this host's LAN address.** The old
  fallback was a hardcoded address, so a fresh install with the field blank
  advertised it to every device over mDNS: the controller ran perfectly and
  no device could reach it, with nothing reporting an error anywhere. If you
  set the field by hand to work around that, you can clear it.
- **No separate dashboard login under Home Assistant.** HA has already
  authenticated you, so the panel signs you in as your HA user and the
  first-run setup token is gone. The first person through becomes admin;
  everyone after is read-only until promoted. There is no Sign out on an
  HA session — sign out of Home Assistant instead.
- **Recordings and transcripts are admin-only.** Saved utterances are
  recognisable speech from inside your home, and every household HA user can
  reach the panel, so read-only accounts no longer see the audio player or
  the transcript text. Enforced on the server, not hidden in the page.
- Roles can be changed via `PATCH /api/users/{id}`; the last admin cannot be
  demoted.
- The provisioning wizard's WebUSB error now names the exact origin the
  browser needs allowed, instead of suggesting an address the add-on refuses.
- Early Access is a separate add-on with its own storage. Switching channels
  is a migration, not a toggle — see this add-on's documentation.

## 2.19.0

- **EchoMuse can be installed as a Home Assistant add-on** rather than run
  with docker compose by hand. The dashboard appears as a sidebar panel
  through ingress, so it is reachable wherever Home Assistant is without
  exposing another port. Thanks to @natecj for building it, and @Pinball3D
  whose earlier attempt worked out the approach.
- Existing docker compose installs are unaffected.
- **Six shipped defaults corrected — new installs only.** Wake threshold
  0.3 → 0.5, beamforming, echo cancellation and barge-in on by default,
  barge-in threshold 0.10 → 0.05. Stored configuration always wins over a
  shipped default, so an existing controller keeps every value it has.

## 1.0.1

- Initial Home Assistant Supervisor add-on packaging: install and run the
  controller from Settings → Add-ons instead of hand-run docker-compose.
- Ingress support for the dashboard (no separate port to expose).
- Add-on config UI labels, icon, and logo.
