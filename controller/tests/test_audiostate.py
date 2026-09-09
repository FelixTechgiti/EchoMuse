"""
The audio-state aggregation and its hold-off.

The feature it serves is an amplifier wired to the Dot's jack, switching its
input when the Echo has something to play and switching back when it stops.
Everything interesting here is about WHEN, so the clock is injected and no
test sleeps.
"""

import pytest

import em_audiostate as A


def live(**kw):
    return A.Inputs(**kw)


# ── which source wins ────────────────────────────────────────────────────────

def test_nothing_playing_is_none():
    assert A.source_of(live()) == A.SOURCE_NONE
    assert not A.is_live(live())


def test_voice_outranks_everything_because_the_mixer_does():
    """A turn DUCKS music rather than pausing it, so when both are audible the
    voice is what is being listened to."""
    inp = live(speaking=True, media=True, local_source=A.SOURCE_SPOTIFY)
    assert A.source_of(inp) == A.SOURCE_VOICE


def test_thinking_counts_as_voice():
    """
    The amp has to be on the right input BEFORE the answer starts: HA still
    has to run STT, the intent and TTS, and an amplifier takes a moment to
    settle. Thinking is the earliest honest signal that something is coming.
    """
    assert A.source_of(live(thinking=True)) == A.SOURCE_VOICE
    assert A.is_live(live(thinking=True))


def test_controller_media_outranks_a_local_source():
    inp = live(media=True, local_source=A.SOURCE_AIRPLAY)
    assert A.source_of(inp) == A.SOURCE_MEDIA


@pytest.mark.parametrize("src", A.LOCAL_SOURCES)
def test_each_local_source_is_reported_by_name(src):
    assert A.source_of(live(local_source=src)) == src


def test_an_unknown_local_source_reads_as_silence_not_as_a_new_name():
    """
    A source this controller cannot describe is one nobody can automate
    against. Reporting it verbatim would put an arbitrary string into
    somebody's dashboard; reporting it as playing would switch an amplifier
    for something we cannot name.
    """
    assert A.source_of(live(local_source="gramophone")) == A.SOURCE_NONE


def test_absence_is_not_unknown_it_is_not_playing():
    """
    None means the device has not said — old firmware, or one that has not
    registered yet. The compatibility rule is to degrade to what the
    controller can see for itself, never to a wrong answer.
    """
    assert A.source_of(live(local_source=None)) == A.SOURCE_NONE
    assert A.source_of(live(speaking=True, local_source=None)) == A.SOURCE_VOICE


# ── the hold-off ─────────────────────────────────────────────────────────────

def test_rising_is_immediate():
    st = A.AudioState(holdoff_ms=5000)
    assert st.update(live(speaking=True), now=100.0) is True
    assert st.active and st.source == A.SOURCE_VOICE


def test_falling_waits_for_the_holdoff():
    st = A.AudioState(holdoff_ms=5000)
    st.update(live(speaking=True), now=100.0)

    assert st.update(live(), now=101.0) is False, "reported the gap immediately"
    assert st.active, "an amplifier would have switched away after one second"

    assert st.update(live(), now=105.9) is False
    assert st.active

    assert st.update(live(), now=106.0) is True
    assert not st.active and st.source == A.SOURCE_NONE


def test_audio_returning_inside_the_holdoff_never_reports_a_gap():
    """
    The case the hold-off exists for: a turn ends and an announcement follows.
    Nothing should reach Home Assistant between them.
    """
    st = A.AudioState(holdoff_ms=5000)
    st.update(live(speaking=True), now=100.0)
    assert st.update(live(), now=101.0) is False
    assert st.update(live(speaking=True), now=102.0) is False, "re-reported a state it was already in"
    assert st.active
    # And the hold-off is not still counting from the first stop.
    assert st.update(live(), now=103.0) is False
    assert st.update(live(), now=107.5) is False
    assert st.update(live(), now=108.1) is True


def test_the_source_is_held_while_the_holdoff_is():
    """The amp is still on that input; reporting `none` while the entity is
    still on would be two halves of one state disagreeing."""
    st = A.AudioState(holdoff_ms=5000)
    st.update(live(local_source=A.SOURCE_SPOTIFY), now=100.0)
    st.update(live(), now=101.0)
    assert st.active and st.source == A.SOURCE_SPOTIFY


def test_a_source_change_while_active_is_reported():
    st = A.AudioState(holdoff_ms=5000)
    st.update(live(local_source=A.SOURCE_SPOTIFY), now=100.0)
    assert st.update(live(speaking=True, local_source=A.SOURCE_SPOTIFY), now=100.5) is True
    assert st.source == A.SOURCE_VOICE


def test_zero_holdoff_reports_the_stop_at_once():
    st = A.AudioState(holdoff_ms=0)
    st.update(live(speaking=True), now=100.0)
    assert st.update(live(), now=100.0) is True
    assert not st.active


def test_quiet_while_already_quiet_says_nothing():
    st = A.AudioState(holdoff_ms=5000)
    assert st.update(live(), now=100.0) is False
    assert st.update(live(), now=200.0) is False


# ── the deadline the caller arms a timer on ──────────────────────────────────

def test_no_deadline_while_playing_or_while_idle():
    st = A.AudioState(holdoff_ms=5000)
    assert st.deadline(now=100.0) is None
    st.update(live(speaking=True), now=100.0)
    assert st.deadline(now=100.0) is None, "armed a timer for audio that is still playing"


def test_the_deadline_counts_down_rather_than_restarting():
    """
    Asked at any point, not only at the moment the hold-off starts — so it has
    to return what is LEFT. Returning the full hold-off would extend it every
    time an unrelated wake-up asked.
    """
    st = A.AudioState(holdoff_ms=5000)
    st.update(live(speaking=True), now=100.0)
    st.update(live(), now=101.0)
    assert st.deadline(now=101.0) == pytest.approx(5.0)
    assert st.deadline(now=104.0) == pytest.approx(2.0)


def test_an_expired_deadline_is_due_now_not_negative():
    st = A.AudioState(holdoff_ms=5000)
    st.update(live(speaking=True), now=100.0)
    st.update(live(), now=101.0)
    assert st.deadline(now=200.0) == 0.0
