"""
The device-link auth decision.

Untested by construction until now, because the decision lived inside
em_controller and the suite deliberately does not import that module. What it
cost: deleting a device removed its row, the token is a column on that row, so
`expected` went to None while the device carried on presenting the credential
it still had on disk. That was rejected on all three planes, including the
shell plane the controller would otherwise push a fresh credential over, and
the device retried forever behind a pulsing orange ring with nothing in the
dashboard to explain it (#96).
"""

import em_linkauth as LA

GOOD = "a" * 40
STALE = "b" * 40


def decide(presented=None, expected=None, secure=True, require_tls=False):
    return LA.decide(presented=presented, expected=expected,
                     secure=secure, require_tls=require_tls)


# ─── The case that cost a device ──────────────────────────────────────────────

def test_a_token_for_a_device_with_none_on_record_is_admitted():
    """
    A deleted device coming back with its old credential. It must be allowed
    through so it can register as pending, because the shell plane the
    controller would use to fix it is behind this same gate.
    """
    v = decide(presented=STALE, expected=None)
    assert v.ok is True
    assert v.stale_token is True, "the log needs to be able to say why"


def test_a_stale_token_is_no_stricter_than_no_token_at_all():
    """
    The argument for admitting it, as a test rather than a comment: an
    unrecognised token cannot be treated as worse than an absent one while
    anyone can simply omit the header. If these two ever disagree, the
    stricter branch is buying nothing and costing an orphaned device.
    """
    for require_tls in (False, True):
        with_token = decide(presented=STALE, expected=None, require_tls=require_tls)
        without    = decide(presented=None,  expected=None, require_tls=require_tls)
        assert with_token.ok == without.ok, (
            f"require_tls={require_tls}: presenting an unknown token changed "
            f"the outcome versus presenting nothing")


# ─── The cases that must keep rejecting ───────────────────────────────────────

def test_a_mismatched_token_always_rejects():
    """The only case where a credential is actually wrong."""
    v = decide(presented=STALE, expected=GOOD)
    assert v.ok is False
    assert "mismatch" in v.reason


def test_a_mismatched_token_rejects_under_tls_too():
    assert decide(presented=STALE, expected=GOOD, require_tls=True).ok is False


def test_require_tls_refuses_a_plain_connection():
    assert decide(presented=GOOD, expected=GOOD,
                  secure=False, require_tls=True).ok is False


def test_require_tls_refuses_a_device_with_no_stored_token():
    """
    The deleted-device case again, but on an install that has flipped the
    posture. Re-provisioning over USB is the intended path there, and this
    fix must not quietly weaken it.
    """
    assert decide(presented=STALE, expected=None, require_tls=True).ok is False


def test_require_tls_refuses_a_tokenless_connection():
    assert decide(presented=None, expected=GOOD, require_tls=True).ok is False


# ─── The rollout rule that must survive ───────────────────────────────────────

def test_a_stored_token_with_none_presented_is_allowed_by_default():
    """
    The DB row is minted before the credential files reach the device, and the
    push itself rides the shell plane. Rejecting in that window would deadlock
    the rollout it exists to perform.
    """
    v = decide(presented=None, expected=GOOD)
    assert v.ok is True
    assert v.stale_token is False


def test_a_matching_token_is_allowed_everywhere():
    for require_tls in (False, True):
        assert decide(presented=GOOD, expected=GOOD, require_tls=require_tls).ok


def test_an_unknown_device_on_a_default_install_is_allowed():
    """A device that has never been issued a credential, on a default install."""
    v = decide(presented=None, expected=None)
    assert v.ok is True
    assert v.stale_token is False


def test_comparison_is_constant_time():
    """
    The mismatch branch compares secrets, so it must not use ==. Checked on
    the source because timing is not observable from a unit test.
    """
    import inspect
    src = inspect.getsource(LA.decide)
    assert "compare_digest" in src
    assert "presented == expected" not in src
