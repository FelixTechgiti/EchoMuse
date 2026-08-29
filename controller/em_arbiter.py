"""
em_arbiter.py — multi-device wake arbitration
==============================================

When one utterance wakes more than one Echo (open-plan rooms, hallways),
every device that scored above threshold starts its own voice turn. The
result is not merely duplicated answers: each device then hears the
others' TTS through the room, transcribes it as a follow-up, and HA's
`continue_conversation` reopens the mic — a self-feeding loop that ran
for ~70 seconds on 2026-07-20 before a no-speech timeout broke it.

**First detector wins, among devices that can answer.** The first device
to cross threshold claims the utterance immediately and answers with zero
added latency. Any other device detecting within `window_s` of that claim
loses and stands down. No one ever waits.

The qualifier is `can_answer`: detection order is a proximity proxy, and
proximity says nothing about whether HA is connected to that device's
satellite port. A device with no pipeline behind it takes no claim, so it
cannot stand a working device down in favour of a turn that dies `no_ha`
in milliseconds.

Why not "best SNR", which this module did until 2026-07-20: it forced
every wake to wait out the whole window (~364ms measured, on every
device, because the gate was "2+ devices *connected*" rather than "in
earshot"), and the field data showed the metric was wrong anyway. On the
three-way trigger that exposed all this, SNR at detection was
0.9 / 1.15 / 0.93 — statistically indistinguishable — and the SNR winner
(Lounge) produced "What's the technique?" while the *first* detector
(Office) produced the correct "What's the weather like today?". Sound
reaches the nearer microphone sooner and louder, so it crosses threshold
sooner; detection order is a better proximity proxy than a ratio of two
noisy RMS estimates, and it is free.

`window_s` is now purely a suppression window, not a wait: it bounds how
long a claim silences other devices. It should comfortably exceed the
spread between devices hearing one utterance (~200ms observed, driven by
the device's 160ms mic batching) without being so long that a genuinely
separate wake in another room gets swallowed.

Pure asyncio, no imports from the rest of the controller — unit-tested
in tests/test_arbiter.py.
"""

from __future__ import annotations

import asyncio


class WakeArbiter:
    """
    Tracks the in-flight claim on the current utterance.

    Deliberately tiny and synchronous: claim() decides with no await, so
    the winner's turn starts on the same event-loop tick as its wake
    detection — that is the whole point of the redesign.
    """

    def __init__(self) -> None:
        self._winner: str | None = None
        self._claimed_at: float = 0.0

    def claim(self, device_id: str, window_s: float,
              can_answer: bool = True) -> str:
        """
        Try to claim the current utterance. Returns the winning device_id
        — equal to device_id if this device won and should answer, or
        another device's id if this detection is a duplicate to discard.

        Returns immediately; there is no waiting on either path.

        `can_answer` is whether this device has a voice pipeline behind it
        (an ESPHome server with HA actually connected). A device that does
        not still gets its own turn — the drop is worth recording, and the
        ring cue is the only thing telling the person in the room why
        nothing happened — but it does NOT take the claim, so it cannot
        silence a device that could have answered. Detection order is a
        proximity proxy, not a capability one: the nearest Echo crosses
        threshold first whether or not HA has ever dialled its port, and
        without this the whole fleet stands down behind a device that then
        dies `no_ha` in milliseconds. The user sees every device stay dark
        and one flash a fault, on a system where one of them was ready.

        Note an incapable device still CEDES to a live claim (the branch
        below is unconditional): another device is already answering, so
        flashing a fault beside it would be noise about a turn that is
        being served.
        """
        now = asyncio.get_running_loop().time()
        held = (
            self._winner is not None
            and now - self._claimed_at < window_s
        )
        if held and self._winner != device_id:
            return self._winner
        if not can_answer:
            # Proceed, but leave the window open for whoever can. Crucially
            # this does not re-arm a claim this device may already hold —
            # it never takes one.
            return device_id
        # Either nothing is claimed, the claim has expired, or this is the
        # same device waking again (a genuinely new utterance in the room
        # that already answered). Re-arm the window from now.
        self._winner = device_id
        self._claimed_at = now
        return device_id

    def release(self, device_id: str) -> None:
        """
        Drop a claim once its turn is over, so an immediate follow-up from
        another device isn't suppressed by a stale window. Ignores calls
        from a device that doesn't hold the claim.
        """
        if self._winner == device_id:
            self._winner = None
            self._claimed_at = 0.0
