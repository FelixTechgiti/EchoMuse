"""
The LED ring as a Home Assistant light (em_ring_light).

The wire format is the easy half. The awkward half is that
`LightCommandRequest` is a PARTIAL update — every field arrives behind its
own has_* flag — so "turn it on", "make it blue" and "dim it" are three
different messages each carrying a fraction of the state, and each has to
leave the rest of it alone. Getting that wrong is not a crash; it is a
colour wheel that resets the dimmer, which nobody reports as a bug because
it reads as the user's own mistake.
"""

import em_ring_light as R


# ── storage encoding ──────────────────────────────────────────────────────

def test_brightness_zero_is_off_and_the_colour_survives_it():
    """
    On/off lives in the brightness, not in the colour, and this is why.
    HA's light card shows the last colour on a light that is OFF and offers
    it back on the next tap — so a sentinel stored in the colour field
    (the obvious encoding, written first) loses exactly the thing the user
    picked, every time they switch the ring off.
    """
    assert R.painted_rgb("#ff0000", 0) is None
    state = R.ha_state("#ff0000", 0)
    assert state["state"] is False
    assert (state["red"], state["green"], state["blue"]) == (1.0, 0.0, 0.0), \
        "an off ring must still report the colour it will come back at"


def test_the_painted_colour_is_scaled_but_the_reported_one_is_not():
    """
    HA's model carries brightness beside an unscaled colour. Reporting a
    pre-multiplied colour makes the slider fight the wheel — each adjustment
    undoing the other — and makes a dimmed colour drift as rounding
    accumulates over repeated changes.
    """
    assert R.painted_rgb("#00ff00", 128) == (0, 128, 0)
    assert R.ha_state("#00ff00", 128)["green"] == 1.0
    assert R.ha_state("#00ff00", 128)["brightness"] == 128 / 255.0


def test_an_unreadable_stored_colour_resolves_rather_than_raising():
    """
    This value comes out of a database that survives downgrades, and it
    once held the sentinel "off". Brightness alone decides whether the ring
    is lit, so a colour that cannot be read can safely be white.
    """
    assert R.parse_color("off") == (255, 255, 255)
    assert R.parse_color(None) == (255, 255, 255)
    assert R.parse_color("#xyzxyz") == (255, 255, 255)
    assert R.painted_rgb("off", 0) is None


def test_brightness_is_clamped_not_trusted():
    assert R.clamp_brightness(-5) == 0
    assert R.clamp_brightness(999) == 255
    assert R.clamp_brightness("nonsense") == 255
    assert R.clamp_brightness(None) == 255


# ── partial-update semantics ──────────────────────────────────────────────

def test_a_colour_change_leaves_the_brightness_alone():
    assert R.apply_command("#0000ff", 128,
                           has_rgb=True, red=1.0, green=0.0, blue=0.0) \
        == ("#ff0000", 128)


def test_a_brightness_change_leaves_the_colour_alone():
    assert R.apply_command("#0000ff", 255,
                           has_brightness=True, brightness_f=0.5) \
        == ("#0000ff", 128)


def test_turning_off_keeps_the_colour_and_turning_on_restores_it():
    off = R.apply_command("#ff0000", 200, has_state=True, state=False)
    assert off == ("#ff0000", 0)
    assert R.apply_command(*off, has_state=True, state=True) == ("#ff0000", 255)


def test_an_adjustment_to_a_dark_ring_switches_it_on():
    """
    HA's model: adjusting a light is a way of switching it on. The
    alternative is a colour wheel that silently does nothing until the user
    finds the toggle, which reads as a broken entity.
    """
    assert R.apply_command("#ffffff", 0,
                           has_rgb=True, red=0.0, green=0.0, blue=1.0) \
        == ("#0000ff", 255)


def test_an_adjustment_to_a_lit_ring_does_not_shove_the_dimmer_to_full():
    assert R.apply_command("#ffffff", 64,
                           has_rgb=True, red=0.0, green=0.0, blue=1.0) \
        == ("#0000ff", 64)


def test_an_explicit_off_beats_an_adjustment_in_the_same_message():
    assert R.apply_command("#ffffff", 255, has_state=True, state=False,
                           has_brightness=True, brightness_f=1.0) \
        == ("#ffffff", 0)


