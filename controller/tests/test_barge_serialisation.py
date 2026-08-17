"""
The ESPHome voice protocol serialises pipeline runs at the SATELLITE or not
at all.

`VoiceAssistantEventResponse` carries `event_type` and a `data` list of
name/value pairs — there is no run identifier anywhere in the protocol, so a
client structurally cannot attribute an event to a particular run. Home
Assistant does not enforce one-run-at-a-time either: `handle_pipeline_start`
clears the audio queue and cancels the TTS streaming task, then overwrites
`_pipeline_task` WITHOUT cancelling the previous one. Starting a second run
therefore orphans the first, which keeps emitting events onto the same
connection.

Barge-in is the only place two runs can overlap, and it did. Measured on
2026-08-17: five barge-ins, five interrupting turns dead in 4-17ms with zero
audio captured, because the aborted run's RUN_END arrived ~4ms after the new
turn started and the "HA ended a run it never started" branch read it as
terminal.

Two halves are pinned here, and both are needed:

  * the abort actually reaches HA — `VoiceAssistantRequest(start=False)`,
    which aioesphomeapi maps to `handle_stop(True)` → `_abort_pipeline()`;
  * the new turn discards the old run's tail until its own RUN_START.

The second half must NOT swallow the setup-flow's genuine
RUN_END-without-RUN_START, which is a real thing HA does and which
`_ha_never_started` exists to catch — so the barrier is armed only by an
abort, and only for one turn.
"""

import re
from pathlib import Path

import em_esphome
from esphome.vendor import api_pb2
from esphome.vendor.api_pb2 import VoiceAssistantEvent as ET

ESPHOME_SRC = Path(em_esphome.__file__).read_text()


class FakeTransport:
    def __init__(self, closing=False):
        self._closing = closing

    def is_closing(self):
        return self._closing


def make_satellite():
    """
    A satellite with just enough state to drive _handle_voice_event and
    cancel_turn. Built without __init__ deliberately: the real constructor
    wants a server, a transport and a device registry, none of which this
    behaviour touches.
    """
    sat = object.__new__(em_esphome.EchoMuseSatellite)
    sat._log_name = "test"
    sat._transport = FakeTransport()
    sat.sent = []
    sat._send_one = sat.sent.append
    # Per-turn state, as run_esphome_voice_turn's reset block leaves it.
    sat._turn_active = True
    sat._turn_cancelled = False
    sat._intent_ended = False
    sat._run_started = False
    sat._ha_never_started = False
    sat._continue_conversation = False
    sat._abort_pending = False
    sat._discard_until_run_start = False
    sat._trace = None
    sat._on_thinking = None
    sat._on_stt_end = None
    sat._tts_audio_url = None
    import asyncio

    sat._tts_event = asyncio.Event()
    sat._ha_vad_end = asyncio.Event()
    sat._ha_vad_start = asyncio.Event()
    return sat


def event(event_type, **data):
    return api_pb2.VoiceAssistantEventResponse(
        event_type=event_type,
        data=[api_pb2.VoiceAssistantEventData(name=k, value=v) for k, v in data.items()],
    )


# ── The abort reaches HA ─────────────────────────────────────────────────────


def test_abort_sends_start_false():
    """
    The one message that reaches into HA's in-flight pipeline. Without it the
    old run is merely ignored locally and carries on emitting.
    """
    sat = make_satellite()
    sat.abort_ha_run()
    assert len(sat.sent) == 1
    msg = sat.sent[0]
    assert isinstance(msg, api_pb2.VoiceAssistantRequest)
    assert msg.start is False


def test_local_only_cancel_sends_nothing():
    """
    The default stays local-only — a button cancel with no turn behind it must
    not abort a pipeline HA is about to answer from.
    """
    sat = make_satellite()
    sat.cancel_turn()
    assert sat.sent == []
    assert sat._turn_cancelled is True
    assert sat._abort_pending is False


def test_cancel_with_abort_sends_start_false_and_arms():
    sat = make_satellite()
    sat.cancel_turn(abort_ha=True)
    assert any(
        isinstance(m, api_pb2.VoiceAssistantRequest) and m.start is False
        for m in sat.sent
    )
    assert sat._turn_cancelled is True
    assert sat._abort_pending is True


def test_abort_arms_even_when_the_transport_is_gone():
    """
    A closed transport means the abort could not be sent, which is exactly
    when a reconnect might replay events at us. Arming is free; not arming
    trades a cheap barrier for the failure it exists to prevent.
    """
    sat = make_satellite()
    sat._transport = FakeTransport(closing=True)
    sat.abort_ha_run()
    assert sat.sent == []
    assert sat._abort_pending is True


