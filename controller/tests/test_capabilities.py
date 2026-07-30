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


def device_capabilities() -> list[str]:
    """The capability list the firmware announces in its register message."""
    src = CONTROL_GO.read_text()
    m = re.search(r'"capabilities":\s*\[\]string\{([^}]*)\}', src)
    assert m, "could not find the capabilities list in control.go"
    return re.findall(r'"([^"]+)"', m.group(1))


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
    src = CONTROLLER.read_text()
    # `"<cap>" in (self.capabilities or [])` is the established idiom.
    checked = set(re.findall(r'"([a-z_]+)"\s+in\s+\(self\.capabilities', src))
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
