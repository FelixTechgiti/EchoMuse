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

import pathlib

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


# ── notifications, as light effects ───────────────────────────────────────

def test_none_and_unknown_effects_play_nothing():
    """
    The effect list is API surface HA caches, so a name from an older or
    newer controller can arrive at any time. It has to read as "nothing to
    play" rather than raise inside a message handler.
    """
    assert R.effect_anim(R.EFFECT_NONE, "#ff0000") is None
    assert R.effect_anim("Disco Inferno", "#ff0000") is None
    assert R.effect_seconds("Disco Inferno") == 0.0
    assert R.EFFECTS[0] == R.EFFECT_NONE, \
        "HA expects the no-effect entry first"


def test_every_one_shot_carries_a_ttl_the_device_can_clear_itself_on():
    """
    The TTL is a dead-man switch: a controller that dies mid-notification
    must leave a ring that clears itself, not one stuck pulsing. 1s is the
    floor the firmware supports (see em_scenes' ack_anim).

    This asks it of ONE-SHOTS only. It used to ask it of every effect, which
    was right while every effect was a notification — see the test below for
    what changed and why a persistent effect is still covered.
    """
    for name in R.EFFECTS[1:]:
        if R.is_persistent(name):
            continue
        anim = R.effect_anim(name, "#00ff00")
        assert anim is not None, f"{name} is advertised but plays nothing"
        assert anim["ttlSec"] >= 1, f"{name} has no dead-man TTL"
        assert anim["ttlSec"] == R.effect_seconds(name), (
            f"{name}: the controller waits {R.effect_seconds(name)}s before "
            f"repainting but the device clears at {anim['ttlSec']}s — the "
            f"ring would be dark in the gap"
        )


def test_a_persistent_effect_runs_until_replaced_and_is_still_dead_manned():
    """
    A resting animation must NOT expire, and the dead-man it gives up is
    covered by something better.

    `ttlSec: 0` is what fielded firmware already reads as "run until
    replaced" — every deadline in `animator.go` sits behind `spec.TTLSec > 0`
    — so this needs no device change and no repaint timer, which would
    otherwise be a message to every device every few seconds for ever.

    What replaces the TTL as the dead-man is the device's own link handling:
    `OnDisconnected` starts `pulseOrange`, which takes the ring. So a
    controller that dies does not leave a ring stuck running an effect — it
    leaves one pulsing orange, which is both self-clearing and more
    informative than dark. That is asserted against the firmware source
    rather than described, because the whole argument for `ttlSec: 0` rests
    on it.
    """
    for name in R.PERSISTENT:
        anim = R.effect_anim(name, "#00ff00")
        assert anim is not None, f"{name} is advertised but plays nothing"
        assert anim["ttlSec"] == 0, (
            f"{name} is persistent but carries a TTL — the ring would go "
            f"dark mid-effect and nothing would bring it back"
        )
        assert R.effect_seconds(name) == 0.0, (
            f"{name}: a persistent effect never returns to anything, so the "
            f"controller must not wait to repaint over it"
        )

    src = (pathlib.Path(__file__).resolve().parents[2]
           / "device" / "cmd" / "server.go").read_text()
    marker = "controlClient.OnDisconnected(func() {"
    assert marker in src, (
        "the firmware no longer has a disconnect handler — a persistent "
        "effect's ttlSec:0 has no dead-man behind it any more"
    )
    body = src[src.index(marker):]
    body = body[:body.index("\n\t})")]
    assert "s.StopAnim()" in body, (
        "the disconnect handler no longer stops the running animation, so a "
        "persistent effect would outlive the controller that set it"
    )
    assert "pulseOrange" in body, (
        "the disconnect handler no longer takes the ring, so a dead "
        "controller would leave it dark rather than saying why"
    )


def test_a_notification_uses_the_stored_colour_at_full_brightness():
    """
    A notification is meant to be noticed. A ring resting at 10% would
    deliver one nobody sees, and one that is switched OFF would deliver
    nothing at all — which is why the colour is stored independently of the
    brightness in the first place.
    """
    assert R.effect_anim("Notify", "#ff8000")["colors"] == [[255, 128, 0]]
    # Brightness is not an input here at all: there is nothing to pass.
    assert R.painted_rgb("#ff8000", 0) is None, \
        "the ring is off, and the notification still plays at full colour"


