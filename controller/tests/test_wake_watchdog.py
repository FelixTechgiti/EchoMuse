"""
#299: the wake-stream watchdog treated a dropped link as a broken mic.

On a lossy link, no mic frames arrive because the transport keeps
dropping — but the watchdog's escalation ladder (defensive mic_start,
then mic_stop+mic_start) assumed a zombie stream device-side and acted:
extra control-plane traffic on an already failing link, and a working
mic pipeline torn down and rebuilt (bedroom, 2026-08-23: AEC resyncs
3, 4, 5, 6).

The fix: "no frames" with the data plane down — or on a connection that
has not delivered a single frame yet — is explained. Stand down; do not
count it against the streak either.

em_controller is deliberately not importable here (see conftest); these
are shape guards on the shipped source.
"""

from pathlib import Path

CONTROLLER = Path(__file__).resolve().parents[1]


def _listener_src() -> str:
    src = (CONTROLLER / "em_controller.py").read_text()
    start = src.index("async def wake_word_listener")
    return src[start:start + 30_000]


def test_the_guard_sits_before_the_escalation_ladder():
    src = _listener_src()
    guard = src.index("device.data_ws is None or not device.frames_seen_this_connection")
    ladder = src.index("dead_streak += 1", guard)
    assert guard < ladder, \
        "the link guard must stand down BEFORE dead_streak counts or mic_start fires"
    # And before both escalation stages:
    esc = src.index("mic_stop()", guard)
    assert guard < esc


def test_an_outage_does_not_count_against_the_streak():
    """
    The muted branch resets the streak (expected silence); the outage branch
    must NOT reset it either — a streak surviving a short blip stays honest —
    it just freezes while the transport is down. So: continue without
    touching dead_streak between guard and frame arrival.
    """
    src = _listener_src()
    guard = src.index("device.data_ws is None or not device.frames_seen_this_connection")
    branch_end = src.find("continue", guard)
    between = src[guard:branch_end]
    assert "dead_streak" not in between, \
        "the outage branch must neither increment nor reset the streak"


def test_a_new_connection_clears_the_flag_and_frames_set_it():
    """handle_data clears on connect; the wake listener sets on first frame.
    File position reflects definition order, not runtime — so pin each site
    inside its own function rather than comparing offsets."""
    src = (CONTROLLER / "em_controller.py").read_text()
    hd = src[src.index("device.data_ws = ws"):]  # handle_data body
    hd = hd[:hd.index("async def", 10)]
    assert "frames_seen_this_connection = False" in hd, \
        "a new data connection must clear the flag"
    wl = src[src.index("payload = await asyncio.wait_for("):
             src.index("except asyncio.TimeoutError")]
    assert "frames_seen_this_connection = True" in wl, \
        "a delivered frame must set the flag"
