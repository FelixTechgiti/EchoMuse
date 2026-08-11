"""The no-speech window must measure the user, not the link.

The regression these guard against is #139: a 1373ms delivery gap before the
first audio frame silently shortened the listening window and answered
`no_speech` to someone who spoke clearly. `test_late_audio_still_gets_a_full_
window` fails against the old turn-start-only logic, which is how it was
verified.
"""
import em_turnclock as tc


def test_speech_seen_never_closes_the_turn():
    # Once anything above the noise floor is heard, HA's VAD owns end-of-turn
    # and this decision must get out of the way — however long it has been.
    close, _ = tc.no_speech_verdict(now=1000.0, turn_start=0.0,
                                    listening_since=0.0, speech_seen=True)
    assert close is False


def test_silence_from_a_prompt_device_closes_on_time():
    # The ordinary accidental wake: audio flowing immediately, nobody speaks.
    # Behaviour here must be exactly what it always was.
    close, why = tc.no_speech_verdict(now=5.3, turn_start=0.0,
                                      listening_since=0.3, speech_seen=False)
    assert close is False, "5.0s of quiet is not yet over the limit"

    close, why = tc.no_speech_verdict(now=5.4, turn_start=0.0,
                                      listening_since=0.3, speech_seen=False)
    assert close is True
    assert "No speech" in why


def test_late_audio_still_gets_a_full_window():
    # THE #139 REGRESSION. Audio arrives 1.373s late, then the user speaks
    # 4s in. Measured from turn start that is 5.4s and the turn is already
    # dead; measured from first audio it is 4.0s and still listening.
    close, _ = tc.no_speech_verdict(now=5.4, turn_start=0.0,
                                    listening_since=1.373, speech_seen=False)
    assert close is False, "a slow link must not shorten the listening window"

    # ...and the window still ENDS, 5s after audio actually started.
    close, why = tc.no_speech_verdict(now=6.5, turn_start=0.0,
                                      listening_since=1.373, speech_seen=False)
    assert close is True
    assert "No speech" in why


def test_audio_that_never_arrives_still_ends_the_turn():
    # A device that dies right after the wake must not hold the HA pipeline
    # open. This is the bound on the fix above.
    close, _ = tc.no_speech_verdict(now=4.9, turn_start=0.0,
                                    listening_since=None, speech_seen=False)
    assert close is False

    close, why = tc.no_speech_verdict(now=5.1, turn_start=0.0,
                                      listening_since=None, speech_seen=False)
    assert close is True
    assert "No audio" in why, "must be distinguishable from a silent user"


def test_the_two_reasons_are_distinguishable():
    # They call for opposite investigations — a quiet room versus a dead link
    # — so the log line must not read the same for both.
    _, no_audio = tc.no_speech_verdict(now=99.0, turn_start=0.0,
                                       listening_since=None, speech_seen=False)
    _, no_speech = tc.no_speech_verdict(now=99.0, turn_start=0.0,
                                        listening_since=1.0, speech_seen=False)
    assert no_audio != no_speech


def test_limits_are_overridable_for_callers_and_tests():
    close, _ = tc.no_speech_verdict(now=2.1, turn_start=0.0,
                                    listening_since=0.0, speech_seen=False,
                                    no_speech_timeout=2.0)
    assert close is True
