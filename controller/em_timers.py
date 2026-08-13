"""
em_timers.py — voice-assistant timer state and alarm audio (pure logic)
========================================================================

Home Assistant owns the timer: "set a timer for one minute" is resolved by
HA's Assist intent, HA counts it down, and HA pushes state to the satellite
as VoiceAssistantTimerEventResponse events (STARTED / UPDATED / CANCELLED /
FINISHED — see esphome/vendor/api_pb2.py). The satellite's only job is to
advertise the TIMERS feature so those intents route to it, track which timers
are live, and RING when one finishes.

This module is the pure half of that, kept out of em_esphome for the same
reason as em_button / em_shadow / em_turnclock — the controller test suite
imports it without pulling in aiohttp/openwakeword:

  * TimerRegistry reduces the event stream into "is a finished timer waiting
    to be dismissed?" and reports the ring transitions (start / stop) that
    the async orchestrator in em_controller acts on.
  * alarm_pcm() synthesises the alarm chime as 48kHz mono S16_LE — the wire
    format the device speaker plane already expects (em_controller
    SPEAKER_RATE) — so the alert needs no firmware support and no bundled
    audio asset.

Dismissal of a RINGING alarm is always local, because HA discards a timer the
moment it finishes and can no longer cancel it: a dot-button tap, or a spoken
dismissal recognised from the transcript (is_dismissal, below). A CANCELLED
event still dismisses too — HA sends one when a timer is cancelled while it is
still counting down — so the registry handles both. Either way the registry is
cleared and the ring stops; a safety cap bounds a ring nobody ever answers.
"""

from __future__ import annotations

import math
import struct

# ── ESPHome VoiceAssistantTimerEvent values ─────────────────────────────────
# Mirrors the api_pb2.VoiceAssistantTimerEvent enum. Reproduced as plain ints
# so this module has no protobuf dependency and stays trivially importable by
# the test environment (numpy/scipy only). Cross-check against
# api_pb2.VoiceAssistantTimerEvent if the vendored protobufs are regenerated.
TIMER_STARTED   = 0
TIMER_UPDATED   = 1
TIMER_CANCELLED = 2
TIMER_FINISHED  = 3

# Ring transitions returned by TimerRegistry.apply().
RING_START = "start"
RING_STOP  = "stop"
RING_NONE  = "none"

# ── Alarm audio parameters ──────────────────────────────────────────────────
SAMPLE_RATE = 48000          # matches em_controller.SPEAKER_RATE (wire rate)
_TONE_HZ    = 880.0          # a clear, non-harsh alert pitch
_BEEP_S     = 0.18           # length of one beep
_GAP_S      = 0.12           # silence between beeps in a burst
_BEEPS      = 3              # beeps per burst
_TAIL_S     = 0.9            # trailing silence so a looped burst is not frantic
_FADE_S     = 0.008          # cosine fade in/out per beep — avoids click artefacts
_AMPLITUDE  = 0.5            # fraction of full scale (headroom against clipping)

# Safety cap: how long a finished timer keeps ringing if nobody dismisses it.
# HA leaves a finished timer ringing indefinitely; the room should not.
MAX_RING_S  = 120.0
# Silence the orchestrator inserts between looped bursts (seconds).
BURST_GAP_S = 0.6

# How far the chime is attenuated once a wake word has been heard over it, so
# the command that follows ("dismiss") reaches STT over the alarm rather than
# under it. Matches the playback duckDb default — same job, same taste call.
DUCK_DB = -18.0
# How long the duck holds with no turn taking over. A wake word that starts no
# turn (a false accept on the chime itself) must not leave the alarm quiet for
# the rest of its 120s cap — the ring is a safety feature.
DUCK_HOLD_S = 12.0

# LED cue while ringing — a distinct pulse, not one of the reserved status
# colours (red=mute, orange=link, cyan=volume). Amber pulse reads as an alert
# without a legend; ttlSec is a dead-man so a controller stall self-clears the
# ring (same contract as em_scenes).
TIMER_ANIM = {
    "pattern":  "pulse",
    "colors":   [[255, 170, 0]],
    "periodMs": 500,
    "ttlSec":   5,
}


class TimerRegistry:
    """
    Per-device timer state, driven by VoiceAssistantTimerEventResponse events.

    Not thread-safe; drive it from a single asyncio task (the satellite's
    message handler). A timer is keyed by HA's timer_id and is either
    "running" or "finished"; the registry is "ringing" while any finished
    timer has not been dismissed.
    """

    def __init__(self) -> None:
        # timer_id -> "running" | "finished"
        self._timers: dict[str, str] = {}

    @property
    def ringing(self) -> bool:
        return any(state == "finished" for state in self._timers.values())

    def apply(self, event_type: int, timer_id: str = "") -> str:
        """
        Fold one event into the registry.

        Returns RING_START when this event begins a ring (first finished timer
        after a quiet registry), RING_STOP when it ends one (the last finished
        timer was dismissed/cancelled), or RING_NONE otherwise.
        """
        was = self.ringing

        if event_type == TIMER_FINISHED:
            self._timers[timer_id] = "finished"
        elif event_type == TIMER_CANCELLED:
            # A CANCELLED for a still-running timer must NOT affect the ring,
            # and a CANCELLED for a finished one is exactly how a spoken "stop"
            # dismisses it — pop covers both.
            self._timers.pop(timer_id, None)
        elif event_type in (TIMER_STARTED, TIMER_UPDATED):
            self._timers[timer_id] = "running"
        # Unknown event types are ignored (degrade to old behaviour).

        now = self.ringing
        if now and not was:
            return RING_START
        if was and not now:
            return RING_STOP
        return RING_NONE

    def clear(self) -> bool:
        """
        Drop all timers (local dismissal). Returns whether it was ringing —
        so a caller can tell "I stopped an alarm" from "nothing was ringing".
        """
        was = self.ringing
        self._timers.clear()
        return was

    def active_count(self) -> int:
        return len(self._timers)