def test_effects_are_advertised_only_where_the_device_can_render_them():
    """
    These are led_anim specs the DEVICE renders on its own ticker. Offering
    the dropdown to firmware that cannot run one puts a control in front of
    someone whose selection silently does nothing.
    """
    # Anchored on `effects=` and read forward, not on a bracket match: the
    # first ")" after it closes `list(em_ring_light.EFFECTS)`, not the
    # entity declaration, so bracket-counting here found the wrong end and
    # failed as BROKEN rather than as this invariant being violated. The
    # invariant has no opinion about how the list is spelled.
    src   = _strip_py_comments((CONTROLLER / "em_esphome.py").read_text())
    decl  = src.index("ListEntitiesLightResponse(")
    field = src.index("effects=", decl)
    assert 'self._device_has("led_anim")' in src[field:field + 250], \
        "the effect list must be gated on led_anim"


def test_an_effect_does_not_change_the_resting_ring():
    """
    HA sends `light.turn_on` with `effect:` — state=on rides in the same
    message. Folding that in the ordinary way would mean every notification
    also switched the resting ring on permanently, so an automation that
    blinks the ring at sunset would leave it lit all night.
    """
    src = _strip_py_comments((CONTROLLER / "em_esphome.py").read_text())
    handler = src[src.index("LightCommandRequest)"):]
    handler = handler[:handler.index("VoiceAssistantAnnounceRequest")]
    effect_at = handler.index("effect_anim(")
    fold_at   = handler.index("apply_command(")
    assert effect_at < fold_at, \
        "the effect branch must be decided before the state is folded"
    between = handler[effect_at:fold_at]
    assert "return" in between, \
        "an effect must RETURN, not fall through into the state folding"


def test_the_light_state_asks_which_kind_of_effect_it_is():
    """
    The two lifetimes need OPPOSITE answers here, so the state message must
    not hardcode either one.

    A one-shot has already been handed to the device and self-clears on its
    TTL, so reporting it leaves HA showing an effect selected long after the
    ring went quiet — a state someone then has to clear by hand. A persistent
    effect IS the resting state, so reporting `None` for it means HA's card
    forgets what is running the moment anything else pushes a state.

    `reported_effect` is the one place that decides; this pins that the state
    message goes through it rather than answering for itself.
    """
    src = _strip_py_comments((CONTROLLER / "em_esphome.py").read_text())
    body = src[src.index("def _light_state_msg"):]
    body = body[:body.index("def ", body.index("LightStateResponse"))]
    assert "em_ring_light.reported_effect" in body, \
        "the state message must ask reported_effect, not decide for itself"
    assert "effect=em_ring_light.EFFECT_NONE" not in body, \
        "a hardcoded None would forget every persistent effect"


def test_reported_effect_gives_the_two_lifetimes_opposite_answers():
    for name in R.PERSISTENT:
        assert R.reported_effect(name) == name, \
            f"{name} is the resting state and must report itself"
    for name in R.EFFECTS[1:]:
        if not R.is_persistent(name):
            assert R.reported_effect(name) == R.EFFECT_NONE, \
                f"{name} is a one-shot and must not linger in HA's card"
    # An unknown name — a stale entry from HA's cached list after a
    # downgrade — must not be reported as a running state nothing renders.
    assert R.reported_effect("Disco Inferno") == R.EFFECT_NONE


def test_a_notification_stands_down_while_a_turn_owns_the_ring():
    """
    Painting over the listening ring tells the user the device stopped
    listening when it did not, and the ring is the only thing saying so.
    Checked twice: before playing, and again after the wait, so a
    notification that overlapped the start of a turn does not paint over
    that turn's ring on its way out.
    """
    src  = (CONTROLLER / "em_controller.py").read_text()
    body = src[src.index("async def _play_ring_effect"):]
    body = _strip_py_comments(body[:body.index("esphome.device_connected")])
    assert body.count("_d.speaking or _d.thinking or _d.listening") == 2, \
        "the turn check belongs both before the anim and after the wait"
    assert "_d.led_anim_capable" in body, \
        "firmware that cannot animate locally must be refused, not sent one"


# ── Persistent effects: the ring's rest becomes an animation (#66) ─────────


def test_a_persistent_effect_is_what_leds_idle_paints():
    """
    The whole cost of this feature is here: every path that returns the ring
    to rest goes through `leds_idle`, so the effect has to be restarted from
    exactly that one place. Get it wrong and the effect stops the first time
    somebody speaks to the Echo and never comes back.
    """
    src  = (CONTROLLER / "em_controller.py").read_text()
    body = src[src.index("async def leds_idle"):]
    body = body[:body.index("\n\n\n")]
    assert "em_ring_light.resting_anim" in body, (
        "leds_idle no longer restarts the resting animation — a persistent "
        "effect would survive until the first voice turn and then be gone"
    )
    assert "led_anim_capable" in body, (
        "leds_idle must fall back to the solid colour on firmware that "
        "cannot animate; an unannounced anim is ignored silently and leaves "
        "a ring the user believes is running an effect and that is dark"
    )


