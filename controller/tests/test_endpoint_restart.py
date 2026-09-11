"""
Tests for em_endpoint_restart.decide — whether installing a binary should
re-execute the endpoint still running the old one.

Both wrong answers cost something, which is why this is a tested decision
rather than an `if` in a handler: restarting when somebody is listening cuts
their music, and not restarting when they are not silently wastes the install
and leaves the old code running behind a success message.
"""

import em_endpoint_restart as r


def test_old_firmware_is_never_told_it_restarted():
    """
    An unknown control message is ignored SILENTLY at both ends. Asking would
    produce no restart and no error — and reporting one would be a specific
    false claim about a process still executing the old inode, which is the
    exact failure this feature exists to end with a reassuring sentence on
    top.
    """
    d = r.decide(kind="airplay", capable=False,
                 health={"enabled": True, "alive": True}, audio_source=None)
    assert d.restart is False
    assert "firmware" in d.reason
    # And it must say what the user can do instead, or the message is just
    # an apology.
    assert "toggle" in d.reason.lower()


def test_a_playing_endpoint_is_left_alone():
    """
    The case the original comment was about, and it was right: killing a
    receiver somebody is listening to, to update a file nobody has asked to
    switch to yet, is the more surprising behaviour.
    """
    d = r.decide(kind="airplay", capable=True,
                 health={"enabled": True, "alive": True},
                 audio_source="airplay")
    assert d.restart is False
    assert "playing" in d.reason


def test_a_different_source_playing_does_not_protect_this_one():
    """
    Spotify playing says nothing about whether anyone is listening to AirPlay.
    Reading "something is playing" as "leave everything alone" would make the
    install a no-op whenever the Echo happened to be in use at all.
    """
    d = r.decide(kind="airplay", capable=True,
                 health={"enabled": True, "alive": True},
                 audio_source="spotify")
    assert d.restart is True


def test_an_idle_enabled_endpoint_is_restarted():
    """The common case, and the whole point."""
    d = r.decide(kind="spotify", capable=True,
                 health={"enabled": True, "alive": True}, audio_source=None)
    assert d.restart is True
    assert "restarted" in d.reason


def test_a_disabled_endpoint_is_not_restarted():
    """
    Nothing is running, so the next Start opens the new file by itself.
    Restarting would be a no-op dressed as an action.
    """
    d = r.decide(kind="spotify", capable=True,
                 health={"enabled": False, "alive": False}, audio_source=None)
    assert d.restart is False
    assert "turned off" in d.reason


def test_an_endpoint_between_attempts_is_not_restarted():
    """
    Enabled but not alive: the supervisor is already about to exec, and it
    will exec the new file. Killing nothing and calling it a restart is the
    kind of claim this module exists to avoid.
    """
    d = r.decide(kind="spotify", capable=True,
                 health={"enabled": True, "alive": False}, audio_source=None)
    assert d.restart is False


def test_not_knowing_is_not_the_same_as_knowing_it_is_idle():
    """
    Capable firmware that has not sent its first stats tick yet — up to ~30s
    after every connect. The cost of guessing wrong here is cutting somebody's
    music, so absence of a report is not taken as absence of playback.
    """
    d = r.decide(kind="airplay", capable=True, health=None, audio_source=None)
    assert d.restart is False
    assert "not reported" in d.reason or "has not reported" in d.reason


def test_every_outcome_explains_itself():
    """
    The reason is shown to whoever pressed Install. An empty or generic one
    turns "nothing happened" into a mystery, which is the state this whole
    feature exists to leave behind.
    """
    cases = [
        dict(capable=False, health=None, audio_source=None),
        dict(capable=True, health=None, audio_source=None),
        dict(capable=True, health={"enabled": False}, audio_source=None),
        dict(capable=True, health={"enabled": True, "alive": True},
             audio_source="airplay"),
        dict(capable=True, health={"enabled": True, "alive": False},
             audio_source=None),
        dict(capable=True, health={"enabled": True, "alive": True},
             audio_source=None),
    ]
    for c in cases:
        d = r.decide(kind="airplay", **c)
        assert d.reason and len(d.reason) > 20, f"thin reason for {c}: {d.reason!r}"


# ─── The gate: EVERY install path has to restart, not just the clicked one ───

import ast
from pathlib import Path

_API = Path(__file__).resolve().parents[1] / "em_api.py"


def _installers() -> dict[str, ast.AST]:
    """
    Every function in em_api that writes an endpoint binary onto a device.

    Found by what the code DOES — a `_stream_file_to_device(..., k.dest, ...)`
    — rather than by a written-out list, because a list is the thing that goes
    stale the day somebody adds a third path.
    """
    tree = ast.parse(_API.read_text())
    found = {}
    for node in ast.walk(tree):
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for call in ast.walk(node):
            if not isinstance(call, ast.Call):
                continue
            if getattr(call.func, "id", None) != "_stream_file_to_device":
                continue
            args = [ast.unparse(a) for a in call.args]
            if "k.dest" in args:
                found[node.name] = node
    return found


def test_both_install_paths_are_still_the_only_two():
    """
    The guard is per call site rather than a count, so a third install path
    has to answer the question too instead of riding on the total.
    """
    assert set(_installers()) == {
        "_post_device_endpoint_bin", "_install_endpoint_locked"
    }, ("a new endpoint install path appeared — it must restart the endpoint "
        "running the old inode, see _restart_after_install")


def test_every_install_path_restarts_the_endpoint():
    """
    A rename replaces a directory entry, not the inode a process is
    executing. So an install over a running endpoint reports success, matches
    md5, and leaves the OLD code running indefinitely — and the only symptom
    is that the thing you installed it for still does not work, which is
    indistinguishable from the new binary being broken.

    The hand-clicked install restarted; the automatic on-connect sync did
    not, and that is the path most devices take most of the time. Measured on
    Studio 2026-09-11: shairport-sync 1.1.0 landed at 17:56 and the receiver
    was still 3h8m into the 1.0.0 inode, with the store, the md5 and the
    device's own stat all reporting the new file.
    """
    for name, node in _installers().items():
        calls = {getattr(c.func, "id", None) for c in ast.walk(node)
                 if isinstance(c, ast.Call)}
        assert "_restart_after_install" in calls, (
            f"{name} installs a binary and never restarts the endpoint "
            f"running the old one")


def test_the_restart_is_decided_in_one_place():
    """
    Two copies of the judgement is two that can disagree, and the one that
    disagrees is the automatic path nobody is watching. `decide` is called
    from the shared helper only.
    """
    tree = ast.parse(_API.read_text())
    callers = set()
    for node in ast.walk(tree):
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for call in ast.walk(node):
            if (isinstance(call, ast.Call)
                    and ast.unparse(call.func) == "em_endpoint_restart.decide"):
                callers.add(node.name)
    assert callers == {"_restart_after_install"}, \
        f"em_endpoint_restart.decide is called from {sorted(callers)}"
