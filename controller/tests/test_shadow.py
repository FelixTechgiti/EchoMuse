"""
Correlating on-device wake crossings with the controller's own detections.

The arithmetic here is small but the failure modes are quiet: a match window
that is too tight records working agreement as a miss, and a match that is not
consumed lets one crossing vouch for two turns. Both produce a plausible
report, which is why they are tested rather than eyeballed.
"""

import em_shadow


def tracker():
    return em_shadow.ShadowTracker()


def test_match_returns_score_and_signed_delta():
    t = tracker()
    t.active = True
    # Device crossed 120ms BEFORE the controller — the expected direction, since
    # the device scores the frame it just captured and the controller scores the
    # same frame after a network hop.
    t.record_cross(0.83, age_ms=0, at_now=1000.0)
    score, delta = t.match(wake_mono=1000.12)

    assert score == 0.83
    assert delta == -120, "device-first must read as a negative delta"


def test_age_is_projected_backwards_onto_the_report():
    """The device reports how long AGO it crossed, because its wall clock is
    not trustworthy before NTP. A report that took 200ms to arrive describes a
    crossing 200ms before it landed, not at arrival."""
    t = tracker()
    t.record_cross(0.7, age_ms=200, at_now=500.0)
    score, delta = t.match(wake_mono=499.8)
    assert score == 0.7
    assert delta == 0, "a 200ms-old report at t=500 describes a crossing at 499.8"


def test_no_crossing_in_window_is_a_miss_not_an_error():
    t = tracker()
    t.record_cross(0.9, age_ms=0, at_now=100.0)
    # Far outside the window: a different utterance entirely.
    score, delta = t.match(wake_mono=100.0 + em_shadow.MATCH_WINDOW_S + 1.0)
    assert (score, delta) == (None, None)


def test_boundary_of_the_match_window():
    t = tracker()
    t.record_cross(0.6, age_ms=0, at_now=0.0)
    just_in = em_shadow.MATCH_WINDOW_S - 0.01
    assert t.match(wake_mono=just_in)[0] == 0.6

    t.record_cross(0.6, age_ms=0, at_now=0.0)
    just_out = em_shadow.MATCH_WINDOW_S + 0.01
    assert t.match(wake_mono=just_out)[0] is None


def test_nearest_crossing_wins():
    t = tracker()
    t.record_cross(0.5, age_ms=0, at_now=10.0)   # 1.0s before the wake
    t.record_cross(0.95, age_ms=0, at_now=10.9)  # 0.1s before the wake
    score, delta = t.match(wake_mono=11.0)
    assert score == 0.95, "the nearer crossing describes this utterance"
    assert delta == -100


def test_a_match_is_consumed_so_two_turns_cannot_share_it():
    """Two turns in quick succession must not both be credited to one crossing —
    that would report agreement on a turn the device never detected."""
    t = tracker()
    t.record_cross(0.88, age_ms=0, at_now=50.0)

    first = t.match(wake_mono=50.05)
    second = t.match(wake_mono=50.10)

    assert first[0] == 0.88
    assert second == (None, None)
    assert t.pending() == 0


def test_only_the_matched_crossing_is_consumed():
    t = tracker()
    t.record_cross(0.4, age_ms=0, at_now=0.0)
    t.record_cross(0.9, age_ms=0, at_now=100.0)
    assert t.match(wake_mono=100.0)[0] == 0.9
    # The unrelated one is still available for its own turn.
    assert t.pending() == 1
    assert t.match(wake_mono=0.0)[0] == 0.4


def test_absurd_age_is_clamped_not_trusted():
    """A stale or malformed age must not be projected onto an unrelated wake."""
    t = tracker()
    t.record_cross(0.9, age_ms=600_000, at_now=1000.0)  # claims 10 minutes ago
    # Clamped to MAX_AGE_S, so it lands there and not 600s back.
    score, _ = t.match(wake_mono=1000.0 - em_shadow.MAX_AGE_S)
    assert score == 0.9


def test_malformed_report_is_dropped_not_coerced():
    """Inventing a 0.0 score for an unparseable report would silently become a
    data point arguing the device detected nothing."""
    t = tracker()
    assert t.record_cross("not-a-number", age_ms=0) is False
    assert t.record_cross(None, age_ms=0) is False
    assert t.pending() == 0


def test_missing_age_is_treated_as_now():
    t = tracker()
    assert t.record_cross(0.75, age_ms=None, at_now=42.0) is True
    assert t.match(wake_mono=42.0) == (0.75, 0)


def test_ring_is_bounded():
    """Crossings arrive without any turn to consume them (false accepts in an
    empty room), so the ring must not grow without bound."""
    t = em_shadow.ShadowTracker(maxlen=4)
    for i in range(20):
        t.record_cross(0.9, age_ms=0, at_now=float(i))
    assert t.pending() == 4


def test_active_defaults_false():
    """A device is not assumed to be scoring: `active` is what stops a missing
    per-turn score from being read as a miss."""
    assert tracker().active is False
