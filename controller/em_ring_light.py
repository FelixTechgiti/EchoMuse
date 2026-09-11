"""
The LED ring as a Home Assistant light, as pure functions.

The ring is not a lamp. It is the device's primary way of saying what it is
doing — listening, thinking, speaking, muted, an error cue — and every one of
those outranks anything Home Assistant asks for. So what the light entity
owns is the ring's RESTING state: the colour it returns to when no voice
state is claiming it. `em_controller.leds_idle` is the one place that paints
it, and every path that used to mean "clear the ring" now means "return it to
rest".

Split out of em_esphome for the reason em_runbarrier and em_linkauth were:
the test suite cannot import em_esphome, and the awkward half of this feature
is not the wire format but the PARTIAL-UPDATE semantics of
`LightCommandRequest`. Every field arrives behind its own `has_*` flag, so
"turn it on" and "make it blue" and "dim it" are three different messages
that each carry a fraction of the state and must not clobber the rest of it.

Stored as two values on the device row (STATE_KEYS, so they survive a restart
and are never fleet-inherited):

  idleRing            "#RRGGBB", the colour at FULL brightness — ALWAYS a
                      colour, never a sentinel
  idleRingBrightness  0-255, applied when painted; **0 is off**

On/off living in the brightness rather than in the colour is the part worth
explaining, because the obvious encoding — storing "off" in `idleRing` — was
written first and is wrong. Home Assistant's light card shows the last colour
on a light that is OFF, and offers it back on the next tap; a sentinel in the
colour field discards exactly that, so the card would go white every time the
ring was switched off and the user would lose the colour they picked. Keeping
brightness out of the stored colour also lets a dimmed ring be brightened
again without the colour drifting as rounding accumulates.

The cost is that the pre-off brightness is not remembered — an off/on cycle
returns to full. That is what HA's own UI does for a light reporting no
brightness, and it costs one slider drag where the alternative cost a colour.
"""

from __future__ import annotations

from typing import NamedTuple

# What the ring rests at when nothing has ever set a colour. White rather
# than a hue nobody chose: the ring's own vocabulary is green (listening),
# red (mute), orange (link down) and cyan (volume), so any coloured default
# would read as the device reporting a state.
DEFAULT_COLOR = "#ffffff"


def parse_color(value) -> tuple[int, int, int]:
    """
    "#RRGGBB" -> (r, g, b), falling back to DEFAULT_COLOR.

    Never raises and never returns None. This value comes back out of a
    database that survives downgrades and once held the sentinel "off", so
    it has to tolerate anything; and since brightness alone decides whether
    the ring is lit, an unreadable colour can safely resolve to white rather
    than having to mean something.
    """
    text = value.strip().lstrip("#") if isinstance(value, str) else ""
    if len(text) == 6:
        try:
            return (int(text[0:2], 16), int(text[2:4], 16), int(text[4:6], 16))
        except ValueError:
            pass
    return (255, 255, 255)


def clamp_brightness(value) -> int:
    """0-255. Anything not a number reads as full."""
    try:
        return max(0, min(255, int(round(float(value)))))
    except (TypeError, ValueError):
        return 255


def painted_rgb(idle_ring, brightness) -> tuple[int, int, int] | None:
    """
    The (r, g, b) to actually send, or None when the ring rests dark.

    Brightness 0 is OFF rather than black-that-is-on: the ring has no way to
    express the difference, and an all-zero frame is the same wire message
    as clearing it.
    """
    scale = clamp_brightness(brightness) / 255.0
    if scale <= 0:
        return None
    return tuple(
        max(0, min(255, int(round(c * scale)))) for c in parse_color(idle_ring)
    )


def ha_state(idle_ring, brightness) -> dict:
    """
    The LightStateResponse field values for the stored state.

    Colour is reported UNSCALED, with brightness carried separately, because
    that is what HA's own light model expects — reporting a pre-multiplied
    colour makes the brightness slider fight the colour wheel, each undoing
    the other on every adjustment. It is reported while OFF too, which is
    what puts the last colour back on HA's light card.
    """
    r, g, b = parse_color(idle_ring)
    level   = clamp_brightness(brightness)
    return {
        "state":      level > 0,
        "brightness": level / 255.0,
        "red":        r / 255.0,
        "green":      g / 255.0,
        "blue":       b / 255.0,
    }