# ── Spoken dismissal ────────────────────────────────────────────────────────
# HA DISCARDS a timer the moment it finishes: cancelling works while one is
# counting down, but once it fires HA's timer manager no longer knows about it
# and answers "there are no timers" to a spoken stop (measured 2026-08-13 —
# 'Stop.' and 'Cancel the timer.' both reached HA correctly over the ringing
# chime and neither produced a CANCELLED event). That is HA's design, not a
# fault: it hands ringing to the satellite and expects the satellite to own
# dismissal, which is how HA's own Voice PE behaves.
#
# So the dismissal is recognised HERE, from the transcript HA already sends us,
# and applied locally. The registry's CANCELLED-while-finished path stays — an
# HA that does send one (or a cancel of a still-running timer) is still handled
# — this is the case HA structurally cannot answer.
_DISMISS_WORDS = (
    "stop", "cancel", "dismiss", "silence", "quiet", "enough",
    "shut up", "turn it off", "turn off", "shut it off", "off",
    "okay okay", "ok ok", "alright already", "im up",
)


def is_dismissal(text: str) -> bool:
    """
    Whether an utterance spoken OVER a ringing alarm means "make it stop".

    Deliberately generous, because it is only ever consulted while an alarm is
    actually ringing — in that context almost anything a person says is about
    the alarm, and the cost of a miss (the alarm keeps going and HA answers
    "there are no timers") is worse than the cost of a false positive (an
    alarm stops that was going to be stopped seconds later anyway).

    It is NOT generous enough to eat a real command, though: "set a timer for
    five minutes" while one rings must still reach HA, which is why this
    matches words rather than simply treating every utterance as a dismissal.
    """
    if not text:
        return False
    # Normalise: lowercase, DROP apostrophes (so "I'm" is one word, not "i m"
    # — the straight and curly forms both, since STT emits either), turn the
    # rest of the punctuation into spaces, collapse runs.
    lowered = text.lower().replace("'", "").replace("’", "")
    cleaned = "".join(c if c.isalnum() or c.isspace() else " " for c in lowered)
    padded  = f" {' '.join(cleaned.split())} "
    if not padded.strip():
        return False
    return any(f" {w} " in padded for w in _DISMISS_WORDS)


def _beep(sample_rate: int) -> list[float]:
    n = int(round(_BEEP_S * sample_rate))
    fade = max(1, int(round(_FADE_S * sample_rate)))
    out = [0.0] * n
    two_pi_f = 2.0 * math.pi * _TONE_HZ / sample_rate
    for i in range(n):
        s = math.sin(two_pi_f * i)
        # Raised-cosine fade at both ends so the beep starts and ends at zero.
        if i < fade:
            s *= 0.5 - 0.5 * math.cos(math.pi * i / fade)
        elif i >= n - fade:
            j = n - 1 - i
            s *= 0.5 - 0.5 * math.cos(math.pi * j / fade)
        out[i] = s * _AMPLITUDE
    return out


def alarm_pcm(sample_rate: int = SAMPLE_RATE, gain_db: float = 0.0) -> bytes:
    """
    One alarm burst as 48kHz mono S16_LE PCM: _BEEPS beeps then trailing
    silence. The orchestrator loops this until the timer is dismissed.

    gain_db attenuates the burst (<= 0). It exists so a wake word spoken over
    a ringing alarm can duck the chime for the length of the turn: the person
    has to be heard saying "dismiss" over it, and the chime is otherwise the
    loudest thing at the mic by far. Detection itself happens at FULL volume
    (AEC plus the barge-in threshold, same as speech over TTS) — the duck is
    for what comes after the wake word, not for the wake word.
    """
    beep = _beep(sample_rate)
    gain = 10.0 ** (min(0.0, gain_db) / 20.0)
    if gain != 1.0:
        beep = [s * gain for s in beep]
    gap  = [0.0] * int(round(_GAP_S * sample_rate))
    tail = [0.0] * int(round(_TAIL_S * sample_rate))

    samples: list[float] = []
    for k in range(_BEEPS):
        samples.extend(beep)
        if k != _BEEPS - 1:
            samples.extend(gap)
    samples.extend(tail)

    # Clamp then quantise to int16.
    pcm = bytearray()
    for s in samples:
        v = int(max(-1.0, min(1.0, s)) * 32767.0)
        pcm += struct.pack("<h", v)
    return bytes(pcm)