def test_on_at_zero_brightness_is_off_and_says_so():
    """`state` and the ring have to agree, or the entity reports on over a
    dark ring — the drift the media player's own state rules exist for."""
    colour, level = R.apply_command("#ff0000", 255, has_state=True, state=True,
                                    has_brightness=True, brightness_f=0.0)
    assert level == 0
    assert R.painted_rgb(colour, level) is None
    assert R.ha_state(colour, level)["state"] is False


def test_a_command_carrying_nothing_changes_nothing():
    assert R.apply_command("#abcdef", 77) == ("#abcdef", 77)


def test_round_trip_is_stable_over_repeated_no_op_commands():
    """Rounding through hex and back must not let the colour walk."""
    state = ("#123456", 200)
    for _ in range(20):
        state = R.apply_command(*state)
    assert state == ("#123456", 200)


# ── wiring ────────────────────────────────────────────────────────────────
#
# em_esphome and em_controller are not importable by this suite, so these are
# shape guards on the shipped source.

import re
from pathlib import Path

import em_config_sections
import em_db

CONTROLLER = Path(__file__).resolve().parents[1]


def _strip_py_comments(src: str) -> str:
    src = re.sub(r'"""(?:.|\n)*?"""', "", src)
    src = re.sub(r"'''(?:.|\n)*?'''", "", src)
    return "\n".join(re.sub(r"#.*$", "", line) for line in src.splitlines())


def test_the_light_entity_key_is_unique():
    """
    HA keys its entity registry on these, so a collision silently merges two
    entities and a renumber renames everyone's. Append-only, like the rest.
    """
    src  = (CONTROLLER / "em_esphome.py").read_text()
    keys = dict(re.findall(r"^(\w+_KEY)\s*=\s*(\d+)", src, re.M))
    assert "LIGHT_KEY" in keys, "the ring light needs its own entity key"
    assert len(set(keys.values())) == len(keys), f"entity key collision: {keys}"


def test_the_light_is_advertised_only_when_the_device_has_leds():
    """
    The standing rule for every entity here: one whose commands can never do
    anything is worse than none, because someone writes an automation
    against it and it silently never runs.
    """
    src = _strip_py_comments((CONTROLLER / "em_esphome.py").read_text())
    decl = src.index("ListEntitiesLightResponse(")
    gate = src.rindex("if self._leds_capable:", 0, decl)
    assert decl - gate < 400, \
        "the light must sit inside the leds capability gate"


def test_every_return_to_rest_goes_through_leds_idle():
    """
    The ring is not a lamp: voice states outrank the light, so what the
    entity owns is the ring's RESTING state. Every path that used to mean
    "clear the ring" has to mean "put it back to rest", or the light
    switches itself off whenever a turn happens to end.

    leds_idle's own fallback to leds_off is the one legitimate caller.
    """
    src  = _strip_py_comments((CONTROLLER / "em_controller.py").read_text())
    body = src[src.index("async def leds_idle"):]
    body = body[:body.index("\n\n\nasync def") if "\n\n\nasync def" in body
                else len(body)]
    assert "leds_off(device)" in body, \
        "leds_idle falls back to leds_off when rest is dark"

    outside = src.replace(body, "")
    strays  = [l.strip() for l in outside.splitlines()
               if "await leds_off(device)" in l]
    assert not strays, \
        f"these must return the ring to REST, not switch it off: {strays}"


def test_the_resting_colour_is_device_state_never_fleet_inherited():
    """
    "Every Echo in the house turns the same colour when one of them is told
    to" is not a fleet default, it is a bug — the same reasoning that keeps
    startupVolume out of the sections.
    """
    for key in ("idleRing", "idleRingBrightness"):
        assert key in em_db.DEFAULT_DEVICE_CONFIG, f"{key} needs a default"
        assert key in em_config_sections.STATE_KEYS, \
            f"{key} must be device state, not a fleet-scoped setting"
        assert key not in em_config_sections.keys_for(
            em_config_sections.SECTION_IDS), \
            f"{key} must belong to no section"


def test_the_shipped_default_leaves_the_ring_dark():
    """
    Brightness 0 is off, so the defaults keep existing behaviour exactly —
    a dark ring at rest — while still giving HA's card a colour to offer on
    the first tap.
    """
    assert R.painted_rgb(em_db.DEFAULT_DEVICE_CONFIG["idleRing"],
                         em_db.DEFAULT_DEVICE_CONFIG["idleRingBrightness"]) is None
