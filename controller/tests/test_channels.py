"""
Release-channel guards.

A channel is a second add-on with its own slug, so it is a second
config.yaml describing the same program. Keeping two in step by hand is the
failure this project has already paid for once: a stale pin in one file
shipped a controller with no ingress support, presenting as two unrelated
faults (#160). Two files multiply that by every option, schema entry and
permission.

So the EA add-on is generated, and these tests fail if the committed copy
is not what the generator produces. The generator and the guard state the
same rule twice, which turns drift into a red test instead of a support
thread.
"""

import sys
from pathlib import Path

import pytest
import yaml

CONTROLLER = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(CONTROLLER / "tools"))

import sync_channels  # noqa: E402

GA = yaml.safe_load((CONTROLLER / "config.yaml").read_text())
EA_PATH = sync_channels.EA.path


def _ea():
    assert EA_PATH.joinpath("config.yaml").is_file(), (
        "controller-ea/config.yaml is missing — run "
        "controller/tools/sync_channels.py")
    return yaml.safe_load((EA_PATH / "config.yaml").read_text())


# ── No drift ──────────────────────────────────────────────────────────────────

def test_the_committed_channel_matches_the_generator():
    problems = sync_channels.check(sync_channels.EA)
    assert not problems, (
        "Channel add-on is out of date:\n  " + "\n  ".join(problems) +
        "\n\nRun: controller/tools/sync_channels.py")


@pytest.mark.parametrize("key", [
    "arch", "host_network", "ingress", "ingress_port", "panel_admin",
    "panel_icon", "homeassistant_api", "environment", "options", "schema",
    "image", "init", "url",
])
def test_channel_shares_every_non_identity_field_with_ga(key):
    """
    A setting reachable in one channel and not the other is the divergence
    the deployment-parity rule exists to prevent, one level up: it would
    make an EA bug report unanswerable without first asking which add-on
    the person installed.
    """
    ea = _ea()
    if key not in GA:
        pytest.skip(f"GA config has no {key}")
    assert ea.get(key) == GA[key], (
        f"{key} differs between channels — regenerate rather than editing")


def test_options_and_schema_agree_key_for_key():
    # Belt and braces over the field comparison above: an option present in
    # one channel's schema but not the other is a validation difference that
    # only shows up when a user sets it.
    ea = _ea()
    assert set(ea["options"]) == set(GA["options"])
    assert set(ea["schema"]) == set(GA["schema"])


# ── Identity, which must differ ───────────────────────────────────────────────

def test_channel_has_its_own_slug():
    """
    The slug is the add-on's identity AND the name of its /data directory.
    Sharing one would make installing EA an in-place replacement of GA
    rather than a choice, and there would be no way back.
    """
    ea = _ea()
    assert ea["slug"] != GA["slug"]
    assert ea["slug"] == "controller-ea"


def test_channel_is_distinguishable_in_the_ui():
    # Two add-ons called "EchoMuse" with two identical panels is a support
    # thread waiting to happen.
    ea = _ea()
    assert ea["name"] != GA["name"]
    assert ea["panel_title"] != GA["panel_title"]


def test_channel_pulls_the_same_image_repository():
    # Channels differ by TAG, not by artefact source. A second repository
    # would need a second publish path and could drift in ways no test here
    # could see.
    ea = _ea()
    assert ea["image"] == GA["image"]


def test_channel_version_is_independent_of_ga():
    """
    version: is the one field a release moves, and it must survive a sync —
    regenerating EA must never quietly drag its pin back to GA's, which
    would ship GA code to everyone who opted into EA.
    """
    ea_before = (EA_PATH / "config.yaml").read_text()
    generated = sync_channels.generate(sync_channels.EA)["config.yaml"]
    import re
    got = re.search(r'^version:\s*"(.*)"', generated, re.M).group(1)
    want = re.search(r'^version:\s*"(.*)"', ea_before, re.M).group(1)
    assert got == want, "sync_channels must preserve the channel's own version"


def test_generated_file_says_it_is_generated():
    # Someone WILL open this file to change a setting.
    text = (EA_PATH / "config.yaml").read_text()
    assert "DO NOT EDIT" in text
    assert "sync_channels.py" in text
