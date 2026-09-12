"""
Tests for em_mdnsscan — what a network scan is allowed to conclude.

The module exists because the same question was answered wrong three times in
one afternoon, and every wrong answer had the same shape: a scan that found
nothing was read as a statement about the device. So that distinction is what
these tests are mostly about.
"""

import em_mdnsscan as m

OURS = "192.168.178.140"


def f(service="spotify", address=OURS, name="EchoDot._spotify-connect._tcp.local.",
      port=44644, target="echodot.local.", txt=None):
    return m.Finding(service, name, address, port, target, txt or {})


def test_visible_when_our_address_answers():
    v = m.verdict("spotify", [f()], OURS, enabled=True)
    assert v.reachable and v.visible
    assert "44644" in v.detail


def test_an_empty_scan_is_never_a_statement_about_the_device():
    """
    The mistake this module exists to prevent, and it was made twice.

    Nothing found, from anybody, means the scan did not work — a quiet
    moment, multicast not reaching this host, a browse that never ran. It is
    NOT evidence that the device is silent, and it must not read as if it is.
    """
    v = m.verdict("spotify", [], OURS, enabled=True)
    assert v.reachable is False, "an empty scan claimed to be a working scan"
    assert v.visible is False
    # The sentence has to say so, not just the flag: the flag is for code and
    # the sentence is what a person acts on.
    assert "not about the device" in v.detail


def test_other_hosts_answering_is_what_makes_a_negative_mean_something():
    others = [f(address="192.168.178.51", name="Denon._spotify-connect._tcp.local."),
              f(address="192.168.178.31", name="Sonos._spotify-connect._tcp.local.")]
    v = m.verdict("spotify", others, OURS, enabled=True)
    assert v.reachable is True, "a scan that found other hosts plainly worked"
    assert v.visible is False
    assert "2 other host" in v.detail


def test_the_two_negatives_never_read_the_same():
    """Same `visible: False`, opposite investigations."""
    empty = m.verdict("spotify", [], OURS, enabled=True)
    others = m.verdict("spotify", [f(address="192.168.178.51")], OURS, enabled=True)
    assert empty.detail != others.detail
    assert empty.reachable != others.reachable


def test_an_unresolvable_target_is_caught_and_named():
    """
    The device shipped advertising `localhost.local` — a complete, correct
    looking record that sends every client to its own loopback. Nothing in
    the whole chain reported it; it took a packet capture to find.
    """
    for bad in ("localhost.", "localhost.local.", "LOCALHOST.LOCAL."):
        v = m.verdict("spotify", [f(target=bad)], OURS, enabled=True)
        assert v.visible is False, f"{bad} was accepted as visible"
        assert "resolves that to itself" in v.detail


def test_a_disabled_endpoint_is_not_reported_as_broken():
    v = m.verdict("airplay", [], OURS, enabled=False)
    assert v.reachable is True and v.visible is False
    assert "switched off" in v.detail


def test_a_finding_without_a_resolved_address_never_counts_as_ours():
    # An unresolved instance has address "", and "" must not match a device
    # whose address we failed to read either.
    v = m.verdict("spotify", [f(address="")], OURS, enabled=True)
    assert v.visible is False


def test_summary_refuses_to_conclude_from_a_failed_scan():
    vs = [m.verdict("spotify", [], OURS, True), m.verdict("airplay", [], OURS, True)]
    s = m.summarise(vs)
    assert "no mDNS on this network at all" in s
    assert "not visible" not in s, "a failed scan was summarised as a finding"


def test_summary_counts_only_what_was_actually_checked():
    vs = [
        m.verdict("spotify", [f()], OURS, True),            # visible
        m.verdict("airplay", [], OURS, enabled=False),      # off, still "checked"
    ]
    assert m.summarise(vs) == "Every enabled endpoint is visible on the network."


def test_summary_of_nothing():
    assert m.summarise([]) == "Nothing was checked."


def test_the_txt_note_does_not_invent_a_fault():
    """
    A certified Denon Spotify Connect receiver ships without `Stack`, so its
    absence is a difference and not a fault — and saying otherwise is exactly
    the false lead this module is meant to stop.
    """
    note = m.txt_note({"CPath": "/", "VERSION": "1.0"})
    assert note is not None and "rather than a fault" in note
    assert m.txt_note({"CPath": "/", "Stack": "SP"}) is None
    assert m.txt_note({}) is None


def test_every_service_has_a_label_and_a_type():
    for key, (typ, label) in m.SERVICES.items():
        assert typ.endswith("._tcp.local."), f"{key} has a malformed type"
        assert label and label != key


# ── A disconnected device runs no endpoints ──────────────────────────────────
#
# The mistake this encodes, made live on 2026-09-11: the Echo lost its
# controller session, its Spotify and AirPlay advertisements went with it, and
# that second silence was read as independent corroboration that the device
# had fallen off the network. It was the same symptom seen twice. The
# endpoints are OFF by default on the device and are started only by the
# controller's config push, so a device with no session cannot be advertising,
# whatever the network is doing.

def _seen(service, addr):
    return m.Finding(service=service, name=f"x.{service}.local.",
                     address=addr, port=1, target="x.local.", txt={})


def test_a_disconnected_device_is_not_accused_of_being_unheard():
    v = m.verdict("spotify", [_seen("spotify", "192.168.1.9")],
                  "192.168.1.5", True, connected=False)
    assert v.running is False
    assert "no controller session" in v.detail
    assert "says nothing about the network" in v.detail
    assert "not being heard" not in v.detail


def test_a_connected_device_still_gets_the_real_finding():
    v = m.verdict("spotify", [_seen("spotify", "192.168.1.9")],
                  "192.168.1.5", True, connected=True)
    assert v.running is True
    assert "not being heard" in v.detail


def test_being_seen_beats_the_session_check():
    # Belt and braces: an orphaned endpoint from before a restart really can
    # answer while the device has no session, and a scan that SAW it must say
    # so rather than assert it cannot be running.
    v = m.verdict("airplay", [_seen("airplay", "192.168.1.5")],
                  "192.168.1.5", True, connected=False)
    assert v.visible is True
    assert v.running is True


def test_a_scan_that_found_nothing_still_wins():
    # "Nothing answered at all" is a statement about the SCAN and must never
    # be overridden by a statement about the device — that ordering is the
    # whole point of this module.
    v = m.verdict("spotify", [], "192.168.1.5", True, connected=False)
    assert v.reachable is False
    assert "about this scan" in v.detail


def test_the_summary_names_the_session_rather_than_the_network():
    vs = [m.verdict(k, [_seen(k, "192.168.1.9")], "192.168.1.5", True,
                    connected=False) for k in ("spotify", "airplay")]
    assert "no controller session" in m.summarise(vs)
    assert "Nothing here is about the network" in m.summarise(vs)


def test_a_switched_off_endpoint_is_not_reported_as_unstarted():
    # Disabled and "enabled but the device never heard about it" are
    # different sentences, and the first one already had its own.
    v = m.verdict("spotify", [], "192.168.1.5", False, connected=False)
    assert v.running is True
    assert "switched off" in v.detail
