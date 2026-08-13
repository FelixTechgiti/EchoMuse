"""Timer state and alarm audio (em_timers).

HA owns the countdown; the satellite only tracks live timers and decides when
to ring. These pin the ring-transition reducer (which is what the async
orchestrator in em_controller keys off) and the chime synthesis.
"""
import struct
import pytest

import em_timers as t


# ── TimerRegistry ────────────────────────────────────────────────────────────

def test_finished_starts_the_ring():
    reg = t.TimerRegistry()
    assert reg.apply(t.TIMER_STARTED, "a") == t.RING_NONE
    assert reg.ringing is False
    assert reg.apply(t.TIMER_FINISHED, "a") == t.RING_START
    assert reg.ringing is True


def test_cancel_of_finished_stops_the_ring():
    reg = t.TimerRegistry()
    reg.apply(t.TIMER_FINISHED, "a")
    # A spoken "stop" dismisses a finished timer as a CANCELLED event.
    assert reg.apply(t.TIMER_CANCELLED, "a") == t.RING_STOP
    assert reg.ringing is False


def test_cancel_of_running_timer_never_rings():
    reg = t.TimerRegistry()
    reg.apply(t.TIMER_STARTED, "a")
    # Cancelling a timer that was still counting down must not ring anything.
    assert reg.apply(t.TIMER_CANCELLED, "a") == t.RING_NONE
    assert reg.ringing is False


def test_updated_does_not_ring():
    reg = t.TimerRegistry()
    reg.apply(t.TIMER_STARTED, "a")
    assert reg.apply(t.TIMER_UPDATED, "a") == t.RING_NONE
    assert reg.ringing is False


def test_second_finished_while_ringing_is_no_transition():
    reg = t.TimerRegistry()
    reg.apply(t.TIMER_FINISHED, "a")
    # Already ringing — a second finished timer does not restart the ring.
    assert reg.apply(t.TIMER_FINISHED, "b") == t.RING_NONE
    assert reg.ringing is True
    # ...and the ring only stops once BOTH finished timers are dismissed.
    assert reg.apply(t.TIMER_CANCELLED, "a") == t.RING_NONE
    assert reg.ringing is True
    assert reg.apply(t.TIMER_CANCELLED, "b") == t.RING_STOP
    assert reg.ringing is False


def test_cancel_of_unknown_timer_is_harmless():
    # HA sends CANCELLED for a timer we cleared locally (button dismissal);
    # it must not error or spuriously stop.
    reg = t.TimerRegistry()
    assert reg.apply(t.TIMER_CANCELLED, "ghost") == t.RING_NONE
    assert reg.ringing is False


def test_unknown_event_type_is_ignored():
    reg = t.TimerRegistry()
    reg.apply(t.TIMER_FINISHED, "a")
    assert reg.apply(999, "a") == t.RING_NONE
    assert reg.ringing is True


def test_clear_reports_whether_it_was_ringing():
    reg = t.TimerRegistry()
    assert reg.clear() is False
    reg.apply(t.TIMER_FINISHED, "a")
    assert reg.clear() is True
    assert reg.ringing is False
    assert reg.active_count() == 0


# ── alarm_pcm ────────────────────────────────────────────────────────────────

def _samples(pcm: bytes):
    assert len(pcm) % 2 == 0, "S16_LE must be an even number of bytes"
    return list(struct.unpack(f"<{len(pcm)//2}h", pcm))


def test_alarm_pcm_is_nonempty_int16():
    pcm = t.alarm_pcm()
    s = _samples(pcm)
    assert len(s) > 0


def test_alarm_pcm_makes_sound():
    # A silent alarm is worse than no alarm — assert real signal energy.
    s = _samples(t.alarm_pcm())
    assert max(abs(x) for x in s) > 8000


def test_alarm_pcm_stays_within_int16_and_leaves_headroom():
    s = _samples(t.alarm_pcm())
    peak = max(abs(x) for x in s)
    assert peak <= 32767            # never clips / wraps
    assert peak < 32000             # amplitude headroom, not slammed to full scale


