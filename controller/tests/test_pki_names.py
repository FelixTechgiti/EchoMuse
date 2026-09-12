"""
The name the device-link certificate answers to.

This is the coupling that took the fleet down on 2026-09-11 and was pinned
by nothing: `em_pki.TLS_SERVER_NAME` and the firmware's `tlsServerName` were
changed together by the rename, which is right for a fresh install and wrong
for every existing one — the name is baked into a certificate that persists
in tls/, so the controller went on presenting `echomuse-controller` while
firmware from v2.28.0-fx.1 demanded `revoice-controller`. Verification then
fails on every dial and the device has no fallback: it holds a CA, mDNS
advertises tls_port, so it redials wss for ever, silently.

Nothing here needs the cryptography package, on purpose — the controller test
environment deliberately does not have it, so a guard that required it would
skip in CI and guard nothing.
"""

import os
import re

import em_pki

HERE = os.path.dirname(os.path.abspath(__file__))
TLSCREDS = os.path.join(HERE, "..", "..", "device", "internal", "client",
                        "tlscreds.go")


def _go_server_name() -> str:
    with open(TLSCREDS, encoding="utf-8") as f:
        m = re.search(r'tlsServerName\s*=\s*"([^"]+)"', f.read())
    assert m, "tlsServerName is no longer a literal in tlscreds.go"
    return m.group(1)


def test_the_firmware_name_is_one_the_certificate_answers_to():
    assert _go_server_name() in em_pki.server_names(), (
        "the firmware verifies against a name the controller's leaf does not "
        "carry — every device taking that firmware loses its link and cannot "
        "report why"
    )


def test_the_current_name_comes_first():
    assert em_pki.server_names()[0] == em_pki.TLS_SERVER_NAME


def test_the_pre_rename_name_is_still_answered_to():
    # Firmware up to v2.27.0-fx.1 shipped with this compiled in. Removing it
    # strands every device still on that firmware — including any device
    # whose OTA is the thing that would have moved it off.
    assert "echomuse-controller" in em_pki.server_names()


def test_no_name_is_listed_twice():
    names = em_pki.server_names()
    assert len(set(names)) == len(names)


def test_missing_names_reports_in_required_order():
    assert em_pki.missing_names((), ("a", "b")) == ("a", "b")
    assert em_pki.missing_names(("b",), ("a", "b")) == ("a",)
    assert em_pki.missing_names(("a", "b", "z"), ("a", "b")) == ()


def _source(name: str) -> str:
    with open(os.path.join(HERE, "..", name), encoding="utf-8") as f:
        return f.read()


def test_an_existing_certificate_is_checked_and_repaired():
    # The repair path only exists if ensure_pki looks at a leaf it already
    # has. Before this, all four files present meant "done" and the SAN was
    # never read again after the day it was written.
    src = _source("em_pki.py")
    body = src[src.index("def ensure_pki("):]
    for call in ("missing_names(", "_leaf_san_names(", "_reissue_leaf("):
        assert call in body, f"ensure_pki no longer calls {call}"


def test_the_repair_never_touches_the_ca():
    # The CA is what devices PIN, and it is already installed across the
    # fleet. Rotating it to fix a name would need a credential push to every
    # device — over the link this repair exists to restore. Only the leaf
    # carries the name, so only the leaf may be rewritten.
    src = _source("em_pki.py")
    body = src[src.index("def _reissue_leaf("):src.index("def ensure_pki(")]
    written = re.findall(r'_write\(os\.path\.join\(tls_dir,\s*"([^"]+)"', body)
    assert set(written) == {"server.key", "server.pem"}, (
        f"the leaf re-issue writes {sorted(written)} — it may only replace "
        "the leaf, never the CA devices have pinned"
    )


def test_a_failed_repair_keeps_serving_the_old_certificate():
    # Half the fleet still verifies against the old name. Taking the
    # listener down because the repair failed would break the devices that
    # were working, to fix the ones that were not.
    src = _source("em_pki.py")
    body = src[src.index("def ensure_pki("):]
    assert "except Exception" in body, \
        "a failed re-issue must not propagate out of ensure_pki"
