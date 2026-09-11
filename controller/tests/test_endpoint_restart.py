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
