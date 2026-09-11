"""
The auto-update tick's ORCHESTRATION, as opposed to its decision.

em_autoupdate.decide is pure and tested next door. What is not pure, and is
where the expensive failure lives, is what happens around it: the halt that
stops a fleet walking into the same wall, the halt clearing when the window
closes, and the log events that make an update nobody watched findable
afterwards.

Same technique as test_update_interval.py — em_api imports aiohttp and the
whole controller stack, so one function's source is extracted and executed in
a stub namespace. What runs is the code that actually ships.
"""

import asyncio
import logging
import types

import em_autoupdate
from apisrc import extract as _extract


class Recorder:
    """Everything the tick did, in order."""

    def __init__(self):
        self.updated = []
        self.events = []


def _ns(*, enabled="1", window="03:00-05:00", now_hhmm=(4, 0),
        devices=(), release={"version": "9.9.9"}, fail=None):
    """
    A namespace the extracted tick can run in.

    `devices` are DeviceViews; the tick's own view-building is stubbed,
    because what is under test here is what it does with the answer.
    """
    rec = Recorder()

    async def fake_run_update(device_id, rel, override=None):
        rec.updated.append(device_id)
        if fail == device_id:
            ns["_update_errors"][device_id] = "did not come back"

    async def fake_push(device_id, level, source, message):
        rec.events.append((device_id, level, message))

    async def fake_release():
        return release

    class FakeStruct:
        tm_hour, tm_min = now_hhmm

    ns = {
        "asyncio": asyncio,
        "time": types.SimpleNamespace(localtime=lambda: FakeStruct()),
        "log": logging.getLogger("test-autoupdate-loop"),
        "db": types.SimpleNamespace(get_all_devices=lambda: list(devices)),
        "em_autoupdate": em_autoupdate,
        "Optional": type(None),
        "_auto_update_settings": lambda: (
            enabled == "1", em_autoupdate.parse_window(window)),
        "_auto_update_device_view": lambda row: row,
        "_get_cached_release": fake_release,
        "_run_update": fake_run_update,
        "_push_log_event": fake_push,
        "_update_errors": {},
        "_auto_update_halted": None,
        "_clear_auto_update_halt": None,   # replaced below
        "_rec": rec,
    }
    exec(_extract("_clear_auto_update_halt"), ns)
    exec(_extract("_auto_update_tick"), ns)
    return ns, rec


def dev(device_id, **kw):
    base = dict(
        device_id=device_id, approved=True, online=True, busy=False,
        audio_source="none", audio_state_capable=True,
        endpoints_enabled=False, firmware_ver="1.0.0", updating=False,
    )
    base.update(kw)
    return em_autoupdate.DeviceView(**base)


def run(ns):
    asyncio.run(ns["_auto_update_tick"]())


def test_an_idle_device_inside_the_window_is_updated():
    ns, rec = _ns(devices=[dev("a")])
    run(ns)
    assert rec.updated == ["a"]


def test_only_one_device_is_touched_per_tick():
    ns, rec = _ns(devices=[dev("a"), dev("b"), dev("c")])
    run(ns)
    assert rec.updated == ["a"]


def test_nothing_happens_outside_the_window():
    ns, rec = _ns(devices=[dev("a")], now_hhmm=(20, 0))
    run(ns)
    assert rec.updated == []


def test_nothing_happens_when_the_feature_is_off():
    ns, rec = _ns(devices=[dev("a")], enabled="0")
    run(ns)
    assert rec.updated == []


def test_no_window_updates_nothing_even_when_enabled():
    ns, rec = _ns(devices=[dev("a")], window="")
    run(ns)
    assert rec.updated == []


def test_a_failure_halts_the_rest_of_the_fleet():
    ns, rec = _ns(devices=[dev("a"), dev("b")], fail="a")
    run(ns)
    assert rec.updated == ["a"]
    assert ns["_auto_update_halted"] == "a"
    # And a second tick in the same window touches nothing, which is the
    # whole point: the first device that did not come back is evidence about
    # the BINARY, so b is worth more than the convenience of not waiting.
    run(ns)
    assert rec.updated == ["a"]


def test_the_halt_says_what_happened_on_the_device_itself():
    ns, rec = _ns(devices=[dev("a")], fail="a")
    run(ns)
    levels = [e[1] for e in rec.events]
    assert "error" in levels
    assert any("no further devices" in e[2] for e in rec.events)


def test_a_success_is_recorded_too():
    ns, rec = _ns(devices=[dev("a")])
    run(ns)
    assert any("started" in e[2] for e in rec.events)
    assert any("finished" in e[2] for e in rec.events)


def test_leaving_the_window_clears_the_halt():
    ns, rec = _ns(devices=[dev("a"), dev("b")], fail="a")
    run(ns)
    assert ns["_auto_update_halted"] == "a"
    # Re-run the tick with the clock outside the window. A halt is one
    # night's evidence, not a permanent verdict.
    ns2, _ = _ns(devices=[dev("b")], now_hhmm=(20, 0))
    ns2["_auto_update_halted"] = "a"
    exec(_extract("_clear_auto_update_halt"), ns2)
    exec(_extract("_auto_update_tick"), ns2)
    asyncio.run(ns2["_auto_update_tick"]())
    assert ns2["_auto_update_halted"] is None


def test_no_release_means_no_update_and_no_noise():
    ns, rec = _ns(devices=[dev("a")], release=None)
    run(ns)
    assert rec.updated == []
    assert rec.events == []
