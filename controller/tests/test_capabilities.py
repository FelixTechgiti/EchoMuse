"""
Device/controller compatibility is negotiated by CAPABILITY, not version.

The two halves of this project ship on independent version schemes (device
`v*`, controller `controller-v*`), so any given moment can pair new firmware
with an old controller or the reverse. Version comparison would mean encoding
release history into the controller and getting it wrong the first time
someone runs a dev build; a capability is the device stating what it
implements.

That only works if both sides spell the capability identically. A typo makes
the feature permanently unavailable and looks exactly like a device that does
not support it — silent, and the sort of thing you debug from the wrong end.
So the strings are asserted to match across the two languages, the same way
CONFIG_SECTIONS is mirrored between Python and dashboard.jsx.
"""

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent
CONTROL_GO = ROOT / "device" / "internal" / "client" / "control.go"
CONTROLLER = ROOT / "controller" / "em_controller.py"
API = ROOT / "controller" / "em_api.py"
ESPHOME = ROOT / "controller" / "em_esphome.py"


def device_capabilities() -> list[str]:
    """
    Every capability the firmware can announce.

    Read from the whole capabilities() function rather than a single literal:
    the list is no longer fixed — "ambient_light" is appended only when the
    hardware actually has a readable sensor, because the controller advertises
    an HA entity off the back of it. A parser that only understood one literal
    would silently stop covering the conditional ones, which is the direction
    that hides a typo rather than surfacing it.
    """
    src = CONTROL_GO.read_text()
    m = re.search(r'func capabilities\(\) \[\]string \{(.*?)\n\}', src, re.S)
    assert m, "could not find func capabilities() in control.go"
    return re.findall(r'"([a-z_]+)"', m.group(1))


def test_device_announces_expected_capabilities():
    caps = device_capabilities()
    for expected in ("mic", "speaker", "leds", "led_anim", "buttons", "oww_shadow"):
        assert expected in caps, f"firmware no longer announces {expected!r}"


def test_every_capability_the_controller_checks_is_one_the_device_sends():
    """
    A controller checking for a capability string the device never sends is a
    feature that is silently off forever. This catches the typo direction that
    the device-side test cannot.
    """
    caps = set(device_capabilities())
    # Two idioms, both scanned: `"<cap>" in (self.capabilities or [])` on
    # Device in em_controller, and _device_has("<cap>") on the ESPHome
    # satellite, which decides which HA entities get advertised. A typo in
    # either is an entity that never appears or never fires, with no error
    # anywhere — exactly what this test exists to catch.
    checked = set(re.findall(r'"([a-z_]+)"\s+in\s+\(self\.capabilities',
                             CONTROLLER.read_text()))
    checked |= set(re.findall(r'_device_has\(\s*"([a-z_]+)"\s*\)',
                              ESPHOME.read_text()))
    assert checked, "no capability checks found — has the idiom changed?"
    unknown = checked - caps
    assert not unknown, (
        f"controller checks capabilities the firmware never announces: {sorted(unknown)}. "
        f"Device sends: {sorted(caps)}"
    )


def test_shadow_capability_is_surfaced_to_the_dashboard():
    """
    The dashboard must be able to tell "cannot" from "off", or it offers a
    toggle that silently does nothing on older firmware — which reads as a
    broken feature rather than an unsupported one.
    """
    assert "oww_shadow_capable" in CONTROLLER.read_text(), \
        "em_controller must expose the shadow capability as a property"
    assert "owwShadowCapable" in API.read_text(), \
        "/api/devices must surface the shadow capability"
    jsx = (ROOT / "controller" / "static" / "dashboard.jsx").read_text()
    assert "owwShadowCapable" in jsx, \
        "the dashboard must gate the on-device toggle on the capability"


def test_capabilities_reported_before_the_server_exists_are_not_lost():
    """
    A device registers BEFORE its ESPHome server is created — the listener
    only comes up once the device is present — so the capability push finds
    no server. Dropping it there built the entity list from an empty set, and
    that list is a ONE-SHOT at ListEntities time: HA caches it and the sensor
    is absent for the life of the connection.

    Observed on Retreat, 2026-08-03: connected 05:25:33, server created
    05:25:34, no ambient light entity in HA afterwards. The same race resolves
    differently per controller restart, which is why the graph came and went.
    """
    import em_esphome

    device_id = "G090LFTESTCAPS"
    em_esphome._pending_caps.pop(device_id, None)
    try:
        # Registration lands first, with no server yet.
        em_esphome.set_device_capabilities(device_id, ["mic", "ambient_light"])
        assert device_id not in em_esphome._servers, "precondition: no server yet"
        assert em_esphome._pending_caps[device_id] == ["mic", "ambient_light"], \
            "the capabilities must be held until a server exists to take them"
    finally:
        em_esphome._pending_caps.pop(device_id, None)


def test_the_pending_capabilities_are_what_the_entity_gate_reads():
    """
    Guard against the two halves drifting: holding the value is only useful
    if server creation applies it to the same attribute the ListEntities gate
    checks (_device_has → srv.capabilities).
    """
    import inspect
    import em_esphome

    src = inspect.getsource(em_esphome._register_device_server)
    assert "_pending_caps" in src, \
        "server creation must seed capabilities from the pending map"
    assert "set_capabilities" in src, \
        "and must apply them via the same setter the gate reads"
