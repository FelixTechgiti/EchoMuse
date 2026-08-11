"""When a voice turn should stop waiting, as a pure function.

Split out of `em_esphome._stream_mic_audio` for the reason `em_button.decide`
and `em_linkauth.decide` were: the test suite does not import em_esphome, so
this was timing logic with two clocks and two limits and no coverage — and it
cost a real voice turn (#139) before anyone looked at it.

THE TWO CLOCKS ANSWER DIFFERENT QUESTIONS, and conflating them is the bug this
module exists to prevent:

  - "Has the user said anything?" can only be asked once their audio is
    arriving. It is measured from the FIRST REAL FRAME.
  - "Is any audio coming at all?" is measured from turn start, and is about
    the device and the link, not the person.

Measured from turn start alone, a slow link masquerades as a silent user. On
this fleet that is not hypothetical: ordinary WiFi loss (5-7%, on a link whose
actual latency is 6ms) drives TCP retransmission delays of 400-1400ms, and a
measured 1373ms gap before the first frame shortened a 5s window to 3.6s and
answered `no_speech` to someone who had spoken clearly. The device had
captured that audio perfectly and TCP was holding it.
"""

# Seconds of quiet, measured from the first real audio frame, before an
# accidental wake is closed quietly. Matches the device's own noSpeechTimeout.
NO_SPEECH_TIMEOUT = 5.0

# Seconds to wait for the first real frame before giving up on the turn
# entirely. Bounds the other side: audio that never arrives must still end the
# turn, or a device that dies immediately after the wake holds the HA pipeline
# open until HA's own timeout. A healthy device delivers its first
# post-preroll frame in ~300ms, so this only elapses on a genuinely stalled
# link and the ordinary path never reaches it.
FIRST_AUDIO_GRACE = 5.0


def no_speech_verdict(now, turn_start, listening_since, speech_seen,
                      no_speech_timeout=NO_SPEECH_TIMEOUT,
                      first_audio_grace=FIRST_AUDIO_GRACE):
    """Should this turn close quietly as a no-speech?

    All times are monotonic seconds from the same clock.

    `listening_since` is when the first real (post-preroll) frame arrived, or
    None if none has. `speech_seen` is whether anything above the room's noise
    floor has been heard — once true this never closes the turn, because HA's
    VAD owns end-of-turn from that point.

    Returns (close, reason); reason is "" when close is False.
    """
    if speech_seen:
        return False, ""

    if listening_since is not None:
        waited, limit = now - listening_since, no_speech_timeout
        why = f"No speech within {limit}s of the first audio frame"
    else:
        waited, limit = now - turn_start, first_audio_grace
        why = f"No audio at all within {limit}s of turn start"

    if waited > limit:
        return True, why
    return False, ""
