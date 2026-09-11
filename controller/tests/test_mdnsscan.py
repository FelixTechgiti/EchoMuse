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