def test_the_dimmer_governs_a_running_effect():
    """
    A resting animation IS the resting state, so the brightness slider has to
    reach it. An effect that ignored the dimmer would be the one thing on the
    ring a user could not turn down.
    """
    full = R.resting_anim("Rotate", "#00ff00", 255)
    half = R.resting_anim("Rotate", "#00ff00", 128)
    assert full is not None and half is not None
    assert half["colors"][0][1] < full["colors"][0][1], \
        "the dimmer did not reach the effect"


def test_brightness_zero_means_off_even_with_an_effect_selected():
    """
    "Off" has to mean off. An effect still running on a light reporting
    itself off is the control-that-lies failure this project names most.
    """
    assert R.resting_anim("Rainbow", "#00ff00", 0) is None
    assert R.resting_anim("Blink", "#ff0000", 0) is None


def test_a_one_shot_is_never_a_resting_state():
    for name in R.EFFECTS[1:]:
        if R.is_persistent(name):
            continue
        assert R.resting_anim(name, "#00ff00", 255) is None, \
            f"{name} is a notification and must not become the rest"
    assert R.resting_anim(R.EFFECT_NONE, "#00ff00", 255) is None


def test_an_unknown_effect_is_not_persistent():
    """
    The effect list is API surface HA caches, so a name from a NEWER
    controller can arrive here after a downgrade. Treating it as persistent
    would store a resting state nothing can render.
    """
    assert R.is_persistent("Disco Inferno") is False
    assert R.resting_anim("Disco Inferno", "#00ff00", 255) is None


def test_a_motion_effect_runs_in_the_colour_the_user_picked():
    """
    Selecting an effect must not silently discard the colour. The ones that
    ARE a colour scheme (Rainbow) are allowed to ignore it; the ones that are
    a motion are not.
    """
    red  = R.effect_anim("Rotate", "#ff0000")["colors"]
    blue = R.effect_anim("Rotate", "#0000ff")["colors"]
    assert red != blue, "Rotate ignored the resting colour"
    # Rainbow is a palette in its own right and is the same either way.
    assert (R.effect_anim("Rainbow", "#ff0000")["colors"]
            == R.effect_anim("Rainbow", "#0000ff")["colors"])


def test_every_effect_uses_a_pattern_the_fielded_firmware_renders():
    """
    `StartAnim`'s default branch CLEARS THE RING on a pattern it does not
    know, so an effect shipped ahead of the firmware turns the ring off
    rather than doing nothing. That is the compatibility trap in this
    feature, and it is why no new capability was needed: every pattern here
    is one the firmware already renders.
    """
    src = (pathlib.Path(__file__).resolve().parents[2]
           / "device" / "internal" / "server" / "animator.go").read_text()
    known = set()
    for line in src.splitlines():
        line = line.strip()
        if line.startswith("case ") and line.endswith(":"):
            for part in line[len("case "):-1].split(","):
                part = part.strip()
                if part.startswith('"'):
                    known.add(part.strip('"'))
    assert "spin" in known and "pulse" in known, \
        "the pattern scrape found nothing — the guard is vacuous"
    for name in R.EFFECTS[1:]:
        pattern = R.effect_anim(name, "#00ff00")["pattern"]
        assert pattern in known, (
            f"{name} uses pattern {pattern!r}, which animator.go does not "
            f"know — the ring would go dark rather than run the effect"
        )


def test_a_gradient_lights_the_whole_ring_and_a_motion_effect_does_not():
    """
    A `solid` spec paints the palette it is given, so a gradient needs one
    triple per LED; `spin` takes a head colour and derives the trail.
    """
    grad = R.effect_anim("Gradient", "#ff0000")
    assert grad["pattern"] == "solid"
    assert len(grad["colors"]) == R.NUM_LEDS
    assert len({tuple(c) for c in grad["colors"]}) > 1, "a flat gradient"
    assert len(R.effect_anim("Rotate", "#ff0000")["colors"]) == 1


def test_the_rainbow_fills_the_ring():
    anim = R.effect_anim("Rainbow", "#ffffff")
    assert anim["pattern"] == "rotate"
    assert len(anim["colors"]) == R.NUM_LEDS, \
        "rotate walks a palette around the ring and needs one entry per LED"


def test_persistent_effects_are_all_advertised():
    for name in R.PERSISTENT:
        assert name in R.EFFECTS, f"{name} is persistent but not offered"
    assert R.PERSISTENT, "the feature advertises no persistent effects at all"