def apply_command(idle_ring, brightness, *,
                  has_state=False, state=False,
                  has_brightness=False, brightness_f=0.0,
                  has_rgb=False, red=0.0, green=0.0, blue=0.0):
    """
    Fold one LightCommandRequest into the stored state.

    Returns (idleRing, idleRingBrightness).

    The partial-update rules, each of which is a real Home Assistant
    interaction rather than a hypothetical:

      - A colour change carries no brightness and a brightness change
        carries no colour. Each must leave the other alone, or the colour
        wheel would reset the dimmer and the dimmer would reset the colour.
      - "Turn it on" carries neither. It restores the stored colour at FULL
        brightness — the stored colour survives being switched off precisely
        so this can happen.
      - "Turn it off" keeps the colour and zeroes the brightness, so HA's
        card still shows what the ring will be when it comes back.
      - A colour or brightness adjustment with no `has_state` turns the ring
        ON. That is HA's model — adjusting a light is a way of switching it
        on — and the alternative is a colour wheel that silently does
        nothing until the user finds the toggle.
      - An explicit off wins over an adjustment riding the same message.
    """
    color = "#{:02x}{:02x}{:02x}".format(*parse_color(idle_ring))
    level = clamp_brightness(brightness)

    if has_rgb:
        color = "#{:02x}{:02x}{:02x}".format(
            *(max(0, min(255, int(round(c * 255.0)))) for c in (red, green, blue))
        )

    if has_state and not state:
        return color, 0

    if has_brightness:
        return color, clamp_brightness(brightness_f * 255.0)

    if (has_state and state) or has_rgb:
        # On, or adjusted-and-therefore-on. Full brightness only when it was
        # resting dark: an adjustment to an already-lit ring must not shove
        # the dimmer back to maximum.
        return color, (level if level > 0 else 255)

    return color, level


# ── Light EFFECTS: one list, two lifetimes ────────────────────────────────
#
# Home Assistant gives a light exactly ONE effect field, and this ring wants
# two different things from it:
#
#   a NOTIFICATION — plays, ends, and the ring goes back to whatever it was
#   resting at. Three throbs and done.
#
#   a RESTING ANIMATION — the ring runs this until somebody changes it. The
#   rotating dot, the rainbow, the slow breathe.
#
# Those are opposite lifetimes sharing one field, and the difference has to be
# carried per effect rather than inferred, because each one gets the REPORTING
# rule the other must not have. A persistent effect must report as active or
# HA's card forgets what is running; a one-shot must report `None`, because
# reporting an effect for something already finished leaves the card showing a
# state the ring is not in. `persistent` is that flag, and it is the only
# thing separating the two.
#
# They share one list rather than splitting the resting animation onto a
# `select` entity, which was the alternative. It is cleaner in the data model
# and worse everywhere a person stands: nobody looks in a select for a light
# effect.
#
# The specs are the device's own `led_anim` vocabulary (em_scenes uses the
# same patterns for turn outcomes) and every pattern used here — solid, spin,
# rotate, pulse — is one FIELDED firmware already renders. Nothing new is
# announced, and nothing needs a capability, which matters because
# `StartAnim`'s default branch CLEARS THE RING on a pattern it does not know:
# an effect name shipped ahead of the firmware would turn the ring off rather
# than do nothing.

EFFECT_NONE = "None"

NUM_LEDS = 12


def _hsv(h: float, s: float, v: float) -> tuple:
    """h in degrees; returns 8-bit RGB. Same helper em_scenes uses."""
    import colorsys
    r, g, b = colorsys.hsv_to_rgb((h % 360) / 360.0, s, v)
    return (int(r * 255), int(g * 255), int(b * 255))


# One hue per LED around the wheel. Value capped below full for _RAINBOW's
# reason in em_scenes: white-ish hues otherwise swamp the saturated ones.
_RAINBOW = [_hsv(i * 360 / NUM_LEDS, 1.0, 0.75) for i in range(NUM_LEDS)]


class Effect(NamedTuple):
    """
    One entry in the effect list.

    `palette` is a function of the resting colour rather than a fixed list,
    because the effects divide into two kinds and both have to work: the ones
    that ARE a colour scheme (Rainbow) ignore it, and the ones that are a
    motion (Rotate, Breathe) have to run in the colour the user picked, or
    selecting an effect would silently discard their choice.

    `seconds` is 0 for a persistent effect, and that is not a placeholder: it
    becomes `ttlSec: 0`, which fielded firmware already reads as "run until
    replaced" — every deadline in `animator.go` is behind `spec.TTLSec > 0`.
    So a resting animation needs no device change and no repaint timer, which
    would otherwise be a message to every device every few seconds for ever.
    """
    pattern: str
    period_ms: int
    seconds: int          # TTL and how long the controller waits; 0 = forever
    persistent: bool
    palette: object       # fn(rgb tuple) -> list of rgb tuples


def _one(rgb):
    return [list(rgb)]


def _ring(rgb):
    return [list(rgb)] * NUM_LEDS


def _rainbow(_rgb):
    return [list(c) for c in _RAINBOW]


def _gradient(rgb):
    """
    A static two-tone sweep: the resting colour fading to a dim version of
    itself and back. One colour rather than two, because a second colour
    would need somewhere to live and the light entity has one colour field.
    """
    r, g, b = rgb
    out = []
    for i in range(NUM_LEDS):
        # Triangle wave over the ring so the two ends meet without a seam.
        t = 1.0 - abs((i / NUM_LEDS) * 2.0 - 1.0)
        f = 0.15 + 0.85 * t
        out.append([int(r * f), int(g * f), int(b * f)])
    return out


def _wheel(_rgb):
    """A single hue walking the wheel — the whole ring in step."""
    return [list(_hsv(i * 360 / NUM_LEDS, 1.0, 0.75)) for i in range(NUM_LEDS)]


