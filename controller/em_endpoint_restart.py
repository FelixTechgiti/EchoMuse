"""
em_endpoint_restart.py — whether installing a binary should re-execute the
endpoint that is running the old one.

# The bug this exists for

Installing over a running endpoint replaces a DIRECTORY ENTRY, not the inode
the process is executing. So the new binary lands, the install reports
success, md5 matches — and the process carries on running the old code
indefinitely. The only symptom is that the thing you installed it for still
does not work, which is indistinguishable from the new binary being broken.

Noted as "known, not fixed here" when librespot's missing discovery was fixed
(#24), and met again the day the metadata-capable shairport-sync shipped:
without this, installing it would have left the previous build running and the
AirPlay volume feature dead, with every panel reporting a successful install.

# Why the decision is here and not on the device

The comment that justified doing nothing was written about a PLAYING stream,
and it is right about that case: killing a receiver somebody is listening to,
to update a file nobody has asked to switch to yet, is the more surprising
behaviour. It is wrong about the common case, where the endpoint is idle and
not restarting it silently wastes the install.

Telling those apart needs to know who owns the music plane, and that is the
CONTROLLER'S knowledge — `audio_source` exists precisely because no frame of
an endpoint's audio passes through here, so the device reports it. The device
gets the verb; this is the judgement.

Pure and tested, for `em_linkauth.decide`'s reason: it is a decision about
somebody's audio and the wrong answer in one direction interrupts music while
the wrong answer in the other ships a lie.
"""

from __future__ import annotations

from typing import NamedTuple, Optional


class Decision(NamedTuple):
    """What to do, and the sentence explaining it to whoever installed."""
    restart: bool
    reason: str


def decide(
    *,
    kind: str,
    capable: bool,
    health: Optional[dict],
    audio_source: Optional[str],
) -> Decision:
    """
    Whether to ask the device to re-execute `kind` after an install.

    `health` is this endpoint's entry from the device's endpoint health
    report, or None when the firmware does not report it. `audio_source` is
    which source owns the music plane right now, or None when unknown.
    """
    if not capable:
        # Old firmware ignores an unknown control message SILENTLY, so asking
        # would produce no restart and no error — and reporting one would be
        # a specific false claim about a process still running the old inode.
        return Decision(False, (
            "This Echo's firmware cannot restart an endpoint on request, so "
            "the new binary starts being used the next time the endpoint does "
            "— toggle it off and on, or reboot."
        ))

    if health is None:
        # Capable but nothing reported yet (up to ~30s after a connect). Not
        # knowing is not the same as knowing it is idle, and the cost of
        # guessing wrong here is cutting somebody's music.
        return Decision(False, (
            "The Echo has not reported its endpoint state yet, so nothing was "
            "restarted. Install again in a moment, or toggle the endpoint."
        ))

    if not health.get("enabled"):
        # Nothing is running: the next Start opens the new file by itself.
        # Restarting would be a no-op dressed as an action.
        return Decision(False, (
            f"{kind} is turned off on this Echo, so the new binary will be "
            f"used as soon as you turn it on."
        ))

    if audio_source == kind:
        # Somebody is listening RIGHT NOW. This is the case the original
        # comment was about and it was right.
        return Decision(False, (
            f"{kind} is playing right now, so it was left alone. The new "
            f"binary takes over when playback ends and it restarts."
        ))

    if not health.get("alive"):
        # Enabled but between attempts — the supervisor is already about to
        # exec, and it will exec the new file.
        return Decision(False, (
            f"{kind} is not running at the moment, so the supervisor will "
            f"start the new binary by itself."
        ))

    return Decision(True, (
        f"{kind} was restarted so the new binary is the one running."
    ))
