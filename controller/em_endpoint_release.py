"""
em_endpoint_release.py — picking the endpoint binaries out of a GitHub release
==============================================================================

The Spotify and AirPlay binaries were built somewhere and uploaded by hand,
once per controller, which is the friction OTA exists to remove. `endpoints-v*`
releases publish them (`.github/workflows/endpoint-release.yml`) and this
module is the pure half of collecting one: which release, which assets, and
whether the store already holds what that release published.

**Its own tag namespace, and the selection is deliberately narrow.**
`em_api._fetch_latest_release` selects a tag starting `v` carrying a `server`
asset, so `endpoints-v1.0.0` matches neither test and the firmware OTA poller
can never see one. This selects on the opposite things, and the two share no
cache, for the reason `_fetch_latest_emos_release` is a separate function
rather than a parameter: one cache holding whichever kind was asked for last
is a cache that answers the wrong question half the time.

**Both binaries or neither.** A release carrying one is one the controller
installs happily while reporting the other as "not installed", which from the
dashboard is indistinguishable from a device that never received it. The
release workflow refuses to publish a half release; this refuses to select
one, because a workflow can be edited and a fielded controller cannot.

**The store is only ever overwritten where the controller wrote it.**
Provenance records the tag a fetch came from and the md5 it wrote for each
kind. A stored binary whose md5 no longer matches that record was put there by
a person — the dashboard's upload, a hand build of a patched librespot — and
replacing it with the published one would silently undo the thing they were
testing, on a timer, with nothing said. Absent provenance is the same answer:
we did not write it, so it is not ours to replace.
"""

from __future__ import annotations

import json
import logging
from pathlib import Path

import em_endpoint_bins as bins
import version

log = logging.getLogger("revoice.endpoint_release")

TAG_PREFIX = "endpoints-v"

# Beside the binaries, inside the data volume, so it survives an image
# upgrade with the files it describes. Dotted so `scan()`'s directory
# listing and a curious `ls` both read as the two binaries and nothing else.
PROVENANCE_NAME = ".provenance.json"


def provenance_path(db_path: str | None = None) -> Path:
    return bins.store_dir(db_path) / PROVENANCE_NAME


def read_provenance(db_path: str | None = None) -> dict:
    """
    What the controller last wrote into the store, or {} when it has written
    nothing it can still vouch for.

    Every failure reads as {} — no file, unreadable, malformed, not an
    object — because they all mean the same thing to every caller: this
    store is not ours, leave it alone. A corrupt provenance that raised
    would stop the fetch entirely, which is the one outcome worse than
    doing nothing.
    """
    try:
        raw = provenance_path(db_path).read_text(encoding="utf-8")
        data = json.loads(raw)
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


def write_provenance(data: dict, db_path: str | None = None) -> bool:
    """
    Record what was just fetched. False if it could not be written, which is
    never a reason for the caller to fail — em_firmware's rule, for the same
    reason: the record is an optimisation and a refusal costs the user the
    thing they asked for while protecting nothing.

    The cost of losing it is that the next poll re-downloads, and then
    declines to overwrite a store it can no longer prove it wrote. Both are
    safe; neither is silent.
    """
    path = provenance_path(db_path)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.parent / (PROVENANCE_NAME + ".part")
        tmp.write_text(json.dumps(data, indent=2), encoding="utf-8")
        tmp.replace(path)
        return True
    except OSError as e:
        log.warning(f"[endpoints] could not record provenance: {e}")
        return False


def asset_names() -> dict[str, str]:
    """The asset filename each kind is published under, keyed by kind."""
    return {k.key: k.filename for k in bins.KINDS.values()}