def test_alarm_pcm_starts_and_ends_at_silence():
    # Faded beeps and trailing silence — no click on loop boundaries.
    s = _samples(t.alarm_pcm())
    assert abs(s[0]) < 100
    assert abs(s[-1]) < 100


def test_alarm_pcm_respects_sample_rate():
    lo = t.alarm_pcm(16000)
    hi = t.alarm_pcm(48000)
    # More samples at a higher rate for the same wall-clock burst.
    assert len(hi) > len(lo)


# ── Ducking the chime for a spoken dismissal ─────────────────────────────────
# A wake word spoken OVER the ring ducks the chime so the command that follows
# ("dismiss") reaches STT over the alarm rather than under it. Detection itself
# happens at full volume — AEC plus the barge-in threshold, same as speech over
# TTS — so the duck must attenuate without changing the burst's shape or length.

def test_duck_attenuates_the_chime():
    full = _samples(t.alarm_pcm(48000))
    duck = _samples(t.alarm_pcm(48000, gain_db=t.DUCK_DB))
    peak_full = max(abs(x) for x in full)
    peak_duck = max(abs(x) for x in duck)
    assert peak_duck < peak_full
    # −18dB is ~0.126×; allow generous slack for quantisation.
    ratio = peak_duck / peak_full
    assert 0.10 < ratio < 0.16


def test_ducked_chime_is_still_audible():
    # Ducked, not muted: the alarm must still read as ringing while the user
    # speaks over it, or the duck is indistinguishable from a dismissal.
    s = _samples(t.alarm_pcm(48000, gain_db=t.DUCK_DB))
    assert max(abs(x) for x in s) > 1500


def test_duck_preserves_burst_length_and_edges():
    # The ring loop swaps between these two mid-ring, so a length change would
    # shift the cadence and a non-zero edge would click at the swap.
    full = t.alarm_pcm(48000)
    duck = t.alarm_pcm(48000, gain_db=t.DUCK_DB)
    assert len(full) == len(duck)
    s = _samples(duck)
    assert abs(s[0]) < 100
    assert abs(s[-1]) < 100


def test_positive_gain_never_boosts():
    # Guards the min(0.0, …) clamp: a duck must never turn into a boost, which
    # on a chime already at 0.5 full scale would clip.
    assert t.alarm_pcm(48000, gain_db=6.0) == t.alarm_pcm(48000)


# ── Spoken dismissal ─────────────────────────────────────────────────────────
# HA discards a timer when it finishes, so a spoken "stop" over a ringing alarm
# reaches HA and is answered "there are no timers" — no CANCELLED is ever sent
# (measured 2026-08-13). The dismissal is therefore recognised from the
# transcript, and only ever while an alarm is actually ringing.

@pytest.mark.parametrize("text", [
    "stop",
    "Stop.",
    " Stop. ",
    "cancel the timer",
    "Cancel the timer.",
    "dismiss",
    "turn it off",
    "shut up",
    "that's enough",
    "ok ok",
    "I'm up",
])
def test_dismissal_phrases_are_recognised(text):
    assert t.is_dismissal(text) is True


@pytest.mark.parametrize("text", [
    "",
    "   ",
    "what's the weather",
    "set a timer for five minutes",
    "how much time is left",
    "turn on the kitchen light",
    "play some jazz",
])
def test_non_dismissals_reach_ha(text):
    # A real command spoken over a ringing alarm must still go to HA — the
    # matcher is generous, not indiscriminate.
    assert t.is_dismissal(text) is False


def test_dismissal_matches_whole_words_only():
    # "stopwatch" and "offer" contain dismissal words as substrings; matching
    # on substrings would eat ordinary commands.
    assert t.is_dismissal("start the stopwatch") is False
    assert t.is_dismissal("what's on offer") is False