_EFFECTS: dict[str, Effect] = {
    # ── One-shots: a notification, three seconds, then back to rest ───────
    #
    # Three unhurried throbs — "something wants you".
    "Notify":  Effect("pulse",  700, 3, False, _one),
    # Agitated — "something is wrong". Deliberately the same rhythm as the
    # turn-outcome error cue, so the two read as one vocabulary.
    "Alert":   Effect("pulse",  220, 3, False, _one),
    # A sweep, for a notification that should catch the eye without the
    # urgency a fast blink carries.
    "Sweep":   Effect("rotate",  80, 3, False, _one),

    # ── Persistent: this IS the resting state until changed ───────────────
    #
    # A bright head with a fading trail, in the resting colour.
    "Rotate":  Effect("spin",   110, 0, True,  _one),
    # Longer trail, faster head.
    "Comet":   Effect("spin",    55, 0, True,  _one),
    # The rainbow palette turning — what the `pride` scene already does.
    "Rainbow": Effect("rotate", 120, 0, True,  _rainbow),
    # One hue walking the wheel, whole ring in step.
    "Colour cycle": Effect("rotate", 200, 0, True, _wheel),
    # A slow, even on/off.
    "Blink":   Effect("pulse",  900, 0, True,  _ring),
    # The calm version of Blink.
    "Breathe": Effect("pulse", 2600, 0, True,  _ring),
    # A static two-tone sweep — no motion, but still an effect rather than a
    # colour, because it is twelve different LEDs.
    "Gradient": Effect("solid",  0,  0, True,  _gradient),
}

EFFECTS: tuple[str, ...] = (EFFECT_NONE, *_EFFECTS)

# The persistent ones, for anything that needs to ask without walking the
# table — and for the test that pins that every one of them is in EFFECTS.
PERSISTENT: tuple[str, ...] = tuple(
    name for name, e in _EFFECTS.items() if e.persistent)


def is_persistent(name) -> bool:
    """
    Does this effect become the resting state?

    An unknown name is NOT persistent. The effect list is API surface HA
    caches, so a name from a newer controller can arrive here after a
    downgrade, and treating it as persistent would store a resting state
    nothing can render.
    """
    e = _EFFECTS.get(name)
    return bool(e and e.persistent)


def effect_anim(name: str, idle_ring) -> dict | None:
    """
    The `led_anim` spec for an effect, or None for "None" and for anything
    unknown — an effect list is API surface HA caches, so a stale name from
    an older controller must read as "nothing to play" rather than raise.

    A ONE-SHOT is played at the stored colour but at FULL brightness, and the
    second half of that is deliberate: a notification is meant to be noticed,
    and a ring resting at 10% would deliver one nobody sees. It is also what
    lets a notification work on a ring that is switched OFF — the colour is
    stored independently of the brightness precisely so there is always one
    to use.

    A PERSISTENT effect is the resting state, so it is painted at the resting
    BRIGHTNESS by `resting_anim` below. This function is still the one place
    the spec is built, so the two cannot drift.
    """
    e = _EFFECTS.get(name)
    if e is None:
        return None
    return {
        "pattern":  e.pattern,
        "colors":   e.palette(parse_color(idle_ring)),
        "periodMs": e.period_ms,
        # 0 for a persistent effect: fielded firmware guards every expiry
        # behind `ttlSec > 0`, so 0 already means "until replaced".
        "ttlSec":   e.seconds,
    }


def effect_seconds(name: str) -> float:
    """How long an effect runs before the ring returns to rest. 0 if unknown,
    and 0 for a persistent effect — which never returns to anything."""
    e = _EFFECTS.get(name)
    return float(e.seconds) if e else 0.0


def resting_anim(effect, idle_ring, brightness) -> dict | None:
    """
    What `leds_idle` should paint, when the rest is an ANIMATION.

    None means "there is no persistent effect" and the caller paints the solid
    colour as it always did — so every existing path is untouched until
    somebody selects one.

    **Scaled by the resting brightness, and off at 0.** A resting animation is
    the resting state, so the dimmer has to govern it exactly as it governs
    the solid colour; an effect that ignored the dimmer would be the one thing
    on the ring a user could not turn down. At brightness 0 this returns None
    and the ring goes dark, because "off" has to mean off — an effect still
    running on a light reporting itself off is the control-that-lies failure
    this project names most often.
    """
    if not is_persistent(effect):
        return None
    level = clamp_brightness(brightness)
    if level <= 0:
        return None
    spec = effect_anim(effect, idle_ring)
    if spec is None:
        return None
    f = level / 255.0
    spec["colors"] = [[int(c * f) for c in triple] for triple in spec["colors"]]
    return spec


def reported_effect(effect) -> str:
    """
    What the light's state message should say is running.

    A persistent effect reports itself, or HA's card forgets what it is
    showing the moment anything else pushes a state. A one-shot reports
    `None` even while it is playing: it is already with the device, it
    self-clears, and reporting it leaves HA showing an effect selected long
    after the ring went quiet.
    """
    return effect if is_persistent(effect) else EFFECT_NONE