def select(releases: list) -> dict | None:
    """
    The newest usable `endpoints-v*` release, or None.

    "Newest" is by PARSED VERSION, never by list order — the same trap
    `_fetch_controller_release` documents, where the refs API sorts lexically
    and returns `controller-v2.9.0` after `controller-v2.10.0`. The releases
    API happens to sort by date, which is usually the same answer and is not
    the same rule.

    A release missing either asset is skipped rather than selected and
    half-used: the caller would install one endpoint and report the other as
    absent, which reads exactly like a device that never received it.
    """
    want = asset_names()
    best = None
    best_key = None
    for data in releases or []:
        if not isinstance(data, dict):
            continue
        if data.get("draft") or data.get("prerelease"):
            continue
        tag = data.get("tag_name") or ""
        if not tag.startswith(TAG_PREFIX):
            continue
        by_name = {a.get("name"): a for a in (data.get("assets") or [])
                   if isinstance(a, dict)}
        assets = {}
        for key, name in want.items():
            a = by_name.get(name)
            if a is None or not a.get("browser_download_url"):
                assets = {}
                break
            assets[key] = {"name": name,
                           "url": a["browser_download_url"],
                           "size": a.get("size", 0)}
        if not assets:
            log.info(f"[endpoints] {tag} does not carry both binaries — skipping")
            continue
        # The PREFIX has to come off first. `version.parse` strips only
        # `controller-`, so `endpoints-v1.2.0` parses as None and every
        # candidate would sort (0, 0, 0) — leaving the "newest by version"
        # rule above quietly reduced to "first in the list", which is the
        # exact bug it is written to prevent.
        #
        # An unparseable tag still sorts last rather than crashing the poll:
        # a release nobody can order beats no release, and (0, 0, 0) only
        # ever wins when it is the only candidate.
        key = version.parse(tag.removeprefix(TAG_PREFIX)) or (0, 0, 0)
        if best_key is None or key > best_key:
            best, best_key = {"tag": tag, "assets": assets}, key
    return best


def published_state(tag: str, prov: dict, store: dict, key: str) -> dict:
    """
    What the store holds for one kind, against what `tag` publishes.

    Four answers, and they exist because "the store has a binary" was the only
    thing anyone could see, and it hid the case that matters:

      `empty`      nothing stored. The automatic fetch fills it.
      `published`  ours, and current with this release.
      `outdated`   ours, from an older release. The automatic fetch replaces
                   it.
      `unmanaged`  a binary the controller cannot prove it wrote, so the
                   automatic fetch will never touch it.

    **`unmanaged` is the one worth naming.** It covers a deliberate hand
    upload — a patched build somebody is testing, which is exactly what
    `needs_fetch` refuses to overwrite — AND every store filled before
    provenance existed, which is all of them. Those two are indistinguishable
    from here: the record that would tell them apart is the record that is
    missing. So this reports the state and does not decide; deciding is the
    user's, through `take_published`.

    Reported even when there is no release to compare against, because
    "we cannot see a release" and "the store is current" are different
    answers and only one of them means nothing to do.
    """
    have = (store or {}).get(key)
    if have is None:
        return {"state": "empty", "stored_md5": None}
    recorded = ((prov.get("kinds") or {}).get(key) or {}).get("md5")
    if recorded != have.get("md5"):
        return {"state": "unmanaged", "stored_md5": have.get("md5")}
    if prov.get("tag") == tag:
        return {"state": "published", "stored_md5": have.get("md5")}
    return {"state": "outdated", "stored_md5": have.get("md5"),
            "stored_tag": prov.get("tag")}


def needs_fetch(tag: str, prov: dict, store: dict) -> list[str]:
    """
    Which kinds should be downloaded from `tag`, given the provenance record
    and what `bins.scan()` found in the store.

    Three answers per kind, and the middle one is the whole point:

      * nothing stored          → fetch. There is nothing to lose.
      * stored, and its md5 is
        the one we recorded      → fetch only if the tag has moved on. This is
                                   the controller's own copy and updating it
                                   is exactly the job.
      * stored, md5 differs      → NEVER. Somebody uploaded that, and
                                   replacing it on a timer would undo their
                                   build with nothing said.

    Called with the whole store rather than per kind so a release that moves
    one binary and not the other still answers per kind — the release
    publishes both every time, but the store does not have to have taken both.
    """
    out = []
    same_tag = (prov.get("tag") == tag)
    for key in bins.KINDS:
        have = store.get(key)
        if have is None:
            out.append(key)
            continue
        recorded = ((prov.get("kinds") or {}).get(key) or {}).get("md5")
        if recorded != have.get("md5"):
            continue  # not ours
        if not same_tag:
            out.append(key)
    return out
