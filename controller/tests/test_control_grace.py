"""
#315: a control-plane drop used to tear down a device's services
immediately — HA entities deregistered, BLE proxy dropped, media session
killed — and rebuilt all of it when the device returned seconds later.
The data plane has had DATA_RECONNECT_GRACE_S for exactly this reason;
the control plane never got the equivalent.

em_controller is deliberately not importable here (see conftest); these
are shape guards on the shipped source.
"""

from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]


def _finally_src() -> str:
    src = (CONTROLLER / "em_controller.py").read_text()
    start = src.index("log.info(f\"[control] Device disconnected")
    end = src.index("# ─── Data plane handler", start)
    return src[start:end]


def test_teardown_is_deferred_not_immediate():
    # Anchored on the code either side of the handover, not on a character
    # count. The window used to be `start + 1200`, and a comment added
    # above the create_task pushed it out of range — which made `.index()`
    # raise ValueError, so the test failed as BROKEN rather than as this
    # invariant being violated. That reading is the one that wastes the
    # most time; the invariant has no opinion about how much prose sits
    # between the log line and the handover.
    seg = _finally_src()
    assert "asyncio.create_task(" in seg, \
        "the close path no longer hands over to a task at all"
    task_call = seg.index("asyncio.create_task(")
    sync_path = seg[:task_call]
    for call in ("esphome.device_disconnected",
                 "em_ble_proxy.device_disconnected",
                 "device_gone", "notify_device_disconnected"):
        assert call not in sync_path, \
            f"{call} must move into the grace task, not run on close"
    assert "_release_device_services" in seg[task_call:], \
        "the close path must hand over to the grace task"


def test_a_blipped_device_is_held_link_down_not_popped():
    """
    #354: the close path marks the device link-down and LEAVES it in the
    registry; the pop moves to the end of the grace.

    Popping at close made "cannot be reached" and "no such device" the same
    answer for the five seconds in which the satellite, the BLE proxy and
    the media session were all still up and still answering Home Assistant
    — the one window in which the difference is the whole point.
    """
    seg = _finally_src()
    task_call = seg.index("asyncio.create_task(")
    sync_path = "\n".join(
        l for l in seg[:task_call].splitlines()
        if not l.lstrip().startswith("#")
    )
    assert "_devices.pop" not in sync_path, \
        "the device must be held through the grace, not popped on close"
    assert "link_down_since" in sync_path, \
        "the close path must record WHEN the link went down"

    src = (CONTROLLER / "em_controller.py").read_text()
    start = src.index("async def _release_device_services")
    task = src[start:src.index("# ─── Data plane handler", start)]
    assert "_devices.pop" in task, \
        "the grace task owns the removal now — nothing else does it"


def test_get_device_refuses_a_link_down_device():
    """
    Nearly every caller of get_device() means "a device I can send
    something to", so the link-down device held by #354 must not be handed
    out there. The escape hatch is get_device_any(), for the callers that
    are reporting the state rather than acting on it.
    """
    src = (CONTROLLER / "em_controller.py").read_text()
    start = src.index("def get_device(device_id")
    body = src[start:src.index("def get_device_any", start)]
    code = "\n".join(
        l for l in body.splitlines() if not l.lstrip().startswith("#")
    )
    assert "link_down" in code, \
        "get_device must filter out a device inside its reconnect grace"
    assert "def get_device_any" in src, \
        "state readouts still need a way to reach the object"


def test_the_grace_task_checks_for_a_replacement():
    src = (CONTROLLER / "em_controller.py").read_text()
    start = src.index("async def _release_device_services")
    task = src[start:start + 2500]
    assert "CONTROL_RECONNECT_GRACE_S" in task
    assert "_devices.get(device.device_id)" in task, \
        "the task must check whether a replacement registered"
    for call in ("notify_device_disconnected", "esphome.device_disconnected",
                 "em_ble_proxy.device_disconnected", "device_gone"):
        assert call in task, f"{call} belongs in the deferred release"


def test_the_grace_window_exists_and_is_documented():
    src = (CONTROLLER / "em_controller.py").read_text()
    assert "CONTROL_RECONNECT_GRACE_S" in src
    # The data-plane constant this mirrors:
    assert "DATA_RECONNECT_GRACE_S = 3.0" in src


def test_the_stale_connection_guard_survives():
    """
    The 2026-07-14 guard solves a different ordering problem (close arriving
    AFTER a replacement registered) and must stay untouched.
    """
    src = (CONTROLLER / "em_controller.py").read_text()
    # the message wraps across two f-string lines — match the fragments
    assert "replacement is active" in src and "services up" in src, \
        "the out-of-order stale guard is still needed alongside the grace"
