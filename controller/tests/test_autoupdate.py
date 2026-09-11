"""The auto-update decision, case by case.

Every one of these is a reboot in somebody's house if it goes the wrong way,
which is why the logic is a pure function and this file is long.
"""

import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

import em_autoupdate as au


def dev(**kw):
    base = dict(
        device_id="dev1",
        approved=True,
        online=True,
        busy=False,
        audio_source="none",
        audio_state_capable=True,
        endpoints_enabled=False,
        firmware_ver="2.26.0-fx.1",
        updating=False,
    )
    base.update(kw)
    return au.DeviceView(**base)


WINDOW = au.Window(3 * 60, 5 * 60)          # 03:00–05:00
NIGHT = au.Window(23 * 60, 2 * 60)          # 23:00–02:00
AT_0400 = 4 * 60


# ── parsing ──────────────────────────────────────────────────────────────

def test_parses_a_plain_window():
    assert au.parse_window("03:00-05:00") == au.Window(180, 300)


def test_parses_a_window_across_midnight():
    assert au.parse_window("23:30-02:15") == au.Window(1410, 135)


def test_rubbish_is_no_window_rather_than_a_guess():
    for bad in ("", None, "3-5", "03:00", "25:00-05:00", "03:60-05:00",
                "03:00-05:00-07:00", "three-five"):
        assert au.parse_window(bad) is None, bad


def test_a_window_with_equal_ends_is_refused():
    # Either reading — no time at all, or every time — is somebody's fleet
    # rebooting at a moment they thought they had excluded.
    assert au.parse_window("03:00-03:00") is None


# ── the window itself ────────────────────────────────────────────────────

def test_inside_and_outside_a_plain_window():
    assert au.in_window(3 * 60, WINDOW)          # the opening minute counts
    assert au.in_window(4 * 60, WINDOW)
    assert not au.in_window(5 * 60, WINDOW)      # half-open at the end
    assert not au.in_window(2 * 60 + 59, WINDOW)


def test_a_window_across_midnight_covers_both_sides():
    assert au.in_window(23 * 60, NIGHT)
    assert au.in_window(0, NIGHT)
    assert au.in_window(1 * 60 + 59, NIGHT)
    assert not au.in_window(2 * 60, NIGHT)
    assert not au.in_window(12 * 60, NIGHT)


# ── idleness ─────────────────────────────────────────────────────────────

def test_a_silent_device_is_idle():
    assert au.device_idle(dev()).update


def test_a_voice_turn_is_not_idle():
    assert not au.device_idle(dev(busy=True)).update


def test_a_device_playing_locally_is_not_idle():
    d = au.device_idle(dev(audio_source="spotify"))
    assert not d.update
    assert "spotify" in d.reason


def test_an_unrecognised_source_counts_as_playing():
    # Guessing here costs somebody their music; the cost of being wrong the
    # other way is one skipped night.
    assert not au.device_idle(dev(audio_source="something-new")).update


def test_firmware_that_cannot_report_is_updated_when_it_has_nothing_to_play():
    d = au.device_idle(dev(audio_source=None, audio_state_capable=False,
                           endpoints_enabled=False))
    assert d.update


def test_firmware_that_cannot_report_waits_when_an_endpoint_is_on():
    d = au.device_idle(dev(audio_source=None, audio_state_capable=False,
                           endpoints_enabled=True))
    assert not d.update
    assert "cannot report" in d.reason


def test_capable_but_silent_is_not_treated_as_having_reported():
    # The capability says it CAN say, not that it HAS.
    d = au.device_idle(dev(audio_source=None, audio_state_capable=True,
                           endpoints_enabled=True))
    assert not d.update


# ── the whole decision ───────────────────────────────────────────────────

def kwargs(**over):
    base = dict(enabled=True, window=WINDOW, now_minutes=AT_0400,
                target_version="2.27.0-fx.1", device=dev())
    base.update(over)
    return base


def test_the_happy_path():
    d = au.decide(**kwargs())
    assert d.update
    assert "2.27.0-fx.1" in d.reason


def test_off_means_off():
    assert not au.decide(**kwargs(enabled=False)).update


def test_no_window_means_no_update():
    assert not au.decide(**kwargs(window=None)).update


def test_outside_the_window_nothing_happens():
    assert not au.decide(**kwargs(now_minutes=20 * 60)).update


def test_no_release_is_not_an_update():
    assert not au.decide(**kwargs(target_version=None)).update


def test_an_unapproved_device_is_never_updated():
    # Unapproved means somebody has not yet decided about this device;
    # installing firmware on it unattended decides for them.
    d = au.decide(**kwargs(device=dev(approved=False)))
    assert not d.update
    assert "approved" in d.reason


def test_an_offline_device_is_skipped():
    assert not au.decide(**kwargs(device=dev(online=False))).update


def test_a_device_already_on_the_target_is_skipped():
    d = au.decide(**kwargs(device=dev(firmware_ver="2.27.0-fx.1")))
    assert not d.update
    assert "already running" in d.reason


def test_a_device_already_updating_is_skipped():
    assert not au.decide(**kwargs(device=dev(updating=True))).update


# ── one at a time ────────────────────────────────────────────────────────

def test_only_one_device_is_chosen_per_tick():
    devices = [dev(device_id="a"), dev(device_id="b"), dev(device_id="c")]
    chosen, skipped = au.next_candidate(
        enabled=True, window=WINDOW, now_minutes=AT_0400,
        target_version="2.27.0-fx.1", devices=devices)
    assert chosen.device_id == "a"
    # The other two are not "skipped for a reason" — they are simply next.
    assert "a" not in skipped


def test_the_order_given_is_the_order_taken():
    devices = [dev(device_id="b"), dev(device_id="a")]
    chosen, _ = au.next_candidate(
        enabled=True, window=WINDOW, now_minutes=AT_0400,
        target_version="2.27.0-fx.1", devices=devices)
    assert chosen.device_id == "b"


def test_a_busy_device_does_not_block_the_one_behind_it():
    devices = [dev(device_id="busy", audio_source="airplay"),
               dev(device_id="free")]
    chosen, skipped = au.next_candidate(
        enabled=True, window=WINDOW, now_minutes=AT_0400,
        target_version="2.27.0-fx.1", devices=devices)
    assert chosen.device_id == "free"
    assert "airplay" in skipped["busy"]


def test_nothing_to_do_returns_nothing_and_says_why():
    devices = [dev(device_id="a", firmware_ver="2.27.0-fx.1")]
    chosen, skipped = au.next_candidate(
        enabled=True, window=WINDOW, now_minutes=AT_0400,
        target_version="2.27.0-fx.1", devices=devices)
    assert chosen is None
    assert "already running" in skipped["a"]
