"""
#354: a device held through a control-plane blip is IN the registry, marked
link-down, rather than missing from it.

Popping it at close made two different claims read the same for the length
of the grace — "this device cannot be reached right now" and "there is no
such device" — during the one window where the difference is the entire
point: the satellite is still registered with Home Assistant, the BLE proxy
is still listening and the media session is still alive, so the device is
very much *there* and merely unreachable.

The hazard the fix introduces is the mirror image, and it is the one these
guards are for: every lookup that means "the device I am about to send
something to" must keep refusing a link-down device, or a four-second blip
becomes a request that reports success and does nothing.

em_controller and em_api are deliberately not importable here (see
conftest), so these are shape guards on the shipped source.
"""

import re
from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]


def _strip_py_comments(src: str) -> str:
    """
    Comments AND docstrings — the same helper as
    test_voice_satellite_status, and for the same reason: the prose
    explaining why `_devices.get` is forbidden here says `_devices.get`,
    so a guard that skips only `#` lines matches its own rationale and
    passes whatever the code does.
    """
    src = re.sub(r'"""(?:.|\n)*?"""', "", src)
    src = re.sub(r"'''(?:.|\n)*?'''", "", src)
    return "\n".join(re.sub(r"#.*$", "", line) for line in src.splitlines())


def _api_code() -> str:
    return _strip_py_comments((CONTROLLER / "em_api.py").read_text())


def test_no_handler_reads_the_registry_directly():
    """
    The whole file goes through _live() / _live_items(). A direct
    `_devices.get` in a handler is the regression: it compiles, it passes
    every existing test, and it hands out a device with a closed socket for
    five seconds at a time.

    Two direct reads are legitimate and both live inside the helpers
    themselves, plus the one in _merge_device that exists precisely to
    REPORT the link-down state rather than act on it.
    """
    code = _api_code()
    reads = [
        line.strip()
        for line in code.splitlines()
        if "_devices.get(" in line or "_devices.items()" in line
    ]
    # _live(), _live_items(), and _merge_device's "linkDown" readout.
    assert len(reads) == 3, (
        "a new direct registry read appeared — route it through _live() "
        f"unless it is reporting link state: {reads}"
    )
    assert any("link_down" in r for r in reads), (
        "the reporting read must be the one asking for link_down"
    )


def test_the_helpers_actually_filter():
    code = _api_code()
    assert 'getattr(device, "link_down", False)' in code, \
        "_live must refuse a device inside its reconnect grace"
    assert 'getattr(d, "link_down", False)' in code, \
        "_live_items must refuse them too"


def test_the_fleet_count_is_reachable_devices():
    """
    /api/system/status "connected" answers "how many Echoes are working".
    len(_devices) stopped meaning that once link-down devices were held.
    """
    code = _api_code()
    assert '"connected":      len(_live_items())' in code, \
        "the connected count must exclude devices inside a reconnect grace"


def test_wake_arbitration_counts_reachable_devices():
    """
    The arbiter skips its contention window for a solo fleet. One live Echo
    plus one mid-blip is a solo fleet, and taxing every wake ~364ms for a
    device that cannot answer is the cost this shortcut exists to avoid.
    """
    src = _strip_py_comments((CONTROLLER / "em_controller.py").read_text())
    assert "_reachable_count() > 1" in src, \
        "arbitration must count devices that can answer, not registry rows"
    assert "len(_devices) > 1" not in src, \
        "the raw length is the pre-#354 meaning of this test"


def test_an_announcement_is_refused_while_the_link_is_down():
    """
    The reply is the ONE thing an announcement reports, and HA blocks on it
    holding the entity in RESPONDING. During a grace the frames are dropped
    by send_data's own budget and the closure still returned True — telling
    HA an announcement played to a device nobody could reach.
    """
    src = (CONTROLLER / "em_controller.py").read_text()
    start = src.index("async def _standalone_play")
    body  = _strip_py_comments(src[start:src.index("async def _send_volume_set", start)])
    assert "_d.link_down" in body, \
        "the announce path must check the link before claiming success"
    assert "return False" in body, \
        "a refused announcement must report failure, not success"


def test_the_data_plane_waits_for_the_replacement():
    """
    A fresh data connection arriving during a grace must not bind to the OLD
    Device — _release_device_services is about to discard it, and the redial
    that brought this data plane back is bringing a control plane with it.
    """
    src = (CONTROLLER / "em_controller.py").read_text()
    start = src.index("async def handle_data")
    body  = _strip_py_comments(src[start:start + 3000])
    assert "device = get_device(device_id)" in body, \
        "the data plane must resolve through the reachability-aware accessor"


def test_the_player_is_handed_the_filtered_accessor():
    """em_player asking for a device means "something to send audio to"."""
    src = _strip_py_comments((CONTROLLER / "em_controller.py").read_text())
    start = src.index("em_player.init(")
    assert "get_device=get_device," in src[start:start + 200], \
        "em_player must not be given the raw registry lookup"
