"""
Tests for em_haadmin — asking Home Assistant who is an administrator.

The behaviour that matters is what happens when the answer does NOT arrive.
Every failure must read as "unknown", never as an assertion about the user:
"not an admin" would demote a working admin the moment Home Assistant
restarts, and "an admin" would hand a household member a root shell.
"""

import asyncio

import pytest

import em_haadmin as ha


@pytest.fixture(autouse=True)
def _clear_cache():
    ha.invalidate()
    yield
    ha.invalidate()


def _run(coro):
    return asyncio.new_event_loop().run_until_complete(coro)


# ── Unavailable means unknown ─────────────────────────────────────────────────

def test_no_supervisor_token_is_unknown_not_false(monkeypatch):
    # The ordinary case on the standalone container. Answering False here
    # would make every non-first user read-only for a reason that has
    # nothing to do with them.
    monkeypatch.delenv("SUPERVISOR_TOKEN", raising=False)
    assert _run(ha.is_admin("abc")) is None


def test_a_failed_lookup_is_unknown(monkeypatch):
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")

    async def boom(_token):
        raise ConnectionError("supervisor unreachable")

    monkeypatch.setattr(ha, "_fetch_users", boom)
    assert _run(ha.is_admin("abc")) is None


def test_a_timeout_is_unknown(monkeypatch):
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")
    monkeypatch.setattr(ha, "TIMEOUT_S", 0.01)

    async def slow(_token):
        await asyncio.sleep(5)

    monkeypatch.setattr(ha, "_fetch_users", slow)
    assert _run(ha.is_admin("abc")) is None


def test_a_user_missing_from_the_list_is_unknown(monkeypatch):
    # Authenticated by Supervisor but absent from the list — do not guess in
    # either direction.
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")

    async def users(_token):
        return [{"id": "someone-else", "group_ids": ["system-admin"]}]

    monkeypatch.setattr(ha, "_fetch_users", users)
    assert _run(ha.is_admin("abc")) is None


# ── The answer, when there is one ─────────────────────────────────────────────

def _with_users(monkeypatch, rows):
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")

    async def users(_token):
        return rows

    monkeypatch.setattr(ha, "_fetch_users", users)


def test_admin_group_membership_is_the_test(monkeypatch):
    _with_users(monkeypatch, [{"id": "abc", "group_ids": ["system-admin"]}])
    assert _run(ha.is_admin("abc")) is True


def test_a_user_group_member_is_not_an_admin(monkeypatch):
    _with_users(monkeypatch, [{"id": "abc", "group_ids": ["system-users"]}])
    assert _run(ha.is_admin("abc")) is False


def test_no_groups_at_all_is_not_an_admin(monkeypatch):
    _with_users(monkeypatch, [{"id": "abc"}])
    assert _run(ha.is_admin("abc")) is False


def test_a_similarly_named_group_does_not_grant_admin(monkeypatch):
    # Exact membership only — no prefix matching anywhere.
    _with_users(monkeypatch, [{"id": "abc", "group_ids": ["system-admins",
                                                          "not-system-admin"]}])
    assert _run(ha.is_admin("abc")) is False


# ── Caching ───────────────────────────────────────────────────────────────────

def test_the_answer_is_cached(monkeypatch):
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")
    calls = []

    async def users(_token):
        calls.append(1)
        return [{"id": "abc", "group_ids": ["system-admin"]}]

    monkeypatch.setattr(ha, "_fetch_users", users)
    assert _run(ha.is_admin("abc")) is True
    assert _run(ha.is_admin("abc")) is True
    assert len(calls) == 1, "login path must not re-ask on every page load"


def test_one_lookup_caches_every_user_it_saw(monkeypatch):
    # The list call returns everyone, so a second user costs no round trip.
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")
    calls = []

    async def users(_token):
        calls.append(1)
        return [
            {"id": "abc", "group_ids": ["system-admin"]},
            {"id": "def", "group_ids": ["system-users"]},
        ]

    monkeypatch.setattr(ha, "_fetch_users", users)
    assert _run(ha.is_admin("abc")) is True
    assert _run(ha.is_admin("def")) is False
    assert len(calls) == 1


def test_a_stale_entry_is_not_used(monkeypatch):
    # Revoking someone's HA admin has to take effect without a restart.
    monkeypatch.setattr(ha, "CACHE_TTL_S", -1)
    ha._cache["abc"] = (0.0, True)
    assert ha.is_admin_cached("abc") is None


def test_a_failure_is_not_cached_as_an_answer(monkeypatch):
    # Caching "unknown" as if it were an answer would extend a momentary HA
    # restart into CACHE_TTL_S of everyone being read-only.
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")

    async def boom(_token):
        raise ConnectionError

    monkeypatch.setattr(ha, "_fetch_users", boom)
    assert _run(ha.is_admin("abc")) is None
    assert ha.is_admin_cached("abc") is None


def test_a_recent_failure_suppresses_the_retry(monkeypatch):
    """
    The login path waits TIMEOUT_S on a lookup. Without this, a Home
    Assistant restart makes EVERY dashboard load hang for five seconds —
    a controller that feels broken for the length of someone else's reboot.
    """
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")
    calls = []

    async def boom(_token):
        calls.append(1)
        raise ConnectionError

    monkeypatch.setattr(ha, "_fetch_users", boom)
    assert _run(ha.is_admin("abc")) is None
    assert _run(ha.is_admin("abc")) is None
    assert _run(ha.is_admin("other")) is None
    assert len(calls) == 1, "a recent failure must not be retried per login"


def test_the_suppression_expires_quickly(monkeypatch):
    # It has to recover within seconds of HA coming back, or the cure is
    # its own outage.
    assert ha.FAILURE_TTL_S <= 30
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")
    monkeypatch.setattr(ha, "FAILURE_TTL_S", -1)
    calls = []

    async def users(_token):
        calls.append(1)
        return [{"id": "abc", "group_ids": ["system-admin"]}]

    ha._failed_at = 0.0
    monkeypatch.setattr(ha, "_fetch_users", users)
    assert _run(ha.is_admin("abc")) is True
    assert len(calls) == 1


def test_a_successful_lookup_clears_the_suppression(monkeypatch):
    monkeypatch.setenv("SUPERVISOR_TOKEN", "t")

    async def boom(_token):
        raise ConnectionError

    monkeypatch.setattr(ha, "_fetch_users", boom)
    assert _run(ha.is_admin("abc")) is None
    ha.invalidate()
    assert ha._failed_at is None
