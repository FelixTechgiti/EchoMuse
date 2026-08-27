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
