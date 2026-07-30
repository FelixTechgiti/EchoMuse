"""
On-device wake word shadow mode — correlating two detectors on one utterance.

A device in shadow mode runs the same wake model over the same audio and
reports when it crosses the threshold, without acting on it. This module holds
the bookkeeping that turns those reports into an answer to the question worth
asking: *when the controller woke, did the device agree, and how far apart were
they?*

It lives apart from em_controller for two reasons. It is the only part of the
feature with any subtlety — clock domains, match windows, consuming a match so
two turns cannot claim it — and em_controller cannot be imported by the test
suite without dragging in openwakeword and aiohttp.

Clocks are the crux. An Echo boots with a bogus wall clock before NTP, so the
device never sends a timestamp: it reports how long AGO a crossing happened, on
its own monotonic clock, and this module converts that against the controller's
monotonic clock on arrival. Same reasoning as the control-plane RTT
instrumentation, and the reason a device with the wrong date still produces
usable comparisons.
"""

import collections
import time

# How far apart the device's crossing and the controller's detection may be and
# still be considered the same utterance.
#
# 2s is deliberately loose. Both detectors see the same frames, but not in the
# same detector STATE: the controller drops wake frames while a turn or TTS is
# in flight, so its feature ring can lag the device's by more than a frame or
# two. Too tight and real agreements are recorded as misses — the more
# misleading of the two possible errors, because a false "miss" argues against
# a feature that is actually working.
MATCH_WINDOW_S = 2.0

# Ceiling on a reported crossing age, so a stale or malformed report cannot be
# projected backwards onto an unrelated wake.
MAX_AGE_S = 10.0

# Crossings are rare — the device applies a refractory period, so one per
# utterance — and only the most recent few can ever fall inside a match window.
# A bounded deque therefore needs no sweeper.
RING = 32


def now() -> float:
    """
    The one clock for shadow correlation.

    Both sides of the comparison — the controller's wake instant and the
    converted crossing timestamps — must come from HERE. They are compared by
    subtraction, so two callers reaching for "a monotonic clock" independently
    is a correctness bug waiting to happen, and a quiet one: CPython's
    asyncio loop.time() happens to BE time.monotonic() today, which means a
    mismatch would produce plausible deltas right up until it didn't.

    (It is also how the original version of this broke, differently: reaching
    for time.monotonic() in em_controller, which does not import time, killed
    the wake listener on the first detection.)
    """
    return time.monotonic()


class ShadowTracker:
    """Per-device record of on-device crossings, and the matching logic."""

    def __init__(self, maxlen: int = RING):
        self._crossings: collections.deque = collections.deque(maxlen=maxlen)
        # True while the device's stats reports carry a shadow summary, i.e. the
        # device is known to be scoring. This is what separates "the device did
        # not detect this" from "the device was not looking" — a distinction a
        # NULL score cannot make on its own, and the reason turns store a
        # dev_shadow flag alongside the score.
        self.active: bool = False
        # The crossing threshold the device last reported it was using. Needed
        # to judge a NON-crossing: the controller drops its own bar to
        # bargeInThreshold during playback, and a device scoring against the
        # normal threshold was never asked the same question. Without this,
        # every barge-in was recorded as an on-device miss.
        self.threshold: float | None = None

    def record_cross(self, score, age_ms, at_now: float | None = None) -> bool:
        """
        Note a crossing reported by the device. Returns whether it was accepted.

        A malformed report is dropped rather than coerced: a score that will not
        parse means the message is not what we think it is, and inventing a 0.0
        would quietly become a data point.
        """
        try:
            score = float(score)
            age_s = float(age_ms or 0) / 1000.0
        except (TypeError, ValueError):
            return False
        # A wild age is clamped, not rejected: a slightly stale report is still
        # evidence, but one claiming to be a minute old would otherwise be
        # projected onto a wake it has nothing to do with.
        age_s = max(0.0, min(age_s, MAX_AGE_S))
        at = (at_now if at_now is not None else now()) - age_s
        self._crossings.append((at, score))
        return True

    def match(self, wake_mono: float):
        """
        Find the crossing corresponding to a controller wake at `wake_mono`.

        Returns (score, delta_ms), or (None, None) if nothing is in range.
        delta_ms is SIGNED, and negative is the expected direction: the device
        scores the frame it just captured, while the controller scores the same
        frame after a network hop.

        The nearest crossing wins, and the match is CONSUMED — two turns in
        quick succession must not both claim one crossing, the same discipline
        the playback_stats attachment follows.
        """
        best_i = best_delta = None
        for i, (at, _score) in enumerate(self._crossings):
            delta = at - wake_mono
            if abs(delta) > MATCH_WINDOW_S:
                continue
            if best_delta is None or abs(delta) < abs(best_delta):
                best_i, best_delta = i, delta
        if best_i is None:
            return None, None
        _at, score = self._crossings[best_i]
        del self._crossings[best_i]
        return round(score, 4), int(round(best_delta * 1000))

    def pending(self) -> int:
        """How many unmatched crossings are held. Diagnostics only."""
        return len(self._crossings)
