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


# ── Comparing our advertisement against the ones that work ───────────────────
#
# The state this was written for, 2026-09-12: everything the device controls
# was verified — complete records, a resolvable SRV target, the advertised
# port answering, and a SECOND HOST on the same network hearing all of it —
# and the Spotify app on a phone on that same network still did not list it,
# while it listed other Spotify Connect speakers. The only question left is
# what those speakers say that we do not, and the scan was collecting their
# records and reducing them to a count.

def _txt(**kw):
    return dict(kw)


def test_nothing_to_compare_against_is_not_no_differences():
    # Silence here would read as "we match the others" on a network with no
    # others — the same conflation this whole module exists to prevent.
    assert m.txt_compare(_txt(a="1"), []) is None
    assert m.txt_compare(_txt(a="1"), [{}, None]) is None


def test_a_key_every_other_host_sends_is_reported_with_its_count():
    c = m.txt_compare(
        _txt(VERSION="1.0", CPath="/"),
        [_txt(VERSION="1.0", CPath="/", Stack="SP"),
         _txt(VERSION="1.0", CPath="/", Stack="SP")])
    assert c.missing == (("stack", "SP", 2),)
    assert c.others == 2
    # "all of them" and "some of them" are different findings, so the count
    # has to survive into the sentence.
    assert "2 of 2 others send it" in m.describe_comparison(c)


def test_keys_are_matched_case_insensitively():
    # mDNS TXT keys are case-insensitive. A receiver writing CPath where
    # another writes cpath is not a finding, and reporting it as one buries
    # the real entry in noise.
    c = m.txt_compare(_txt(CPath="/"), [_txt(cpath="/")])
    assert c.missing == () and c.differing == () and c.extra == ()
    assert m.describe_comparison(c) is None


def test_a_differing_value_is_not_reported_as_missing():
    c = m.txt_compare(_txt(CPath="/"), [_txt(CPath="/zc/0"), _txt(CPath="/zc/0")])
    assert c.missing == ()
    assert c.differing == (("cpath", "/", "/zc/0", 2),)


def test_a_key_only_we_send_lands_in_extra():
    c = m.txt_compare(_txt(a="1", mine="x"), [_txt(a="1")])
    assert c.extra == (("mine", "x"),)


def test_the_three_tuples_do_not_swap():
    # They are three same-shaped tuples and they DID swap positionally the
    # first time this was written, which the sentence builder then crashed
    # on. Pinned by content, not by position.
    c = m.txt_compare(_txt(same="1", differs="ours", only_ours="x"),
                      [_txt(same="1", differs="theirs", only_theirs="y")])
    assert c.missing == (("only_theirs", "y", 1),)
    assert c.extra == (("only_ours", "x"),)
    assert c.differing == (("differs", "ours", "theirs", 1),)
    m.describe_comparison(c)          # must not raise


def test_agreement_says_nothing_at_all():
    # A line that prints on every scan is a line nobody reads on the day it
    # matters.
    c = m.txt_compare(_txt(VERSION="1.0"), [_txt(VERSION="1.0")])
    assert m.describe_comparison(c) is None
    assert m.describe_comparison(None) is None


def test_the_most_common_value_wins_not_the_first_seen():
    c = m.txt_compare(_txt(k="ours"),
                      [_txt(k="rare"), _txt(k="common"), _txt(k="common")])
    assert c.differing == (("k", "ours", "common", 2),)


# --- AirPlay generation: classic vs AirPlay 2 -------------------------------
#
# Filed after a device that was advertised, reachable, port-open and visible to
# the controller still did not appear in its owner's AirPlay picker. The scan
# browsed `_raop._tcp` only, so the one property that could explain it — this
# build never advertises `_airplay._tcp` — was invisible from here.

def test_airplay2_is_browsed_but_never_becomes_a_verdict():
    """A service nobody switched on must not render as an endpoint that is down.

    It is also one the device CANNOT offer, so a verdict would report a
    permanent fault about a build decision.
    """
    assert m.AIRPLAY2_TYPE == "_airplay._tcp.local."
    types = [t for t, _ in m.SERVICES.values()]
    assert m.AIRPLAY2_TYPE not in types, (
        "AirPlay 2 is in SERVICES, so it gets its own enabled/visible verdict "
        "and renders as a broken endpoint")


def test_the_note_says_the_build_cannot_do_it_rather_than_was_not_seen():
    """The distinction is the whole value of the line.

    "Not seen" invites somebody to go looking for a network fault. The truth is
    that shairport-sync built --with-tinysvcmdns has no code to advertise the
    second service, so no amount of network will produce it.
    """
    note = m.airplay_generation_note(0)
    assert "never" in note and "cannot" in note
    assert "not seen" not in note.lower()


def test_the_note_reports_how_many_others_offer_airplay2():
    note = m.airplay_generation_note(4)
    assert "4 other host(s)" in note
    # And it points the reader at the comparison rather than at the radio,
    # because that comparison is what they can act on.
    assert "not the network" in note


def test_with_no_airplay2_on_the_network_the_note_says_so_plainly():
    """Zero others is a real reading and must not read as "3 others"-shaped.

    It means the network offers no example of the difference, which is a
    different next step: look at the client, not at the other devices.
    """
    note = m.airplay_generation_note(0)
    assert "No other host here advertises it" in note
    assert "other host(s) on this network do advertise" not in note


def test_the_note_never_claims_a_fault():
    """Same rule as txt_note: a difference is not a failure.

    A build decision rendered as a fault is how somebody spends an evening on
    their router for something that was never going to work.
    """
    for n in (0, 1, 9):
        note = m.airplay_generation_note(n)
        for word in ("fault", "broken", "error", "failed"):
            assert word not in note.lower(), f"{word!r} in: {note}"