def test_abort_is_a_no_op_on_an_inactive_turn():
    sat = make_satellite()
    sat._turn_active = False
    sat.cancel_turn(abort_ha=True)
    assert sat.sent == []
    assert sat._abort_pending is False


# ── The barrier: discard the aborted run's tail ──────────────────────────────


def test_stale_run_end_does_not_end_the_new_turn():
    """
    The measured failure, reproduced: the aborted run's RUN_END lands a few ms
    after the interrupting turn starts. Before the barrier this set
    _ha_never_started and the turn reported pipeline_refused with no audio.
    """
    sat = make_satellite()
    sat._discard_until_run_start = True
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_RUN_END))
    assert sat._ha_never_started is False
    assert sat._tts_event.is_set() is False


def test_stale_vad_end_does_not_stop_the_new_turns_mic():
    """
    The same second of the same log also showed a stale STT_VAD_END landing on
    the new turn, which stops mic streaming — so the interrupting turn would
    have captured nothing even if it had survived the RUN_END.
    """
    sat = make_satellite()
    sat._discard_until_run_start = True
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_STT_VAD_END))
    assert sat._ha_vad_end.is_set() is False


def test_stale_error_does_not_reach_the_new_turn():
    """
    The orphaned run's real ending arrived ~470ms later as
    stt-no-text-recognized. On the new turn that unblocks both waiters.
    """
    sat = make_satellite()
    sat._discard_until_run_start = True
    sat._handle_voice_event(
        event(ET.VOICE_ASSISTANT_ERROR, code="stt-no-text-recognized")
    )
    assert sat._tts_event.is_set() is False
    assert sat._ha_vad_end.is_set() is False


def test_run_start_drops_the_barrier_and_is_itself_processed():
    """
    RUN_START is the barrier's release AND a real event — swallowing it would
    leave _run_started False and re-arm the very bug this fixes for the
    turn's own terminal RUN_END.
    """
    sat = make_satellite()
    sat._discard_until_run_start = True
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_RUN_START))
    assert sat._discard_until_run_start is False
    assert sat._run_started is True


def test_events_after_run_start_are_processed_normally():
    sat = make_satellite()
    sat._discard_until_run_start = True
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_RUN_END))       # stale
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_RUN_START))     # ours
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_INTENT_END))
    assert sat._intent_ended is True
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_RUN_END))       # ours, terminal
    assert sat._tts_event.is_set() is True


# ── What the barrier must not break ──────────────────────────────────────────


def test_unarmed_run_end_without_run_start_is_still_terminal():
    """
    HA's wake-word interception (the voice satellite setup dialog) emits
    RUN_END having never started a pipeline. That is genuinely terminal and
    holding the mic through it is what stalled the setup flow for the life of
    the feature. The barrier is armed only by an abort, so this path is
    untouched.
    """
    sat = make_satellite()
    assert sat._discard_until_run_start is False
    sat._handle_voice_event(event(ET.VOICE_ASSISTANT_RUN_END))
    assert sat._ha_never_started is True
    assert sat._tts_event.is_set() is True


# ── Handoff and lifetime, pinned against the source ──────────────────────────


def test_turn_reset_hands_the_arm_over_rather_than_clearing_it():
    """
    The arm is set during the PREVIOUS turn (at the barge) and has to survive
    the next turn's reset block to reach the turn it protects. Resetting it to
    False alongside its neighbours is the obvious tidy-up and would silently
    restore the bug, with every test above still passing.
    """
    m = re.search(
        r"self\._discard_until_run_start = self\._abort_pending\s*\n"
        r"\s*self\._abort_pending\s*= False",
        ESPHOME_SRC,
    )
    assert m, (
        "run_esphome_voice_turn must hand _abort_pending to "
        "_discard_until_run_start, not clear it"
    )
    assert "self._discard_until_run_start = False\n" in ESPHOME_SRC, (
        "the barrier must be cleared when the turn ends — it is bounded to "
        "exactly one turn, or a lost RUN_START deafens the satellite forever"
    )


def test_barge_during_thinking_aborts_upstream():
    """
    em_controller's barge watcher: the thinking branch starts another turn on
    this connection, so it must abort HA's run first. The playback branch must
    serialise too — RUN_END follows TTS_END and a barge in the first
    milliseconds of audio can beat it.
    """
    src = (Path(em_esphome.__file__).parent / "em_controller.py").read_text()
    watcher = src[src.index("async def _barge_watcher") :]
    watcher = watcher[: watcher.index("\nasync def ", 10)]
    assert "abort_ha=True" in watcher, (
        "barge during thinking must abort HA's pipeline before the "
        "interrupting turn starts"
    )
    assert "abort_ha_run" in watcher, (
        "barge during playback must serialise against the interrupting turn"
    )
