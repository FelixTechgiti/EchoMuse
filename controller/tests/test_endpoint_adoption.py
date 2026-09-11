"""
Recognising a stored binary as a published build, without provenance.

The provenance rule — never overwrite what the controller cannot prove it
wrote — is right, and it protects a patched build somebody is testing. But it
cannot tell that apart from a file downloaded off the releases page and
uploaded through the dashboard: the record that would distinguish them is the
record that is missing. So every store filled before provenance existed was
frozen for ever, and the published build never arrived.

A byte-for-byte match against something a release published settles it. Not a
heuristic — identical bytes ARE that build. GitHub reports each asset's
sha256 as its `digest`, so the comparison costs no download.

The store below is the LIVE one from the fleet on 2026-09-11, and it is the
reason this exists: a hand-built librespot beside a shairport-sync that is
byte-identical to endpoints-v1.0.0's.
"""

import em_endpoint_release as r

V100, V110 = "endpoints-v1.0.0", "endpoints-v1.1.0"

# Real digests, read off the published assets.
SHA_SHAIRPORT_110 = "4a21aa0bcefa6aa461318bb710a99cee04d783051569a2581e11d2b5c6a28804"
SHA_SHAIRPORT_100 = "1111111111111111111111111111111111111111111111111111111111111111"
SHA_LIBRESPOT_110 = "2ef2536a99b3374ed21dab83f3312e8901b81691d9d1021703c8fca71e0d51f5"


def _release(tag, shairport_sha, librespot_sha, **kw):
    def asset(name, sha):
        a = {"name": name, "browser_download_url": f"https://x/{tag}/{name}",
             "size": 1}
        if sha:
            a["digest"] = f"sha256:{sha}"
        return a
    return {"tag_name": tag, "draft": False, "prerelease": False,
            "assets": [asset("librespot", librespot_sha),
                       asset("shairport-sync", shairport_sha)], **kw}


RELEASES = [                              # newest first, as GitHub returns
    _release(V110, SHA_SHAIRPORT_110, SHA_LIBRESPOT_110),
    _release(V100, SHA_SHAIRPORT_100, "aaaa"),
]

# The fleet's actual store: a hand build and a downloaded release asset.
LIVE_STORE = {
    "spotify": {"md5": "81d4f316600572df2d52f42d9e33ad60",
                "sha256": "ffff" * 16},          # matches no release
    "airplay": {"md5": "127028c5b6b904e041c39ab04f43a976",
                "sha256": SHA_SHAIRPORT_100},    # endpoints-v1.0.0 exactly
}


def test_the_index_maps_every_release_not_only_the_newest():
    idx = r.digest_index(RELEASES)
    assert idx["airplay"][SHA_SHAIRPORT_100] == V100
    assert idx["airplay"][SHA_SHAIRPORT_110] == V110


def test_a_draft_or_prerelease_publishes_nothing_to_match_against():
    idx = r.digest_index([
        _release("endpoints-v9.9.9", "dddd", "eeee", draft=True),
        _release("endpoints-v9.9.8", "ffff", "0000", prerelease=True),
    ])
    assert idx == {}


def test_an_asset_with_no_digest_can_adopt_nothing():
    # Older releases, and hosts that do not report it. Absence must never
    # read as a match.
    idx = r.digest_index([_release(V100, None, None)])
    assert idx == {}


def test_the_hand_build_is_left_alone():
    d = r.published_state(V110, {}, LIVE_STORE, "spotify", r.digest_index(RELEASES))
    assert d["state"] == "unmanaged"
    assert "adopted" not in d


def test_the_downloaded_release_asset_is_recognised_as_one():
    d = r.published_state(V110, {}, LIVE_STORE, "airplay", r.digest_index(RELEASES))
    assert d["state"] == "outdated"
    assert d["stored_tag"] == V100
    assert d["adopted"] is True


def test_a_store_already_holding_the_newest_reads_published():
    store = {"airplay": {"md5": "x", "sha256": SHA_SHAIRPORT_110}}
    d = r.published_state(V110, {}, store, "airplay", r.digest_index(RELEASES))
    assert d["state"] == "published"
    assert d["adopted"] is True


def test_only_the_recognised_kind_is_fetched():
    got = r.needs_fetch(V110, {}, LIVE_STORE, r.digest_index(RELEASES))
    assert got == ["airplay"], "the hand-built librespot must never be fetched over"


def test_nothing_is_fetched_when_the_newest_is_already_stored():
    store = {"spotify": {"md5": "x", "sha256": SHA_LIBRESPOT_110},
             "airplay": {"md5": "y", "sha256": SHA_SHAIRPORT_110}}
    assert r.needs_fetch(V110, {}, store, r.digest_index(RELEASES)) == []


def test_without_digests_the_old_behaviour_is_exact():
    # An older controller, or a poll that could not reach GitHub. Adoption is
    # additive: absent evidence must change nothing.
    assert r.needs_fetch(V110, {}, LIVE_STORE) == []
    assert r.published_state(V110, {}, LIVE_STORE, "airplay")["state"] == "unmanaged"


def test_a_store_with_no_sha256_adopts_nothing():
    # A scan written by a controller before sha256 was recorded. Both kinds
    # are present so this isolates the missing hash — an ABSENT kind is a
    # different case and is correctly fetched.
    store = {"spotify": {"md5": "81d4f316600572df2d52f42d9e33ad60"},
             "airplay": {"md5": "127028c5b6b904e041c39ab04f43a976"}}
    idx = r.digest_index(RELEASES)
    assert r.adopted_tag(idx, store, "airplay") is None
    assert r.needs_fetch(V110, {}, store, idx) == []


def test_a_kind_missing_from_the_store_is_still_fetched():
    # Unrelated to adoption, and the one case where "nothing to lose" makes
    # the fetch unconditional.
    store = {"airplay": {"md5": "y", "sha256": SHA_SHAIRPORT_110}}
    assert r.needs_fetch(V110, {}, store, r.digest_index(RELEASES)) == ["spotify"]


def test_provenance_still_wins_when_it_exists():
    # A recorded write is a stronger statement than a digest match and must
    # keep deciding: this is the controller's own copy, current with V110.
    prov = {"tag": V110, "kinds": {"airplay": {"md5": "127028c5b6b904e041c39ab04f43a976"}}}
    d = r.published_state(V110, prov, LIVE_STORE, "airplay", r.digest_index(RELEASES))
    assert d["state"] == "published"
    assert "adopted" not in d
