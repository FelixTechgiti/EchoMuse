"""
The audio-state entities, and the four places they are wired.

`em_audiostate` is unit-tested on its own — it is a pure state machine and its
tests are in test_audiostate.py. What cannot be unit-tested is whether anything
CALLS it, because the suite cannot import em_controller or em_esphome. That is
the half worth guarding: every one of these connections is silent when it
breaks, and the symptom is identical in all four cases — an entity that sits at
"off" while the Echo is audibly playing, and an amplifier automated on it that
switches to the wrong input.

The failure this file exists to prevent is not a crash. It is a person building
an automation on a sensor that quietly stopped reporting.
"""

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent
CONTROLLER = (ROOT / "controller" / "em_controller.py").read_text()
ESPHOME = (ROOT / "controller" / "em_esphome.py").read_text()
CONTROL_GO = (ROOT / "device" / "internal" / "client" / "control.go").read_text()
SERVER_GO = (ROOT / "device" / "cmd" / "server.go").read_text()
SECTIONS = (ROOT / "controller" / "em_config_sections.py").read_text()
DASHBOARD = (ROOT / "controller" / "static" / "dashboard.jsx").read_text()
DEFAULTS = (ROOT / "controller" / "em_db.py").read_text()


def test_both_entities_are_gated_on_the_capability():
    """
    Firmware that runs Sendspin, Spotify and AirPlay without being able to
    report them is in the field — the three shipped before `audio_state` did.
    Advertising these entities there gives a sensor that reads "off" through a
    whole album, which is worse than no sensor: the automation built on it
    switches the amplifier AWAY from the music.
    """
    for entity in ("ListEntitiesBinarySensorResponse",
                   "ListEntitiesTextSensorResponse"):
        m = re.search(r"\n( *)yield api_pb2\." + entity, ESPHOME)
        assert m, f"{entity} is no longer advertised"
        before = ESPHOME[:m.start()]
        guard = before.rsplit("if self.", 1)[-1].split(":")[0]
        assert guard == "_audio_state_capable", (
            f"{entity} is advertised under `if self.{guard}` rather than the "
            f"audio_state capability"
        )


def test_the_refresh_rides_the_state_push_that_already_exists():
    """
    speaking and thinking are the two controller-side halves of the answer,
    and they already have exactly one funnel: _push_device_state, which
    Device._set_speaking is pinned to call. Hanging the refresh off it is what
    means a new path that changes those flags cannot forget this one.
    """
    body = CONTROLLER.split("async def _push_device_state")[1].split("\nasync def ")[0]
    assert "refresh_audio_state(device)" in body, (
        "_push_device_state no longer refreshes the audio state — every "
        "speaking/thinking transition is now invisible to Home Assistant"
    )


def test_media_state_reaches_the_aggregate_rather_than_going_straight_to_ha():
    """
    em_player has exactly one way out to Home Assistant, and em_controller
    injects it. Pointing that at esphome.push_media_state directly is the
    version that looks right and loses the controller's own music plane: the
    entity would then report only voice and whatever the device said last.
    """
    m = re.search(r"notify_state\s*=\s*([A-Za-z_.]+)", CONTROLLER)
    assert m, "em_player.init no longer takes notify_state — has it moved?"
    assert m.group(1) == "_notify_media_state", (
        f"em_player pushes state through {m.group(1)}, which does not refresh "
        f"the audio state"
    )
    wrapper = CONTROLLER.split("async def _notify_media_state")[1].split("\nasync def ")[0]
    assert "push_media_state" in wrapper and "refresh_audio_state" in wrapper, (
        "the wrapper must do both — HA's media_player still needs its push"
    )


def test_the_device_reports_its_plane_on_change_and_on_registration():
    """
    Two halves, and each alone leaves a real gap.

    Without OnChange nothing is reported until the next reconnect. Without the
    register field, a control-plane blip mid-track — which this fleet has
    measured plenty of — leaves the controller believing a playing device went
    quiet, and the amplifier switches away from a room that is still playing.
    """
    assert '"audio_source": c.currentAudioSource()' in CONTROL_GO, (
        "the register message no longer carries the music plane's owner"
    )
    assert "MusicPlane().OnChange(" in SERVER_GO, (
        "nothing subscribes to the music plane's handovers"
    )
    assert "SetAudioSourceFunc(" in SERVER_GO, (
        "the register message has no way to read the current owner"
    )


def test_the_source_names_agree_across_the_two_languages():
    """
    The device names the source and the controller decides what it means. A
    name only one side knows is a local source read as silence — music playing
    with the sensor off, and nothing logged anywhere.
    """
    audiostate = (ROOT / "controller" / "em_audiostate.py").read_text()
    known = set(re.findall(r'^SOURCE_\w+\s*=\s*"([a-z]+)"', audiostate, re.M))
    owner_go = (ROOT / "device" / "internal" / "musicplane" / "owner.go").read_text()
    stringer = owner_go.split("func (s Source) String() string")[1].split("\n}")[0]
    device_names = set(re.findall(r'return "([a-z]+)"', stringer))
    # "controller" is the controller's own 0x04 stream, which it can see for
    # itself through em_player; "unknown" is the Stringer's fallback. Neither
    # is a local source and both correctly read as silence here.
    unmatched = device_names - known - {"controller", "unknown"}
    assert not unmatched, (
        f"the device can name sources em_audiostate has never heard of: "
        f"{sorted(unmatched)}"
    )


def test_the_holdoff_is_configurable_and_mirrored_everywhere_a_key_must_be():
    """
    The hold-off is a taste value tuned against the amplifier in the room, so
    it is config rather than a constant — which means it has to appear in all
    four places a config key lives, or it is settable and does nothing.
    """
    assert '"audioHoldoffMs"' in DEFAULTS, "no default"
    assert '"audioHoldoffMs"' in SECTIONS, (
        "not in any section — em_config_sections requires a total partition, "
        "so a key belonging to no section can never be overridden per device"
    )
    assert '"audioHoldoffMs"' in DASHBOARD, "not in the dashboard's mirror"
    assert "audioHoldoffMs" in CONTROLLER, "never read by the controller"
