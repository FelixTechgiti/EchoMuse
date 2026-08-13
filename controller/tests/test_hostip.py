"""
Tests for em_hostip — the SERVER_IP resolution policy.

The bug these exist for produced no error anywhere: an unconfigured
controller advertised a hardcoded developer IP over mDNS, so devices
dialled a machine that was not the controller and simply never arrived.
Every assertion below is about refusing to invent an address.
"""

import pytest

import em_hostip


def test_configured_address_wins():
    assert em_hostip.resolve("10.10.1.81", "192.168.0.5") == (
        "10.10.1.81", "configured")


def test_empty_configured_falls_back_to_detection():
    assert em_hostip.resolve("", "192.168.0.5") == ("192.168.0.5", "detected")


def test_none_configured_falls_back_to_detection():
    assert em_hostip.resolve(None, "192.168.0.5") == ("192.168.0.5", "detected")


def test_whitespace_is_not_a_configured_value():
    # The add-on writes "" for an untouched field, but a user who typed a
    # space into the box means "unset", not an address.
    assert em_hostip.resolve("   ", "192.168.0.5") == ("192.168.0.5", "detected")


def test_nothing_configured_and_nothing_detected_refuses():
    with pytest.raises(em_hostip.ServerIPError) as e:
        em_hostip.resolve("", None)
    # The message has to name the fix — this fires on a fresh add-on install
    # where the only person who can act is looking at the add-on log.
    assert "SERVER_IP" in str(e.value)


def test_malformed_address_refuses_rather_than_being_repaired():
    # socket.inet_aton accepts this and yields 10.0.0.1 — an address the
    # operator never typed, advertised to the whole fleet.
    with pytest.raises(em_hostip.ServerIPError):
        em_hostip.resolve("10.10.1", None)


def test_malformed_address_refuses_even_when_detection_would_work():
    # Silently substituting a detected address for a typo'd one hides the
    # typo; the operator would see devices working and the field wrong.
    with pytest.raises(em_hostip.ServerIPError):
        em_hostip.resolve("not-an-ip", "192.168.0.5")


def test_hostnames_are_refused():
    # mDNS advertises A records via socket.inet_aton — a hostname cannot be
    # advertised at all, so accepting one here defers the failure to startup.
    with pytest.raises(em_hostip.ServerIPError):
        em_hostip.resolve("homeassistant.local", None)


def test_no_literal_fallback_address_remains_in_the_module():
    # The regression itself: any hardcoded routable address here is a
    # deployment being sent to somebody else's machine.
    import inspect
    import re

    source = inspect.getsource(em_hostip)
    literals = re.findall(r"\b\d{1,3}(?:\.\d{1,3}){3}\b", source)
    # 192.0.2.1 is RFC 5737 TEST-NET-1, the routing probe, and is never
    # advertised to anything.
    assert set(literals) <= {"192.0.2.1"}, (
        f"unexpected IP literal(s) in em_hostip: {literals}")


def test_detect_returns_an_address_or_none_but_never_raises():
    # Called during startup on hosts we know nothing about; a raise here
    # would be an unhandled crash instead of the actionable ServerIPError.
    result = em_hostip.detect()
    assert result is None or isinstance(result, str)
