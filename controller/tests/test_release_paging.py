"""
The releases list is read a page at a time, and the page size decides which
tag NAMESPACES are visible at all.

Three selectors read one list — firmware `v*`, `emos-v*`, `endpoints-v*` —
and with `per_page=10` the release cadence of one silently hid another.
Measured on the live controller 2026-09-11: ten firmware releases had
accumulated in front of `endpoints-v1.1.0`, so `/api/endpoint_binaries`
answered `"release": null` about a two-day-old release carrying both assets.
Nothing failed and nothing logged; the only symptom was a button that never
appeared and an AirPlay feature that could not work.

These are structural assertions on em_api's source rather than behaviour
tests, for the reason test_deploy.py's are: the thing that broke is a
constant and a URL, and the failure mode is silence.
"""

import pathlib
import re

import pytest

import em_endpoint_release

CONTROLLER = pathlib.Path(__file__).resolve().parents[1]
SRC = (CONTROLLER / "em_api.py").read_text()


def test_the_page_is_big_enough_that_one_request_is_the_normal_case():
    m = re.search(r"RELEASES_PER_PAGE\s*=\s*(\d+)", SRC)
    assert m, "RELEASES_PER_PAGE is gone — the page size must stay nameable"
    assert int(m.group(1)) >= 100, (
        "a small page is how one tag namespace hides another; 100 is "
        "GitHub's maximum and covers any plausible fork in one request")


def test_running_out_of_page_is_not_a_cliff():
    m = re.search(r"MAX_RELEASE_PAGES\s*=\s*(\d+)", SRC)
    assert m, "pagination must be bounded AND present"
    assert int(m.group(1)) >= 2, "one page is not pagination"


def test_only_one_place_builds_a_releases_url():
    # A fourth selector that builds its own URL reintroduces exactly the bug
    # this file exists for.
    assert SRC.count("GITHUB_RELEASES_URL.format(") == 1, (
        "every releases poll goes through _github_releases, so the page "
        "policy is decided once")
    assert "per_page=10\"" not in SRC, "the old fixed 10-item page is back"


def test_every_poll_defaults_to_the_same_repository():
    defaults = set(re.findall(r'get_config\(\s*"github_repo",\s*([^)]+)\)', SRC))
    assert defaults == {"DEFAULT_GITHUB_REPO"}, (
        f"release polls disagree about the default repository: {defaults}. "
        "They did, and upstream publishes no endpoints-v* at all — so a "
        "fresh install answered 'nothing is published' about the wrong repo.")


def test_a_failed_poll_is_not_an_empty_one():
    # `_github_releases` returns None for a failure and a list for a real
    # empty answer. Collapsing them makes a network blip read as "the
    # release went away", which is what the endpoint cache guards against.
    body = SRC[SRC.index("async def _github_releases"):]
    body = body[:body.index("\nasync def ", 1)]
    assert "return out or None" in body
    assert "-> Optional[list]" in body


# ── the selector itself was never the problem, and this proves it ──────────

def _release(tag, assets=()):
    return {
        "tag_name": tag, "draft": False, "prerelease": False,
        "assets": [{"name": n, "browser_download_url": f"https://x/{n}",
                    "size": 1} for n in assets],
    }


def test_the_endpoints_release_is_found_behind_ten_firmware_releases():
    # The exact shape of the live failure: position 12 of the list.
    releases = [_release(f"v2.{n}.0-fx.1") for n in range(27, 16, -1)]
    releases.append(_release("endpoints-v1.1.0",
                             ("librespot", "shairport-sync")))
    picked = em_endpoint_release.select(releases)
    assert picked is not None, (
        "the selector reads the whole list; it was the PAGE that was short")
    assert picked["tag"] == "endpoints-v1.1.0"


def test_a_ten_item_page_is_exactly_what_hid_it():
    # Kept as the regression's own record: truncate to 10 and it vanishes,
    # which is what the controller saw.
    releases = [_release(f"v2.{n}.0-fx.1") for n in range(27, 16, -1)]
    releases.append(_release("endpoints-v1.1.0",
                             ("librespot", "shairport-sync")))
    assert em_endpoint_release.select(releases[:10]) is None
